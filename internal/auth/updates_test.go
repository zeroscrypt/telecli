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

func TestParseChatReadInboxUpdateValid(t *testing.T) {
	update := map[string]interface{}{
		"@type":                      "updateChatReadInbox",
		"chat_id":                    float64(42),
		"last_read_inbox_message_id": float64(100),
		"unread_count":               float64(5),
	}
	chatID, unreadCount, ok := ParseChatReadInboxUpdate(update)
	if !ok {
		t.Fatal("expected ok == true for valid updateChatReadInbox")
	}
	if chatID != 42 {
		t.Errorf("expected chatID 42, got %d", chatID)
	}
	if unreadCount != 5 {
		t.Errorf("expected unreadCount 5, got %d", unreadCount)
	}
}

func TestParseChatReadInboxUpdateWrongType(t *testing.T) {
	update := map[string]interface{}{"@type": "updateAuthorizationState"}
	if _, _, ok := ParseChatReadInboxUpdate(update); ok {
		t.Fatal("expected ok == false for wrong @type")
	}
}

func TestParseChatReadInboxUpdateMissingFields(t *testing.T) {
	// Нет chat_id.
	update := map[string]interface{}{"@type": "updateChatReadInbox", "unread_count": float64(5)}
	if _, _, ok := ParseChatReadInboxUpdate(update); ok {
		t.Fatal("expected ok == false when chat_id missing")
	}
	// Нет unread_count.
	update = map[string]interface{}{"@type": "updateChatReadInbox", "chat_id": float64(42)}
	if _, _, ok := ParseChatReadInboxUpdate(update); ok {
		t.Fatal("expected ok == false when unread_count missing")
	}
	// chat_id не число.
	update = map[string]interface{}{"@type": "updateChatReadInbox", "chat_id": "42", "unread_count": float64(5)}
	if _, _, ok := ParseChatReadInboxUpdate(update); ok {
		t.Fatal("expected ok == false when chat_id is not a number")
	}
}

func TestParseChatReadOutboxUpdateValid(t *testing.T) {
	update := map[string]interface{}{
		"@type":                       "updateChatReadOutbox",
		"chat_id":                     float64(42),
		"last_read_outbox_message_id": float64(123456789),
	}
	chatID, lastReadOutboxMessageID, ok := ParseChatReadOutboxUpdate(update)
	if !ok {
		t.Fatal("expected ok == true for valid updateChatReadOutbox")
	}
	if chatID != 42 {
		t.Errorf("expected chatID 42, got %d", chatID)
	}
	if lastReadOutboxMessageID != 123456789 {
		t.Errorf("expected lastReadOutboxMessageID 123456789, got %d", lastReadOutboxMessageID)
	}
}

func TestParseChatReadOutboxUpdateWrongType(t *testing.T) {
	update := map[string]interface{}{"@type": "updateAuthorizationState"}
	if _, _, ok := ParseChatReadOutboxUpdate(update); ok {
		t.Fatal("expected ok == false for wrong @type")
	}
}

func TestParseChatReadOutboxUpdateMissingFields(t *testing.T) {
	// Нет chat_id.
	update := map[string]interface{}{"@type": "updateChatReadOutbox", "last_read_outbox_message_id": float64(100)}
	if _, _, ok := ParseChatReadOutboxUpdate(update); ok {
		t.Fatal("expected ok == false when chat_id missing")
	}
	// Нет last_read_outbox_message_id.
	update = map[string]interface{}{"@type": "updateChatReadOutbox", "chat_id": float64(42)}
	if _, _, ok := ParseChatReadOutboxUpdate(update); ok {
		t.Fatal("expected ok == false when last_read_outbox_message_id missing")
	}
	// chat_id не число.
	update = map[string]interface{}{"@type": "updateChatReadOutbox", "chat_id": "42", "last_read_outbox_message_id": float64(100)}
	if _, _, ok := ParseChatReadOutboxUpdate(update); ok {
		t.Fatal("expected ok == false when chat_id is not a number")
	}
}

func TestParseChatTitleUpdateValid(t *testing.T) {
	update := map[string]interface{}{
		"@type":   "updateChatTitle",
		"chat_id": float64(42),
		"title":   "Иван Петров",
	}
	chatID, title, ok := ParseChatTitleUpdate(update)
	if !ok {
		t.Fatal("expected ok == true for valid updateChatTitle")
	}
	if chatID != 42 {
		t.Errorf("expected chatID 42, got %d", chatID)
	}
	if title != "Иван Петров" {
		t.Errorf("expected title \"Иван Петров\", got %q", title)
	}
}

