package auth

import (
	"reflect"
	"testing"
)

// Тесты четырёх парсеров живости списка чатов. Схема каждого апдейта сверена с
// ~/.local/tdlib/include/td/telegram/td_api.h собранного TDLib — в тестах
// используются те же имена полей, что присылает TDLib (chat.id, а не chat_id:
// первый — это id объекта chat, второй существует только у апдейтов).

// tdlibChat — объект chat в том виде, в каком его отдаёт TDLib (getChat,
// updateNewChat). chat_id_ в нём НЕТ: идентификатор лежит в поле id_.
func tdlibChat(id float64, title string) map[string]interface{} {
	return map[string]interface{}{
		"@type": "chat",
		"id":    id,
		"type":  map[string]interface{}{"@type": "chatTypePrivate"},
		"title": title,
	}
}

// mainPosition — позиция в главном списке с заданным order.
func mainPosition(order float64) map[string]interface{} {
	return map[string]interface{}{
		"@type": "chatPosition",
		"list":  map[string]interface{}{"@type": "chatListMain"},
		"order": order,
	}
}

// archivePosition — позиция в архиве. Служит отрицательным случаем: апдейт про
// архив не должен читаться как «чат убран из главного списка».
func archivePosition(order float64) map[string]interface{} {
	return map[string]interface{}{
		"@type": "chatPosition",
		"list":  map[string]interface{}{"@type": "chatListArchive"},
		"order": order,
	}
}

func folderPosition(order float64) map[string]interface{} {
	return map[string]interface{}{
		"@type": "chatPosition",
		"list":  map[string]interface{}{"@type": "chatListFolder", "chat_folder_id": float64(3)},
		"order": order,
	}
}

func positionsArray(positions ...map[string]interface{}) []interface{} {
	result := make([]interface{}, 0, len(positions))
	for _, position := range positions {
		result = append(result, position)
	}
	return result
}

// --- ParseNewChatUpdate -------------------------------------------------------

// TestParseNewChatUpdateReadsWholeChatObject — апдейт несёт объект chat целиком,
// и наружу отдаётся готовый Chat. Без этого у нового чата не было бы ни названия,
// ни категории, ни признака заглушения, а карточки его сообщений рисовались бы
// как chat#<id> и группу для неизвестного типа.
func TestParseNewChatUpdateReadsWholeChatObject(t *testing.T) {
	chat := tdlibChat(42, "Работа")
	chat["type"] = map[string]interface{}{"@type": "chatTypeSupergroup", "is_channel": true}
	chat["unread_count"] = float64(7)
	chat["last_read_outbox_message_id"] = float64(99)
	chat["notification_settings"] = map[string]interface{}{
		"use_default_mute_for": false,
		"mute_for":             float64(3600),
	}

	got, ok := ParseNewChatUpdate(map[string]interface{}{
		"@type": "updateNewChat",
		"chat":  chat,
	})
	if !ok {
		t.Fatal("валидный updateNewChat разобран как ok=false")
	}
	want := Chat{
		ID: 42, Title: "Работа", UnreadCount: 7, LastReadOutboxMessageID: 99,
		Kind: ChatChannel, Muted: true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("chat = %+v, want %+v", got, want)
	}
}

// TestParseNewChatUpdateFallsBackToChatIDOnEmptyTitle — чат без названия (part
// chats, пользователь без имени) получает тот же фолбэк chat#<id>, что и в
// GetChats: разбор объекта chat общий, значит и деградация обязана быть общей.
func TestParseNewChatUpdateFallsBackToChatIDOnEmptyTitle(t *testing.T) {
	got, ok := ParseNewChatUpdate(map[string]interface{}{
		"@type": "updateNewChat",
		"chat":  tdlibChat(42, ""),
	})
	if !ok {
		t.Fatal("валидный updateNewChat разобран как ok=false")
	}
	if got.Title != "chat#42" {
		t.Fatalf("Title = %q, want chat#42", got.Title)
	}
}

