package tgwall

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/auth"
	"telecli/internal/config"
)

// Удаление сообщения по Delete с подтверждением. Механика перенесена из ленты
// tgcli (internal/tgclitui/confirm_test.go), и тесты проверяют тот же контракт:
// вопрос с превью, Enter — реальный запрос deleteMessages и карточка исчезает
// сразу, Esc — ничего не отправляет, а открытая модалка не пропускает ввод
// дальше. Отличия стены (своя палитра, целая стена вместо ленты) проверяются
// своими же утверждениями: ширины блока считаются cellWidth, а не lipgloss.

type wallDeleteClient struct {
	*wallLoadClient
	// deleteMu защищает список удалений: команда удаления исполняется в
	// отдельной горутине bubbletea, а тест читает список после неё.
	deleteMu sync.Mutex
	deletes  []map[string]interface{}
	// deleteErr — deleteMessages отвечает ошибкой вместо "ok".
	deleteErr error
}

func newWallDeleteClient() *wallDeleteClient {
	return &wallDeleteClient{wallLoadClient: newWallLoadClient(wallTestChats(), wallTestHistory())}
}

func (c *wallDeleteClient) Send(ctx context.Context, request map[string]interface{}) (map[string]interface{}, error) {
	if request["@type"] != "deleteMessages" {
		return c.wallLoadClient.Send(ctx, request)
	}
	c.deleteMu.Lock()
	c.deletes = append(c.deletes, request)
	c.deleteMu.Unlock()
	if c.deleteErr != nil {
		return nil, c.deleteErr
	}
	return map[string]interface{}{"@type": "ok"}, nil
}

// deleteRequests — что ушло в TDLib на удаление, снимается с мока, а не с
// модели: проверяется, что ушло в Telegram, а не что модель о себе думает.
func (c *wallDeleteClient) deleteRequests() []map[string]interface{} {
	c.deleteMu.Lock()
	defer c.deleteMu.Unlock()
	return append([]map[string]interface{}(nil), c.deletes...)
}

// confirmTestCards — три карточки разных источников, у каждой своё
// сообщение с настоящим id: по id и проверяется, что удаляется именно та
// карточка, которую человек видел под курсором.
func confirmTestCards() []card {
	return wallCards([]wallMessage{
		{Chat: auth.Chat{ID: 1, Title: "Новости", Kind: auth.ChatChannel}, Message: auth.Message{ID: 11, Text: "первое", Date: 1}},
		{Chat: auth.Chat{ID: 2, Title: "Соседи", Kind: auth.ChatGroup}, Message: auth.Message{ID: 22, Text: "второе", Date: 2}},
		{Chat: auth.Chat{ID: 3, Title: "Андрей", Kind: auth.ChatPrivate}, Message: auth.Message{ID: 33, Text: "третье", Date: 3}},
	})
}

func newConfirmTestModel(t *testing.T, client auth.TDClientInterface, width, height int, cards []card) Model {
	t.Helper()
	m := New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = next.(Model)
	next, _ = m.Update(wallLoadedMsg{cards: cards, chats: wallTestChats()})
	m = next.(Model)
	if m.loading {
		t.Fatal("после wallLoadedMsg стена осталась в состоянии загрузки")
	}
	if m.input == nil {
		t.Fatal("поле ввода не создано")
	}
	// Фокус в живой программе ставит команда из Init(), а в тесте её никто не
	// исполнит, и виджет без фокуса молча игнорировал бы весь ввод.
	_ = m.input.Focus()
	return m
}

func confirmTestModel(t *testing.T, client auth.TDClientInterface, width, height int) Model {
	t.Helper()
	return newConfirmTestModel(t, client, width, height, confirmTestCards())
}

// pressWallKey — нажатие через настоящий Update, как это делает программа.
// Проверять обработчик модалки прямым вызовом нельзя: неотличимое от «всё прошло
// хорошо» поведение обычно живёт именно в недостающем перехвате клавиши.
func pressWallKey(t *testing.T, m Model, msg tea.Msg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("Update вернул %T, ждали Model", next)
	}
	return updated, cmd
}

func openWallConfirm(t *testing.T, m Model) Model {
	t.Helper()
	m, _ = pressWallKey(t, m, tea.KeyPressMsg{Code: tea.KeyDelete})
	if !m.showConfirm {
		t.Fatal("Delete не открыл модалку подтверждения")
	}
	return m
}

// cardTexts — тексты карточек в порядке потока: после удаления тест должен
// видеть по нему, какая карточка исчезла.
func cardTexts(m Model) []string {
	texts := make([]string, 0, len(m.cards))
	for _, item := range m.cards {
		texts = append(texts, item.Text)
	}
	return texts
}

func assertCardTexts(t *testing.T, m Model, want ...string) {
	t.Helper()
	got := cardTexts(m)
	if len(got) != len(want) {
		t.Fatalf("карточки = %v, ждали %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("карточки = %v, ждали %v", got, want)
		}
	}
}

func inputBody(m Model) string {
	if m.input == nil {
		return ""
	}
	return m.input.Value()
}

// TestDeleteOpensConfirmForCardUnderCursor — Delete открывает вопрос с коротким
// превью текста КАРТОЧКИ ПОД КУСОРОМ: на стене карточка и есть «выделенное
// сообщение», отдельного выделения внутри чата тут нет.
func TestDeleteOpensConfirmForCardUnderCursor(t *testing.T) {
	m := confirmTestModel(t, newWallDeleteClient(), 100, 24)

	m = openWallConfirm(t, m)

	if m.confirmChatID != 3 || m.confirmMessageID != 33 {
		t.Fatalf("цель подтверждения = %d/%d, ждали 3/33 (карточка под курсором)", m.confirmChatID, m.confirmMessageID)
	}
	if !strings.Contains(m.confirmPrompt, confirmDeleteQuestion) {
		t.Fatalf("вопрос = %q, ждали текст удаления", m.confirmPrompt)
	}
	if !strings.Contains(m.confirmPrompt, "третье") {
		t.Fatalf("вопрос = %q, ждали превью текста карточки", m.confirmPrompt)
	}

	// Ищем по тексту без ANSI: в v2 Render() всегда даёт полноцветный вывод,
	// и между двумя половинами кнопки всегда стоит SGR-последовательность.
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{confirmDeleteQuestion, "Да / Нет", "↵ — да", "esc — нет"} {
		if !strings.Contains(view, want) {
			t.Fatalf("на экране модалки нет %q: %q", want, view)
		}
	}
}

