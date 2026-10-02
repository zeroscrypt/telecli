package tgwall

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/auth"
	"telecli/internal/config"
)

// Отправка сообщений и ответ по Ctrl+R. Проверяются обе гонки, ради которых
// ответ на отправку сравнивает присланное с тем, что человек делает сейчас: и
// «стереть начатый заново текст», и «погасить только что выбранную цель
// ответа». Проверять их через настоящий Update, а не прямым вызовом обработчика:
// неотличимое от «всё прошло хорошо» поведение обычно живёт именно в
// недостающем перехвате клавиши.

// wallSendRequest — разобранный запрос sendMessage. Снимается с мока, а не с
// модели: проверяется, что ушло в Telegram, а не что модель о себе думает.
type wallSendRequest struct {
	chatID int64
	text   string
	// replyToMessage и hasReplyTo — разбираются раздельно: у обычной отправки
	// поля reply_to в запросе нет вовсе, и «replyToMessage == 0» такой запрос
	// от обычного не отличило бы.
	replyToMessage int64
	hasReplyTo     bool
}

// wallSendClient — мок TDLib, который умеет отправлять сообщения. Сверху над
// готовым моком загрузки (wallLoadClient) переопределён только Send: тому
// запросы getChats/getChatHistory отвечать как умеет, а sendMessage берёт на
// себя. Тот же приём, что у wallLiveTestClient с каналом апдейтов.
type wallSendClient struct {
	*wallLoadClient
	// sendMu защищает список отправок: команда отправки исполняется в отдельной
	// горутине bubbletea, а тест читает список после неё.
	sendMu sync.Mutex
	sent   []wallSendRequest
	// failSend — sendMessage отвечает ошибкой вместо сообщения.
	failSend bool
	// sentMessageID и sentMessageDate — id и дата в ответе на успешную отправку.
	// Дата заведомо позже любой из фикстур, поэтому отправленное встаёт в конец
	// потока, и проверка «карточка появилась» однозначна.
	sentMessageID   int64
	sentMessageDate int64
}

func newWallSendClient() *wallSendClient {
	return &wallSendClient{
		wallLoadClient:  newWallLoadClient(wallTestChats(), wallTestHistory()),
		sentMessageID:   500,
		sentMessageDate: 5000,
	}
}

func (c *wallSendClient) Send(ctx context.Context, request map[string]interface{}) (map[string]interface{}, error) {
	if request["@type"] != "sendMessage" {
		return c.wallLoadClient.Send(ctx, request)
	}
	parsed := parseWallSendRequest(request)

	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	c.sent = append(c.sent, parsed)
	if c.failSend {
		return nil, errors.New("Chat is not accessible")
	}
	return map[string]interface{}{
		"@type":       "message",
		"id":          float64(c.sentMessageID),
		"chat_id":     float64(parsed.chatID),
		"date":        float64(c.sentMessageDate),
		"is_outgoing": true,
		"sender_id":   map[string]interface{}{"@type": "messageSenderUser", "user_id": float64(1)},
		"content": map[string]interface{}{
			"@type": "messageText",
			"text":  map[string]interface{}{"@type": "formattedText", "text": parsed.text},
		},
	}, nil
}

func parseWallSendRequest(request map[string]interface{}) wallSendRequest {
	parsed := wallSendRequest{}
	parsed.chatID, _ = request["chat_id"].(int64)
	if content, ok := request["input_message_content"].(map[string]interface{}); ok {
		if text, ok := content["text"].(map[string]interface{}); ok {
			parsed.text, _ = text["text"].(string)
		}
	}
	if reply, ok := request["reply_to"].(map[string]interface{}); ok {
		parsed.hasReplyTo = true
		parsed.replyToMessage, _ = reply["message_id"].(int64)
	}
	return parsed
}

// sentRequests — что ушло в TDLib с момента создания мока.
func (c *wallSendClient) sentRequests() []wallSendRequest {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return append([]wallSendRequest(nil), c.sent...)
}

