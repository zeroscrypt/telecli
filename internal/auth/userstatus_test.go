package auth

import (
	"context"
	"testing"
)

// Статус собеседника и счётчик участников (задача 0163) — первые данные о
// TDLib-объектах, которых в проекте до этого не было: ни статуса
// пользователя, ни member_count. Схема сверена с
// ~/.local/tdlib/include/td/telegram/td_api.h (сборка из master, ID -1529460876
// и далее) — см. комментарии у ParseUserStatus и ParseGroupMemberCountUpdate.

// TestGetChatsFillsPeerID — из типа чата достаётся id объекта, на который этот
// тип указывает: user_id у личного диалога, basic_group_id у базовой группы,
// supergroup_id у супергруппы и канала.
//
// Это единственный мост к живым данным: число участников и статус приходят
// апдейтами ПРО САМ ОБЪЕКТ (updateBasicGroup/updateSupergroup/updateUser), а не
// про чат, поэтому без id в чате их не к чему было бы приложить, и карточка
// осталась бы с пустым местом рядом с названием.
//
// Отдельно проверяется, что тип БЕЗ нужного поля даёт 0, а не чужое число:
// у секретного чата поле называется secret_chat_id, и искать по нему user_id
// нельзя — иначе карточка показала бы счётчик участников и статус случайного
// пользователя с тем же номером.
func TestGetChatsFillsPeerID(t *testing.T) {
	cases := []struct {
		name     string
		chatType map[string]interface{}
		want     int64
	}{
		{name: "private", chatType: map[string]interface{}{"@type": "chatTypePrivate", "user_id": float64(42)}, want: 42},
		{name: "basic_group", chatType: map[string]interface{}{"@type": "chatTypeBasicGroup", "basic_group_id": float64(-100)}, want: -100},
		{name: "supergroup", chatType: map[string]interface{}{"@type": "chatTypeSupergroup", "supergroup_id": float64(1000)}, want: 1000},
		{name: "channel", chatType: map[string]interface{}{"@type": "chatTypeSupergroup", "supergroup_id": float64(2000), "is_channel": true}, want: 2000},
		{name: "secret_has_own_id_field", chatType: map[string]interface{}{"@type": "chatTypeSecret", "secret_chat_id": float64(7), "user_id": float64(42)}, want: 0},
		{name: "unknown_type", chatType: map[string]interface{}{"@type": "chatTypeSomethingNew", "user_id": float64(42)}, want: 0},
		{name: "type_without_id", chatType: map[string]interface{}{"@type": "chatTypePrivate"}, want: 0},
		{name: "missing_type", chatType: nil, want: 0},
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
			if chats[0].PeerID != tc.want {
				t.Errorf("PeerID = %d, want %d", chats[0].PeerID, tc.want)
			}
		})
	}
}

// TestParseUserStatus — все шесть вариантов userStatus разбираются в свои
// значения, а неизвестный @type и отсутствующий объект дают «статуса нет».
//
// Именно «нет», а не ошибка: новый вариант статуса в будущей версии TDLib не
// должен превращать карточку в битую, и молча исчезнуть из неё тоже нельзя —
// «показать столько, сколько знаем» здесь безопаснее молчания.
//
// was_online проверяется только у userStatusOffline: у остальных точного
// времени нет вовсе, и выдумывать ноль секунд как «момент выхода» нельзя.
func TestParseUserStatus(t *testing.T) {
	cases := []struct {
		name   string
		status map[string]interface{}
		want   UserStatus
	}{
		{name: "online", status: map[string]interface{}{"@type": "userStatusOnline", "expires": float64(1700000000)}, want: UserStatus{Kind: UserStatusOnline}},
		{name: "offline", status: map[string]interface{}{"@type": "userStatusOffline", "was_online": float64(1700000000)}, want: UserStatus{Kind: UserStatusOffline, WasOnline: 1700000000}},
		{name: "recently", status: map[string]interface{}{"@type": "userStatusRecently"}, want: UserStatus{Kind: UserStatusRecently}},
		{name: "last_week", status: map[string]interface{}{"@type": "userStatusLastWeek"}, want: UserStatus{Kind: UserStatusLastWeek}},
		{name: "last_month", status: map[string]interface{}{"@type": "userStatusLastMonth"}, want: UserStatus{Kind: UserStatusLastMonth}},
		{name: "empty", status: map[string]interface{}{"@type": "userStatusEmpty"}, want: UserStatus{Kind: UserStatusEmpty}},
		{name: "unknown", status: map[string]interface{}{"@type": "userStatusFromTheFuture"}, want: UserStatus{Kind: UserStatusEmpty}},
		{name: "offline_without_was_online", status: map[string]interface{}{"@type": "userStatusOffline"}, want: UserStatus{Kind: UserStatusOffline}},
		{name: "nil_object", status: nil, want: UserStatus{Kind: UserStatusEmpty}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ParseUserStatus(tc.status); got != tc.want {
				t.Errorf("ParseUserStatus(%v) = %+v, want %+v", tc.status, got, tc.want)
			}
		})
	}
}