// TestDeleteOpensConfirmForCursorNotForLatest — «выделено» — это карточка под
// курсором, а не «последнее сообщение вообще»: после ухода курсора вверх
// Delete обязан спросить про ту карточку, которую человек видит выделенной.
func TestDeleteOpensConfirmForCursorNotForLatest(t *testing.T) {
	m := confirmTestModel(t, newWallDeleteClient(), 100, 24)
	m = m.moveCursor(-1)

	m = openWallConfirm(t, m)

	if m.confirmChatID != 2 || m.confirmMessageID != 22 {
		t.Fatalf("цель подтверждения = %d/%d, ждали 2/22 (курсор стоит на второй карточке)", m.confirmChatID, m.confirmMessageID)
	}
	if !strings.Contains(m.confirmPrompt, "второе") {
		t.Fatalf("вопрос = %q, ждали превью текста карточки под курсором", m.confirmPrompt)
	}
}

// Выбор цели удаления в открытой переписке (задача 0161). Условие то же, что у
// Ctrl+R: переписка или карточка стены — по zoomHasFocus.
//
// Фикстура переписки здесь своя, а не zoomHistory, и на то две причины.
//
// Первая: Delete проверяется там, где «последнее сообщение чата» и «карточка
// стены» СОВПАДАЮТ (на стене карточка — это ровно последнее сообщение
// источника, задача 0152). Из-за совпадения промах по любой из двух неверных
// ветвей даёт один и тот же неверный ответ, а верный от обоих отличается: одна
// проверка ловит обе ошибки сразу. Общая фикстура этого не даёт — её короткие
// истории мок добивает до предела переписки своим хвостом (см.
// wallLoadClient.fullHistory), и последним сообщением оказывается не настоящее.
//
// Вторая: тексты сообщений должны быть различимы, иначе по вопросу модалки
// нельзя отличить одно сообщение от другого.

// zoomDeleteChat — источник переписки в этих тестах. Тот же, что у общей
// фикстуры стены (wallTestChats()[1]): карточка и переписка должны быть про один
// чат, иначе проверялось бы не то расхождение, которое чинится.
var zoomDeleteChat = wallTestChats()[1]

// Нумерация переписки заведомо не совпадает с ID карточек стены (11/22/33 в
// confirmTestCards): иначе промах «взял не то сообщение» нельзя было бы заметить
// по одному только числу.
const (
	zoomDeleteFirstID   = 1001
	zoomDeleteFirstDate = 5000
	// Последнее сообщение переписки — оно же стоит на карточке стены.
	zoomDeleteLastID = zoomDeleteFirstID + wallZoomMessagesLimit - 1
	// Сообщение перед последним: на него уводится курсор переписки.
	zoomDeleteSelectedID = zoomDeleteLastID - 1
	// Тексты ждущих ответа: у карточки стены и у сообщения под курсором они
	// разные, иначе утверждение «вопрос называет НЕ текст карточки» было бы
	// пустым.
	zoomDeleteLastText     = "сообщение 50"
	zoomDeleteSelectedText = "сообщение 49"
)

// zoomDeleteHistory — переписка чата 2 ровно на wallZoomMessagesLimit сообщений.
// Ровно на предел, а не больше и не меньше: короче — мок добьёт хвостом
// (fullHistory) и последним станет не то сообщение, длиннее — обрежется до
// последних 50, и та же подмена.
func zoomDeleteHistory() map[int64][]auth.Message {
	messages := make([]auth.Message, 0, wallZoomMessagesLimit)
	for index := range wallZoomMessagesLimit {
		messages = append(messages, auth.Message{
			ID:   zoomDeleteFirstID + int64(index),
			Text: fmt.Sprintf("сообщение %d", index+1),
			Date: zoomDeleteFirstDate + int64(index),
		})
	}
	return map[int64][]auth.Message{zoomDeleteChat.ID: messages}
}

// zoomDeleteCards — одна карточка стены, и она же последнее сообщение переписки.
// Карточка одна намеренно: расхождение «карточка стены ≠ сообщение под курсором
// переписки» должен показывать курсор переписки, а не вторая карточка стены —
// иначе тест проходил бы и на неверной ветке, просто потому что под курсором
// стены оказалось бы другое сообщение.
func zoomDeleteCards() []card {
	history := zoomDeleteHistory()[zoomDeleteChat.ID]
	return wallCards([]wallMessage{{Chat: zoomDeleteChat, Message: history[len(history)-1]}})
}

// newZoomDeleteModel — широкая стена по размеру терминала с этой фикстурой.
// Ширина 100 — широкий режим, где переписка открыта сама за курсором стены, а
// фокус по умолчанию на списке (его переводит Tab в zoomDeleteModel).
func newZoomDeleteModel(t *testing.T, client auth.TDClientInterface) Model {
	t.Helper()
	return newConfirmTestModel(t, client, 100, 30, zoomDeleteCards())
}

// newZoomDeleteClient — мок, отдающий историю этой переписки И ловящий
// deleteMessages. Раздельные моки (zoomClient ловит историю, wallDeleteClient —
// удаление) не годятся: нужен один и тот же чат и в переписке, и в удалении.
func newZoomDeleteClient() *wallDeleteClient {
	// fullHistoryLimit НЕ задаётся намеренно: история и так ровно на предел
	// переписки, и добивка ей не нужна (см. zoomDeleteHistory).
	return &wallDeleteClient{wallLoadClient: newWallLoadClient(wallTestChats(), zoomDeleteHistory())}
}

