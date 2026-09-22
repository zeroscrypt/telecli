package auth

import (
	"context"
	"testing"
	"time"
)

// setInstantRetryDelays включает retry-цикл с мгновенными задержками, чтобы
// тесты не ждали реальные секунды. TestMain оставляет historyRetryDelays == nil
// (retry выключен) для legacy-тестов одного запроса; здесь же он включается, а
// исходное значение возвращается через t.Cleanup.
func setInstantRetryDelays(t *testing.T) {
	t.Helper()
	original := historyRetryDelays
	historyRetryDelays = []time.Duration{time.Microsecond, time.Microsecond, time.Microsecond}
	t.Cleanup(func() { historyRetryDelays = original })
}

// historyResponse собирает ответ getChatHistory из исходящих сообщений
// (is_outgoing=true — резолв отправителя не делает сетевых вызовов, поэтому
// каждый ответ расходует ровно один Send).
func historyResponse(ids ...int64) map[string]interface{} {
	messages := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		messages = append(messages, map[string]interface{}{
			"@type":       "message",
			"id":          float64(id),
			"is_outgoing": true,
			"date":        float64(id),
			"content": map[string]interface{}{
				"@type": "messageText",
				"text": map[string]interface{}{
					"@type": "formattedText",
					"text":  "текст",
				},
			},
		})
	}
	return map[string]interface{}{
		"@type":    "messages",
		"messages": messages,
	}
}

func TestGetMessagesRetriesOnIncompleteHistory(t *testing.T) {
	setInstantRetryDelays(t)
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		historyResponse(1),
		historyResponse(3, 2, 1),
	}

	got, err := GetMessages(context.Background(), mock, 42, 3)
	if err != nil {
		t.Fatalf("GetMessages failed: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("expected 3 messages after retry, got %d: %+v", len(got), got)
	}
	wantIDs := []int64{1, 2, 3}
	for i, id := range wantIDs {
		if got[i].ID != id {
			t.Errorf("message %d: got id %d, want %d", i, got[i].ID, id)
		}
	}
	if mock.sendCount != 2 {
		t.Errorf("expected 2 sends (initial + 1 retry), got %d", mock.sendCount)
	}
}

func TestGetMessagesStopsRetryingOnFullResponse(t *testing.T) {
	setInstantRetryDelays(t)
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		historyResponse(2, 1),
	}

	got, err := GetMessages(context.Background(), mock, 42, 2)
	if err != nil {
		t.Fatalf("GetMessages failed: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(got), got)
	}
	if mock.sendCount != 1 {
		t.Errorf("expected exactly 1 send when response already full, got %d", mock.sendCount)
	}
}

func TestGetMessagesReturnsLastResultAfterExhaustingRetries(t *testing.T) {
	setInstantRetryDelays(t)
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		historyResponse(1),
		historyResponse(2),
		historyResponse(3),
		historyResponse(4),
	}

	got, err := GetMessages(context.Background(), mock, 42, 50)
	if err != nil {
		t.Fatalf("GetMessages failed: %v", err)
	}
	if len(got) != 1 || got[0].ID != 4 {
		t.Errorf("expected last retry result (id=4), got %+v", got)
	}
	if mock.sendCount != 4 {
		t.Errorf("expected 4 sends (initial + 3 retries), got %d", mock.sendCount)
	}
}

func TestGetMessagesRetryErrorDoesNotLoseEarlierResult(t *testing.T) {
	setInstantRetryDelays(t)
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		historyResponse(1),
		{"@type": "error", "code": 500, "message": "NETWORK"},
	}

	got, err := GetMessages(context.Background(), mock, 42, 50)
	if err != nil {
		t.Fatalf("GetMessages failed: %v", err)
	}
	if len(got) != 1 || got[0].ID != 1 {
		t.Errorf("expected first (successful) result preserved, got %+v", got)
	}
	if mock.sendCount != 2 {
		t.Errorf("expected 2 sends (initial + failing retry), got %d", mock.sendCount)
	}
}