func TestParseChatTitleUpdateWrongType(t *testing.T) {
	update := map[string]interface{}{"@type": "updateAuthorizationState"}
	if _, _, ok := ParseChatTitleUpdate(update); ok {
		t.Fatal("expected ok == false for wrong @type")
	}
}

func TestParseChatTitleUpdateMissingFields(t *testing.T) {
	// Нет chat_id.
	update := map[string]interface{}{"@type": "updateChatTitle", "title": "Иван"}
	if _, _, ok := ParseChatTitleUpdate(update); ok {
		t.Fatal("expected ok == false when chat_id missing")
	}
	// Нет title.
	update = map[string]interface{}{"@type": "updateChatTitle", "chat_id": float64(42)}
	if _, _, ok := ParseChatTitleUpdate(update); ok {
		t.Fatal("expected ok == false when title missing")
	}
	// chat_id не число (int53 в JSON TDLib приходят как float64).
	update = map[string]interface{}{"@type": "updateChatTitle", "chat_id": "42", "title": "Иван"}
	if _, _, ok := ParseChatTitleUpdate(update); ok {
		t.Fatal("expected ok == false when chat_id is not a number")
	}
	// title не строка.
	update = map[string]interface{}{"@type": "updateChatTitle", "chat_id": float64(42), "title": float64(1)}
	if _, _, ok := ParseChatTitleUpdate(update); ok {
		t.Fatal("expected ok == false when title is not a string")
	}
}

func TestParseUnreadMessageCountUpdateMain(t *testing.T) {
	update := map[string]interface{}{
		"@type": "updateUnreadMessageCount",
		"chat_list": map[string]interface{}{
			"@type": "chatListMain",
		},
		"unread_count":         float64(3),
		"unread_unmuted_count": float64(2),
	}
	folderID, unreadCount, ok := ParseUnreadMessageCountUpdate(update)
	if !ok {
		t.Fatal("expected ok == true for chatListMain")
	}
	if folderID != 0 {
		t.Errorf("expected folderID 0 (Все чаты), got %d", folderID)
	}
	// unread_count (3, ВКЛЮЧАЯ замьюченные), НЕ unread_unmuted_count (2) —
	// по живой проверке человека: у "Все чаты" в самом Telegram бейдж
	// считает сумму непрочитанных сообщений без фильтра по mute (в отличие
	// от папок — там другая метрика, число чатов, см.
	// TestParseUnreadChatCountUpdateFolder).
	if unreadCount != 3 {
		t.Errorf("expected unreadCount 3 (unread_count), got %d", unreadCount)
	}
}

func TestParseUnreadMessageCountUpdateFolder(t *testing.T) {
	// Парсер технически разбирает chatListFolder тоже (для полноты), но
	// результат для folderID != 0 в internal/tui игнорируется — папки
	// берут бейдж из ParseUnreadChatCountUpdate (другая метрика).
	update := map[string]interface{}{
		"@type": "updateUnreadMessageCount",
		"chat_list": map[string]interface{}{
			"@type":          "chatListFolder",
			"chat_folder_id": float64(9),
		},
		"unread_count": float64(517),
	}
	folderID, unreadCount, ok := ParseUnreadMessageCountUpdate(update)
	if !ok {
		t.Fatal("expected ok == true for chatListFolder")
	}
	if folderID != 9 {
		t.Errorf("expected folderID 9, got %d", folderID)
	}
	if unreadCount != 517 {
		t.Errorf("expected unreadCount 517, got %d", unreadCount)
	}
}

func TestParseUnreadMessageCountUpdateArchive(t *testing.T) {
	// chatListArchive этой задачей не показывается — ok == false.
	update := map[string]interface{}{
		"@type": "updateUnreadMessageCount",
		"chat_list": map[string]interface{}{
			"@type": "chatListArchive",
		},
		"unread_count": float64(1),
	}
	if _, _, ok := ParseUnreadMessageCountUpdate(update); ok {
		t.Fatal("expected ok == false for chatListArchive")
	}
}

func TestParseUnreadMessageCountUpdateWrongType(t *testing.T) {
	update := map[string]interface{}{"@type": "updateAuthorizationState"}
	if _, _, ok := ParseUnreadMessageCountUpdate(update); ok {
		t.Fatal("expected ok == false for wrong @type")
	}
}