// TestParseNewChatUpdateRejectsForeignAndBroken — контракт тот же, что у всех
// остальных парсеров файла: чужой @type или битый объект — ok == false, без
// паники. Без проверки на «нет id» апдейт без чата добавил бы в список запись с
// нулевым id, и по нему потом звался бы openChat с chat_id=0.
func TestParseNewChatUpdateRejectsForeignAndBroken(t *testing.T) {
	cases := []struct {
		name   string
		update map[string]interface{}
	}{
		{name: "чужой @type", update: map[string]interface{}{"@type": "updateChatTitle", "chat": tdlibChat(1, "Аня")}},
		{name: "нет объекта chat", update: map[string]interface{}{"@type": "updateNewChat"}},
		{name: "chat не объект", update: map[string]interface{}{"@type": "updateNewChat", "chat": "текст"}},
		{name: "нет id", update: map[string]interface{}{"@type": "updateNewChat", "chat": map[string]interface{}{"@type": "chat", "title": "Аня"}}},
		{name: "id не число", update: map[string]interface{}{"@type": "updateNewChat", "chat": map[string]interface{}{"@type": "chat", "id": "42", "title": "Аня"}}},
		{name: "пустой апдейт", update: map[string]interface{}{}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got, ok := ParseNewChatUpdate(testCase.update); ok {
				t.Fatalf("разбор вернул ok=true, чат %+v", got)
			}
		})
	}
}

// --- ParseChatListMembershipUpdate -------------------------------------------

// TestParseChatListMembershipUpdate — членство чата в списке разбирается в одну
// сторону (added) и один список (onlyMainList). Проверяются все три вида списка
// из схемы: приложение показывает только chatListMain, поэтому архив и папка
// обязаны отсеиваться по имени — иначе перенос чата в папку дёргал бы список.
func TestParseChatListMembershipUpdate(t *testing.T) {
	cases := []struct {
		name         string
		update       map[string]interface{}
		wantAdded    bool
		wantOnlyMain bool
		wantOK       bool
	}{
		{
			name:         "добавлен в главный",
			update:       map[string]interface{}{"@type": "updateChatAddedToList", "chat_id": float64(5), "chat_list": map[string]interface{}{"@type": "chatListMain"}},
			wantAdded:    true,
			wantOnlyMain: true,
			wantOK:       true,
		},
		{
			name:         "убран из главного",
			update:       map[string]interface{}{"@type": "updateChatRemovedFromList", "chat_id": float64(5), "chat_list": map[string]interface{}{"@type": "chatListMain"}},
			wantAdded:    false,
			wantOnlyMain: true,
			wantOK:       true,
		},
		{
			name:         "добавлен в архив",
			update:       map[string]interface{}{"@type": "updateChatAddedToList", "chat_id": float64(5), "chat_list": map[string]interface{}{"@type": "chatListArchive"}},
			wantAdded:    true,
			wantOnlyMain: false,
			wantOK:       true,
		},
		{
			name:         "добавлен в папку",
			update:       map[string]interface{}{"@type": "updateChatAddedToList", "chat_id": float64(5), "chat_list": map[string]interface{}{"@type": "chatListFolder", "chat_folder_id": float64(2)}},
			wantAdded:    true,
			wantOnlyMain: false,
			wantOK:       true,
		},
		{
			// Неизвестный вид списка обязан остаться ok=true с onlyMainList=false:
			// апдейт разобран верно, он просто не про главный список, и молча
			// спутать его с битым апдейтом (который надо переждать) нельзя.
			name:         "неизвестный вид списка",
			update:       map[string]interface{}{"@type": "updateChatAddedToList", "chat_id": float64(5), "chat_list": map[string]interface{}{"@type": "chatListSomethingNew"}},
			wantAdded:    true,
			wantOnlyMain: false,
			wantOK:       true,
		},
		{name: "чужой @type", update: map[string]interface{}{"@type": "updateChatPosition", "chat_id": float64(5)}},
		{name: "нет chat_id", update: map[string]interface{}{"@type": "updateChatAddedToList", "chat_list": map[string]interface{}{"@type": "chatListMain"}}},
		{name: "chat_id не число", update: map[string]interface{}{"@type": "updateChatAddedToList", "chat_id": "5", "chat_list": map[string]interface{}{"@type": "chatListMain"}}},
		{name: "нет chat_list", update: map[string]interface{}{"@type": "updateChatAddedToList", "chat_id": float64(5)}},
		{name: "chat_list не объект", update: map[string]interface{}{"@type": "updateChatAddedToList", "chat_id": float64(5), "chat_list": "main"}},
		{name: "пустой апдейт", update: map[string]interface{}{}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			chatID, added, onlyMainList, ok := ParseChatListMembershipUpdate(testCase.update)
			if ok != testCase.wantOK {
				t.Fatalf("ok = %v, want %v", ok, testCase.wantOK)
			}
			if !ok {
				return
			}
			if chatID != 5 {
				t.Fatalf("chatID = %d, want 5", chatID)
			}
			if added != testCase.wantAdded {
				t.Fatalf("added = %v, want %v", added, testCase.wantAdded)
			}
			if onlyMainList != testCase.wantOnlyMain {
				t.Fatalf("onlyMainList = %v, want %v", onlyMainList, testCase.wantOnlyMain)
			}
		})
	}
}

