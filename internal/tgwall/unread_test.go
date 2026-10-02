package tgwall

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/auth"
	"telecli/internal/config"
)

// wallLiveReadInboxUpdate — updateChatReadInbox в той форме, которую отдаёт
// TDLib. Формат скопирован из internal/tgclitui/live_test.go
// (chatReadInboxUpdate): проверяется обновление числа на карточке, а не разбор
// JSON (он уже покрыт тестами auth.ParseChatReadInboxUpdate).
func wallLiveReadInboxUpdate(chatID int64, unreadCount int32) map[string]interface{} {
	return map[string]interface{}{
		"@type":        "updateChatReadInbox",
		"chat_id":      float64(chatID),
		"unread_count": float64(unreadCount),
	}
}

// applyLiveUnreadUpdate — прогон живого апдейта непрочитанных через настоящий
// Update, как это делает программа, и возврат модели и команды.
func applyLiveUnreadUpdate(t *testing.T, m Model, update wallUnreadUpdateMsg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(update)
	return next.(Model), cmd
}

// Снимок несёт число непрочитанных из того же auth.Chat, из которого берутся
// заголовок и тег: если бы wallCard его не переносил, счётчик на стене всегда
// был бы нулём, то есть показывал бы точку наравне с прочитанным.
func TestWallCardTakesUnreadCountFromChat(t *testing.T) {
	cases := []struct {
		name        string
		kind        auth.ChatKind
		unreadCount int32
	}{
		{name: "канал без непрочитанных", kind: auth.ChatChannel, unreadCount: 0},
		{name: "чат с непрочитанными", kind: auth.ChatGroup, unreadCount: 3},
		{name: "личное с непрочитанными", kind: auth.ChatPrivate, unreadCount: 128},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			chat := auth.Chat{ID: 7, Title: "Источник", Kind: testCase.kind, UnreadCount: testCase.unreadCount}
			got := wallCard(chat, auth.Message{ID: 1, Text: "текст", Date: 100})
			if got.UnreadCount != testCase.unreadCount {
				t.Fatalf("UnreadCount = %d, ждали %d", got.UnreadCount, testCase.unreadCount)
			}
		})
	}
}

// Правило маркера целиком, отдельно от отрисовки: ноль и отрицательное —
// ничего (точка-заглушка убрана по прямому указанию человека, 2026-09-30, «не
// нравятся»), 1..99 — точное число в скобках, сотня и больше — «99+».
// Проверяется и граница 99/100 (самый вероятный промах), и то, что предел не
// срезает числа до 99.
func TestUnreadMarker(t *testing.T) {
	cases := []struct {
		unreadCount int32
		want        string
	}{
		{unreadCount: 0, want: ""},
		{unreadCount: -1, want: ""},
		{unreadCount: 1, want: "[1]"},
		{unreadCount: 9, want: "[9]"},
		{unreadCount: 99, want: "[99]"},
		{unreadCount: 100, want: "[99+]"},
		{unreadCount: 1000, want: "[99+]"},
	}
	for _, testCase := range cases {
		t.Run(fmt.Sprintf("UnreadCount=%d", testCase.unreadCount), func(t *testing.T) {
			got := unreadMarker(card{UnreadCount: testCase.unreadCount})
			if got != testCase.want {
				t.Fatalf("unreadMarker = %q, ждали %q", got, testCase.want)
			}
		})
	}
}

// counterPattern — вид счётчика в готовой строке: «[12]» или «[99+]». Именно
// он, а не просто «[», потому что квадратные скобки есть и в заголовке
// карточки («[Новости]»), и проверка на них ничего бы не говорила.
var counterPattern = regexp.MustCompile(`\[\d+\]|\[99\+\]`)