// zoomDeleteModel — стена с открытой перепиской и её настоящей, загруженной
// историей. Курсор переписки всегда уводится с последнего сообщения на
// предыдущее, а focus решает только одно — на ком фокус: на переписке (Tab) или
// на стене (Tab туда и обратно).
//
// Порядок именно такой, и это не просто аккуратность: сначала фокус на
// переписке и только потом уход с неё. Пока фокус на стене, стрелка двигает
// карточки стены, а курсор переписки так и остался бы на последнем сообщении —
// а это ровно то сообщение, на котором стоит карточка стены. Проверка «фокус на
// стене» при таком раскладе отличала бы правильный ответ от неверного только
// по совпадению, то есть ничего бы не проверяла.
func zoomDeleteModel(t *testing.T, m Model, focus bool) Model {
	t.Helper()
	if m.zoom == nil || m.zoom.chatID != zoomDeleteChat.ID {
		t.Fatal("подготовка: переписка источника под курсором не открыта")
	}
	if len(m.cards) != 1 || m.cards[0].MessageID != zoomDeleteLastID {
		t.Fatalf("подготовка: карточек %d, под курсором %d, ждали одну с сообщением %d",
			len(m.cards), m.cards[0].MessageID, zoomDeleteLastID)
	}
	m = runZoomLoad(t, m, m.wallZoomLoadedCmd(m.zoom.panel, m.zoom.chatID, m.zoom.loadID))
	if got, want := len(m.zoom.messages), wallZoomMessagesLimit; got != want {
		t.Fatalf("в переписке %d сообщений, ждали %d", got, want)
	}
	focused, _ := pressWallKey(t, m, keyTab())
	if !focused.zoomHasFocus() {
		t.Fatal("Tab не перевёл фокус на переписку")
	}
	// Курсор переписки — на сообщение, которое НЕ является ни последним в чате,
	// ни карточкой стены. Заодно проверяется, что стрелка при фокусе на
	// переписке двигает именно её, а не стену.
	moved, _ := pressWallKey(t, focused, keyUp())
	if got := moved.zoom.messages[moved.zoom.cursor].ID; got != zoomDeleteSelectedID {
		t.Fatalf("под курсором переписки сообщение %d, ждали %d", got, zoomDeleteSelectedID)
	}
	if moved.cursor != m.cursor {
		t.Fatalf("↑ при фокусе на переписке сдвинул курсор стены на %d, ждали %d", moved.cursor, m.cursor)
	}
	if focus {
		return moved
	}
	// Фокус обратно на стену, а курсор переписки остаётся на выбранном
	// сообщении: теперь «фокус не на переписке» и «взяли сообщение переписки
	// вместо карточки стены» дают разные ответы, и различие видно.
	back, _ := pressWallKey(t, moved, keyTab())
	if back.zoomHasFocus() {
		t.Fatal("второй Tab не вернул фокус на стену")
	}
	if got := back.zoom.messages[back.zoom.cursor].ID; got != zoomDeleteSelectedID {
		t.Fatalf("курсор переписки уехал на %d при уходе фокуса, ждали %d", got, zoomDeleteSelectedID)
	}
	return back
}

// TestDeleteInFocusedZoomTargetsMessageUnderZoomCursor — Delete в открытой и
// СФОКУСИРОВАННОЙ переписке спрашивает про сообщение под курсором переписки, а
// не про карточку стены и не про последнее сообщение чата (в этой фикстуре оба
// — одно и то же сообщение, так что проверка ловит и ту, и другую ошибку).
// Заголовок модалки называет текст именно этого сообщения.
//
// Отдельно проверяется конец пути: Enter уходит в TDLib запросом на то же
// сообщение. Модель могла бы верно показать вопрос и удалить-таки не то.
func TestDeleteInFocusedZoomTargetsMessageUnderZoomCursor(t *testing.T) {
	client := newZoomDeleteClient()
	m := zoomDeleteModel(t, newZoomDeleteModel(t, client), true)

	m = openWallConfirm(t, m)

	if m.confirmChatID != zoomDeleteChat.ID || m.confirmMessageID != zoomDeleteSelectedID {
		t.Fatalf("цель подтверждения = %d/%d, ждали %d/%d (сообщение под курсором переписки)",
			m.confirmChatID, m.confirmMessageID, zoomDeleteChat.ID, zoomDeleteSelectedID)
	}
	if !strings.Contains(m.confirmPrompt, zoomDeleteSelectedText) {
		t.Fatalf("вопрос = %q, ждали превью текста сообщения под курсором переписки (%q)",
			m.confirmPrompt, zoomDeleteSelectedText)
	}
	if strings.Contains(m.confirmPrompt, zoomDeleteLastText) {
		t.Fatalf("вопрос = %q, ждали НЕ превью последнего сообщения чата (текста карточки стены %q)",
			m.confirmPrompt, zoomDeleteLastText)
	}

	m, cmd := pressWallKey(t, m, keyEnter())
	result, ok := cmd().(deleteMessageMsg)
	if !ok {
		t.Fatalf("Enter вернул %T, ждали deleteMessageMsg", cmd())
	}
	if result.err != nil {
		t.Fatalf("удаление не удалось: %v", result.err)
	}
	requests := client.deleteRequests()
	if len(requests) != 1 {
		t.Fatalf("запросов deleteMessages: %d, ждали 1", len(requests))
	}
	if requests[0]["chat_id"] != zoomDeleteChat.ID {
		t.Fatalf("chat_id = %#v, ждали %d (чат переписки)", requests[0]["chat_id"], zoomDeleteChat.ID)
	}
	ids, ok := requests[0]["message_ids"].([]int64)
	if !ok || len(ids) != 1 || ids[0] != zoomDeleteSelectedID {
		t.Fatalf("message_ids = %#v, ждали [%d]", requests[0]["message_ids"], zoomDeleteSelectedID)
	}
}

