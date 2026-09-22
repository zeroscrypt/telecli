package auth

import (
	"context"
	"reflect"
	"testing"
)

func TestOpenChatSuccess(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{{"@type": "ok"}}

	if err := OpenChat(context.Background(), mock, 42); err != nil {
		t.Fatalf("OpenChat failed: %v", err)
	}
	if mock.sendCount != 1 {
		t.Errorf("expected 1 send, got %d", mock.sendCount)
	}
}

func TestOpenChatError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{{"@type": "error", "code": 400, "message": "CHAT_NOT_FOUND"}}

	err := OpenChat(context.Background(), mock, 42)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "openChat failed") || !contains(err.Error(), "CHAT_NOT_FOUND") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestCloseChatSuccess(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{{"@type": "ok"}}

	if err := CloseChat(context.Background(), mock, 42); err != nil {
		t.Fatalf("CloseChat failed: %v", err)
	}
	if mock.sendCount != 1 {
		t.Errorf("expected 1 send, got %d", mock.sendCount)
	}
}

func TestCloseChatError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{{"@type": "error", "code": 400, "message": "CHAT_NOT_FOUND"}}

	err := CloseChat(context.Background(), mock, 42)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "closeChat failed") || !contains(err.Error(), "CHAT_NOT_FOUND") {
		t.Errorf("unexpected error: %v", err)
	}
}

// validNewMessageUpdate строит "сырой" апдейт updateNewMessage с исходящим
// текстовым сообщением (исходящее резолвится как "Вы" без доп. Send-вызовов —
// так тест изолирует разбор апдейта от резолва отправителя).
func validNewMessageUpdate(chatID float64, msgID float64, text string) map[string]interface{} {
	return map[string]interface{}{
		"@type": "updateNewMessage",
		"message": map[string]interface{}{
			"@type":       "message",
			"chat_id":     chatID,
			"id":          msgID,
			"is_outgoing": true,
			"date":        float64(300),
			"content": map[string]interface{}{
				"@type": "messageText",
				"text": map[string]interface{}{
					"@type": "formattedText",
					"text":  text,
				},
			},
		},
	}
}

func TestParseNewMessageUpdateValid(t *testing.T) {
	mock := newMockTDClient()
	update := validNewMessageUpdate(777, 42, "привет")

	chatID, msg, ok := ParseNewMessageUpdate(context.Background(), mock, update)
	if !ok {
		t.Fatal("expected ok == true for valid updateNewMessage")
	}
	if chatID != 777 {
		t.Errorf("expected chatID 777, got %d", chatID)
	}
	want := Message{ID: 42, SenderName: "Вы", IsOutgoing: true, Date: 300, Text: "привет"}
	if !reflect.DeepEqual(msg, want) {
		t.Errorf("unexpected message:\n got: %+v\nwant: %+v", msg, want)
	}
	// Исходящее сообщение — без доп. Send-вызовов на резолв отправителя.
	if mock.sendCount != 0 {
		t.Errorf("expected 0 sends, got %d", mock.sendCount)
	}
}

func TestParseNewMessageUpdateWrongType(t *testing.T) {
	mock := newMockTDClient()
	update := validNewMessageUpdate(777, 42, "привет")
	update["@type"] = "updateAuthorizationState"

	chatID, msg, ok := ParseNewMessageUpdate(context.Background(), mock, update)
	if ok {
		t.Fatal("expected ok == false for wrong @type")
	}
	if chatID != 0 || msg != (Message{}) {
		t.Errorf("expected zero value on failure, got chatID=%d msg=%+v", chatID, msg)
	}
}

func TestParseNewMessageUpdateMissingMessage(t *testing.T) {
	mock := newMockTDClient()
	update := map[string]interface{}{"@type": "updateNewMessage"}

	if _, _, ok := ParseNewMessageUpdate(context.Background(), mock, update); ok {
		t.Fatal("expected ok == false when message field is missing")
	}
}

func TestParseNewMessageUpdateBrokenMessage(t *testing.T) {
	mock := newMockTDClient()
	update := map[string]interface{}{
		"@type":   "updateNewMessage",
		"message": "не объект",
	}

	if _, _, ok := ParseNewMessageUpdate(context.Background(), mock, update); ok {
		t.Fatal("expected ok == false when message is not an object")
	}
}

func TestParseNewMessageUpdateMissingChatID(t *testing.T) {
	mock := newMockTDClient()
	update := validNewMessageUpdate(777, 42, "привет")
	delete(update["message"].(map[string]interface{}), "chat_id")

	if _, _, ok := ParseNewMessageUpdate(context.Background(), mock, update); ok {
		t.Fatal("expected ok == false when chat_id is missing")
	}
}

func TestParseNewMessageUpdateBrokenChatID(t *testing.T) {
	mock := newMockTDClient()
	update := validNewMessageUpdate(777, 42, "привет")
	update["message"].(map[string]interface{})["chat_id"] = "777"

	if _, _, ok := ParseNewMessageUpdate(context.Background(), mock, update); ok {
		t.Fatal("expected ok == false when chat_id is not a number")
	}
}