// sendWallMessages — по одному сообщению на каждый чат из фикстур тестов загрузки
// стены (wallTestChats/wallTestHistory) — то есть ровно то, что теперь даёт
// снимок стены: последнее сообщение чата. Раньше здесь были ВСЕ сообщения
// истории, и у канала их было два, но состояние с двумя карточками одного чата на
// стене с переходом на одну карточку на источник больше недостижимо через
// реальный код. Заголовки у чатов различимы, а поиск «нарисована ли карточка»
// (wallRowOf) идёт именно по заголовку.
func sendWallMessages() []wallMessage {
	chats := wallTestChats()
	history := wallTestHistory()
	messages := make([]wallMessage, 0, len(chats))
	for _, chat := range chats {
		chatMessages := history[chat.ID]
		if len(chatMessages) == 0 {
			continue
		}
		messages = append(messages, wallMessage{Chat: chat, Message: chatMessages[len(chatMessages)-1]})
	}
	// Порядок снимка стены — по дате сообщения (см. loadWallMessages), и фикстура
	// обязана быть такой же: неотсортированный поток проверял бы навигацию и
	// удержание курсора на состоянии, которого стена не показывает.
	sort.SliceStable(messages, func(left, right int) bool {
		return messages[left].Date < messages[right].Date
	})
	return messages
}

// sendWallModel — загруженная стена под размер терминала с готовым к вводу полем
// (то же состояние, что у живой стены после wallLoadedMsg).
func sendWallModel(t *testing.T, client auth.TDClientInterface, width, height int) Model {
	t.Helper()
	m := New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	next, _ := m.Update(wallLoadedMsg{cards: wallCards(sendWallMessages()), chats: wallTestChats()})
	m = next.(Model)
	next, _ = m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = next.(Model)
	if m.loading {
		t.Fatal("после wallLoadedMsg стена осталась в состоянии загрузки")
	}
	_ = m.input.Focus()
	return m
}

// emptyWallModel — стена без единой карточки: отвечать и отправлять не на что.
func emptyWallModel(t *testing.T, client auth.TDClientInterface, width, height int) Model {
	t.Helper()
	m := New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = next.(Model)
	_ = m.input.Focus()
	return m
}

// typeKeysInWallField — ввод текста настоящими нажатиями, а не SetValue:
// проверяется путь, по которому идёт живой человек, включая пробелы.
func typeKeysInWallField(t *testing.T, m Model, text string) Model {
	t.Helper()
	for _, symbol := range text {
		next, _ := m.Update(tea.KeyPressMsg{Code: symbol, Text: string(symbol)})
		m = next.(Model)
	}
	return m
}

// typeInWallField — ввод в пустое поле: значение после ввода обязано совпасть.
func typeInWallField(t *testing.T, m Model, text string) Model {
	t.Helper()
	m = typeKeysInWallField(t, m, text)
	if got := m.input.Value(); got != text {
		t.Fatalf("значение поля = %q, ждали %q", got, text)
	}
	return m
}

func pressKey(t *testing.T, m Model, key tea.KeyPressMsg) Model {
	t.Helper()
	next, _ := m.Update(key)
	return next.(Model)
}

var (
	ctrlRKey  = tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}
	escapeKey = tea.KeyPressMsg{Code: tea.KeyEscape}
	enterKey  = tea.KeyPressMsg{Code: tea.KeyEnter}
)

// runSendCmd — исполнить команду отправки и достать её ответ. Ответ сети приходит
// отдельным сообщением в Update ровно так же, как в живой программе, и тесты
// гонок обязаны имитировать именно эту задержку, а не звать обработчик напрямую.
func runSendCmd(t *testing.T, cmd tea.Cmd) wallSendMessageMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("Enter не вернул команду отправки")
	}
	raw := cmd()
	msg, ok := raw.(wallSendMessageMsg)
	if !ok {
		t.Fatalf("команда отправки вернула %T, ждали wallSendMessageMsg", raw)
	}
	return msg
}

func applySend(t *testing.T, m Model, msg wallSendMessageMsg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	return next.(Model)
}