// TestDeleteWithOpenZoomButFocusOnWallTargetsWallCard — переписка открыта и
// курсор в ней стоит на сообщении, которого на стене нет, но фокус на стене:
// цель — карточка стены, то есть последнее сообщение чата. Ровно то же условие,
// что у Ctrl+R, отдельной проверки для Delete не заводится.
//
// Проверяется и обратное: вопрос НЕ называет сообщение, выбранное в переписке.
// Иначе тест прошёл бы и на коде, который условие фокуса вовсе игнорирует.
func TestDeleteWithOpenZoomButFocusOnWallTargetsWallCard(t *testing.T) {
	m := zoomDeleteModel(t, newZoomDeleteModel(t, newZoomDeleteClient()), false)

	m = openWallConfirm(t, m)

	if m.confirmChatID != zoomDeleteChat.ID || m.confirmMessageID != zoomDeleteLastID {
		t.Fatalf("цель подтверждения = %d/%d, ждали %d/%d (карточка стены под курсором)",
			m.confirmChatID, m.confirmMessageID, zoomDeleteChat.ID, zoomDeleteLastID)
	}
	if !strings.Contains(m.confirmPrompt, zoomDeleteLastText) {
		t.Fatalf("вопрос = %q, ждали превью текста карточки стены (%q)", m.confirmPrompt, zoomDeleteLastText)
	}
	if strings.Contains(m.confirmPrompt, zoomDeleteSelectedText) {
		t.Fatalf("вопрос = %q, ждали НЕ превью сообщения переписки (%q) — фокус на стене",
			m.confirmPrompt, zoomDeleteSelectedText)
	}
}

// TestDeleteInFocusedEmptyZoomDoesNothing — переписка открыта и в фокусе, но
// сообщений в ней нет: модалку без цели не показываем, ровно как на пустой
// стене (см. deleteTarget). Проверяются и то, что экран не поехал, и то, что
// запрос удаления не ушло.
func TestDeleteInFocusedEmptyZoomDoesNothing(t *testing.T) {
	client := newZoomDeleteClient()
	m := zoomDeleteModel(t, newZoomDeleteModel(t, client), false)
	// Пустой ответ getChatHistory — то же, что у реального чата без истории и то
	// же, что при сорванной загрузке: applyWallZoomLoaded оставляет переписку
	// открытой и пустой.
	next, _ := m.Update(wallZoomLoadedMsg{panel: m.zoom.panel, chatID: m.zoom.chatID, loadID: m.zoom.loadID})
	m = next.(Model)
	if m.zoom == nil || len(m.zoom.messages) != 0 {
		t.Fatalf("подготовка: переписка открыта=%v, сообщений %d — ждали открытую пустую",
			m.zoom != nil, len(m.zoom.messages))
	}
	focused, _ := pressWallKey(t, m, keyTab())
	if !focused.zoomHasFocus() {
		t.Fatal("Tab не перевёл фокус на переписку")
	}
	m = focused

	before := m.View().Content
	m, cmd := pressWallKey(t, m, tea.KeyPressMsg{Code: tea.KeyDelete})

	if m.showConfirm {
		t.Fatal("Delete открыл модалку в переписке без сообщений")
	}
	if cmd != nil {
		t.Fatalf("Delete в пустой переписке вернул команду %T, ждали nil", cmd())
	}
	if m.View().Content != before {
		t.Fatal("Delete в пустой переписке изменил экран")
	}
	if requests := client.deleteRequests(); len(requests) != 0 {
		t.Fatalf("Delete в пустой переписке отправил %d запросов удаления, ждали ни одного", len(requests))
	}
}

// TestDeleteOnEmptyWallDoesNothing — удалять нечего: ни модалки, ни запроса, ни
// сдвига курсора. Проверяется и экран целиком, а не только флаг: молчаливый
// no-op не должен даже на пиксель двигать кадр.
func TestDeleteOnEmptyWallDoesNothing(t *testing.T) {
	client := newWallDeleteClient()
	m := newConfirmTestModel(t, client, 100, 24, nil)
	before := m.View().Content

	m, cmd := pressWallKey(t, m, tea.KeyPressMsg{Code: tea.KeyDelete})

	if m.showConfirm {
		t.Fatal("Delete открыл модалку на пустой стене")
	}
	if len(m.cards) != 0 {
		t.Fatalf("карточек после Delete на пустой стене: %v", cardTexts(m))
	}
	if cmd != nil {
		t.Fatalf("Delete на пустой стене вернул команду %T, ждали nil", cmd())
	}
	if m.View().Content != before {
		t.Fatal("Delete на пустой стене изменил экран")
	}
	if requests := client.deleteRequests(); len(requests) != 0 {
		t.Fatalf("Delete на пустой стене отправил %d запросов удаления, ждали ни одного", len(requests))
	}
}

// TestEnterInConfirmSendsDeleteMessages — подтверждение удаляет ровно ту
// карточку, о которой спрашивали: реальный запрос deleteMessages с её
// chat_id/message_ids, revoke — только у себя (false), и карточка исчезает со
// стены сразу, не дожидаясь следующего снимка.
func TestEnterInConfirmSendsDeleteMessages(t *testing.T) {
	client := newWallDeleteClient()
	m := openWallConfirm(t, confirmTestModel(t, client, 100, 24))

	m, cmd := pressWallKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.showConfirm {
		t.Fatal("Enter не закрыл модалку")
	}
	if m.confirmChatID != 0 || m.confirmMessageID != 0 || m.confirmPrompt != "" {
		t.Fatalf("после Enter осталось состояние модалки: чат=%d сообщение=%d вопрос=%q",
			m.confirmChatID, m.confirmMessageID, m.confirmPrompt)
	}
	if cmd == nil {
		t.Fatal("Enter не вернул команду удаления")
	}
	result, ok := cmd().(deleteMessageMsg)
	if !ok {
		t.Fatal("Enter вернул не deleteMessageMsg")
	}
	if result.err != nil {
		t.Fatalf("удаление не удалось: %v", result.err)
	}
	if result.chatID != 3 || result.messageID != 33 {
		t.Fatalf("результат удаления = %d/%d, ждали 3/33", result.chatID, result.messageID)
	}

	requests := client.deleteRequests()
	if len(requests) != 1 {
		t.Fatalf("запросов deleteMessages: %d, ждали 1", len(requests))
	}
	if requests[0]["chat_id"] != int64(3) {
		t.Fatalf("chat_id = %#v, ждали 3", requests[0]["chat_id"])
	}
	ids, ok := requests[0]["message_ids"].([]int64)
	if !ok || len(ids) != 1 || ids[0] != 33 {
		t.Fatalf("message_ids = %#v, ждали [33]", requests[0]["message_ids"])
	}
	// revoke:false — удаление только у себя. Проверяется явно, потому что
	// смена флага на true удалила бы сообщение у всех собеседников, а
	// отличить такой запрос от правильного по остальным полям нельзя.
	if revoke, ok := requests[0]["revoke"].(bool); !ok || revoke {
		t.Fatalf("revoke = %#v, ждали false", requests[0]["revoke"])
	}

	m, _ = pressWallKey(t, m, result)
	assertCardTexts(t, m, "первое", "второе")
	// Место удалённой карточки занимает соседняя, а не уезжает курсор: человек
	// продолжает смотреть на поток с той же позиции.
	if m.cursor != 1 {
		t.Fatalf("курсор = %d после удаления последней карточки, ждали 1", m.cursor)
	}
	if got := m.cards[m.cursor].Text; got != "второе" {
		t.Fatalf("под курсором %q, ждали \"второе\"", got)
	}
	if strings.Contains(ansi.Strip(m.View().Content), "третье") {
		t.Fatalf("удалённое сообщение всё ещё на экране: %q", m.View().Content)
	}
}

