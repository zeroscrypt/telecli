package tgwall

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/auth"
	"telecli/internal/config"
)

// Живые сведения об источнике: счётчик участников у группы и канала, статус
// собеседника в личном диалоге (задача 0163). Разбор апдейтов TDLib покрыт
// тестами internal/auth, здесь проверяется путь от апдейта до карточки на
// экране — ровно то, что было бы проверено, если бы разбор жил здесь же.

// wallLiveBasicGroupUpdate / wallLiveSupergroupUpdate — updateBasicGroup и
// updateSupergroup в той форме, которую отдаёт TDLib. Форма member_count взят
// из живого прогона на настоящем аккаунте (probe, задача 0163).
func wallLiveBasicGroupUpdate(groupID int64, memberCount int) map[string]interface{} {
	return map[string]interface{}{
		"@type": "updateBasicGroup",
		"basic_group": map[string]interface{}{
			"@type":        "basicGroup",
			"id":           float64(groupID),
			"member_count": float64(memberCount),
		},
	}
}

func wallLiveSupergroupUpdate(groupID int64, memberCount int) map[string]interface{} {
	return map[string]interface{}{
		"@type": "updateSupergroup",
		"supergroup": map[string]interface{}{
			"@type":        "supergroup",
			"id":           float64(groupID),
			"member_count": float64(memberCount),
			"is_channel":   true,
		},
	}
}

// wallLiveUserStatusUpdate — updateUserStatus в форме TDLib.
func wallLiveUserStatusUpdate(userID int64, status map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"@type":   "updateUserStatus",
		"user_id": float64(userID),
		"status":  status,
	}
}

// wallLiveUserProfileUpdate — updateUser (снимок пользователя целиком) в форме
// TDLib: имя и статус лежат в одном объекте user.
func wallLiveUserProfileUpdate(userID int64, firstName string, status map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"@type": "updateUser",
		"user": map[string]interface{}{
			"@type":      "user",
			"id":         float64(userID),
			"first_name": firstName,
			"status":     status,
		},
	}
}

// applyLiveSourceInfoUpdate — прогон живого апдейта через настоящий Update, как
// это делает программа.
func applyLiveSourceInfoUpdate(t *testing.T, m Model, update tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(update)
	return next.(Model), cmd
}