func TestParseUnreadMessageCountUpdateMissingFields(t *testing.T) {
	// Нет unread_count.
	update := map[string]interface{}{
		"@type":     "updateUnreadMessageCount",
		"chat_list": map[string]interface{}{"@type": "chatListMain"},
	}
	if _, _, ok := ParseUnreadMessageCountUpdate(update); ok {
		t.Fatal("expected ok == false when unread_count missing")
	}
	// Нет chat_list.
	update = map[string]interface{}{"@type": "updateUnreadMessageCount", "unread_count": float64(2)}
	if _, _, ok := ParseUnreadMessageCountUpdate(update); ok {
		t.Fatal("expected ok == false when chat_list missing")
	}
	// chat_listFolder без chat_folder_id.
	update = map[string]interface{}{
		"@type":        "updateUnreadMessageCount",
		"chat_list":    map[string]interface{}{"@type": "chatListFolder"},
		"unread_count": float64(2),
	}
	if _, _, ok := ParseUnreadMessageCountUpdate(update); ok {
		t.Fatal("expected ok == false when chat_folder_id missing")
	}
}

// TestParseUnreadChatCountUpdateFolder — папка (не "Все чаты"): бейдж — ЧИСЛО
// ЧАТОВ с непрочитанным (unread_count у updateUnreadChatCount), ВКЛЮЧАЯ
// замьюченные — по живой проверке человека (12 замьюченных + 1 незамьюченный
// чат с непрочитанным = "13" в самом Telegram).
func TestParseUnreadChatCountUpdateFolder(t *testing.T) {
	update := map[string]interface{}{
		"@type": "updateUnreadChatCount",
		"chat_list": map[string]interface{}{
			"@type":          "chatListFolder",
			"chat_folder_id": float64(9),
		},
		"total_count":          float64(50),
		"unread_count":         float64(13),
		"unread_unmuted_count": float64(1),
	}
	folderID, chatCount, ok := ParseUnreadChatCountUpdate(update)
	if !ok {
		t.Fatal("expected ok == true for chatListFolder")
	}
	if folderID != 9 {
		t.Errorf("expected folderID 9, got %d", folderID)
	}
	// unread_count (13, ВКЛЮЧАЯ замьюченные), НЕ unread_unmuted_count (1).
	if chatCount != 13 {
		t.Errorf("expected chatCount 13 (unread_count, includes muted), got %d", chatCount)
	}
}

func TestParseUnreadChatCountUpdateMain(t *testing.T) {
	update := map[string]interface{}{
		"@type":        "updateUnreadChatCount",
		"chat_list":    map[string]interface{}{"@type": "chatListMain"},
		"unread_count": float64(42),
	}
	folderID, chatCount, ok := ParseUnreadChatCountUpdate(update)
	if !ok {
		t.Fatal("expected ok == true for chatListMain")
	}
	if folderID != 0 {
		t.Errorf("expected folderID 0, got %d", folderID)
	}
	if chatCount != 42 {
		t.Errorf("expected chatCount 42, got %d", chatCount)
	}
}

func TestParseUnreadChatCountUpdateArchive(t *testing.T) {
	update := map[string]interface{}{
		"@type":        "updateUnreadChatCount",
		"chat_list":    map[string]interface{}{"@type": "chatListArchive"},
		"unread_count": float64(1),
	}
	if _, _, ok := ParseUnreadChatCountUpdate(update); ok {
		t.Fatal("expected ok == false for chatListArchive")
	}
}

func TestParseUnreadChatCountUpdateWrongType(t *testing.T) {
	update := map[string]interface{}{"@type": "updateAuthorizationState"}
	if _, _, ok := ParseUnreadChatCountUpdate(update); ok {
		t.Fatal("expected ok == false for wrong @type")
	}
}

func TestParseUnreadChatCountUpdateMissingFields(t *testing.T) {
	// Нет unread_count.
	update := map[string]interface{}{
		"@type":     "updateUnreadChatCount",
		"chat_list": map[string]interface{}{"@type": "chatListMain"},
	}
	if _, _, ok := ParseUnreadChatCountUpdate(update); ok {
		t.Fatal("expected ok == false when unread_count missing")
	}
	// Нет chat_list.
	update = map[string]interface{}{"@type": "updateUnreadChatCount", "unread_count": float64(2)}
	if _, _, ok := ParseUnreadChatCountUpdate(update); ok {
		t.Fatal("expected ok == false when chat_list missing")
	}
	// chatListFolder без chat_folder_id.
	update = map[string]interface{}{
		"@type":        "updateUnreadChatCount",
		"chat_list":    map[string]interface{}{"@type": "chatListFolder"},
		"unread_count": float64(2),
	}
	if _, _, ok := ParseUnreadChatCountUpdate(update); ok {
		t.Fatal("expected ok == false when chat_folder_id missing")
	}
}