// TestEnterInConfirmKeepsCursorOnNeighbour — удаление из середины потока
// оставляет курсор на соседе, а не на уехавшем сообщении и не за пределами
// стены.
func TestEnterInConfirmKeepsCursorOnNeighbour(t *testing.T) {
	client := newWallDeleteClient()
	m := confirmTestModel(t, client, 100, 24)
	m = m.moveCursor(-1)

	m = openWallConfirm(t, m)
	m, cmd := pressWallKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = pressWallKey(t, m, cmd().(deleteMessageMsg))

	assertCardTexts(t, m, "первое", "третье")
	if got := m.cards[m.cursor].Text; got != "третье" {
		t.Fatalf("под курсором %q, ждали \"третье\" (сосед занял удалённое место)", got)
	}
}

// TestEscCancelsConfirmWithoutDeleting — отмена необратимого действия не должна
// ни отправлять запрос в TDLib, ни менять стену. Мутационная проверка: с
// убранной веткой Esc тест падает (модалка осталась бы открытой).
func TestEscCancelsConfirmWithoutDeleting(t *testing.T) {
	client := newWallDeleteClient()
	// Экран сравнивается ДО открытия модалки: после Esc стена обязана выглядеть
	// ровно так же, как до нажатия Delete, — снятие вопроса не должно оставлять
	// на экране ни вмятины.
	before := confirmTestModel(t, client, 100, 24).View().Content
	m := openWallConfirm(t, confirmTestModel(t, client, 100, 24))

	m, cmd := pressWallKey(t, m, tea.KeyPressMsg{Code: tea.KeyEscape})

	if m.showConfirm {
		t.Fatal("Esc не закрыл модалку")
	}
	if m.confirmChatID != 0 || m.confirmMessageID != 0 || m.confirmPrompt != "" {
		t.Fatalf("Esc оставил за собой состояние модалки: чат=%d сообщение=%d вопрос=%q",
			m.confirmChatID, m.confirmMessageID, m.confirmPrompt)
	}
	if cmd != nil {
		t.Fatalf("Esc вернул команду %T, ждали nil", cmd())
	}
	if requests := client.deleteRequests(); len(requests) != 0 {
		t.Fatalf("Esc отправил %d запросов удаления, ждали ни одного", len(requests))
	}
	assertCardTexts(t, m, "первое", "второе", "третье")
	if m.cursor != 2 {
		t.Fatalf("курсор = %d после отмены, ждали 2 (карточка осталась на месте)", m.cursor)
	}
	if m.View().Content != before {
		t.Fatal("Esc изменил экран стены, кроме исчезновения самой модалки")
	}
}

// TestConfirmBlocksOtherInput — открытая «Да/Нет» перехватывает ввод: нажатия,
// задуманные для стены и поля ввода, не должны срабатывать, пока человек не
// ответил на вопрос.
func TestConfirmBlocksOtherInput(t *testing.T) {
	client := newWallDeleteClient()
	m := openWallConfirm(t, confirmTestModel(t, client, 100, 24))

	for _, msg := range []tea.KeyMsg{
		tea.KeyPressMsg{Code: tea.KeyUp},
		tea.KeyPressMsg{Code: tea.KeyDown},
		tea.KeyPressMsg{Code: tea.KeyPgUp},
		tea.KeyPressMsg{Code: tea.KeyPgDown},
		tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl},
		tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl},
		tea.KeyPressMsg{Code: 'x', Text: "x"},
		tea.KeyPressMsg{Code: tea.KeyDelete},
	} {
		updated, _ := pressWallKey(t, m, msg)
		if !updated.showConfirm {
			t.Fatalf("нажатие %v закрыло модалку подтверждения", msg)
		}
		if updated.cursor != m.cursor {
			t.Fatalf("нажатие %v сдвинуло курсор стены: %d, ждали %d", msg, updated.cursor, m.cursor)
		}
		if updated.replyTarget != nil {
			t.Fatalf("нажатие %v выбрало цель ответа под модалкой: %+v", msg, updated.replyTarget)
		}
		if body := inputBody(updated); body != "" {
			t.Fatalf("нажатие %v изменило поле ввода под модалкой: %q", msg, body)
		}
	}
	if requests := client.deleteRequests(); len(requests) != 0 {
		t.Fatalf("под модалкой ушло %d запросов удаления, ждали ни одного", len(requests))
	}
}

// TestDeleteKeepsCardOnTDLibError — неудачное удаление не выбрасывает карточку:
// у стены нет индикатора ошибок (осознанно, см. applyWallSendMessage), поэтому
// молчаливое сохранение карточки — единственный сигнал, что ничего не вышло.
// Мутационная проверка: с убранной проверкой err тест падает (карточка ушла бы
// со стены).
func TestDeleteKeepsCardOnTDLibError(t *testing.T) {
	client := newWallDeleteClient()
	client.deleteErr = errors.New("Message not found")
	// Ширина 60, а не 100: на широком терминале зона стены сужается до колонки
	// рядом с панелью переписки, и карточка под курсором по решению человека не
	// показывает текст последнего сообщения (задача 0155). Проверка «карточка
	// осталась на экране» ищет на экране именно ЕЁ текст, поэтому на широком
	// терминале искала бы то, чего там по замыслу нет.
	m := openWallConfirm(t, confirmTestModel(t, client, 60, 24))

	m, cmd := pressWallKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	result, ok := cmd().(deleteMessageMsg)
	if !ok {
		t.Fatal("Enter вернул не deleteMessageMsg")
	}
	if result.err == nil {
		t.Fatal("результат неудачного удаления пришёл без ошибки")
	}

	m, _ = pressWallKey(t, m, result)

	assertCardTexts(t, m, "первое", "второе", "третье")
	if m.cursor != 2 {
		t.Fatalf("курсор = %d после неудачного удаления, ждали 2", m.cursor)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "третье") {
		t.Fatalf("карточка исчезла с экрана после неудачного удаления: %q", m.View().Content)
	}
}