// sourceInfoModel — стена со снимком из одного личного диалога, одной группы и
// одного канала: ровно те три типа, у которых сведения различаются.
func sourceInfoModel(t *testing.T) Model {
	t.Helper()
	chats := []auth.Chat{
		{ID: 1, Title: "Марина", Kind: auth.ChatPrivate, PeerID: 101},
		{ID: 2, Title: "Команда", Kind: auth.ChatGroup, PeerID: 202},
		{ID: 3, Title: "Новости", Kind: auth.ChatChannel, PeerID: 303},
	}
	messages := []wallMessage{
		{Chat: chats[0], Message: auth.Message{ID: 11, Text: "привет", Date: 1}},
		{Chat: chats[1], Message: auth.Message{ID: 12, Text: "всем привет", Date: 2}},
		{Chat: chats[2], Message: auth.Message{ID: 13, Text: "новость", Date: 3}},
	}
	m := New(context.Background(), nil, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	next, _ := m.Update(tea.WindowSizeMsg{Width: scrollWidth, Height: 30})
	m = next.(Model)
	next, _ = m.Update(wallLoadedMsg{cards: wallCards(messages), chats: chats})
	m = next.(Model)
	if m.loading {
		t.Fatal("после wallLoadedMsg стена осталась в состоянии загрузки")
	}
	return m
}

// titleOf — строка 1 карточки в виде, как её видит человек.
func titleOf(t *testing.T, m Model, chatID int64) string {
	t.Helper()
	for _, item := range m.cards {
		if item.ChatID == chatID {
			return ansi.Strip(renderCard(item, scrollWidth, false)[0])
		}
	}
	t.Fatalf("карточки чата %d на стене нет", chatID)
	return ""
}

// Снимок НЕ содержит ни счётчика участников, ни статуса: они приезжают
// апдейтами позже (проверено вживью — TDLib шлёт updateBasicGroup/
// updateSupergroup/updateUser при загрузке списка чатов сам). Если бы wallCard
// выдумывал их из снимка, на стене с самого начала светились бы нули.
func TestWallCardHasNoSourceInfoFromSnapshot(t *testing.T) {
	m := sourceInfoModel(t)
	for _, item := range m.cards {
		if item.MemberCount != 0 {
			t.Errorf("карточка %q получила MemberCount=%d из снимка — он должен приходить апдейтом",
				item.Name, item.MemberCount)
		}
		if item.Status.Kind != auth.UserStatusEmpty {
			t.Errorf("карточка %q получила статус %v из снимка — он должен приходить апдейтом",
				item.Name, item.Status.Kind)
		}
		if item.PeerID == 0 {
			t.Errorf("карточка %q без PeerID — по нему ищутся живые сведения", item.Name)
		}
	}
}

// Счётчик участников: апдейт с числом показывает его на строке заголовка
// компактной записью. Проверяется ЦЕЛИКОМ — от сырого JSON TDLib до карточки,
// потому что по отдельности «разбор вернул число» и «карточка показала число»
// ничего не говорят о том, что они состыкованы тем же идентификатором.
// Часть B задачи 0163.
func TestLiveMemberCountAppearsInTitle(t *testing.T) {
	cases := []struct {
		name   string
		chatID int64
		peerID int64
		update map[string]interface{}
		want   string
	}{
		{"базовая группа", 2, 202, wallLiveBasicGroupUpdate(202, 37), "37"},
		{"канал на тысячу", 3, 303, wallLiveSupergroupUpdate(303, 1000), "1K"},
		{"канал, 1200", 3, 303, wallLiveSupergroupUpdate(303, 1200), "1,2K"},
		{"канал, 45 тысяч", 3, 303, wallLiveSupergroupUpdate(303, 45_000), "45K"},
		{"канал, 2,1 млн", 3, 303, wallLiveSupergroupUpdate(303, 2_100_000), "2,1M"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			m := sourceInfoModel(t)
			groupID, memberCount, ok := auth.ParseGroupMemberCountUpdate(testCase.update)
			if !ok {
				t.Fatal("разбор апдейта TDLib не удался")
			}
			if groupID != testCase.peerID {
				t.Fatalf("из апдейта взяли groupID=%d, ждали %d", groupID, testCase.peerID)
			}
			next, _ := applyLiveSourceInfoUpdate(t, m, wallGroupCountUpdateMsg{
				valid: true, groupID: groupID, memberCount: memberCount})
			if got := titleOf(t, next, testCase.chatID); !strings.Contains(got, testCase.want) {
				t.Fatalf("на заголовке карточки нет счётчика %q: %q", testCase.want, got)
			}
		})
	}
}

// Счётчик участников появляется на карточке после апдейта и обновляется на
// следующий — без перезапуска приложения. Основной случай части B.
func TestLiveMemberCountUpdatesCardWithoutRestart(t *testing.T) {
	m := sourceInfoModel(t)
	if title := titleOf(t, m, 3); strings.Contains(title, "K") || strings.Contains(title, "303") {
		t.Fatalf("до апдейта на карточке уже есть счётчик участников: %q", title)
	}

	next, _ := applyLiveSourceInfoUpdate(t, m,
		wallGroupCountUpdateMsg{valid: true, groupID: 303, memberCount: 1337})
	if got := titleOf(t, next, 3); !strings.Contains(got, "1,3K") {
		t.Fatalf("после апдейта на карточке канала нет счётчика 1,3K: %q", got)
	}

	next, _ = applyLiveSourceInfoUpdate(t, next,
		wallGroupCountUpdateMsg{valid: true, groupID: 303, memberCount: 2500})
	if got := titleOf(t, next, 3); !strings.Contains(got, "2,5K") {
		t.Fatalf("после второго апдейта на карточке канала нет счётчика 2,5K: %q", got)
	}
}