// Отрисовка маркера: при непрочитанных — счётчик в скобках ЦВЕТОМ ТИПА
// ИСТОЧНИКА; без непрочитанных — ничего вовсе (точки-заглушки нет, убрана по
// прямому указанию человека, 2026-09-30). Проверяется именно цвет: счётчик
// серым был бы неотличим от приглушённого текста тега рядом с ним. Маркер
// красится акцентом ВСЕГДА, независимо от выделения карточки — в отличие от
// левой границы (см. TestCardAccentBorderOnBothLines), у которой акцент
// теперь только на выделенной карточке; это два разных, намеренно не связанных
// решения, здесь не сверяется одно с другим.
func TestRenderCardTagShowsCounterInsteadOfDot(t *testing.T) {
	t.Run("есть непрочитанные", func(t *testing.T) {
		c := card{Type: cardChannel, Name: "Новости", Time: "13:04", Text: "пост", Tag: "#канал", UnreadCount: 12}
		line := renderCard(c, 100, false)[0]
		plain := ansi.Strip(line)
		if !strings.Contains(plain, "[12]") {
			t.Fatalf("счётчика [12] нет в строке: %q", plain)
		}
		if strings.Contains(plain, "●") {
			t.Fatalf("точка осталась рядом со счётчиком: %q", plain)
		}
		accent := foregroundSGR(c.accent(), PaletteBackgroundMain)
		if !strings.Contains(line, accent+"[12]") {
			t.Fatalf("счётчик покрашен не в цвет типа источника: %q", line)
		}
	})

	t.Run("нет непрочитанных", func(t *testing.T) {
		c := card{Type: cardChannel, Name: "Новости", Time: "13:04", Text: "пост", Tag: "#канал", UnreadCount: 0}
		line := renderCard(c, 100, false)[0]
		plain := ansi.Strip(line)
		if strings.Contains(plain, "●") {
			t.Fatalf("точка-заглушка осталась в строке без непрочитанных (убрана по указанию человека): %q", plain)
		}
		if counterPattern.MatchString(plain) {
			t.Fatalf("счётчик в скобках показан при нулевом числе непрочитанных: %q", plain)
		}
	})
}

// Правый край блока тега — это правый край карточки, и он не должен зависеть
// от того, что стоит в конце блока: «[1]» — три ячейки, «[12]» — четыре,
// «[99+]» — пять. Раньше ширины колонки на это не хватало, и самый широкий
// маркер выдавливал бы блок за правый край карточки. Случай «непрочитанных
// нет» сюда не входит: маркера тогда нет вовсе (точка-заглушка убрана,
// 2026-09-30), выравнивать нечего.
func TestCardTagRightEdgeAlignsWithCardRightEdgeForCounters(t *testing.T) {
	width := 100
	markers := []struct {
		name        string
		unreadCount int32
		marker      string
		tag         string
	}{
		{name: "однозначный счётчик", unreadCount: 1, marker: "[1]", tag: "#личное"},
		{name: "двузначный счётчик", unreadCount: 12, marker: "[12]", tag: "#канал"},
		{name: "предел 99+", unreadCount: 250, marker: "[99+]", tag: "#личное"},
	}
	wantEdge := width - cardMarginH
	for _, testCase := range markers {
		t.Run(testCase.name, func(t *testing.T) {
			c := card{
				Type:        cardPersonal,
				Name:        "Андрей",
				Time:        "13:12",
				Text:        "Купи хлеба",
				Tag:         testCase.tag,
				UnreadCount: testCase.unreadCount,
			}
			line := renderCard(c, width, false)[0]
			plain := ansi.Strip(line)
			index := strings.LastIndex(plain, testCase.marker)
			if index < 0 {
				t.Fatalf("маркер %q не найден в строке: %q", testCase.marker, plain)
			}
			// Срез — по байтовой длине символа (len), а не по его видимой ширине:
			// «●» — три байта в UTF-8, но одна клетка на экране.
			gotEdge := cellWidth(plain[:index+len(testCase.marker)])
			if gotEdge != wantEdge {
				t.Fatalf("правый край маркера = %d, ждали %d (правый край карточки)", gotEdge, wantEdge)
			}
			if got := cellWidth(plain); got != width {
				t.Fatalf("строка карточки шириной %d, ждали %d — блок тега вылез за карточку", got, width)
			}
		})
	}
}

