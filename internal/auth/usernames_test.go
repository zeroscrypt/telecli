package auth

import (
	"context"
	"strings"
	"testing"
)

// TestSearchPublicChatAsksByBareUsername — ник уходит в TDLib без ведущей «@»:
// TDLib ники всегда без неё, и запрос с «@» отвергается ошибкой (сверено с
// документацией td_api к searchPublicChat).
func TestSearchPublicChatAsksByBareUsername(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{publicChatAnswer(4242, "Хакатао", "chatTypePrivate", false)}

	chat, err := SearchPublicChat(context.Background(), mock, "@hakatao")
	if err != nil {
		t.Fatalf("SearchPublicChat: %v", err)
	}
	if len(mock.requests) != 1 {
		t.Fatalf("запросов %d, want 1", len(mock.requests))
	}
	request := mock.requests[0]
	if request["@type"] != "searchPublicChat" {
		t.Fatalf("тип запроса %q, want searchPublicChat", request["@type"])
	}
	if request["username"] != "hakatao" {
		t.Fatalf("username %q, want %q без @", request["username"], "hakatao")
	}
	if chat.ID != 4242 || chat.Title != "Хакатао" {
		t.Fatalf("чат = %d %q, want 4242 Хакатао", chat.ID, chat.Title)
	}
	if chat.Kind != ChatPrivate {
		t.Fatalf("категория %v, want личный чат", chat.Kind)
	}
}

// Ник без «@» — тоже рабочая форма: человек может набрать и так.
func TestSearchPublicChatAcceptsANameWithoutTheAtSign(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{publicChatAnswer(7, "Канал", "chatTypeSupergroup", true)}

	chat, err := SearchPublicChat(context.Background(), mock, "hakatao")
	if err != nil {
		t.Fatalf("SearchPublicChat: %v", err)
	}
	if mock.requests[0]["username"] != "hakatao" {
		t.Fatalf("username %q, want hakatao", mock.requests[0]["username"])
	}
	if chat.Kind != ChatChannel {
		t.Fatalf("категория %v, want канал", chat.Kind)
	}
}

// Ошибка TDLib показывается человеку как есть: «chat not found» без перевода
// ничего не объяснит.
func TestSearchPublicChatReportsTheTDLibError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "error", "message": "Chat not found"},
	}

	_, err := SearchPublicChat(context.Background(), mock, "@hakatao")
	if err == nil {
		t.Fatal("ошибка TDLib не дошла до вызывающего")
	}
	if !strings.Contains(err.Error(), "Chat not found") {
		t.Fatalf("ошибка %q, want текст TDLib", err)
	}
}

// Пустой ник и отсутствие клиента — ошибки до запроса: ни то, ни другое нечего
// отправлять в TDLib.
func TestSearchPublicChatRefusesEmptyUsernameAndNilClient(t *testing.T) {
	mock := newMockTDClient()
	if _, err := SearchPublicChat(context.Background(), mock, " @ "); err == nil {
		t.Fatal("пустой ник принят как запрос")
	}
	if len(mock.requests) != 0 {
		t.Fatalf("запросов %d для пустого ника, want 0", len(mock.requests))
	}
	if _, err := SearchPublicChat(context.Background(), nil, "@hakatao"); err == nil {
		t.Fatal("запрос без клиента прошёл")
	}
}

// Ответ без чата (не ошибка и не chat) — тоже «не найден», а не пустой чат с нулём:
// с нулём в поле отправителя сообщение ушло бы не туда.
func TestSearchPublicChatRejectsAnAnswerThatIsNotAChat(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{{"@type": "ok"}}

	if _, err := SearchPublicChat(context.Background(), mock, "@hakatao"); err == nil {
		t.Fatal("ответ без чата принят как найденный чат")
	}
}

// publicChatAnswer собирает ответ searchPublicChat. Канал в TDLib — это
// chatTypeSupergroup с is_channel=true в том же объекте, отдельного типа
// «канал» в схеме нет, поэтому флаг isChannel и есть единственный признак.
func publicChatAnswer(id int64, title, chatType string, isChannel bool) map[string]interface{} {
	chatTypeObject := map[string]interface{}{"@type": chatType}
	if isChannel {
		chatTypeObject["is_channel"] = true
	}
	return map[string]interface{}{
		"@type": "chat",
		"id":    float64(id),
		"title": title,
		"type":  chatTypeObject,
	}
}