// Апдейт счётчика НЕ двигает карточку и не трогает ни курсор, ни окно
// прокрутки: перестановка на стене — это новое сообщение, а не «у источника
// изменилось число участников» (тот же контракт, что у живых непрочитанных).
func TestLiveMemberCountDoesNotMoveCard(t *testing.T) {
	m := sourceInfoModel(t)
	order := chatIDOrder(m.cards)
	cards, cursor, scrollTop := len(m.cards), m.cursor, m.scrollTop

	next, _ := applyLiveSourceInfoUpdate(t, m,
		wallGroupCountUpdateMsg{valid: true, groupID: 202, memberCount: 5})

	if got := chatIDOrder(next.cards); !equalInt64s(got, order) {
		t.Errorf("порядок карточек изменился: %v -> %v", order, got)
	}
	if len(next.cards) != cards || next.cursor != cursor || next.scrollTop != scrollTop {
		t.Errorf("апдейт числа участников изменил стену: карточек %d->%d, курсор %d->%d, окно %d->%d",
			cards, len(next.cards), cursor, next.cursor, scrollTop, next.scrollTop)
	}
}

// Счётчик участников канала НЕ показывается на карточке личного диалога и
// наоборот: у личного диалога на его месте статус, а у группы и канала —
// число. Одно поле «информация об источнике» с двумя разными смыслами читалось
// бы как «у этого чата 1337 подписчиков».
func TestSourceInfoIsNotShownOnWrongCardType(t *testing.T) {
	m := sourceInfoModel(t)
	next, _ := applyLiveSourceInfoUpdate(t, m,
		wallGroupCountUpdateMsg{valid: true, groupID: 101, memberCount: 1337})
	if got := titleOf(t, next, 1); strings.Contains(got, "1337") || strings.Contains(got, "1,3K") {
		t.Fatalf("счётчик участников показан на личном диалоге: %q", got)
	}
}

// Апдейт числа может прийти ДО карточки источника (TDLib шлёт апдейты по всем
// чатам аккаунта, а карточки строятся из снимка) — значение обязано пережить
// отсутствие карточки и появиться на ней позже. Без этого счётчик у части
// источников молчал бы до следующего апдейта.
func TestMemberCountUpdateBeforeSnapshotLandsOnCardLater(t *testing.T) {
	chats := []auth.Chat{{ID: 3, Title: "Новости", Kind: auth.ChatChannel, PeerID: 303}}
	next, _ := applyLiveSourceInfoUpdate(t, sourceInfoModel(t),
		wallGroupCountUpdateMsg{valid: true, groupID: 303, memberCount: 4500})
	if got := titleOf(t, next, 3); !strings.Contains(got, "4,5K") {
		t.Fatalf("апдейт дошёл до карточки, уже существующей: %q", got)
	}

	// Тот же апдейт, но на модели, где карточек этого источника ещё нет.
	bare := New(context.Background(), nil, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	pending, _ := applyLiveSourceInfoUpdate(t, bare,
		wallGroupCountUpdateMsg{valid: true, groupID: 303, memberCount: 4500})
	withValue := pending

	afterSize, _ := withValue.Update(tea.WindowSizeMsg{Width: scrollWidth, Height: 30})
	withValue = afterSize.(Model)
	loaded, _ := withValue.Update(wallLoadedMsg{
		cards: wallCards([]wallMessage{{Chat: chats[0], Message: auth.Message{ID: 13, Text: "новость", Date: 3}}}),
		chats: chats,
	})
	if got := titleOf(t, loaded.(Model), 3); !strings.Contains(got, "4,5K") {
		t.Fatalf("апдейт, пришедший ДО снимка, не подхватился карточкой: %q", got)
	}
}

// Статус собеседника: updateUserStatus меняет строку на карточке личного
// диалога без перезапуска. Часть C задачи 0163.
func TestLiveUserStatusUpdatesCardWithoutRestart(t *testing.T) {
	m := sourceInfoModel(t)
	if got := titleOf(t, m, 1); strings.Contains(got, "в сети") {
		t.Fatalf("до апдейта на карточке уже есть статус: %q", got)
	}

	next, _ := applyLiveSourceInfoUpdate(t, m, wallUserStatusUpdateMsg{valid: true, userID: 101,
		status: auth.UserStatus{Kind: auth.UserStatusOnline}})
	if got := titleOf(t, next, 1); !strings.Contains(got, "в сети") {
		t.Fatalf("после апдейта на карточке нет статуса «в сети»: %q", got)
	}

	// Момент берётся от начала СУТОК плюс час, а не «два часа назад от сейчас»:
	// в первые два часа после полуночи «два часа назад» — это вчера, и проверка
	// падала бы в зависимости от времени запуска (воспроизведено 2026-10-01 в
	// 00:23 по местному). От полуночи бакет всегда либо «сегодня в», либо
	// «недавно» — оба и ждёт проверка.
	now := time.Now()
	wasOnline := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).
		Add(time.Hour).Unix()
	next, _ = applyLiveSourceInfoUpdate(t, next, wallUserStatusUpdateMsg{valid: true, userID: 101,
		status: auth.UserStatus{Kind: auth.UserStatusOffline, WasOnline: wasOnline}})
	got := titleOf(t, next, 1)
	if strings.Contains(got, "в сети") {
		t.Fatalf("после смены статуса на «вышел из сети» осталось «в сети»: %q", got)
	}
	if !strings.Contains(got, "сегодня в") && !strings.Contains(got, "недавно") {
		t.Fatalf("после апдейта статуса «вышел из сети» на карточке нет бакет «сегодня в»/«недавно»: %q", got)
	}
}