func assertSameTarget(t *testing.T, got *replyTarget, want card) {
	t.Helper()
	if got == nil {
		t.Fatalf("цель ответа = nil, ждали адрес (%d, %d)", want.ChatID, want.MessageID)
	}
	if got.ChatID != want.ChatID || got.MessageID != want.MessageID {
		t.Fatalf("цель ответа = (%d, %d), ждали (%d, %d)", got.ChatID, got.MessageID, want.ChatID, want.MessageID)
	}
}

// Ctrl+R выбирает карточку под курсором В МОМЕНТ нажатия. Дальше человек
// листает стену стрелками, а цель ответа обязана остаться прежней: ответ поехал
// бы за курсором на соседнее сообщение, и «ответил не туда» перестал бы быть
// страшным, а случился бы от одного ↑. Проверяется и само состояние, и то, куда
// реально уходит ответ — второе важнее, ведь «цель едет за курсором» может
// выглядеть в модели совершенно правильно и сломаться только на отправке.
func TestCtrlRTakesCardUnderCursorAndKeepsItWhileCursorMoves(t *testing.T) {
	client := newWallSendClient()
	m := sendWallModel(t, client, 100, 30)
	target := m.cards[m.cursor]

	m = pressKey(t, m, ctrlRKey)
	assertSameTarget(t, m.replyTarget, target)
	if !strings.Contains(m.replyTarget.Label, target.title()) {
		t.Fatalf("подпись цели = %q, ждали заголовок карточки %q", m.replyTarget.Label, target.title())
	}

	for range 2 {
		m = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	}
	// Подготовка: курсор уехал, значит «цель едет за курсором» вообще возможна.
	under := m.cards[m.cursor]
	if under.ChatID == target.ChatID && under.MessageID == target.MessageID {
		t.Fatalf("курсор остался на той же карточке (%d, %d) — тест не проверяет ничего", under.ChatID, under.MessageID)
	}
	assertSameTarget(t, m.replyTarget, target)
	if want := target.title() + " — \"" + truncateVisible(target.Text, wallReplyPreviewWidth) + "\""; m.replyTarget.Label != want {
		t.Fatalf("подпись цели = %q, ждали снимок исходной карточки %q", m.replyTarget.Label, want)
	}

	// И главное: ответ уходит туда, куда человек указал в момент Ctrl+R, а не туда,
	// где оказался курсор к моменту Enter.
	m = typeInWallField(t, m, "ответ не туда")
	next, cmd := m.Update(enterKey)
	m = next.(Model)
	_ = applySend(t, m, runSendCmd(t, cmd))

	sent := client.sentRequests()
	if len(sent) != 1 {
		t.Fatalf("ушло %d отправок, ждали 1: %v", len(sent), sent)
	}
	if sent[0].chatID != target.ChatID || !sent[0].hasReplyTo || sent[0].replyToMessage != target.MessageID {
		t.Fatalf("ответ ушёл на (%d, %d), ждали на (%d, %d) — цель ответа поехала за курсором",
			sent[0].chatID, sent[0].replyToMessage, target.ChatID, target.MessageID)
	}
}

// На пустой стене отвечать не на что: Ctrl+R не обязан выбирать цель, но и
// ронять программу на индексе за пределами потока не должен.
func TestCtrlROnEmptyWallSelectsNothing(t *testing.T) {
	m := emptyWallModel(t, newWallSendClient(), 100, 30)
	if len(m.cards) != 0 {
		t.Fatalf("подготовка: на стене %d карточек, ждали 0", len(m.cards))
	}

	m = pressKey(t, m, ctrlRKey)
	if m.replyTarget != nil {
		t.Fatalf("Ctrl+R на пустой стене выбрал цель %+v", m.replyTarget)
	}
}

