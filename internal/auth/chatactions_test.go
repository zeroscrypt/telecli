package auth

import (
	"context"
	"strings"
	"testing"
)

// TestLeaveChatRequest проверяет, что LeaveChat отправляет ровно
// {"@type":"leaveChat","chat_id":<id>} и пробрасывает ошибку Send.
func TestLeaveChatRequest(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{{"@type": "ok"}}

	if err := LeaveChat(context.Background(), mock, 42); err != nil {
		t.Fatalf("LeaveChat failed: %v", err)
	}
	if len(mock.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(mock.requests))
	}
	req := mock.requests[0]
	if req["@type"] != "leaveChat" {
		t.Errorf("expected @type leaveChat, got %v", req["@type"])
	}
	if req["chat_id"] != int64(42) {
		t.Errorf("expected chat_id 42, got %v", req["chat_id"])
	}
}

func TestLeaveChatError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{{"@type": "error", "code": 400, "message": "Chat not found"}}

	err := LeaveChat(context.Background(), mock, 42)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "leaveChat") {
		t.Errorf("expected wrapped leaveChat error, got %v", err)
	}
}

// TestDeleteChatHistoryRequest проверяет форму записи deleteChatHistory:
// remove_from_chat_list всегда true, revoke передаётся как есть.
func TestDeleteChatHistoryRequest(t *testing.T) {
	for _, tc := range []struct {
		name   string
		revoke bool
	}{
		{name: "revoke_true", revoke: true},
		{name: "revoke_false", revoke: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := newMockTDClient()
			mock.responses = []map[string]interface{}{{"@type": "ok"}}

			if err := DeleteChatHistory(context.Background(), mock, 7, tc.revoke); err != nil {
				t.Fatalf("DeleteChatHistory failed: %v", err)
			}
			if len(mock.requests) != 1 {
				t.Fatalf("expected 1 request, got %d", len(mock.requests))
			}
			req := mock.requests[0]
			if req["@type"] != "deleteChatHistory" {
				t.Errorf("expected @type deleteChatHistory, got %v", req["@type"])
			}
			if req["chat_id"] != int64(7) {
				t.Errorf("expected chat_id 7, got %v", req["chat_id"])
			}
			if got, ok := req["remove_from_chat_list"].(bool); !ok || !got {
				t.Errorf("expected remove_from_chat_list=true, got %v", req["remove_from_chat_list"])
			}
			if got, ok := req["revoke"].(bool); !ok || got != tc.revoke {
				t.Errorf("expected revoke=%v, got %v", tc.revoke, req["revoke"])
			}
		})
	}
}

func TestDeleteChatHistoryError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{{"@type": "error", "code": 400, "message": "bad"}}

	err := DeleteChatHistory(context.Background(), mock, 7, true)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "deleteChatHistory") {
		t.Errorf("expected wrapped deleteChatHistory error, got %v", err)
	}
}