// TestDeleteWithoutClientKeepsCard — клиент nil не должен ронять Update: путь
// «модалка → Enter» остаётся рабочим и тихо ничего не удаляет (та же схема, что
// у отправки, см. sendWallMessageCmd).
func TestDeleteWithoutClientKeepsCard(t *testing.T) {
	m := openWallConfirm(t, confirmTestModel(t, nil, 100, 24))

	m, cmd := pressWallKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("Enter без клиента не вернул команду удаления")
	}
	result, ok := cmd().(deleteMessageMsg)
	if !ok {
		t.Fatal("Enter без клиента вернул не deleteMessageMsg")
	}
	if result.err == nil {
		t.Fatal("удаление без клиента прошло без ошибки")
	}

	m, _ = pressWallKey(t, m, result)
	assertCardTexts(t, m, "первое", "второе", "третье")
}

// TestDeleteOpensConfirmWithDraftInInput — Delete не смотрит на содержимое поля,
// ровно как Ctrl+R: человек печатает ответ, нажал Delete и хочет удалить
// сообщение, а не стереть букву в черновике. Нажатие перехватывается до того,
// как уходит в m.input.Update.
func TestDeleteOpensConfirmWithDraftInInput(t *testing.T) {
	m := confirmTestModel(t, newWallDeleteClient(), 100, 24)
	m = m.startReply()
	m.input.SetValue("мой ответ")
	// Каретка в начало строки — Delete по нажатию в поле стёр бы символ ПОСЛЕ
	// неё, то есть «м». Так проверяется именно перехват нажатия, а не совпадение
	// текста: с кареткой в конце Delete в поле не изменил бы ничего, и тест
	// прошёл бы даже с убранным перехватом.
	m.input.CursorStart()

	m = openWallConfirm(t, m)

	if body := inputBody(m); body != "мой ответ" {
		t.Fatalf("поле ввода = %q, ждали нетронутый черновик (Delete не должен стереть символ поля)", body)
	}
	// Подтверждение удаляет ту карточку, которую человек видел под курсором, и
	// не трогает напечатанный им текст. Сама цель ответа на удалённое сообщение
	// при этом снимается (см. TestDeleteDropsReplyTargetOfDeletedMessage), но
	// написанный в поле текст остаётся: человек набирал его сам, и стирать его
	// чужим действием нельзя.
	m, cmd := pressWallKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = pressWallKey(t, m, cmd().(deleteMessageMsg))
	assertCardTexts(t, m, "первое", "второе")
	if body := inputBody(m); body != "мой ответ" {
		t.Fatalf("поле после удаления = %q, ждали черновик на месте", body)
	}
}

// TestDeleteDropsReplyTargetOfDeletedMessage — отвечать на удалённое сообщение
// бессмысленно: TDLib такой запрос отвергнет, а стена не покажет ни ошибки, ни
// чего-либо ещё. Цель снимается тем же удалением, что и сама карточка.
func TestDeleteDropsReplyTargetOfDeletedMessage(t *testing.T) {
	client := newWallDeleteClient()
	m := confirmTestModel(t, client, 100, 24)
	m = m.startReply()
	if m.replyTarget == nil || m.replyTarget.MessageID != 33 {
		t.Fatalf("подготовка: цель ответа = %+v, ждали 33", m.replyTarget)
	}

	m = openWallConfirm(t, m)
	m, cmd := pressWallKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = pressWallKey(t, m, cmd().(deleteMessageMsg))

	if m.replyTarget != nil {
		t.Fatalf("цель ответа %+v пережила удаление её сообщения", m.replyTarget)
	}
	// Строка контекста ответа исчезла — поле должно занять её место, иначе под
	// стеной осталась бы дыра в одну строку.
	if got := m.replyRows(); got != 0 {
		t.Fatalf("replyRows = %d после снятия цели, ждали 0", got)
	}
	// Формула — та же, что в wallHeight, включая три новые строки каркаса: со
	// старой формулой стена после снятия ответа занимала бы на три строки
	// больше, чем есть на экране.
	if m.wallHeight() != m.height-wallReservedRows-wallTopBarRows-wallNoticeRows-m.inputRows {
		t.Fatalf("высота стены %d не пересчитана под снятую строку ответа", m.wallHeight())
	}
	for index, line := range strings.Split(m.renderScreen(), "\n") {
		if got := cellWidth(line); got != m.width {
			t.Fatalf("строка %d после снятия цели ответа — %d ячеек, ждали %d", index, got, m.width)
		}
	}
}

// TestDeleteKeepsNewerCardOfSameChat — карточка на стене одна на источник, и пока
// ждали ответ на удаление, тот же чат мог прислать своё новое сообщение: его
// карточка встала на место удаляемой. Удалённого сообщения на стене уже нет, и
// снимать с неё нечего — новое сообщение остаётся.
func TestDeleteKeepsNewerCardOfSameChat(t *testing.T) {
	client := newWallDeleteClient()
	m := openWallConfirm(t, confirmTestModel(t, client, 100, 24))

	// Живой апдейт по тому же чату, пока запрос удаления ещё в полёте.
	m = m.applyWallMessageUpdate(3, auth.Message{ID: 99, Text: "новое от Андрея", Date: 4})
	if len(m.cards) != 3 {
		t.Fatalf("после живого апдейта карточек %d, ждали 3: %v", len(m.cards), cardTexts(m))
	}

	m, cmd := pressWallKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = pressWallKey(t, m, cmd().(deleteMessageMsg))

	assertCardTexts(t, m, "первое", "второе", "новое от Андрея")
	if requests := client.deleteRequests(); len(requests) != 1 {
		t.Fatalf("запросов удаления: %d, ждали 1 (ушедшее сообщение всё равно удалено у Telegram)", len(requests))
	}
}