// Esc выходит из режима ответа. Вне режима ответа Esc не значит ничего и уж тем
// более не должен трогать набранный текст.
func TestEscapeLeavesReplyModeAndKeepsDraftWithoutIt(t *testing.T) {
	m := sendWallModel(t, newWallSendClient(), 100, 30)
	m = typeInWallField(t, m, "черновик")
	m = pressKey(t, m, ctrlRKey)

	m = pressKey(t, m, escapeKey)
	if m.replyTarget != nil {
		t.Fatalf("Esc не сбросил цель ответа: %+v", m.replyTarget)
	}
	if got := m.input.Value(); got != "черновик" {
		t.Fatalf("Esc стёр набранный текст: поле = %q", got)
	}

	// Esc без цели ответа — тоже пустое дело, и поле остаётся нетронутым.
	before, cursor := m.cards[m.cursor], m.cursor
	m = pressKey(t, m, escapeKey)
	if m.replyTarget != nil {
		t.Fatalf("Esc без режима ответа зачем-то выбрал цель %+v", m.replyTarget)
	}
	if got := m.input.Value(); got != "черновик" {
		t.Fatalf("Esc без режима ответа стёр текст: поле = %q", got)
	}
	if m.cursor != cursor || m.cards[m.cursor].MessageID != before.MessageID {
		t.Fatal("Esc без режима ответа сдвинул курсор")
	}
}

// Enter с пустым полем ничего НЕ ОТПРАВЛЯЕТ. В простом режиме он при этом
// выбирает чат и показывает подсказку над полем (решение человека, 2026-10-01),
// так что команда возвращается — и проверять надо именно отправку, а не
// наличие команды: она заказывает загрузку истории и таймер гашения подсказки.
func TestEmptyEnterSendsNothingAndKeepsReplyTarget(t *testing.T) {
	client := newWallSendClient()
	m := sendWallModel(t, client, 100, 30)
	target := m.cards[m.cursor]

	next, _ := m.Update(enterKey)
	plain := next.(Model)
	if len(client.sentRequests()) != 0 {
		t.Fatalf("пустой Enter что-то отправил: %v", client.sentRequests())
	}
	if plain.notice == nil || plain.notice.text != wallNoticeTypeToSend {
		t.Fatalf("пустой Enter не показал подсказку %q: %+v", wallNoticeTypeToSend, plain.notice)
	}
	if plain.input.Value() != "" {
		t.Fatalf("пустой Enter что-то вписал в поле: %q", plain.input.Value())
	}
	if plain.replyTarget != nil {
		t.Fatal("пустой Enter вне режима ответа выбрал цель ответа")
	}

	m = pressKey(t, m, ctrlRKey)
	// Пустое поле в режиме ответа: из ответа можно выйти только Esc, поэтому Enter
	// не должен ни отправлять, ни сбрасывать цель. Команда при этом может
	// вернуться — она заказывает подсказку над полем, но не отправку.
	next, _ = m.Update(enterKey)
	replying := next.(Model)
	if len(client.sentRequests()) != 0 {
		t.Fatalf("пустой Enter в режиме ответа что-то отправил: %v", client.sentRequests())
	}
	assertSameTarget(t, replying.replyTarget, target)
}

// Enter с текстом вне режима ответа уходит в чат карточки под курсором, и по
// успеху поле очищается, а на стене появляется новая карточка.
func TestEnterSendsMessageToChatUnderCursor(t *testing.T) {
	client := newWallSendClient()
	m := sendWallModel(t, client, 100, 30)
	target := m.cards[m.cursor]
	before := len(m.cards)
	m = typeInWallField(t, m, "привет")

	next, cmd := m.Update(enterKey)
	m = next.(Model)
	if got := m.input.Value(); got != "" {
		t.Fatalf("Enter не очистил поле сразу (оптимистично): %q", got)
	}
	// Команда исполняется только здесь — как её исполнил бы bubbletea, — и уже
	// после этого у мока можно смотреть, что ушло в Telegram.
	response := runSendCmd(t, cmd)

	sent := client.sentRequests()
	if len(sent) != 1 {
		t.Fatalf("ушло %d отправок, ждали 1: %v", len(sent), sent)
	}
	if sent[0].chatID != target.ChatID {
		t.Fatalf("отправлено в чат %d, ждали %d (чат карточки под курсором)", sent[0].chatID, target.ChatID)
	}
	if sent[0].hasReplyTo || sent[0].replyToMessage != 0 {
		t.Fatalf("обычная отправка набрала reply_to = %d, ждали его отсутствия", sent[0].replyToMessage)
	}
	if sent[0].text != "привет" {
		t.Fatalf("отправлен текст %q, ждали %q", sent[0].text, "привет")
	}

	m = applySend(t, m, response)
	if got := m.input.Value(); got != "" {
		t.Fatalf("после успешной отправки поле = %q, ждали пустое", got)
	}
	if m.replyTarget != nil {
		t.Fatalf("обычная отправка задела цель ответа: %+v", m.replyTarget)
	}
	// На стене — с меткой «Вы:»: собственное сообщение получает её так же, как
	// и любое другое своё, пришедшее через updateNewMessage (see wallCard).
	// cardsBefore = before: своя отправка в чат, у которого уже была карточка,
	// её ЗАМЕНЯЕТ (одна карточка на источник, задача 0152), число не растёт.
	assertSentCardAppeared(t, m, target.ChatID, int64(before), 500, "Вы: привет")
}

