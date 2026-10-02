package auth

import (
	"context"
	"testing"
)

// TestGetAllChatIDsReturnsOnlyIDs — GetAllChatIDs отдаёт id чатов и НЕ дёргает getChat
// на каждый из них. Именно ради этого он и выделен: стене (фильтр по папкам)
// нужен ответ на вопрос «в каких чатах состоит эта папка», и тянуть за ним полные
// объекты чатов — сотни лишних запросов getChat на аккаунте с сотнями чатов.
//
// Тот же цикл докачки loadChats, что у GetAllChats, и тот же chat_list: отличие
// только в том, что возвращается.
func TestGetAllChatIDsReturnsOnlyIDs(t *testing.T) {
	mock := newMockTDClient()
	// loadChats отвечает «Not Found» — конец списка; дальше getChats.
	mock.responses = []map[string]interface{}{
		{"@type": "error", "message": "Not Found"},
		{"@type": "chats", "chat_ids": []interface{}{float64(7), float64(9)}},
	}

	chatList := map[string]interface{}{"@type": "chatListFolder", "chat_folder_id": 7}
	ids, err := GetAllChatIDs(context.Background(), mock, chatList)
	if err != nil {
		t.Fatalf("GetAllChatIDs failed: %v", err)
	}
	if len(ids) != 2 || ids[0] != 7 || ids[1] != 9 {
		t.Fatalf("ids = %v, ждали [7 9]", ids)
	}
	for _, req := range mock.requests {
		if req["@type"] == "getChat" {
			t.Fatal("GetAllChatIDs не должен запрашивать объекты чатов")
		}
		if req["@type"] != "loadChats" && req["@type"] != "getChats" {
			t.Fatalf("неожиданный запрос %v", req["@type"])
		}
		got, ok := req["chat_list"].(map[string]interface{})
		if !ok {
			t.Fatalf("запрос %v без chat_list", req["@type"])
		}
		if got["@type"] != "chatListFolder" || got["chat_folder_id"] != 7 {
			t.Errorf("запрос %v: chat_list = %#v, ждали chatListFolder(7)", req["@type"], got)
		}
	}
}

// TestGetAllChatIDsSkipsMalformedIDs — нечисловые элементы chat_ids пропускаются, а
// не превращаются в ноль: id 0 означал бы несуществующий чат, и в фильтре по папкам
// он вёл бы себя как настоящий.
func TestGetAllChatIDsSkipsMalformedIDs(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "error", "message": "Not Found"},
		{"@type": "chats", "chat_ids": []interface{}{"восемь", float64(9)}},
	}

	ids, err := GetAllChatIDs(context.Background(), mock, map[string]interface{}{"@type": "chatListFolder", "chat_folder_id": 7})
	if err != nil {
		t.Fatalf("GetAllChatIDs failed: %v", err)
	}
	if len(ids) != 1 || ids[0] != 9 {
		t.Fatalf("ids = %v, ждали [9]", ids)
	}
}

// TestGetAllChatIDsPropagatesGetChatsError — ошибка самого getChats не
// проглатывается: вызывающий код обязан знать, что состав папки неизвестен, и не
// считать папку пустой по ошибке чтения.
func TestGetAllChatIDsPropagatesGetChatsError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "error", "message": "Not Found"},
		{"@type": "error", "message": "Chat list is loading"},
	}

	_, err := GetAllChatIDs(context.Background(), mock, map[string]interface{}{"@type": "chatListFolder", "chat_folder_id": 7})
	if err == nil {
		t.Fatal("ошибка getChats обязана возвращаться, а не теряться")
	}
}

// TestGetAllChatIDsStopsOnCancelledContext — отменённый контекст останавливает
// докачку и возвращается ошибкой, а не «пустым составом папки».
func TestGetAllChatIDsStopsOnCancelledContext(t *testing.T) {
	mock := newMockTDClient()
	// loadChats отвечает успехом, но контекст к этому моменту уже отменён.
	mock.responses = []map[string]interface{}{{"@type": "ok"}}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := GetAllChatIDs(ctx, mock, map[string]interface{}{"@type": "chatListFolder", "chat_folder_id": 7})
	if err == nil {
		t.Fatal("отменённый контекст должен возвращаться ошибкой")
	}
}