// TestParseUserStatusUpdate — отдельный апдейт смены статуса разбирается в пару
// (user_id, статус) и отбрасывается на чужом @type или битом объекте.
//
// Отдельный контракт здесь один: у апдейта нет НИКАКОГО поля кроме user_id и
// status, поэтому сломанный статус нельзя «долить» из updateUser — на нём лежит
// только снимок, а не изменения. ok == false на битом status означает «молча
// пропустить», а не «показать пустое место»: иначе одна битая смена статуса
// стирала бы живой статус карточки.
func TestParseUserStatusUpdate(t *testing.T) {
	cases := []struct {
		name     string
		update   map[string]interface{}
		wantID   int64
		wantKind UserStatusKind
		wantOK   bool
	}{
		{
			name:     "online",
			update:   map[string]interface{}{"@type": "updateUserStatus", "user_id": float64(42), "status": map[string]interface{}{"@type": "userStatusOnline"}},
			wantID:   42,
			wantKind: UserStatusOnline,
			wantOK:   true,
		},
		{
			name:     "offline",
			update:   map[string]interface{}{"@type": "updateUserStatus", "user_id": float64(42), "status": map[string]interface{}{"@type": "userStatusOffline", "was_online": float64(1700000000)}},
			wantID:   42,
			wantKind: UserStatusOffline,
			wantOK:   true,
		},
		{
			name:   "чужой апдейт",
			update: map[string]interface{}{"@type": "updateUser", "user_id": float64(42)},
		},
		{
			name:   "без user_id",
			update: map[string]interface{}{"@type": "updateUserStatus", "status": map[string]interface{}{"@type": "userStatusOnline"}},
		},
		{
			name:   "битый status",
			update: map[string]interface{}{"@type": "updateUserStatus", "user_id": float64(42)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			userID, status, ok := ParseUserStatusUpdate(tc.update)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				if userID != 0 {
					t.Errorf("на неопознанном апдейте вернулся userID %d, want 0", userID)
				}
				return
			}
			if userID != tc.wantID {
				t.Errorf("userID = %d, want %d", userID, tc.wantID)
			}
			if status.Kind != tc.wantKind {
				t.Errorf("status.Kind = %v, want %v", status.Kind, tc.wantKind)
			}
		})
	}
}