// Тот же Enter в режиме ответа — это ответ: уходит reply_to с адресом выбранной
// карточки, а по успеху цель ответа сбрасывается, потому что отвечать на то же
// сообщение второй раз человек не заходил.
func TestEnterInReplyModeSendsReplyAndClearsTarget(t *testing.T) {
	client := newWallSendClient()
	m := sendWallModel(t, client, 100, 30)
	target := m.cards[m.cursor]
	before := len(m.cards)
	m = pressKey(t, m, ctrlRKey)
	m = typeInWallField(t, m, "согласен")

	next, cmd := m.Update(enterKey)
	m = next.(Model)
	response := runSendCmd(t, cmd)

	sent := client.sentRequests()
	if len(sent) != 1 {
		t.Fatalf("ушло %d отправок, ждали 1: %v", len(sent), sent)
	}
	if sent[0].chatID != target.ChatID || !sent[0].hasReplyTo || sent[0].replyToMessage != target.MessageID {
		t.Fatalf("ответ ушёл как (%d, reply %d/%v), ждали (%d, reply %d/true)",
			sent[0].chatID, sent[0].replyToMessage, sent[0].hasReplyTo, target.ChatID, target.MessageID)
	}
	if sent[0].text != "согласен" {
		t.Fatalf("отправлен текст %q, ждали %q", sent[0].text, "согласен")
	}

	m = applySend(t, m, response)
	if got := m.input.Value(); got != "" {
		t.Fatalf("после успешного ответа поле = %q, ждали пустое", got)
	}
	if m.replyTarget != nil {
		t.Fatalf("цель ответа не сброшена: %+v", m.replyTarget)
	}
	assertSentCardAppeared(t, m, target.ChatID, int64(before), 500, "Вы: согласен")
}

// Сбой отправки: текст остаётся в поле ровно тем же, цель ответа остаётся
// активной, стена не меняется. Отдельного сообщения об ошибке у стены нет —
// решение оркестратора, лишний ради одного этого случая индикатор не заводится.
func TestFailedSendKeepsTextAndReplyTarget(t *testing.T) {
	client := newWallSendClient()
	client.failSend = true
	m := sendWallModel(t, client, 100, 30)
	target := m.cards[m.cursor]
	before := len(m.cards)
	m = pressKey(t, m, ctrlRKey)
	m = typeInWallField(t, m, "не уйдёт")

	next, cmd := m.Update(enterKey)
	m = next.(Model)
	if len(client.sentRequests()) != 0 {
		t.Fatalf("команда отправки не исполнена, а у мока уже есть отправка: %v", client.sentRequests())
	}
	response := runSendCmd(t, cmd)
	if len(client.sentRequests()) != 1 {
		t.Fatalf("после сбоя ушло %d отправок, ждали 1", len(client.sentRequests()))
	}
	if response.err == nil {
		t.Fatal("мок настроен на сбой, а ответ пришёл без ошибки")
	}
	m = applySend(t, m, response)

	if got := m.input.Value(); got != "не уйдёт" {
		t.Fatalf("после сбоя поле = %q, ждали исходный текст", got)
	}
	assertSameTarget(t, m.replyTarget, target)
	if len(m.cards) != before {
		t.Fatalf("после сбоя на стене %d карточек, ждали %d", len(m.cards), before)
	}
}