// TestGetChatsUsesProvidedChatList проверяет, что GetChats передаёт в
// loadChats/getChats именно переданный chat_list (например, chatListFolder), а
// не хардкодит chatListMain. По совместимости проверяет и заполнение
// Chat.UnreadCount из поля unread_count ответа getChat (задача 0028).
func TestGetChatsUsesProvidedChatList(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "ok"},
		{"@type": "chats", "chat_ids": []interface{}{float64(7)}},
		{"@type": "chat", "id": float64(7), "title": "Чат семь", "unread_count": float64(5)},
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

// TestGetChatsFallsBackToChatIDOnEmptyTitle проверяет, что чат с пустым
// title (реальный кейс: part chats, пользователи без имени) получает
// фолбэк-заголовок chat#<id> — та же деградация, что в resolveSearchChat
// (баг, найденный первой живой проверкой, 0042).
func TestGetChatsFallsBackToChatIDOnEmptyTitle(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "ok"},
		{"@type": "chats", "chat_ids": []interface{}{float64(42)}},
		{"@type": "chat", "id": float64(42), "title": ""},
	}

	chats, err := GetChats(context.Background(), mock, map[string]interface{}{"@type": "chatListMain"}, 10)
	if err != nil {
		t.Fatalf("GetChats failed: %v", err)
	}
	if len(chats) != 1 {
		t.Fatalf("expected 1 chat, got %d", len(chats))
	}
	if chats[0].Title != "chat#42" {
		t.Errorf("expected fallback title 'chat#42' for empty title, got %q", chats[0].Title)
	}
}

// TestGetChatsFillsChatKind проверяет разбор типа чата из ответа getChat на три
// категории: chatTypePrivate/chatTypeSecret → личный, chatTypeBasicGroup → группа,
// chatTypeSupergroup → группа или канал по полю is_channel. Категория решает, чем
// удалять чат (leaveChat для группы и канала, deleteChatHistory для личного), и по
// ней же фильтрует меню панели.
//
// Отдельного типа «канал» в TDLib нет: канал — это chatTypeSupergroup с
// is_channel=true в том же объекте (сверено с td_api.tl). Разбирать его как
// отдельный тип было бессмысленно: категория «Каналы» в меню оставалась пустой
// всегда. Неизвестный тип по-прежнему деградирует в личный чат (осторожная
// трактовка: необратимое действие не для всех).
func TestGetChatsFillsChatKind(t *testing.T) {
	cases := []struct {
		name      string
		chatType  map[string]interface{}
		want      ChatKind
		wantGroup bool
	}{
		{name: "private", chatType: map[string]interface{}{"@type": "chatTypePrivate"}, want: ChatPrivate, wantGroup: false},
		{name: "secret", chatType: map[string]interface{}{"@type": "chatTypeSecret"}, want: ChatPrivate, wantGroup: false},
		{name: "basic_group", chatType: map[string]interface{}{"@type": "chatTypeBasicGroup"}, want: ChatGroup, wantGroup: true},
		{name: "supergroup_without_is_channel", chatType: map[string]interface{}{"@type": "chatTypeSupergroup"}, want: ChatGroup, wantGroup: true},
		{name: "supergroup_is_channel_false", chatType: map[string]interface{}{"@type": "chatTypeSupergroup", "is_channel": false}, want: ChatGroup, wantGroup: true},
		{name: "channel", chatType: map[string]interface{}{"@type": "chatTypeSupergroup", "is_channel": true}, want: ChatChannel, wantGroup: true},
		{name: "unknown_type", chatType: map[string]interface{}{"@type": "chatTypeSomethingNew"}, want: ChatPrivate, wantGroup: false},
		{name: "missing_type", chatType: nil, want: ChatPrivate, wantGroup: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mock := newMockTDClient()
			chat := map[string]interface{}{
				"@type": "chat",
				"id":    float64(111),
				"title": "Чат",
			}
			if tc.chatType != nil {
				chat["type"] = tc.chatType
			}
			mock.responses = []map[string]interface{}{
				{"@type": "ok"},
				{"@type": "chats", "chat_ids": []interface{}{float64(111)}},
				chat,
			}

			chats, err := GetChats(context.Background(), mock, map[string]interface{}{"@type": "chatListMain"}, 10)
			if err != nil {
				t.Fatalf("GetChats failed: %v", err)
			}
			if len(chats) != 1 {
				t.Fatalf("expected 1 chat, got %d", len(chats))
			}
			if chats[0].Kind != tc.want {
				t.Errorf("case %s: kind = %v, want %v", tc.name, chats[0].Kind, tc.want)
			}
			if chats[0].Kind.IsGroupOrChannel() != tc.wantGroup {
				t.Errorf("case %s: IsGroupOrChannel = %v, want %v",
					tc.name, chats[0].Kind.IsGroupOrChannel(), tc.wantGroup)
			}
		})
	}
}