// Живой апдейт меняет ТОЛЬКО число. Карточка источника не переставляется, поток
// не меняется, курсор и окно не трогаются: перестановка на стене — это
// новое сообщение, а не «у этого источника стало больше непрочитанных».
// Если бы апдейт проходил по логике живых сообщений, карточка уехала бы вниз и
// уводила курсор за собой.
func TestLiveUnreadUpdateChangesNumberWithoutMovingCard(t *testing.T) {
	m := liveWallModel(t, 3)
	order := chatIDOrder(m.cards)
	cards, cursor, scrollTop := len(m.cards), m.cursor, m.scrollTop
	target := liveWallChat(0).ID

	next, _ := applyLiveUnreadUpdate(t, m, wallUnreadUpdateMsg{valid: true, chatID: target, unreadCount: 7})

	if got := chatIDOrder(next.cards); !equalInt64s(got, order) {
		t.Fatalf("порядок карточек изменился: %v -> %v", order, got)
	}
	if len(next.cards) != cards {
		t.Fatalf("число карточек изменилось: %d -> %d", cards, len(next.cards))
	}
	if next.cursor != cursor {
		t.Errorf("курсор сдвинулся: %d -> %d", cursor, next.cursor)
	}
	if next.scrollTop != scrollTop {
		t.Errorf("окно прокрутки сдвинулось: %d -> %d", scrollTop, next.scrollTop)
	}
	if got := unreadCountOf(next, target); got != 7 {
		t.Errorf("UnreadCount источника = %d, ждали 7", got)
	}
	// Остальные карточки не задеты: цикл обязан остановиться на первом
	// совпадении, а не пройти по всем.
	for _, existing := range next.cards {
		if existing.ChatID != target && existing.UnreadCount != 0 {
			t.Errorf("у постороннего источника %d появился UnreadCount = %d", existing.ChatID, existing.UnreadCount)
		}
	}
}

// Найдено при ревью оркестратора: applyWallMessageUpdate пересобирает
// карточку через wallCard(chatsByID[...], message), а chatsByID — это снимок
// со старым UnreadCount (обычно нулевым). Без переноса значения со старой
// карточки счётчик мигнул бы к значению снимка на один кадр при каждом новом
// сообщении источника, у которого уже обновился живой счётчик — и вернулся
// бы к правильному только следующим updateChatReadInbox, который TDLib шлёт
// вместе с сообщением, но не гарантированно раньше него.
func TestLiveMessageDoesNotResetUnreadCountToSnapshotValue(t *testing.T) {
	m := liveWallModel(t, 3)
	target := liveWallChat(0).ID

	next, _ := applyLiveUnreadUpdate(t, m, wallUnreadUpdateMsg{valid: true, chatID: target, unreadCount: 5})
	if unreadCountOf(next, target) != 5 {
		t.Fatalf("подготовка: UnreadCount = %d, ждали 5", unreadCountOf(next, target))
	}

	next, _ = applyLiveUpdate(t, next, wallMessageUpdateMsg{
		valid:   true,
		chatID:  target,
		message: auth.Message{ID: 900, Date: 9000, Text: "ещё одно"},
	})
	if got := unreadCountOf(next, target); got != 5 {
		t.Fatalf("после нового сообщения UnreadCount = %d, ждали 5 (перенесено со старой карточки, не сброшено к снимку)", got)
	}
}