// Сбой отправки, но человек уже не ждал: пока сеть отвечала (с ошибкой), он
// начал печатать заново. Восстанавливать провалившийся текст поверх этого
// нельзя — поле уже не пустое, и затирать НОВЫЙ черновik старым, уже
// неактуальным, ровно так же вредно, как стереть его совсем.
func TestFailedSendDoesNotClobberNewDraftTypedAfterwards(t *testing.T) {
	client := newWallSendClient()
	client.failSend = true
	m := sendWallModel(t, client, 100, 30)
	m = typeInWallField(t, m, "не дойдёт")

	next, cmd := m.Update(enterKey)
	m = next.(Model)
	m = typeKeysInWallField(t, m, "новое")
	response := runSendCmd(t, cmd)
	if response.err == nil {
		t.Fatal("мок настроен на сбой, а ответ пришёл без ошибки")
	}

	m = applySend(t, m, response)
	if got := m.input.Value(); got != "новое" {
		t.Fatalf("сбой отправки затёр новый черновик: поле = %q, ждали %q", got, "новое")
	}
}

// Первая гонка: пока сеть думала, человек уже печатал дальше. Поле очищается
// СРАЗУ по Enter (оптимистично, см. submitInput) — именно поэтому текст,
// набранный после, не склеивается с отправленным, а успешный ответ сети не
// имеет права его тронуть: иначе он стёр бы то, что человек печатает СЕЙЧАС.
func TestSuccessfulSendKeepsDraftTypedAfterwards(t *testing.T) {
	m := sendWallModel(t, newWallSendClient(), 100, 30)
	m = typeInWallField(t, m, "первое")

	next, cmd := m.Update(enterKey)
	m = next.(Model)
	if got := m.input.Value(); got != "" {
		t.Fatalf("поле не очистилось сразу по Enter: %q — подготовка проверки неверна", got)
	}
	// Человек печатает новое, пока ответа на первое ещё нет.
	m = typeKeysInWallField(t, m, "второе")
	if got := m.input.Value(); got != "второе" {
		t.Fatalf("новый текст склеился со старым: %q, ждали %q", got, "второе")
	}

	m = applySend(t, m, runSendCmd(t, cmd))
	if got := m.input.Value(); got != "второе" {
		t.Fatalf("ответ сети стёр новый черновик: поле = %q, ждали %q", got, "второе")
	}
}

// Вторая гонка: пока сеть думала, человек уже нажал Ctrl+R на другую карточку.
// Успешный ответ на ПЕРВЫЙ ответ не имеет права погасить новую, ещё не
// отправленную цель — иначе следующий Enter ушёл бы в чат, который человек уже
// не выбирал.
func TestSuccessfulReplyKeepsReplyTargetChosenAfterwards(t *testing.T) {
	m := sendWallModel(t, newWallSendClient(), 100, 30)
	m = pressKey(t, m, ctrlRKey)
	m = typeInWallField(t, m, "ответ первому")
	next, cmd := m.Update(enterKey)
	m = next.(Model)

	// Пока идёт ответ, человек передумывает и выбирает другую карточку.
	m = pressKey(t, m, tea.KeyPressMsg{Code: tea.KeyUp})
	second := m.cards[m.cursor]
	m = pressKey(t, m, ctrlRKey)
	assertSameTarget(t, m.replyTarget, second)

	m = applySend(t, m, runSendCmd(t, cmd))
	assertSameTarget(t, m.replyTarget, second)
}