// TestGetChatsReadsTheMuteFlag — признак «заглушён» берётся из
// notification_settings чата. Проверяются и явное заглушение, и дефолтное
// (use_default_mute_for == true — это не наш случай, см. chatMutedOf), и
// отсутствие объекта: лишний чат в ленте лучше, чем молча спрятанная переписка.
func TestGetChatsReadsTheMuteFlag(t *testing.T) {
	cases := []struct {
		name                 string
		notificationSettings interface{}
		want                 bool
	}{
		{
			name: "явно заглушен",
			notificationSettings: map[string]interface{}{
				"use_default_mute_for": false,
				"mute_for":             float64(3600),
			},
			want: true,
		},
		{
			name: "разглушен",
			notificationSettings: map[string]interface{}{
				"use_default_mute_for": false,
				"mute_for":             float64(0),
			},
			want: false,
		},
		{
			name: "настройки по умолчанию для типа чата",
			notificationSettings: map[string]interface{}{
				"use_default_mute_for": true,
				"mute_for":             float64(0),
			},
			want: false,
		},
		{
			// Отличает реальную проверку use_default_mute_for от случайно
			// совпадающего результата: mute_for здесь ненулевой (наследуется
			// от scope-настроек по типу чата), но use_default_mute_for=true
			// означает, что чат не заглушен явно — предыдущий кейс с mute_for=0
			// прошёл бы одинаково с любой реализацией.
			name: "настройки по умолчанию, но унаследованный mute_for ненулевой",
			notificationSettings: map[string]interface{}{
				"use_default_mute_for": true,
				"mute_for":             float64(3600),
			},
			want: false,
		},
		{
			name:                 "объекта настроек нет",
			notificationSettings: nil,
			want:                 false,
		},
		{
			name: "битое значение mute_for",
			notificationSettings: map[string]interface{}{
				"use_default_mute_for": false,
				"mute_for":             "час",
			},
			want: false,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mock := newMockTDClient()
			chat := map[string]interface{}{
				"@type": "chat",
				"id":    float64(111),
				"title": "Чат",
				"type":  map[string]interface{}{"@type": "chatTypePrivate"},
			}
			if testCase.notificationSettings != nil {
				chat["notification_settings"] = testCase.notificationSettings
			}
			mock.responses = []map[string]interface{}{
				{"@type": "ok"},
				{"@type": "chats", "chat_ids": []interface{}{float64(111)}},
				chat,
			}

			chats, err := GetChats(context.Background(), mock, map[string]interface{}{"@type": "chatListMain"}, 10)
			if err != nil {
				t.Fatalf("GetChats failed: %v", err)
			}
			if len(chats) != 1 {
				t.Fatalf("expected 1 chat, got %d", len(chats))
			}
			if chats[0].Muted != testCase.want {
				t.Errorf("Muted = %v, want %v", chats[0].Muted, testCase.want)
			}
		})
	}
}

// TestChatKindNamesMatchThePanelMenu — названия категорий совпадают с пунктами меню
// панели чатов, иначе фильтр и подпись разойдутся.
func TestChatKindNamesMatchThePanelMenu(t *testing.T) {
	for kind, want := range map[ChatKind]string{
		ChatPrivate: "Личные",
		ChatGroup:   "Чаты",
		ChatChannel: "Каналы",
	} {
		if got := kind.String(); got != want {
			t.Errorf("ChatKind(%d).String() = %q, want %q", kind, got, want)
		}
	}
}
