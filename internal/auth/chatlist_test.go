package auth

import (
	"context"
	"testing"
)

// TestGetChatsUsesProvidedChatList проверяет, что GetChats передаёт в
// loadChats/getChats именно переданный chat_list (например, chatListFolder), а
// не хардкодит chatListMain.
func TestGetChatsUsesProvidedChatList(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "ok"},
		{"@type": "chats", "chat_ids": []interface{}{}},
	}

	chatList := map[string]interface{}{"@type": "chatListFolder", "chat_folder_id": 7}
	_, err := GetChats(context.Background(), mock, chatList, 10)
	if err != nil {
		t.Fatalf("GetChats failed: %v", err)
	}

	if len(mock.requests) < 2 {
		t.Fatalf("expected at least 2 requests (loadChats+getChats), got %d", len(mock.requests))
	}
	for _, req := range mock.requests {
		switch req["@type"] {
		case "loadChats", "getChats":
			got, ok := req["chat_list"].(map[string]interface{})
			if !ok {
				t.Fatalf("request %v has no chat_list", req["@type"])
			}
			if got["@type"] != "chatListFolder" || got["chat_folder_id"] != 7 {
				t.Errorf("request %v: expected chat_list=chatListFolder(7), got %#v", req["@type"], got)
			}
		default:
			t.Errorf("unexpected request type %v", req["@type"])
		}
	}
}