// Снимок статуса приезжает updateUser, а не updateUserStatus: на живом аккаунте
// при загрузке списка чатов TDLib шлёт updateUser пачками, а updateUserStatus не
// приходит ни разу. Поэтому обработчик updateUser — обязательный, а не
// запасной.
func TestLiveUserProfileUpdateSetsStatus(t *testing.T) {
	m := sourceInfoModel(t)
	next, _ := applyLiveSourceInfoUpdate(t, m, wallUserProfileUpdateMsg{valid: true, userID: 101,
		status: auth.UserStatus{Kind: auth.UserStatusRecently}})
	if got := titleOf(t, next, 1); !strings.Contains(got, "был(а) недавно") {
		t.Fatalf("updateUser не показал статус на карточке: %q", got)
	}
}

// Статус личного диалога не показывается на карточке группы: там на его месте
// счётчик участников.
func TestUserStatusIsNotShownOnGroupCard(t *testing.T) {
	m := sourceInfoModel(t)
	next, _ := applyLiveSourceInfoUpdate(t, m, wallUserStatusUpdateMsg{valid: true, userID: 202,
		status: auth.UserStatus{Kind: auth.UserStatusOnline}})
	if got := titleOf(t, next, 2); strings.Contains(got, "в сети") {
		t.Fatalf("статус личного собеседника показан на карточке группы: %q", got)
	}
}

// Переподписка — тот же трёхвариантный контракт, что у живых сообщений и живых
// непрочитанных: нераспознанный апдейт молча и с переподпиской, закрытый канал —
// без неё.
func TestLiveSourceInfoResubscribesUnlessChannelClosed(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		valid  tea.Msg
		closed tea.Msg
	}{
		{
			name:   "число участников",
			valid:  wallGroupCountUpdateMsg{valid: false},
			closed: wallGroupCountUpdateMsg{closed: true},
		},
		{
			name:   "смена статуса",
			valid:  wallUserStatusUpdateMsg{valid: false},
			closed: wallUserStatusUpdateMsg{closed: true},
		},
		{
			name:   "снимок пользователя",
			valid:  wallUserProfileUpdateMsg{valid: false},
			closed: wallUserProfileUpdateMsg{closed: true},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			m := sourceInfoModel(t)
			cards, cursor, scrollTop := len(m.cards), m.cursor, m.scrollTop

			next, cmd := applyLiveSourceInfoUpdate(t, m, testCase.valid)
			if cmd == nil {
				t.Error("на нераспознанный апдейт переподписка не вернулась — сведения перестанут жить")
			}
			if len(next.cards) != cards || next.cursor != cursor || next.scrollTop != scrollTop {
				t.Errorf("нераспознанный апдейт изменил стену: карточек %d->%d, курсор %d->%d, окно %d->%d",
					cards, len(next.cards), cursor, next.cursor, scrollTop, next.scrollTop)
			}

			if _, cmd := applyLiveSourceInfoUpdate(t, m, testCase.closed); cmd != nil {
				t.Error("на закрытый канал вернулась команда — ждать нечего, она зависнет")
			}
		})
	}
}