// Счётчик уходит и в ноль (чат прочитан в другом клиенте) — тогда маркер
// снова пуст, а не «[0]» и не точка-заглушка (убрана, 2026-09-30).
func TestLiveUnreadUpdateToZeroRestoresEmptyMarker(t *testing.T) {
	m := liveWallModel(t, 1)
	target := liveWallChat(0).ID
	next, _ := applyLiveUnreadUpdate(t, m, wallUnreadUpdateMsg{valid: true, chatID: target, unreadCount: 5})
	if unreadCountOf(next, target) != 5 {
		t.Fatalf("первый апдейт не применился")
	}

	next, _ = applyLiveUnreadUpdate(t, next, wallUnreadUpdateMsg{valid: true, chatID: target, unreadCount: 0})
	if got := unreadCountOf(next, target); got != 0 {
		t.Fatalf("UnreadCount после прочтения = %d, ждали 0", got)
	}
	plain := ansi.Strip(renderCard(next.cards[0], 100, false)[0])
	if strings.Contains(plain, "●") {
		t.Fatalf("после прочтения на карточке осталась точка-заглушка: %q", plain)
	}
	if counterPattern.MatchString(plain) {
		t.Fatalf("после прочтения на карточке остался счётчик в скобках: %q", plain)
	}
}

// Апдейт по источнику, которого на стене нет, — стена не меняется вовсе:
// молчаливый игнор, ровно как у живых сообщений (живость списка чатов —
// отдельная работа).
func TestLiveUnreadUpdateForUnknownChatChangesNothing(t *testing.T) {
	m := liveWallModel(t, 2)
	before := append([]card(nil), m.cards...)
	cursor, scrollTop := m.cursor, m.scrollTop

	// 90 — чат снимка без истории, карточки на стене у него нет; заведомо
	// чужой id взят нарочно, чтобы не спутать «нет карточки» с «нет чата».
	next, _ := applyLiveUnreadUpdate(t, m, wallUnreadUpdateMsg{valid: true, chatID: 90, unreadCount: 4})
	next, _ = applyLiveUnreadUpdate(t, next, wallUnreadUpdateMsg{valid: true, chatID: 4242, unreadCount: 4})

	if len(next.cards) != len(before) {
		t.Fatalf("число карточек изменилось: %d -> %d", len(before), len(next.cards))
	}
	for index := range before {
		if next.cards[index] != before[index] {
			t.Fatalf("карточка %d изменилась: %+v -> %+v", index, before[index], next.cards[index])
		}
	}
	if next.cursor != cursor || next.scrollTop != scrollTop {
		t.Errorf("курсор/окно сдвинулись: курсор %d->%d, окно %d->%d", cursor, next.cursor, scrollTop, next.scrollTop)
	}
}

// Переподписка на непрочитанные — тот же трёхвариантный контракт, что у живых
// сообщений: нераспознанный апдейт молча и с переподпиской, закрытый канал —
// без неё (ждать нечего, иначе стена завесила бы мёртвой командой).
func TestLiveUnreadUpdateResubscribesUnlessChannelClosed(t *testing.T) {
	m := liveWallModel(t, 3)
	cards, cursor, scrollTop := len(m.cards), m.cursor, m.scrollTop

	next, cmd := applyLiveUnreadUpdate(t, m, wallUnreadUpdateMsg{valid: false, chatID: liveWallChat(0).ID})
	if cmd == nil {
		t.Error("на нераспознанный апдейт переподписка не вернулась — счётчики перестанут жить")
	}
	if len(next.cards) != cards || next.cursor != cursor || next.scrollTop != scrollTop {
		t.Errorf("нераспознанный апдейт изменил стену: карточек %d->%d, курсор %d->%d, окно %d->%d",
			cards, len(next.cards), cursor, next.cursor, scrollTop, next.scrollTop)
	}

	next, cmd = applyLiveUnreadUpdate(t, m, wallUnreadUpdateMsg{closed: true})
	if cmd != nil {
		t.Error("на закрытый канал вернулась команда — ждать нечего, она зависнет")
	}
	if len(next.cards) != cards || next.cursor != cursor {
		t.Errorf("закрытый канал изменил стену: карточек %d->%d, курсор %d->%d",
			cards, len(next.cards), cursor, next.cursor)
	}
}