// Отправлять некуда: на пустой стене Enter с набранным текстом обязан быть тихим
// no-op, а не падением за границу потока. Отдельно — модель без клиента TDLib
// (такая собирается и в тестах, и в первый момент жизни): команда обязана вернуть
// ошибку, обработать её и оставить текст на месте.
func TestSubmitOnEmptyWallAndWithoutClientFailsQuietly(t *testing.T) {
	m := emptyWallModel(t, newWallSendClient(), 100, 30)
	m = typeInWallField(t, m, "некуда")
	m = pressKey(t, m, ctrlRKey)
	if m.replyTarget != nil {
		t.Fatalf("Ctrl+R на пустой стене выбрал цель %+v", m.replyTarget)
	}

	next, cmd := m.Update(enterKey)
	m = next.(Model)
	if cmd != nil {
		t.Fatalf("Enter на пустой стене вернул команду %T", cmd())
	}
	if got := m.input.Value(); got != "некуда" {
		t.Fatalf("Enter на пустой стене стёр текст: поле = %q", got)
	}

	// Клиента нет — отправка обязана сорваться ошибкой, а не паникой, и текст
	// обязан остаться в поле.
	lonely := typeInWallField(t, sendWallModel(t, nil, 100, 30), "некуда")
	next, cmd = lonely.Update(enterKey)
	lonely = next.(Model)
	response := runSendCmd(t, cmd)
	if response.err == nil {
		t.Fatal("отправка без клиента прошла без ошибки")
	}
	lonely = applySend(t, lonely, response)
	if got := lonely.input.Value(); got != "некуда" {
		t.Fatalf("после сбоя без клиента поле = %q, ждали исходный текст", got)
	}
	if len(lonely.cards) != len(sendWallMessages()) {
		t.Fatalf("после сбоя стена изменилась: %d карточек", len(lonely.cards))
	}
}

// Карточка без текста (такой текст даёт, например, нетекстовое сообщение) не даёт
// пустой подписи вида `"" — ""`: остаётся один заголовок.
func TestReplyLabelWithoutTextIsJustTitle(t *testing.T) {
	if got := replyLabel(card{Type: cardPersonal, Name: "Андрей"}); got != "Андрей" {
		t.Fatalf("подпись карточки без текста = %q, ждали %q", got, "Андрей")
	}
}

// Строка ответа занимает ровно одну строку экрана, и её место забирают ОБЕ формулы
// высоты: и та, что считает окно прокрутки, и предел роста поля. Расхождение хоть
// на строку уводило бы поле на строку контекста либо оставляло под ним дыру.
func TestReplyContextRowIsReservedInBothHeightFormulas(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {100, 30}, {80, 24}, {62, 20}} {
		plain := sendWallModel(t, newWallSendClient(), size[0], size[1])
		replying := plain.startReply()
		if replying.replyTarget == nil {
			t.Fatalf("терминал %dx%d: подготовка — Ctrl+R не выбрал цель", size[0], size[1])
		}
		// Короткий ввод: высоту поля упирает не потолок, и стена отдаёт ровно
		// одну строку под контекст ответа.
		if got := plain.wallHeight() - replying.wallHeight(); got != 1 {
			t.Fatalf("терминал %dx%d: стена отдала под ответ %d строк, ждали 1 (было %d, стало %d)",
				size[0], size[1], got, plain.wallHeight(), replying.wallHeight())
		}
		// Длинный ввод: теперь упирает потолок роста поля, и он обязан уступить
		// ту же одну строку. Значение заведомо длиннее потолка даже на самом
		// высоком терминале набора (120x40, потолок 35 строк).
		long := strings.Repeat("строка\n", 60)
		plain.input.SetValue(long)
		plain.applyLayout()
		replying.input.SetValue(long)
		replying.applyLayout()
		if got := plain.inputRows - replying.inputRows; got != 1 {
			t.Fatalf("терминал %dx%d: поле в режиме ответа выросло на %d строк, ждали на 1 меньше (было %d, стало %d)",
				size[0], size[1], got, plain.inputRows, replying.inputRows)
		}
		// Экран при этом остаётся ровно из m.height строк: обе формулы согласованы.
		if got := len(splitLines(replying.renderScreen())); got != size[1] {
			t.Fatalf("терминал %dx%d: с ответом на экране %d строк, ждали %d", size[0], size[1], got, size[1])
		}
	}
}

