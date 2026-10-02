package auth

import (
	"context"
	"testing"
)

// getChatHistoryAnswer — ответ TDLib на getChatHistory с сообщениями от новых к
// старым, как их отдаёт документация td_api («reverse chronological order»).
func getChatHistoryAnswer(ids ...int64) map[string]interface{} {
	messages := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		messages = append(messages, map[string]interface{}{
			"@type": "message",
			"id":    float64(id),
			"date":  float64(id),
			"content": map[string]interface{}{
				"@type": "messageText",
				"text":  map[string]interface{}{"@type": "formattedText", "text": "текст"},
			},
		})
	}
	return map[string]interface{}{"@type": "messages", "messages": messages}
}

// TestGetMessagesBeforeAsksFromTheGivenMessage — догрузка вверх идёт от точки
// продолжения: from_message_id равен самому старому уже загруженному сообщению, а
// offset нулевой, иначе TDLib вернул бы ещё и более новые (см. документацию td_api:
// offset_ = 0 — «начиная ровно с from_message_id»).
func TestGetMessagesBeforeAsksFromTheGivenMessage(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{getChatHistoryAnswer(9, 8, 7)}

	messages, err := GetMessagesBefore(context.Background(), mock, 7, 10, 50)
	if err != nil {
		t.Fatalf("GetMessagesBefore: %v", err)
	}
	if len(mock.requests) != 1 {
		t.Fatalf("запросов %d, want 1", len(mock.requests))
	}
	request := mock.requests[0]
	if got, want := request["chat_id"], int64(7); got != want {
		t.Fatalf("chat_id = %v, want %v", got, want)
	}
	if got, want := request["from_message_id"], int64(10); got != want {
		t.Fatalf("from_message_id = %v, want %v (самое старое загруженное)", got, want)
	}
	if got := request["offset"]; got != 0 {
		t.Fatalf("offset = %v, want 0: с отрицательным TDLib вернул бы и более новые", got)
	}
	if got, want := request["limit"], 50; got != want {
		t.Fatalf("limit = %v, want %v", got, want)
	}
	// Порядок хронологический: старые сверху, новые снизу — как во всей ленте.
	if len(messages) != 3 || messages[0].ID != 7 || messages[2].ID != 9 {
		t.Fatalf("сообщения %v, want 7, 8, 9", []int64{messages[0].ID, messages[1].ID, messages[2].ID})
	}
}

// TDLib при offset_ = 0 начинает ровно с from_message_id, а оно у ленты уже есть:
// повторно отдавать его — значит показать дубль.
func TestGetMessagesBeforeDropsTheStartingMessage(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{getChatHistoryAnswer(10, 9, 8)}

	messages, err := GetMessagesBefore(context.Background(), mock, 7, 10, 50)
	if err != nil {
		t.Fatalf("GetMessagesBefore: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("сообщений %d, want 2 без самого from_message_id", len(messages))
	}
	for _, message := range messages {
		if message.ID == 10 {
			t.Fatal("from_message_id попал в результат вторым разом: в ленте будет дубль")
		}
	}
}

// Пустой ответ — сигнал «дочитан»: лента запоминает его и больше не спрашивает. Ответ
// короче запрошенного сигналом не является: документация td_api прямо говорит, что
// число возвращённых сообщений TDLib выбирает сам ради скорости.
func TestGetMessagesBeforeReturnsNothingWhenHistoryEnds(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{getChatHistoryAnswer(10)}

	messages, err := GetMessagesBefore(context.Background(), mock, 7, 10, 50)
	if err != nil {
		t.Fatalf("GetMessagesBefore: %v", err)
	}
	if len(messages) != 0 {
		t.Fatalf("сообщений %d, want 0: история кончилась", len(messages))
	}
}

func TestGetMessagesBeforeHistoryError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{{"@type": "error", "message": "Chat not found"}}

	if _, err := GetMessagesBefore(context.Background(), mock, 7, 10, 50); err == nil {
		t.Fatal("ошибка getChatHistory прошла как успех: чат помечался бы дочитанным")
	}
}