// TestDeleteLastCardLeavesEmptyWall — на стене не остаётся ни одной карточки:
// курсор и окно зажимаются под пустой поток, а кадр не разъезжается.
func TestDeleteLastCardLeavesEmptyWall(t *testing.T) {
	client := newWallDeleteClient()
	m := newConfirmTestModel(t, client, 100, 24, confirmTestCards()[:1])

	m = openWallConfirm(t, m)
	m, cmd := pressWallKey(t, m, tea.KeyPressMsg{Code: tea.KeyEnter})
	m, _ = pressWallKey(t, m, cmd().(deleteMessageMsg))

	if len(m.cards) != 0 {
		t.Fatalf("после удаления единственной карточки остались: %v", cardTexts(m))
	}
	if m.cursor != 0 || m.scrollTop != 0 {
		t.Fatalf("курсор/окно = %d/%d на пустой стене, ждали 0/0", m.cursor, m.scrollTop)
	}
	lines := strings.Split(m.renderScreen(), "\n")
	if len(lines) != m.height {
		t.Fatalf("экран из %d строк на пустой стене, ждали %d", len(lines), m.height)
	}
	for index, line := range lines {
		if got := cellWidth(line); got != m.width {
			t.Fatalf("строка %d — %d ячеек, ждали %d", index, got, m.width)
		}
	}
}

// TestConfirmIsDrawnOverWallWithoutBreakingGeometry — модалка накладывается
// поверх обычного экрана, а не заменяет его: карточки и нижняя строка остаются
// видны, и при этом ни высота, ни ширина экрана не меняются. Геометрия важна
// сама по себе: сдвиг на строку или колонку в bubbletea дёргает весь кадр.
func TestConfirmIsDrawnOverWallWithoutBreakingGeometry(t *testing.T) {
	const width, height = 100, 24
	m := confirmTestModel(t, newWallDeleteClient(), width, height)
	before := m.View().Content

	m = openWallConfirm(t, m)
	after := m.View().Content

	lines := strings.Split(after, "\n")
	if len(lines) != height {
		t.Fatalf("View() из %d строк с модалкой, ждали %d", len(lines), height)
	}
	for index, line := range lines {
		if got := cellWidth(line); got != width {
			t.Fatalf("строка %d — %d ячеек с модалкой, ждали %d: %q", index, got, width, line)
		}
	}
	if beforeLines := len(strings.Split(before, "\n")); beforeLines != len(lines) {
		t.Fatalf("модалка изменила число строк: %d без, %d с", beforeLines, len(lines))
	}
	if !strings.Contains(after, "третье") {
		t.Fatalf("модалка заменила стену вместо наложения на неё: %q", after)
	}
	if !strings.Contains(after, testAppName) {
		t.Fatalf("модалка закрыла нижнюю строку: %q", after)
	}
	if strings.Contains(before, confirmDeleteQuestion) {
		t.Fatal("тест сломан: обычный экран стены уже содержит вопрос модалки")
	}

	// Модалка стоит по центру экрана, а не в углу: иначе она закрыла бы верх
	// стены или уехала вправо за пределы кадра.
	block, _, _ := confirmModalBlock(width, height, m.confirmPrompt, m.confirmModalKeys())
	row, column := confirmWallPosition(lines, confirmDeleteQuestion)
	if row < 0 {
		t.Fatalf("вопрос модалки не на экране: %q", after)
	}
	blockTop := row - confirmModalPaddingV
	if blockTop <= 0 || blockTop+len(block) >= height {
		t.Fatalf("блок модалки занял строки %d..%d из %d, ждали с отступом от обеих границ",
			blockTop, blockTop+len(block)-1, height)
	}
	if gap := confirmWallAbs(blockTop - (height - blockTop - len(block))); gap > 1 {
		t.Fatalf("блок модалки не по центру по вертикали: %d строк сверху, %d снизу",
			blockTop, height-blockTop-len(block))
	}
	boxWidth := cellWidth(block[0])
	questionWidth := cellWidth(confirmDeleteQuestion)
	wantColumn := (width-boxWidth)/2 + confirmModalPaddingH + (boxWidth-2*confirmModalPaddingH-questionWidth)/2
	if confirmWallAbs(column-wantColumn) > 1 {
		t.Fatalf("вопрос модалки начинается с колонки %d, ждали около %d (по центру блока в %d колонок)",
			column, wantColumn, boxWidth)
	}
}

// confirmWallPosition — строка и ВИДИМАЯ колонка (в ячейках, а не в байтах), с
// которых начинается текст на отрисованном экране. -1, если текста нет.
func confirmWallPosition(lines []string, text string) (int, int) {
	for index, line := range lines {
		plain := ansi.Strip(line)
		if at := strings.Index(plain, text); at >= 0 {
			return index, cellWidth(plain[:at])
		}
	}
	return -1, -1
}

func confirmWallAbs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