// Контекстная строка стоит между подсказками и полем, узнаётся по знаку ответа и
// называет цель; после Esc её нет.
func TestReplyContextLineSitsBetweenHintAndField(t *testing.T) {
	m := sendWallModel(t, newWallSendClient(), 100, 30)
	m = typeInWallField(t, m, "проверка")
	m = pressKey(t, m, ctrlRKey)
	target := m.cards[m.cursor]

	hint, reply, field := replyBlockRows(t, m)
	if hint.index < 0 || field.index < 0 || reply.index < 0 {
		t.Fatalf("в блоке ввода не все строки на месте: подсказка %d, ответ %d, поле %d",
			hint.index, reply.index, field.index)
	}
	if !(hint.index < reply.index && reply.index < field.index) {
		t.Fatalf("порядок строк блока ввода нарушен: подсказка %d, ответ %d, поле %d",
			hint.index, reply.index, field.index)
	}
	// Нарисованная строка называет цель: заголовок карточки обрезается по ширине
	// поля, поэтому сверяем ровно то, что на экране.
	drawn := truncateVisible(wallReplyMarker+" "+m.replyTarget.Label, 100-3)
	if !strings.Contains(ansi.Strip(reply.line), strings.TrimSpace(drawn)) {
		t.Fatalf("строка ответа на экране = %q, ждали начало %q", ansi.Strip(reply.line), strings.TrimSpace(drawn))
	}
	if !strings.Contains(ansi.Strip(m.replyTarget.Label), target.title()) {
		t.Fatalf("подпись цели %q не называет карточку %q", m.replyTarget.Label, target.title())
	}

	m = pressKey(t, m, escapeKey)
	if _, reply, _ := replyBlockRows(t, m); reply.index != -1 {
		t.Fatalf("после Esc строка ответа осталась на экране (строка %d)", reply.index)
	}
}

// assertSentCardAppeared — отправленное сообщение встало на стену тем же путём,
// что и живые апдейты (задачи 0150/0152): у чата под курсором карточка на стене
// уже была, поэтому своя отправка не добавляет вторую, а ЗАМЕНЯет её. Значит на
// стене столько же карточек, а у чата под курсором — новое сообщение.
//
// Считать карточки «до +1» здесь нельзя: с переходом стены на одну карточку на
// источник отправка в непустой чат уже не увеличивает поток, и проверка на
// прирост проверяла бы модель, которой на стене больше нет.
func assertSentCardAppeared(t *testing.T, m Model, chatID, cardsBefore, messageID int64, text string) {
	t.Helper()
	if len(m.cards) != int(cardsBefore) {
		t.Fatalf("после отправки карточек %d, ждали %d (своя отправка заменяет карточку чата, а не добавляет)", len(m.cards), cardsBefore)
	}
	if count := wallCountOfChat(m.cards, chatID); count != 1 {
		t.Fatalf("карточек чата %d на стене %d, ждали ровно 1", chatID, count)
	}
	found := -1
	for index, item := range m.cards {
		if item.MessageID == messageID {
			found = index
		}
	}
	if found < 0 {
		t.Fatalf("отправленного сообщения %d нет на стене", messageID)
	}
	if m.cards[found].Text != text {
		t.Fatalf("текст отправленной карточки = %q, ждали %q", m.cards[found].Text, text)
	}
}

// wallBlockRow — строка блока ввода на экране: индекс и сама строка.
type wallBlockRow struct {
	index int
	line  string
}

// replyBlockRows — где на экране подсказки, строка контекста ответа и само поле.
// Отсутствие строки ответа — минус один, а не «где-то там»: иначе проверка «пропала
// после Esc» прошла бы и на экране, где её и не было.
func replyBlockRows(t *testing.T, m Model) (hint, reply, field wallBlockRow) {
	t.Helper()
	hint, reply, field = wallBlockRow{index: -1}, wallBlockRow{index: -1}, wallBlockRow{index: -1}
	value := m.input.Value()
	for index, line := range splitLines(ansi.Strip(m.renderScreen())) {
		switch {
		case strings.Contains(line, m.hintLine()):
			hint = wallBlockRow{index: index, line: line}
		case strings.Contains(line, wallReplyMarker):
			reply = wallBlockRow{index: index, line: line}
		case value != "" && index > 0 && strings.Contains(line, value):
			field = wallBlockRow{index: index, line: line}
		}
	}
	return hint, reply, field
}