// --- ParseChatPositionRemoval -------------------------------------------------

// TestParseChatPositionRemovalOnMainList — единственное, ради чего существует
// парсер: order == 0 у позиции в chatListMain означает «убрать чат из списка».
// Это не домысел, а документация к chatPosition в td_api.h. Без этого чат,
// архивированный на другом клиенте, оставался бы в списке до перезапуска.
func TestParseChatPositionRemovalOnMainList(t *testing.T) {
	cases := []struct {
		name   string
		update map[string]interface{}
		want   bool
	}{
		{
			name:   "updateChatPosition с order 0",
			update: map[string]interface{}{"@type": "updateChatPosition", "chat_id": float64(5), "position": mainPosition(0)},
			want:   true,
		},
		{
			// Ненулевой order — обычное «чат поднялся», а не удаление. Именно этот
			// кейс отличает правильную реализацию от «order всегда 0 ⇒ удалять».
			name:   "updateChatPosition с ненулевым order",
			update: map[string]interface{}{"@type": "updateChatPosition", "chat_id": float64(5), "position": mainPosition(1234567)},
			want:   false,
		},
		{
			name:   "updateChatLastMessage с order 0 среди прочих",
			update: map[string]interface{}{"@type": "updateChatLastMessage", "chat_id": float64(5), "last_message": nil, "positions": positionsArray(archivePosition(9), mainPosition(0))},
			want:   true,
		},
		{
			name:   "updateChatLastMessage с ненулевым order",
			update: map[string]interface{}{"@type": "updateChatLastMessage", "chat_id": float64(5), "last_message": nil, "positions": positionsArray(mainPosition(42))},
			want:   false,
		},
		{
			name:   "updateChatDraftMessage с order 0",
			update: map[string]interface{}{"@type": "updateChatDraftMessage", "chat_id": float64(5), "draft_message": nil, "positions": positionsArray(mainPosition(0))},
			want:   true,
		},
		{
			name:   "updateChatDraftMessage с ненулевым order",
			update: map[string]interface{}{"@type": "updateChatDraftMessage", "chat_id": float64(5), "draft_message": nil, "positions": positionsArray(mainPosition(42))},
			want:   false,
		},
		{
			// Позиция не про главный список: TDLib вполне может прислать апдейт
			// только ради папки. Убрать из нашего списка тут нечего.
			name:   "updateChatPosition про архив с order 0",
			update: map[string]interface{}{"@type": "updateChatPosition", "chat_id": float64(5), "position": archivePosition(0)},
			want:   false,
		},
		{
			name:   "updateChatPosition про папку с order 0",
			update: map[string]interface{}{"@type": "updateChatPosition", "chat_id": float64(5), "position": folderPosition(0)},
			want:   false,
		},
		{
			// Пустой массив — это «апдейт не про главный список», а НЕ «чат убран».
			// Такое смешение вычеркнуло бы чат из списка на каждом апдейте позиции
			// в папке, то есть почти на каждом живом сообщении в папке.
			name:   "updateChatLastMessage с пустым массивом позиций",
			update: map[string]interface{}{"@type": "updateChatLastMessage", "chat_id": float64(5), "last_message": nil, "positions": []interface{}{}},
			want:   false,
		},
		{
			name:   "updateChatLastMessage только с позицией архива",
			update: map[string]interface{}{"@type": "updateChatLastMessage", "chat_id": float64(5), "last_message": nil, "positions": positionsArray(archivePosition(0))},
			want:   false,
		},
		{
			name:   "updateChatDraftMessage с битым элементом массива",
			update: map[string]interface{}{"@type": "updateChatDraftMessage", "chat_id": float64(5), "draft_message": nil, "positions": []interface{}{mainPosition(0), "мусор"}},
			want:   true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			chatID, removed, ok := ParseChatPositionRemoval(testCase.update)
			if !ok {
				t.Fatal("валидный апдейт позиции разобран как ok=false")
			}
			if chatID != 5 {
				t.Fatalf("chatID = %d, want 5", chatID)
			}
			if removed != testCase.want {
				t.Fatalf("removed = %v, want %v", removed, testCase.want)
			}
		})
	}
}