// TestConfirmBlockStaysInsideNarrowTerminal — блок никогда не выходит за границы
// терминала по ШИРИНЕ: на узком экране он сжимается и центрируется, иначе
// наложение сломало бы геометрию кадра. Совсем узкий экран (1 ячейка) не вмещает
// даже поля отступов — тогда блок пуст и на экране просто ничего не появляется;
// ломать кадр из-за этого нельзя. По высоте блок обрезает сам экран
// (overlayConfirm не выходит за последнюю строку), поэтому на низком терминале
// вопрос может не поместиться целиком — но кадр от этого не поедет, и это тоже
// проверяется.
func TestConfirmBlockStaysInsideNarrowTerminal(t *testing.T) {
	prompt := confirmDeleteQuestion + "\nочень длинный текст сообщения"
	for _, size := range [][2]int{{40, 12}, {24, 8}, {12, 5}, {1, 1}, {0, 10}, {10, 0}} {
		width, height := size[0], size[1]
		block, _, _ := confirmModalBlock(width, height, prompt, defaultConfirmKeys())
		if width <= 0 || height <= 0 {
			if block != nil {
				t.Fatalf("confirmModalBlock(%d, %d) = %v, ждали nil", width, height, block)
			}
			continue
		}
		for index, line := range block {
			if got := cellWidth(line); got > width {
				t.Fatalf("строка %d блока — %d ячеек при ширине %d", index, got, width)
			}
		}

		// Наложение на экран такого же размера обязано оставить его геометрию
		// целой, сколько бы строк блок ни занял: ни одна строка не может стать
		// шире той, что была без модалки. Сравнение с базовым экраном, а не с
		// самой шириной терминала, — потому что на совсем узком экране сама
		// стена (разделители по cardMarginH в каждую сторону) уже шире терминала,
		// и это её собственное поведение, к модалке отношения не имеющее.
		m := newConfirmTestModel(t, newWallDeleteClient(), width, height, confirmTestCards())
		base := strings.Split(m.renderScreen(), "\n")
		m.showConfirm = true
		m.confirmPrompt = prompt
		overlaid := strings.Split(m.overlayConfirm(m.renderScreen()), "\n")
		if len(overlaid) != len(base) {
			t.Fatalf("размер %dx%d: наложение дало %d строк вместо %d", width, height, len(overlaid), len(base))
		}
		for index, line := range overlaid {
			if got, was := cellWidth(line), cellWidth(base[index]); got > was {
				t.Fatalf("размер %dx%d: строка %d после наложения — %d ячеек вместо %d", width, height, index, got, was)
			}
		}
	}
}

// TestConfirmBlockKeepsLongPromptWithinMaxWidth — даже очень длинный вопрос не
// растягивает блок во весь широкий терминал: модалка остаётся компактным
// окном, а не полосой во всю ширину.
func TestConfirmBlockKeepsLongPromptWithinMaxWidth(t *testing.T) {
	limit := confirmModalMaxWidth + 2*confirmModalPaddingH
	for _, width := range []int{80, 200} {
		block, _, _ := confirmModalBlock(width, 20, strings.Repeat("я", 100), defaultConfirmKeys())
		if got := cellWidth(block[0]); got > limit {
			t.Fatalf("блок вопроса на 100 рун при ширине %d — %d колонок, ждали не больше %d", width, got, limit)
		}
	}
}

// TestDeleteTruncatesLongPreviewInQuestion — длинный текст сообщения попадает в
// вопрос обрезанным: подтверждение должно назвать сообщение, но не сломать
// геометрию модалки и не съесть весь экран. Обрезка по РУНАМ, а не по ячейкам.
func TestDeleteTruncatesLongPreviewInQuestion(t *testing.T) {
	cards := confirmTestCards()
	cards[2].Text = strings.Repeat("длинный текст ", 20)
	m := newConfirmTestModel(t, newWallDeleteClient(), 200, 24, cards)

	m = openWallConfirm(t, m)

	if !strings.Contains(m.confirmPrompt, "…") {
		t.Fatalf("длинное превью не обрезано: %q", m.confirmPrompt)
	}
	block, _, _ := confirmModalBlock(200, 24, m.confirmPrompt, m.confirmModalKeys())
	if got := cellWidth(block[0]); got > confirmModalMaxWidth+2*confirmModalPaddingH {
		t.Fatalf("блок модалки — %d колонок, ждали не больше %d", got, confirmModalMaxWidth+2*confirmModalPaddingH)
	}
	for index, line := range strings.Split(m.View().Content, "\n") {
		if got := cellWidth(line); got != 200 {
			t.Fatalf("строка %d — %d ячеек, ждали 200", index, got)
		}
	}
}

// TestConfirmRendersAnyPrompt — блок не привязан к удалению: он рендерит тот
// вопрос, который ему дали, и не подставляет свой.
func TestConfirmRendersAnyPrompt(t *testing.T) {
	m := confirmTestModel(t, newWallDeleteClient(), 60, 12)
	m.showConfirm = true
	m.confirmPrompt = "Выйти из аккаунта?"

	block, _, _ := confirmModalBlock(m.width, m.height, m.confirmPrompt, m.confirmModalKeys())
	view := strings.Join(block, "\n")
	if !strings.Contains(ansi.Strip(view), "Выйти из аккаунта?") {
		t.Fatalf("блок не отрисовал свой вопрос: %q", ansi.Strip(view))
	}
	if strings.Contains(ansi.Strip(view), "Удалить сообщение") {
		t.Fatalf("блок зашил в себя вопрос удаления: %q", ansi.Strip(view))
	}
}

// TestTruncateRunesCountsRunesNotBytes — обрезка превью считает руны, а не
// байты, и схлопывает перенос строки: кириллица в сообщениях — норма, и
// обрезанное по байтам превью съедало бы ширину блока.
func TestTruncateRunesCountsRunesNotBytes(t *testing.T) {
	if got := truncateRunes("привет", 100); got != "привет" {
		t.Fatalf("короткий текст = %q, ждали %q", got, "привет")
	}
	if got := truncateRunes("привет", 3); got != "при…" {
		t.Fatalf("обрезанный текст = %q, ждали %q", got, "при…")
	}
	if got := truncateRunes("две\nстроки", 100); got != "две строки" {
		t.Fatalf("многострочное превью = %q, ждали %q", got, "две строки")
	}
	if got := truncateRunes("", 10); got != "" {
		t.Fatalf("пустое превью = %q, ждали пустую строку", got)
	}
}

// TestConfirmPromptWithoutText — у нетекстового сообщения превью может оказаться
// пустым (текст не пришёл). Вопрос без него всё равно осмыслен, и пустой строки
// в блоке быть не должно.
func TestConfirmPromptWithoutText(t *testing.T) {
	cards := confirmTestCards()
	cards[2].Text = ""
	m := newConfirmTestModel(t, newWallDeleteClient(), 100, 24, cards)

	m = openWallConfirm(t, m)

	if m.confirmPrompt != confirmDeleteQuestion {
		t.Fatalf("вопрос = %q, ждали только текст удаления", m.confirmPrompt)
	}
	block, _, _ := confirmModalBlock(100, 24, m.confirmPrompt, m.confirmModalKeys())
	for index, line := range block {
		if cellWidth(line) != cellWidth(block[0]) {
			t.Fatalf("строка %d блока — %d ячеек, ждали одинаковые с остальными %d", index, cellWidth(line), cellWidth(block[0]))
		}
	}
}
