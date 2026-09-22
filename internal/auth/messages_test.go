package auth

import (
	"context"
	"os"
	"reflect"
	"testing"
)

// TestMain отключает retry-задержки истории по умолчанию: тесты ниже проверяют
// логику одного запроса (fetchChatHistoryOnce), и без этого каждый из них при
// limit больше фактического числа сообщений делал бы лишние повторы с реальными
// задержками. Тесты самого retry (messages_retry_test.go) включают его сами.
func TestMain(m *testing.M) {
	historyRetryDelays = nil
	os.Exit(m.Run())
}

func TestGetMessagesParsesAndReverses(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{
			"@type":       "messages",
			"total_count": 4,
			"messages": []interface{}{
				// TDLib отдаёт новые первыми — здесь порядок id 11, 10, 9, 8.
				map[string]interface{}{
					"@type":       "message",
					"id":          float64(11),
					"is_outgoing": false,
					"date":        float64(200),
					"sender_id": map[string]interface{}{
						"@type":   "messageSenderChat",
						"chat_id": float64(777),
					},
					"content": map[string]interface{}{
						"@type": "messageText",
						"text": map[string]interface{}{
							"@type": "formattedText",
							"text":  "объявление",
						},
					},
				},
				map[string]interface{}{
					"@type":       "message",
					"id":          float64(10),
					"is_outgoing": false,
					"date":        float64(150),
					"sender_id": map[string]interface{}{
						"@type":   "messageSenderUser",
						"user_id": float64(1),
					},
					"content": map[string]interface{}{
						"@type": "messageText",
						"text": map[string]interface{}{
							"@type": "formattedText",
							"text":  "привет",
						},
					},
				},
				map[string]interface{}{
					"@type":       "message",
					"id":          float64(9),
					"is_outgoing": false,
					"date":        float64(100),
					"sender_id": map[string]interface{}{
						"@type":   "messageSenderUser",
						"user_id": float64(2),
					},
					"content": map[string]interface{}{
						"@type": "messagePhoto",
						"photo": map[string]interface{}{},
					},
				},
				map[string]interface{}{
					"@type":       "message",
					"id":          float64(8),
					"is_outgoing": true,
					"date":        float64(50),
					"content": map[string]interface{}{
						"@type": "messageText",
						"text": map[string]interface{}{
							"@type": "formattedText",
							"text":  "исходящее",
						},
					},
				},
			},
		},
		// Разрешение отправителей идёт в том же порядке, в каком TDLib отдал
		// сообщения (новые первыми): id=11 (getChat), затем id=10 (getUser).
		{"@type": "chat", "id": float64(777), "title": "Тестовый канал"},
		{"@type": "user", "id": float64(1), "first_name": "Иван", "last_name": "Петров"},
		// getUser для user_id=2 (сообщение id=9) отвечает ошибкой — резолв
		// имени должен деградировать до user#2, а не ронять весь вызов.
	}

	got, err := GetMessages(context.Background(), mock, 42, 50)
	if err != nil {
		t.Fatalf("GetMessages failed: %v", err)
	}

	want := []Message{
		{ID: 8, SenderName: "Вы", IsOutgoing: true, Date: 50, Text: "исходящее"},
		{ID: 9, SenderName: "user#2", IsOutgoing: false, Date: 100, Text: "[тип сообщения: messagePhoto]"},
		{ID: 10, SenderName: "Иван Петров", IsOutgoing: false, Date: 150, Text: "привет"},
		{ID: 11, SenderName: "Тестовый канал", IsOutgoing: false, Date: 200, Text: "объявление"},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("unexpected result:\n got: %+v\nwant: %+v", got, want)
	}

	// getChatHistory + getUser + getChat + getUser (с ошибкой) = 4 вызова.
	if mock.sendCount != 4 {
		t.Errorf("expected 4 sends, got %d", mock.sendCount)
	}
}

func TestGetMessagesSenderResolveErrorDoesNotFailCall(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{
			"@type":       "messages",
			"total_count": 2,
			"messages": []interface{}{
				map[string]interface{}{
					"@type":       "message",
					"id":          float64(20),
					"is_outgoing": false,
					"date":        float64(10),
					"sender_id": map[string]interface{}{
						"@type":   "messageSenderUser",
						"user_id": float64(5),
					},
					"content": map[string]interface{}{
						"@type": "messageText",
						"text": map[string]interface{}{
							"@type": "formattedText",
							"text":  "текст",
						},
					},
				},
				map[string]interface{}{
					"@type":       "message",
					"id":          float64(19),
					"is_outgoing": false,
					"date":        float64(5),
					"sender_id": map[string]interface{}{
						"@type":   "messageSenderChat",
						"chat_id": float64(999),
					},
					"content": map[string]interface{}{
						"@type": "messageDice",
						"value": float64(5),
					},
				},
			},
		},
	}
	// Ответов на getUser/getChat нет — резолв вернёт ошибку для обоих сообщений.

	got, err := GetMessages(context.Background(), mock, 42, 50)
	if err != nil {
		t.Fatalf("GetMessages failed: %v", err)
	}

	want := []Message{
		{ID: 19, SenderName: "chat#999", IsOutgoing: false, Date: 5, Text: "[тип сообщения: messageDice]"},
		{ID: 20, SenderName: "user#5", IsOutgoing: false, Date: 10, Text: "текст"},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("unexpected result:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestGetMessagesHistoryError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "error", "code": 400, "message": "CHAT_NOT_FOUND"},
	}

	_, err := GetMessages(context.Background(), mock, 42, 50)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "CHAT_NOT_FOUND") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetMessagesUnexpectedResponseType(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "ok"},
	}
	_, err := GetMessages(context.Background(), mock, 42, 50)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSendMessageSuccess(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{
			"@type":       "message",
			"id":          float64(42),
			"is_outgoing": true,
			"date":        float64(300),
			"content": map[string]interface{}{
				"@type": "messageText",
				"text": map[string]interface{}{
					"@type": "formattedText",
					"text":  "привет",
				},
			},
		},
	}

	got, err := SendMessage(context.Background(), mock, 777, "привет")
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	want := Message{ID: 42, SenderName: "Вы", IsOutgoing: true, Date: 300, Text: "привет"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unexpected result:\n got: %+v\nwant: %+v", got, want)
	}

	// Исходящее сообщение резолвится как "Вы" без сетевых вызовов — ровно одни
	// Send (сам sendMessage), никаких getUser/getChat сверх него.
	if mock.sendCount != 1 {
		t.Errorf("expected 1 send, got %d", mock.sendCount)
	}
}

func TestSendMessageError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "error", "code": 400, "message": "CHAT_NOT_FOUND"},
	}

	_, err := SendMessage(context.Background(), mock, 777, "привет")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "sendMessage failed") || !contains(err.Error(), "CHAT_NOT_FOUND") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSendMessageUnexpectedResponseType(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "ok"},
	}

	_, err := SendMessage(context.Background(), mock, 777, "привет")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