// TestParseChatPositionRemovalRejectsForeignAndBroken — битый апдейт обязан быть
// отброшен (ok=false), а не истолкован как «чат убран»: иначе одно
// несоответствие формы удалило бы чат из списка.
func TestParseChatPositionRemovalRejectsForeignAndBroken(t *testing.T) {
	cases := []struct {
		name   string
		update map[string]interface{}
	}{
		{name: "чужой @type", update: map[string]interface{}{"@type": "updateNewChat", "chat_id": float64(5)}},
		{name: "нет chat_id", update: map[string]interface{}{"@type": "updateChatPosition", "position": mainPosition(0)}},
		{name: "chat_id не число", update: map[string]interface{}{"@type": "updateChatPosition", "chat_id": "5", "position": mainPosition(0)}},
		{name: "updateChatPosition без position", update: map[string]interface{}{"@type": "updateChatPosition", "chat_id": float64(5)}},
		{name: "updateChatPosition с position не объектом", update: map[string]interface{}{"@type": "updateChatPosition", "chat_id": float64(5), "position": 0}},
		{name: "updateChatLastMessage без positions", update: map[string]interface{}{"@type": "updateChatLastMessage", "chat_id": float64(5), "last_message": nil}},
		{name: "updateChatDraftMessage с positions не массивом", update: map[string]interface{}{"@type": "updateChatDraftMessage", "chat_id": float64(5), "positions": map[string]interface{}{}}},
		{name: "пустой апдейт", update: map[string]interface{}{}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, removed, ok := ParseChatPositionRemoval(testCase.update); ok {
				t.Fatalf("разбор вернул ok=true, removed=%v", removed)
			}
		})
	}
}

// TestParseChatPositionRemovalIgnoresMissingOrderField — позиция без order не
// значит «убрать»: отсутствие поля — это неизвестность, а не нулевой порядок.
// Считать её нулём значило бы удалять чат на каждом апдейте с неполной позицией.
func TestParseChatPositionRemovalIgnoresMissingOrderField(t *testing.T) {
	_, removed, ok := ParseChatPositionRemoval(map[string]interface{}{
		"@type":    "updateChatPosition",
		"chat_id":  float64(5),
		"position": map[string]interface{}{"@type": "chatPosition", "list": map[string]interface{}{"@type": "chatListMain"}},
	})
	if !ok {
		t.Fatal("валидный апдейт разобран как ok=false")
	}
	if removed {
		t.Fatal("позиция без order дала removed=true, а отсутствие поля — не нулевой порядок")
	}
}

// TestParseChatPositionRemovalIgnoresMissingListField — позиция без list тоже не
// значит «убрать из главного списка»: без list не известно, про какой список
// апдейт, и по умолчанию считать его главным значило бы вычеркнуть чат на
// апдейте про папку с неполной позицией.
func TestParseChatPositionRemovalIgnoresMissingListField(t *testing.T) {
	_, removed, ok := ParseChatPositionRemoval(map[string]interface{}{
		"@type":    "updateChatPosition",
		"chat_id":  float64(5),
		"position": map[string]interface{}{"@type": "chatPosition", "order": float64(0)},
	})
	if !ok {
		t.Fatal("валидный апдейт разобран как ok=false")
	}
	if removed {
		t.Fatal("позиция без list дала removed=true")
	}
}