// Сам waitForWallUnreadUpdate: читает апдейт из канала и заворачивает его в
// wallUnreadUpdateMsg. Без отдельного теста проверялся бы только разбор уже
// готового сообщения, а на command-функцию можно было бы и не позвать вовсе.
func TestWaitForWallUnreadUpdateParsesTDLibEvent(t *testing.T) {
	client := newWallLiveTestClient()
	client.unreadUpdates <- wallLiveReadInboxUpdate(7, 42)
	m := New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)

	raw := m.waitForWallUnreadUpdate()()
	update, ok := raw.(wallUnreadUpdateMsg)
	if !ok {
		t.Fatalf("ожидание вернуло %T, ждали wallUnreadUpdateMsg", raw)
	}
	if update.closed || !update.valid || update.chatID != 7 || update.unreadCount != 42 {
		t.Fatalf("разобранный апдейт = %+v", update)
	}
}

// Отменённый контекст закрывает ожидание: иначе команда из Init() висела бы
// вечно на выходе из программы.
func TestWaitForWallUnreadUpdateStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := New(ctx, newWallLiveTestClient(), "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	cancel()

	raw := m.waitForWallUnreadUpdate()()
	update, ok := raw.(wallUnreadUpdateMsg)
	if !ok || !update.closed {
		t.Fatalf("ожидание по отменённому контексту = %#v, ждали closed", raw)
	}
}

// Канал, закрытый уже во время работы (не «его нет», а его закрыли), — тот же
// closed: ждать больше нечего, и переподписка на него только завесила бы стену
// мёртвой командой.
func TestWaitForWallUnreadUpdateStopsOnClosedChannel(t *testing.T) {
	client := newWallLiveTestClient()
	close(client.unreadUpdates)
	m := New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)

	raw := m.waitForWallUnreadUpdate()()
	update, ok := raw.(wallUnreadUpdateMsg)
	if !ok || !update.closed {
		t.Fatalf("ожидание по закрытому каналу = %#v, ждали closed", raw)
	}
}

// Клиента нет и канала апдейтов нет — оба случая обязаны отдавать closed сразу:
// ждать в канале, которого не будет никогда, значило бы зависнуть в Init().
func TestWaitForWallUnreadUpdateWithoutClientOrChannel(t *testing.T) {
	assertClosed := func(name string, m Model) {
		t.Helper()
		update, ok := m.waitForWallUnreadUpdate()().(wallUnreadUpdateMsg)
		if !ok || !update.closed || update.valid {
			t.Fatalf("%s: ожидание = %#v, ждали closed", name, update)
		}
	}
	// wallLoadClient в ChatReadInboxUpdates() отдаёт nil — так же ведёт себя
	// клиент, у которого поток апдейтов не подключён.
	assertClosed("клиент без канала апдейтов", New(context.Background(), newWallLoadClient(wallTestChats(), wallTestHistory()), "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion))
	assertClosed("клиент без клиента", New(context.Background(), nil, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion))
}

// Init() обязан подписаться и на живые сообщения, и на числа непрочитанных: без
// второго счётчик на стене остался бы таким, каким был на момент снимка.
func TestInitSubscribesToLiveUnreadUpdates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := newWallLiveTestClient()
	m := New(ctx, client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	cancel()

	raw := m.Init()()
	batch, ok := raw.(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init вернул %T, ждали tea.BatchMsg", raw)
	}
	sawUnread := false
	for _, cmd := range batch {
		if cmd == nil {
			continue
		}
		if update, ok := cmd().(wallUnreadUpdateMsg); ok {
			sawUnread = update.closed
		}
	}
	if !sawUnread {
		t.Error("Init не запустил подписку на обновления непрочитанных")
	}
}

// chatIDOrder — источники карточек в порядке потока: по нему видно, что
// карточка именно ПЕРЕСТАЛА, а не просто получила другое число.
func chatIDOrder(cards []card) []int64 {
	order := make([]int64, len(cards))
	for index, item := range cards {
		order[index] = item.ChatID
	}
	return order
}

func equalInt64s(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// unreadCountOf — число непрочитанных на карточке источника, -1 если такой
// карточки на стене нет (тогда проверка обязана упасть сама).
func unreadCountOf(m Model, chatID int64) int32 {
	for _, item := range m.cards {
		if item.ChatID == chatID {
			return item.UnreadCount
		}
	}
	return -1
}