// TestParseGroupMemberCountUpdate — оба апдейта о числе участников разбираются
// ОДНИМ парсером в пару (id группы, member_count) и отбрасываются на чужом
// @type или битом объекте.
//
// member_count кладётся рядом с id, а не вместо него: потеря самого числа —
// самый вероятный дефект разбора, и на объекте без него проверка бы молчала.
// Проверяется и ЗАЩИТА от подмены объекта: updateBasicGroup обязан брать
// basic_group, а updateSupergroup — supergroup, и перепутать их в общей ветке
// легко (тогда у канала показывалось бы число участников какой-то группы).
func TestParseGroupMemberCountUpdate(t *testing.T) {
	cases := []struct {
		name      string
		update    map[string]interface{}
		wantID    int64
		wantCount int32
		wantOK    bool
	}{
		{
			name:      "basic_group",
			update:    map[string]interface{}{"@type": "updateBasicGroup", "basic_group": map[string]interface{}{"@type": "basicGroup", "id": float64(-100), "member_count": float64(250)}},
			wantID:    -100,
			wantCount: 250,
			wantOK:    true,
		},
		{
			name:      "supergroup",
			update:    map[string]interface{}{"@type": "updateSupergroup", "supergroup": map[string]interface{}{"@type": "supergroup", "id": float64(1000), "member_count": float64(123456)}},
			wantID:    1000,
			wantCount: 123456,
			wantOK:    true,
		},
		{
			name:   "чужой апдейт",
			update: map[string]interface{}{"@type": "updateUser", "user": map[string]interface{}{"@type": "user", "id": float64(42)}},
		},
		{
			name:   "объект не на своём месте",
			update: map[string]interface{}{"@type": "updateSupergroup", "basic_group": map[string]interface{}{"@type": "basicGroup", "id": float64(1), "member_count": float64(2)}},
		},
		{
			name:   "объект чужого типа",
			update: map[string]interface{}{"@type": "updateSupergroup", "supergroup": map[string]interface{}{"@type": "basicGroup", "id": float64(1), "member_count": float64(2)}},
		},
		{
			name:   "без member_count",
			update: map[string]interface{}{"@type": "updateSupergroup", "supergroup": map[string]interface{}{"@type": "supergroup", "id": float64(1)}},
		},
		{
			name:   "без объекта",
			update: map[string]interface{}{"@type": "updateBasicGroup"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id, count, ok := ParseGroupMemberCountUpdate(tc.update)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				if id != 0 || count != 0 {
					t.Errorf("на неопознанном апдейте вернулось %d/%d, want 0 и 0", id, count)
				}
				return
			}
			if id != tc.wantID {
				t.Errorf("id = %d, want %d", id, tc.wantID)
			}
			if count != tc.wantCount {
				t.Errorf("memberCount = %d, want %d", count, tc.wantCount)
			}
		})
	}
}

// TestParseUserUpdateCarriesStatus — снимок статуса едет именно в updateUser:
// на живой проверке (задача 0163) TDLib при загрузке списка чатов шлёт
// updateUser пачками и НЕ шлёт ни одного updateUserStatus, то есть updateUser —
// единственный источник статуса до первой живой смены.
//
// Проверяется и то, что статус пустой у объекта без поля status (битый или
// урезанный updateUser), и то, что апдейт со статусом остаётся годным по
// остальным контрактам: имя и id разбираются как прежде.
func TestParseUserUpdateCarriesStatus(t *testing.T) {
	userID, name, status, ok := ParseUserUpdate(map[string]interface{}{
		"@type": "updateUser",
		"user": map[string]interface{}{
			"@type": "user", "id": float64(42), "first_name": "Иван", "last_name": "Петров",
			"status": map[string]interface{}{"@type": "userStatusRecently"},
		},
	})
	if !ok {
		t.Fatal("ok = false на валидном updateUser со статусом")
	}
	if userID != 42 || name != "Иван Петров" {
		t.Errorf("разбор апдейта поехал: id %d, имя %q", userID, name)
	}
	if status.Kind != UserStatusRecently {
		t.Errorf("status.Kind = %v, want %v (UserStatusRecently)", status.Kind, UserStatusRecently)
	}

	// Без поля status апдейт остаётся годным, а статус читается как «нет».
	if _, _, status, ok = ParseUserUpdate(userUpdateJSON(42, "Иван", "Петров")); !ok {
		t.Fatal("updateUser без поля status перестал быть валидным")
	}
	if status.Kind != UserStatusEmpty {
		t.Errorf("status.Kind = %v, want %v (UserStatusEmpty)", status.Kind, UserStatusEmpty)
	}
}

// userUpdateJSON — updateUser без поля status, то есть ровно тот вид, который
// присылает TDLib для пользователя без статуса.
func userUpdateJSON(userID float64, first, last string) map[string]interface{} {
	return map[string]interface{}{
		"@type": "updateUser",
		"user": map[string]interface{}{
			"@type":      "user",
			"id":         userID,
			"first_name": first,
			"last_name":  last,
		},
	}
}