// Сами команды ожидания: читают апдейт из канала и заворачивают его в своё
// сообщение. Без отдельной проверки на command-функцию можно было бы и не позвать
// вовсе — а именно они и стоят в Init().
func TestWaitForSourceInfoUpdatesParsesTDLibEvent(t *testing.T) {
	client := newWallLiveTestClient()
	client.groupCountUpdates <- wallLiveSupergroupUpdate(303, 1337)
	client.userStatusUpdates <- wallLiveUserStatusUpdate(101,
		map[string]interface{}{"@type": "userStatusOnline", "expires": float64(1)})
	client.userProfileUpdates <- wallLiveUserProfileUpdate(101, "Марина",
		map[string]interface{}{"@type": "userStatusRecently"})
	m := New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)

	raw := m.waitForWallGroupCountUpdate()()
	countUpdate, ok := raw.(wallGroupCountUpdateMsg)
	if !ok {
		t.Fatalf("ожидание числа участников вернуло %T, ждали wallGroupCountUpdateMsg", raw)
	}
	if countUpdate.closed || !countUpdate.valid || countUpdate.groupID != 303 || countUpdate.memberCount != 1337 {
		t.Fatalf("разобранный updateSupergroup = %+v", countUpdate)
	}

	raw = m.waitForWallUserStatusUpdate()()
	statusUpdate, ok := raw.(wallUserStatusUpdateMsg)
	if !ok {
		t.Fatalf("ожидание статуса вернуло %T, ждали wallUserStatusUpdateMsg", raw)
	}
	if statusUpdate.closed || !statusUpdate.valid || statusUpdate.userID != 101 ||
		statusUpdate.status.Kind != auth.UserStatusOnline {
		t.Fatalf("разобранный updateUserStatus = %+v", statusUpdate)
	}

	raw = m.waitForWallUserProfileUpdate()()
	profileUpdate, ok := raw.(wallUserProfileUpdateMsg)
	if !ok {
		t.Fatalf("ожидание снимка пользователя вернуло %T, ждали wallUserProfileUpdateMsg", raw)
	}
	if profileUpdate.closed || !profileUpdate.valid || profileUpdate.userID != 101 ||
		profileUpdate.status.Kind != auth.UserStatusRecently {
		t.Fatalf("разобранный updateUser = %+v", profileUpdate)
	}
}

// Отменённый контекст закрывает каждое из трёх ожиданий: иначе команда из
// Init() висела бы вечно на выходе из программы.
func TestWaitForSourceInfoUpdatesStopOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := New(ctx, newWallLiveTestClient(), "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	cancel()

	for name, raw := range map[string]tea.Msg{
		"число участников": m.waitForWallGroupCountUpdate()(),
		"смена статуса":    m.waitForWallUserStatusUpdate()(),
		"снимок":           m.waitForWallUserProfileUpdate()(),
	} {
		closed := false
		switch update := raw.(type) {
		case wallGroupCountUpdateMsg:
			closed = update.closed
		case wallUserStatusUpdateMsg:
			closed = update.closed
		case wallUserProfileUpdateMsg:
			closed = update.closed
		default:
			t.Fatalf("%s: неожиданный тип %T", name, raw)
		}
		if !closed {
			t.Errorf("%s: ожидание по отменённому контексту не вернуло closed: %#v", name, raw)
		}
	}
}
