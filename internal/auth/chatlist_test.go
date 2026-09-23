package auth

import (
	"context"
	"testing"
)

// TestGetChatsUsesProvidedChatList проверяет, что GetChats передаёт в
// loadChats/getChats именно переданный chat_list (например, chatListFolder), а
// не хардкодит chatListMain. По совместимости проверяет и заполнение
// Chat.UnreadCount из поля unread_count ответа getChat (задача 0028).
func TestGetChatsUsesProvidedChatList(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "ok"},
		{"@type": "chats", "chat_ids": []interface{}{float64(7)}},
		{"@type": "chat", "chat_id": float64(7), "title": "Чат семь", "unread_count": float64(5)},
	}

	chatList := map[string]interface{}{"@type": "chatListFolder", "chat_folder_id": 7}
	chats, err := GetChats(context.Background(), mock, chatList, 10)
	if err != nil {
		t.Fatalf("GetChats failed: %v", err)
	}
	if len(chats) != 1 {
		t.Fatalf("expected 1 chat, got %d: %+v", len(chats), chats)
	}
	if chats[0].ID != 7 || chats[0].Title != "Чат семь" {
		t.Errorf("unexpected chat: %+v", chats[0])
	}
	if chats[0].UnreadCount != 5 {
		t.Errorf("expected UnreadCount 5 from getChat response, got %d", chats[0].UnreadCount)
	}

	if len(mock.requests) < 3 {
		t.Fatalf("expected at least 3 requests (loadChats+getChats+getChat), got %d", len(mock.requests))
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
		case "getChat":
			// отдельный запрос на детали чата — chat_list ему не нужен, пропускаем
		default:
			t.Errorf("unexpected request type %v", req["@type"])
		}
	}
}

// TestGetChatsFillsIsGroup проверяет разбор типа чата из ответа getChat:
// chatTypePrivate/chatTypeSecret → IsGroup=false (операция deleteChatHistory),
// chatTypeBasicGroup/chatTypeSupergroup → IsGroup=true (операция leaveChat).
// Неизвестный тип безопасно деградирует в false (личный чат — осторожная
// консервативная трактовка: там необратимое действие не для всех).
func TestGetChatsFillsIsGroup(t *testing.T) {
	cases := []struct {
		name     string
		chatType string
		want     bool
	}{
		{name: "private", chatType: "chatTypePrivate", want: false},
		{name: "secret", chatType: "chatTypeSecret", want: false},
		{name: "basic_group", chatType: "chatTypeBasicGroup", want: true},
		{name: "supergroup", chatType: "chatTypeSupergroup", want: true},
		{name: "unknown_type", chatType: "chatTypeChannel", want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newMockTDClient()
			mock.responses = []map[string]interface{}{
				{"@type": "ok"},
				{"@type": "chats", "chat_ids": []interface{}{float64(111)}},
				{
					"@type": "chat",
					"id":    float64(111),
					"title": "Чат",
					"type":  map[string]interface{}{"@type": tc.chatType},
				},
			}

			chats, err := GetChats(context.Background(), mock, map[string]interface{}{"@type": "chatListMain"}, 10)
			if err != nil {
				t.Fatalf("GetChats failed: %v", err)
			}
			if len(chats) != 1 {
				t.Fatalf("expected 1 chat, got %d", len(chats))
			}
			if chats[0].IsGroup != tc.want {
				t.Errorf("chat type %s: expected IsGroup=%v, got %v", tc.chatType, tc.want, chats[0].IsGroup)
			}
		})
	}
}
