package tgwall

import (
	"context"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/auth"
	"telecli/internal/config"
)

// Живые удаления на стене (задача 0165). Проверяется ровно то, что человек
// сообщил вживую: удалили последнее сообщение источника — и стена обязана
// показать его предыдущее, а не исчезнуть вместе с удалённым.
//
// Проверки идут через настоящий Update и настоящие команды: неотличимое от «всё
// прошло хорошо» поведение обычно живёт в недостающем перехвате или в том, что
// команда не доехала.

const (
	// deleteTestGoneID — последнее сообщение источника под удалением.
	deleteTestGoneID = 22
	// deleteTestLeftID — сообщение перед ним: именно оно обязано встать на
	// карточку вместо удалённого.
	deleteTestLeftID = 21
	// deleteTestGoneDate и deleteTestLeftDate расходятся так же, как id: новое
	// последнее сообщение СТАРШЕ удалённого, и карточка после замены обязана
	// уехать по потоку вверх, а не остаться на прежнем месте (проверка порядка по
	// дате).
	deleteTestGoneDate = 1310
	deleteTestLeftDate = 1305
)

// deleteTestChat — источник под удалением. Тот же чат, что у общей фикстуры стены
// (wallTestChats()[1]): карточка стены и открытая переписка должны быть про один
// чат, иначе проверялось бы не то расхождение, которое чинится.
var deleteTestChat = wallTestChats()[1]

// deleteTestHistory — история снимка, где у источника под удалением ДВА
// сообщения, а у двух других по одному. Третий источник нужен не для красоты:
// без карточек вокруг замены не видно, куда встала новая (см.
// TestWallDeleteUpdateKeepsDateOrderOfStream).
//
// chat 1 в общей фикстуре истории (wallTestHistory) с историей из двух сообщений —
// здесь ровно одно: лишнее сообщение в нём сделало бы карточку снимка не той, что
// удаляется.
func deleteTestHistory() map[int64][]auth.Message {
	return map[int64][]auth.Message{
		1: {{ID: 11, Text: "канал, 13:04", Date: 1304}},
		deleteTestChat.ID: {
			{ID: deleteTestLeftID, Text: "прошлый разговор", Date: deleteTestLeftDate},
			{ID: deleteTestGoneID, Text: "удалённое сообщение", Date: deleteTestGoneDate},
		},
		3: {{ID: 31, Text: "личное, 13:07", Date: 1307}},
	}
}

// deleteTestCards — снимок стены перед удалением: каждая карточка показывает
// последнее сообщение своего источника, а карточка источника под удалением
// стоит последней (её дата самая свежая) и после замены обязана уехать выше
// неё.
//
// Порядок задан здесь, а не полагается на сортировку: wallCards сохраняет
// порядок переданных сообщений, а в модель карточки идут напрямую (тем же путём,
// что у общих фикстур стены).
func deleteTestCards() []card {
	return wallCards([]wallMessage{
		{Chat: wallTestChats()[0], Message: auth.Message{ID: 11, Text: "канал, 13:04", Date: 1304}},
		{Chat: wallTestChats()[2], Message: auth.Message{ID: 31, Text: "личное, 13:07", Date: 1307}},
		{Chat: deleteTestChat, Message: auth.Message{ID: deleteTestGoneID, Text: "удалённое сообщение", Date: deleteTestGoneDate}},
	})
}

// wallDeleteTestClient — мок живых удалений: канал updateDeleteMessages поверх
// мока загрузки стены (встроенный wallLoadClient отдаёт в DeleteMessagesUpdates
// nil, и ждать апдейт было бы негде).
//
// Отдельный мок, а не wallLiveTestClient, ради подменяемой истории: удалённое
// сообщение обязано исчезнуть из ответа getChatHistory, иначе перезапрос вернул бы
// то же самое сообщение, что и удалили, и проверка «карточка показала новое
// последнее» проверяла бы мок, а не код.
type wallDeleteTestClient struct {
	*wallLoadClient
	deleteUpdates chan map[string]interface{}
}

func newWallDeleteTestClient(history map[int64][]auth.Message) *wallDeleteTestClient {
	return &wallDeleteTestClient{
		wallLoadClient: newWallLoadClient(wallTestChats(), history),
		deleteUpdates:  make(chan map[string]interface{}, 4),
	}
}

func (c *wallDeleteTestClient) DeleteMessagesUpdates() <-chan map[string]interface{} {
	return c.deleteUpdates
}

// setHistory — подменить историю чата так, как её отдавал бы TDLib после
// удаления: сообщения, которых больше нет, из ответа исчезают.
func (c *wallDeleteTestClient) setHistory(chatID int64, messages []auth.Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.history[chatID] = messages
}

// breakHistory — getChatHistory этого чата падает: этим проверяется, что
// неудачный перезапрос оставляет стену как была.
func (c *wallDeleteTestClient) breakHistory(chatID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.brokenChats[chatID] = true
}

// historyAsked — сколько раз бралась история чата и с каким пределом: по
// пределу видно, что перезапрос точечный и в одно сообщение, то есть ровно как
// берёт снимок стены.
func (c *wallDeleteTestClient) historyAsked(chatID int64) (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.historyCalls[chatID], c.historyLimits[chatID]
}

// wallDeleteEvent — updateDeleteMessages в той форме, которую отдаёт TDLib.
// Формат скопирован из тестов ленты tgcli, а разбор и не проверяется здесь: он
// покрыт тестами auth.ParseDeleteMessagesUpdate.
func wallDeleteEvent(chatID int64, messageIDs ...int64) map[string]interface{} {
	ids := make([]interface{}, 0, len(messageIDs))
	for _, id := range messageIDs {
		ids = append(ids, float64(id))
	}
	return map[string]interface{}{
		"@type":       "updateDeleteMessages",
		"chat_id":     float64(chatID),
		"message_ids": ids,
	}
}

// runWallDeleteUpdate — прогнать апдейт удаления через настоящий Update и
// выполнить ВСЕ вернувшиеся команды до конца.
//
// Их обычно две: переподписка на канал удалений и точечный перезапрос последнего
// сообщения источника. Переподписке заранее подкладывается апдейт — без этого она
// зависла бы на канале намертво и тест не завершился бы. Её собственный апдейт
// тесту не нужен: он проверяется отдельно (см. TestWaitForWallDeleteUpdate*).
//
// Третий результат — был ли в пакете ответ перезапроса. Апдейт, удаливший не то
// сообщение, что показано на карточке, перезапроса не вызывает вовсе, и тест
// обязан это видеть, а не молча ждать несуществующего ответа.
func runWallDeleteUpdate(t *testing.T, m Model, client *wallDeleteTestClient, update wallDeleteUpdateMsg) (Model, wallCardLatestMsg, bool) {
	t.Helper()
	next, cmd := m.Update(update)
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("Update вернул %T, ждали Model", next)
	}
	if cmd == nil {
		t.Fatal("апдейт удаления не вернул ни одной команды")
	}
	client.deleteUpdates <- wallDeleteEvent(3, 31337)
	// Каждая команда выполняется РОВНО ОДИН РАЗ: ожидание на канале удалений
	// блокируется, и повторный запуск той же команды завис бы уже на пустом
	// канале. Поэтому пакет разворачивается в его сообщения, а не в команды.
	raw := cmd()
	results := []tea.Msg{raw}
	if batch, isBatch := raw.(tea.BatchMsg); isBatch {
		results = make([]tea.Msg, 0, len(batch))
		for _, one := range batch {
			if one == nil {
				continue
			}
			results = append(results, one())
		}
	}
	var latest wallCardLatestMsg
	found := false
	for _, message := range results {
		switch message := message.(type) {
		case wallCardLatestMsg:
			latest, found = message, true
		case wallDeleteUpdateMsg:
			// переподписка, её результат не проверяется здесь
		default:
			t.Fatalf("в пакете апдейта удаления неожиданное сообщение %T", message)
		}
	}
	return updated, latest, found
}

// applyWallCardResponse — прогнать ответ перезапроса через Update, как это делает
// программа. Отдельный хелпер нужен, чтобы ответ применялся ОДИНАКОВО во всех
// проверках этого файла: прямой вызов applyWallCardLatest проверял бы половину
// пути (само применение) и не проверял бы вторую (что с этим делает Update).
func applyWallCardResponse(t *testing.T, m Model, msg wallCardLatestMsg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("Update вернул %T, ждали Model", next)
	}
	return updated
}

// assertWallUnchanged — стена ровно та же. Сравниваются ВСЕ поля карточек
// (card сравнима по значению), курсор и окно.
//
// Именно сравнение, а не «тест не упал»: идемпотентность это свойство «ничего не
// изменилось», и проверять его чем-то иным, как «стало не хуже», нельзя — на
// коде, который молча переставил карточки, такая проверка была бы зелёной.
func assertWallUnchanged(t *testing.T, got, want Model) {
	t.Helper()
	if !slices.Equal(got.cards, want.cards) {
		t.Fatalf("карточки изменились: %+v -> %+v", want.cards, got.cards)
	}
	if got.cursor != want.cursor {
		t.Fatalf("курсор сдвинулся: %d -> %d", want.cursor, got.cursor)
	}
	if got.scrollTop != want.scrollTop {
		t.Fatalf("окно прокрутки сдвинулось: %d -> %d", want.scrollTop, got.scrollTop)
	}
}

// cardOfChat — карточка источника, падение теста если её нет на стене.
func cardOfChat(t *testing.T, m Model, chatID int64) card {
	t.Helper()
	index := cardIndexOfChat(m, chatID)
	if index < 0 {
		t.Fatalf("на стене нет карточки источника %d: %v", chatID, chatIDOrder(m.cards))
	}
	return m.cards[index]
}

// datesAscending — даты карточек по возрастанию: порядок по дате инвариант
// потока, и проверяется он здесь целиком, а не «карточка встала на своё место».
func datesAscending(cards []card) bool {
	for index := 1; index < len(cards); index++ {
		if cards[index-1].Date > cards[index].Date {
			return false
		}
	}
	return true
}

// Главная проверка задачи: у источника два сообщения, удаляется последнее — и
// карточка источника ОСТАЁТСЯ на стене, показывая предыдущее сообщение. Ровно то,
// что человек описал вживую.
//
// Проверяются конкретные MessageID и текст карточки, а не «карточек столько же»:
// количество осталось бы тем же и вовсе без карточки источника, то есть ровно на
// том поведении, которое тут чинится. Текст проверяется и на экране: карточка,
// которой нет в модели, на экране тоже не появилась бы, а модель в тесте никто
// не рисует «по памяти».
func TestWallDeleteUpdateShowsPreviousMessageOfSource(t *testing.T) {
	client := newWallDeleteTestClient(deleteTestHistory())
	m := newConfirmTestModel(t, client, 60, 24, deleteTestCards())
	if got := m.cards[m.cursor].MessageID; got != deleteTestGoneID {
		t.Fatalf("подготовка: под курсором сообщение %d, ждали удаляемое %d", got, deleteTestGoneID)
	}
	// Удалённого сообщения на сервере больше нет — так и отвечает TDLib.
	client.setHistory(deleteTestChat.ID, []auth.Message{
		{ID: deleteTestLeftID, Text: "прошлый разговор", Date: deleteTestLeftDate},
	})

	next, latest, found := runWallDeleteUpdate(t, m, client,
		wallDeleteUpdateMsg{ok: true, chatID: deleteTestChat.ID, messageIDs: []int64{deleteTestGoneID}})

	if !found {
		t.Fatal("апдейт не вызвал перезапрос последнего сообщения источника")
	}
	if latest.err != nil {
		t.Fatalf("перезапрос не удался: %v", latest.err)
	}
	if !slices.Equal(latest.deletedIDs, []int64{deleteTestGoneID}) {
		t.Fatalf("в ответе удалённые id = %v, ждали [%d] (без них ответ не отличить от опоздавшего)",
			latest.deletedIDs, deleteTestGoneID)
	}
	// Перезапрос точечный и в одно сообщение — те же два вызова и тот же предел,
	// что у снимка стены. Иначе «перезапросить последнее сообщение» означало бы
	// «перезагрузить историю источника».
	if asked, limit := client.historyAsked(deleteTestChat.ID); asked != 1 || limit != wallSnapshotMessagesPerChat {
		t.Fatalf("история источника запрошена %d раз с пределом %d, ждали 1 раз с пределом %d",
			asked, limit, wallSnapshotMessagesPerChat)
	}

	next = applyWallCardResponse(t, next, latest)

	card := cardOfChat(t, next, deleteTestChat.ID)
	if card.MessageID != deleteTestLeftID {
		t.Fatalf("карточка источника показывает сообщение %d, ждали %d (предыдущее)", card.MessageID, deleteTestLeftID)
	}
	if card.Text != "прошлый разговор" {
		t.Fatalf("текст карточки = %q, ждали %q", card.Text, "прошлый разговор")
	}
	if len(next.cards) != len(m.cards) {
		t.Fatalf("карточек после удаления %d, было %d: источник не должен исчезать",
			len(next.cards), len(m.cards))
	}
	if plain := ansi.Strip(next.View().Content); !strings.Contains(plain, "прошлый разговор") {
		t.Fatalf("новое последнее сообщение не на экране: %q", plain)
	}
}

// Ответ, опоздавший к месту, не применяется: пока перезапрос был в полёте, тот же
// источник мог прислать своё новое сообщение, и тогда ответ откатил бы карточку
// назад по времени, к уже удалённому.
//
// Это не теория: тот же источник в этой фикстуре присылает сообщение с датой
// позже удалённого, и подмена видна по id и тексту карточки.
func TestStaleCardLatestResponseIsDiscarded(t *testing.T) {
	client := newWallDeleteTestClient(deleteTestHistory())
	m := newConfirmTestModel(t, client, 60, 24, deleteTestCards())
	client.setHistory(deleteTestChat.ID, []auth.Message{
		{ID: deleteTestLeftID, Text: "прошлый разговор", Date: deleteTestLeftDate},
	})

	_, latest, found := runWallDeleteUpdate(t, m, client,
		wallDeleteUpdateMsg{ok: true, chatID: deleteTestChat.ID, messageIDs: []int64{deleteTestGoneID}})
	if !found || latest.err != nil {
		t.Fatalf("перезапрос не состоялся: found=%v, err=%v", found, latest.err)
	}

	// Пока перезапрос летел, источник прислал своё новое сообщение.
	fresh := m.applyWallMessageUpdate(deleteTestChat.ID, auth.Message{ID: 99, Text: "новое от соседа", Date: 1320})

	next := applyWallCardResponse(t, fresh, latest)

	card := cardOfChat(t, next, deleteTestChat.ID)
	if card.MessageID != 99 {
		t.Fatalf("опоздавший ответ вернул карточку на сообщение %d, ждали новое %d", card.MessageID, 99)
	}
	if card.Text != "новое от соседа" {
		t.Fatalf("текст карточки = %q, ждали %q (опоздавший ответ переписал её)", card.Text, "новое от соседа")
	}
}

// Счётчик непрочитанных переживает замену карточки: wallCard строит карточку из
// снимка чата, где число — то, что было на момент снимка, а не то, что уже
// показывает живой счётчик. Без переноса на стене мигало бы к значению снимка
// при каждой замене карточки (тот же приём и то же обоснование, что у живых
// сообщений, см. applyWallMessageUpdate).
func TestWallDeleteUpdateKeepsUnreadCountOfReplacedCard(t *testing.T) {
	client := newWallDeleteTestClient(deleteTestHistory())
	m := newConfirmTestModel(t, client, 60, 24, deleteTestCards())
	m = m.applyWallUnreadUpdate(deleteTestChat.ID, 5)
	if got := unreadCountOf(m, deleteTestChat.ID); got != 5 {
		t.Fatalf("подготовка: счётчик непрочитанных = %d, ждали 5", got)
	}
	client.setHistory(deleteTestChat.ID, []auth.Message{
		{ID: deleteTestLeftID, Text: "прошлый разговор", Date: deleteTestLeftDate},
	})

	next, latest, found := runWallDeleteUpdate(t, m, client,
		wallDeleteUpdateMsg{ok: true, chatID: deleteTestChat.ID, messageIDs: []int64{deleteTestGoneID}})
	if !found {
		t.Fatal("апдейт не вызвал перезапрос")
	}
	next = applyWallCardResponse(t, next, latest)

	if got := unreadCountOf(next, deleteTestChat.ID); got != 5 {
		t.Fatalf("счётчик непрочитанных после замены карточки = %d, ждали 5 (перенесён со старой карточки)", got)
	}
}

// Порядок карточек по дате — инвариант потока, и замена одной карточки его не
// нарушает: новое последнее сообщение СТАРШЕ удалённого, поэтому карточка встаёт
// на своё место по дате, а не дописывается в конец.
//
// Отдельная проверка от предыдущей не для повтора: подставлять новую карточку в
// конец потока умеет код, который на трёх карточках и на трёх выглядит почти так
// же, — нужна именно подстановка, после которой порядок изменился бы.
func TestWallDeleteUpdateKeepsDateOrderOfStream(t *testing.T) {
	client := newWallDeleteTestClient(deleteTestHistory())
	m := newConfirmTestModel(t, client, 60, 24, deleteTestCards())
	before := chatIDOrder(m.cards)
	if got, want := before, []int64{1, 3, deleteTestChat.ID}; !slices.Equal(got, want) {
		t.Fatalf("подготовка: порядок карточек = %v, ждали %v", got, want)
	}
	client.setHistory(deleteTestChat.ID, []auth.Message{
		{ID: deleteTestLeftID, Text: "прошлый разговор", Date: deleteTestLeftDate},
	})

	next, latest, found := runWallDeleteUpdate(t, m, client,
		wallDeleteUpdateMsg{ok: true, chatID: deleteTestChat.ID, messageIDs: []int64{deleteTestGoneID}})
	if !found {
		t.Fatal("апдейт не вызвал перезапрос последнего сообщения источника")
	}
	next = applyWallCardResponse(t, next, latest)

	// Карточка источника уехала между 13:04 и 13:07 — по дате нового сообщения, а
	// не осталась последней и не встала в конец.
	if got, want := chatIDOrder(next.cards), []int64{1, deleteTestChat.ID, 3}; !slices.Equal(got, want) {
		t.Fatalf("порядок карточек после замены = %v, ждали %v", got, want)
	}
	if !datesAscending(next.cards) {
		t.Fatalf("даты карточек после замены не по возрастанию: %+v", next.cards)
	}
	if got := next.cards[next.cursor].ChatID; got != deleteTestChat.ID {
		t.Fatalf("под курсором источник %d, ждали %d: человек смотрел на этот источник, и он должен остаться под курсором",
			got, deleteTestChat.ID)
	}
}

// Источник, у которого после удаления не осталось сообщений, снимается с карточки
// — и это ЛЕГИТИМНЫЙ случай, а не сбой: показывать нечего, а на источник стена
// показывает одну карточку.
//
// Проверяется на чистом ответе «сообщений нет» (message без id), а не на пустом
// getChatHistory: GetMessages повторяет запрос, пока ответ короче предела (chat
// мог быть не досинхронизирован, см. historyRetryDelays в internal/auth), и такой
// тест платил бы за три реальные задержки в сотни миллисекунд, ничего не проверяя
// сверх этого. Запрос при этом сделан ровно один — тем же пределом в одно
// сообщение, что и снимок стены (см. TestWallDeleteUpdateShowsPreviousMessageOfSource).
func TestWallCardLatestDropsCardWhenSourceHasNoMessagesLeft(t *testing.T) {
	m := newConfirmTestModel(t, newWallDeleteTestClient(deleteTestHistory()), 60, 24, deleteTestCards())
	deleted := []int64{deleteTestGoneID}

	next := m.applyWallCardLatest(wallCardLatestMsg{chatID: deleteTestChat.ID, deletedIDs: deleted})

	if index := cardIndexOfChat(next, deleteTestChat.ID); index >= 0 {
		t.Fatalf("карточка источника без сообщений осталась на стене: %+v", next.cards[index])
	}
	if got, want := chatIDOrder(next.cards), []int64{1, 3}; !slices.Equal(got, want) {
		t.Fatalf("карточек после снятия = %v, ждали %v", got, want)
	}
	// Место снятой карточки занимает соседняя, а курсор не уезжает за поток: под
	// ним и был последний источник.
	if got := next.cards[next.cursor].ChatID; got != 3 {
		t.Fatalf("под курсором источник %d, ждали 3 (сосед занял место снятой карточки)", got)
	}
}

// Открытая переписка теряет удалённое сообщение из своего списка, а курсор на нём
// прижимается, а не остаётся за границей.
//
// Широкий терминал: переписка открыта сама за курсором стены, и она — тот самый
// список, который человек читает, пока удаляет сообщения. Список ровно на пределе
// переписки (фикстура zoomDeleteHistory), чтобы мок не добивал его хвостом и
// последним не оказалось не то сообщение.
func TestWallDeleteUpdateRemovesDeletedMessageFromOpenZoom(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		deleted int64
	}{
		// Последнее сообщение: без зажимания курсор ушёл бы за пределы списка
		// (zoomDeleteLastID) — ровно тот случай, о котором говорит задача.
		{name: "удалено последнее сообщение", deleted: zoomDeleteLastID},
		// Не последнее: список короче, а курсор сдвигается на соседнее сообщение,
		// и тоже обязан остаться внутри списка.
		{name: "удалено сообщение в начале", deleted: zoomDeleteFirstID},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			client := newWallDeleteTestClient(zoomDeleteHistory())
			m := newConfirmTestModel(t, client, 100, 30, zoomDeleteCards())
			m = runZoomLoad(t, m, m.wallZoomLoadedCmd(m.zoom.panel, m.zoom.chatID, m.zoom.loadID))
			if got := len(m.zoom.messages); got != wallZoomMessagesLimit {
				t.Fatalf("подготовка: в переписке %d сообщений, ждали %d", got, wallZoomMessagesLimit)
			}
			if m.zoom.messages[m.zoom.cursor].ID != zoomDeleteLastID {
				t.Fatalf("подготовка: курсор переписки не на последнем сообщении")
			}
			client.setHistory(zoomDeleteChat.ID, zoomDeleteHistory()[zoomDeleteChat.ID][:wallZoomMessagesLimit-1])

			next, latest, _ := runWallDeleteUpdate(t, m, client,
				wallDeleteUpdateMsg{ok: true, chatID: zoomDeleteChat.ID, messageIDs: []int64{testCase.deleted}})
			next = applyWallCardResponse(t, next, latest)

			if next.zoom == nil {
				t.Fatal("переписка закрылась удалением сообщения")
			}
			if got := len(next.zoom.messages); got != wallZoomMessagesLimit-1 {
				t.Fatalf("в переписке %d сообщений, ждали %d", got, wallZoomMessagesLimit-1)
			}
			for _, message := range next.zoom.messages {
				if message.ID == testCase.deleted {
					t.Fatalf("удалённое сообщение %d осталось в переписке", testCase.deleted)
				}
			}
			// Курсор внутри списка: на удалённом сообщении он обязан быть прижат.
			if next.zoom.cursor < 0 || next.zoom.cursor >= len(next.zoom.messages) {
				t.Fatalf("курсор переписки %d вне списка из %d сообщений", next.zoom.cursor, len(next.zoom.messages))
			}
			if next.zoom.scrollTop < 0 || next.zoom.scrollTop >= len(next.zoom.messages) {
				t.Fatalf("окно переписки %d вне списка из %d сообщений", next.zoom.scrollTop, len(next.zoom.messages))
			}
		})
	}
}

// Идемпотентность обоих путей удаления — обязательное требование задачи: TDLib
// шлёт updateDeleteMessages и на собственное удаление, так что один и тот же
// факт может прийти и по ответу deleteMessages, и апдейтом.
//
// Проверяется именно «НЕ ИЗМЕНИЛОСЬ» (сравнением всех полей карточек, курсора и
// окна), а не «стало не хуже»: иначе проверка зелёной осталась бы и на коде,
// который на втором применении тихо переставил карточки.
func TestWallDeleteUpdateRepeatedDoesNotChangeWall(t *testing.T) {
	client := newWallDeleteTestClient(deleteTestHistory())
	m := newConfirmTestModel(t, client, 60, 24, deleteTestCards())
	client.setHistory(deleteTestChat.ID, []auth.Message{
		{ID: deleteTestLeftID, Text: "прошлый разговор", Date: deleteTestLeftDate},
	})
	update := wallDeleteUpdateMsg{ok: true, chatID: deleteTestChat.ID, messageIDs: []int64{deleteTestGoneID}}

	once, latest, found := runWallDeleteUpdate(t, m, client, update)
	if !found {
		t.Fatal("первый апдейт не вызвал перезапрос")
	}
	once = applyWallCardResponse(t, once, latest)
	if got := cardOfChat(t, once, deleteTestChat.ID).MessageID; got != deleteTestLeftID {
		t.Fatalf("подготовка: карточка показывает %d, ждали %d", got, deleteTestLeftID)
	}
	asked, _ := client.historyAsked(deleteTestChat.ID)

	// Тот же апдейт второй раз. Карточки с удалённым сообщением на стене уже нет,
	// поэтому перезапроса быть не должно — а состояние не должно измениться.
	twice, _, foundAgain := runWallDeleteUpdate(t, once, client, update)
	if foundAgain {
		t.Fatal("повторный апдейт снова вызвал перезапрос: удаление не идемпотентно")
	}
	assertWallUnchanged(t, twice, once)
	if askedAgain, _ := client.historyAsked(deleteTestChat.ID); askedAgain != asked {
		t.Fatalf("повторный апдейт сходил в сеть: история бралась %d раз, было %d", askedAgain, asked)
	}
}

// Тот же апдейт, но уже ПОСЛЕ применённого своего удаления: своё удаление клиент
// применяет сразу по ответу deleteMessages (задача 0161), и TDLib присылает по
// нему ещё и updateDeleteMessages. Ни повторное применение своего ответа, ни
// приход апдейта не должны ни воскресить источник, ни сдвинуть что-либо.
func TestWallDeleteUpdateAfterOwnDeleteDoesNotChangeWall(t *testing.T) {
	client := newWallDeleteTestClient(deleteTestHistory())
	m := newConfirmTestModel(t, client, 60, 24, deleteTestCards())
	own := deleteMessageMsg{chatID: deleteTestChat.ID, messageID: deleteTestGoneID}

	deleted := m.applyDeleteMessage(own)
	if index := cardIndexOfChat(deleted, deleteTestChat.ID); index >= 0 {
		t.Fatalf("подготовка: своё удаление не сняло карточку источника: %+v", deleted.cards[index])
	}
	// Тот же ответ deleteMessages второй раз (TDLib и повторная доставка): снять
	// вторую карточку он не может — снимать больше нечего.
	deletedTwice := deleted.applyDeleteMessage(own)
	assertWallUnchanged(t, deletedTwice, deleted)
	asked, _ := client.historyAsked(deleteTestChat.ID)

	next, _, found := runWallDeleteUpdate(t, deletedTwice, client,
		wallDeleteUpdateMsg{ok: true, chatID: deleteTestChat.ID, messageIDs: []int64{deleteTestGoneID}})

	if found {
		t.Fatal("апдейт после уже применённого своего удаления вызвал перезапрос: источник был бы воскрешён")
	}
	assertWallUnchanged(t, next, deletedTwice)
	if askedAgain, _ := client.historyAsked(deleteTestChat.ID); askedAgain != asked {
		t.Fatalf("апдейт после своего удаления сходил в сеть: история бралась %d раз, было %d", askedAgain, asked)
	}
}

// Неудачный перезапрос оставляет стену как была: индикатора ошибок у стены нет
// (осознанно, см. applyWallSendMessage), и выключать источник из-за неудачной
// попытки узнать, что за сообщением было, хуже, чем показать устаревшую карточку.
//
// Проверяется и то, что ошибка ДОШЛА (мок роняет getChatHistory именно этого
// чата), и то, что после неё карточка осталась с прежним содержимым, включая
// текст на экране.
func TestWallCardLatestKeepsWallWhenRefetchFails(t *testing.T) {
	client := newWallDeleteTestClient(deleteTestHistory())
	m := newConfirmTestModel(t, client, 60, 24, deleteTestCards())
	client.breakHistory(deleteTestChat.ID)

	next, latest, found := runWallDeleteUpdate(t, m, client,
		wallDeleteUpdateMsg{ok: true, chatID: deleteTestChat.ID, messageIDs: []int64{deleteTestGoneID}})

	if !found {
		t.Fatal("апдейт не вызвал перезапрос")
	}
	if latest.err == nil {
		t.Fatal("перезапрос сломанного чата прошёл без ошибки — проверка ничего не значила бы")
	}
	next = applyWallCardResponse(t, next, latest)

	assertWallUnchanged(t, next, m)
	card := cardOfChat(t, next, deleteTestChat.ID)
	if card.MessageID != deleteTestGoneID {
		t.Fatalf("после сбоя перезапроса карточка показывает %d, ждали прежнее %d", card.MessageID, deleteTestGoneID)
	}
	if plain := ansi.Strip(next.View().Content); !strings.Contains(plain, "удалённое сообщение") {
		t.Fatalf("карточка исчезла с экрана после сбоя перезапроса: %q", plain)
	}
}

// Сам waitForWallDeleteUpdate: читает апдейт из канала и заворачивает его в
// wallDeleteUpdateMsg. Без отдельного теста проверялся бы только разбор уже
// готового сообщения, а на command-функцию можно было бы и не позвать вовсе.
func TestWaitForWallDeleteUpdateParsesTDLibEvent(t *testing.T) {
	client := newWallDeleteTestClient(deleteTestHistory())
	client.deleteUpdates <- wallDeleteEvent(deleteTestChat.ID, deleteTestGoneID, deleteTestLeftID)
	m := New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)

	raw := m.waitForWallDeleteUpdate()()
	update, ok := raw.(wallDeleteUpdateMsg)
	if !ok {
		t.Fatalf("ожидание вернуло %T, ждали wallDeleteUpdateMsg", raw)
	}
	if update.closed || !update.ok || update.chatID != deleteTestChat.ID {
		t.Fatalf("разобранный апдейт = %+v", update)
	}
	if !slices.Equal(update.messageIDs, []int64{deleteTestGoneID, deleteTestLeftID}) {
		t.Fatalf("удалённые id = %v, ждали [%d %d]", update.messageIDs, deleteTestGoneID, deleteTestLeftID)
	}
}

// Отменённый контекст закрывает ожидание: иначе команда из Init() висела бы
// вечно на выходе из программы.
func TestWaitForWallDeleteUpdateStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := New(ctx, newWallDeleteTestClient(deleteTestHistory()), "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	cancel()

	raw := m.waitForWallDeleteUpdate()()
	update, ok := raw.(wallDeleteUpdateMsg)
	if !ok || !update.closed {
		t.Fatalf("ожидание по отменённому контексту = %#v, ждали closed", raw)
	}
}

// Канал, закрытый уже во время работы (не «его нет», а его закрыли), — тот же
// closed: ждать больше нечего, и переподписка на него только завесила бы стену
// мёртвой командой.
func TestWaitForWallDeleteUpdateStopsOnClosedChannel(t *testing.T) {
	client := newWallDeleteTestClient(deleteTestHistory())
	close(client.deleteUpdates)
	m := New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)

	raw := m.waitForWallDeleteUpdate()()
	update, ok := raw.(wallDeleteUpdateMsg)
	if !ok || !update.closed {
		t.Fatalf("ожидание по закрытому каналу = %#v, ждали closed", raw)
	}
}

// Клиента нет и канала апдейтов нет — оба случая обязаны отдавать closed сразу:
// ждать в канале, которого не будет никогда, значило бы зависнуть в Init().
func TestWaitForWallDeleteUpdateWithoutClientOrChannel(t *testing.T) {
	assertClosed := func(name string, m Model) {
		t.Helper()
		update, ok := m.waitForWallDeleteUpdate()().(wallDeleteUpdateMsg)
		if !ok || !update.closed || update.ok {
			t.Fatalf("%s: ожидание = %#v, ждали closed", name, update)
		}
	}
	// wallLoadClient в DeleteMessagesUpdates() отдаёт nil — так же ведёт себя
	// клиент, у которого поток апдейтов не подключён.
	assertClosed("клиент без канала апдейтов", New(context.Background(), newWallLoadClient(wallTestChats(), wallTestHistory()), "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion))
	assertClosed("клиент без клиента", New(context.Background(), nil, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion))
}

// Init() обязан подписаться и на удаления: без третьей подписки стена не узнает
// ни о чужом удалении, ни о том, что карточке источника пора показать новое
// последнее сообщение.
func TestInitSubscribesToWallDeleteUpdates(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := newWallDeleteTestClient(deleteTestHistory())
	m := New(ctx, client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	cancel()

	raw := m.Init()()
	batch, ok := raw.(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init вернул %T, ждали tea.BatchMsg", raw)
	}
	sawDelete := false
	for _, cmd := range batch {
		if cmd == nil {
			continue
		}
		if update, ok := cmd().(wallDeleteUpdateMsg); ok {
			sawDelete = update.closed
		}
	}
	if !sawDelete {
		t.Error("Init не запустил подписку на удаления")
	}
}

// Переподписка на удаления — тот же трёхвариантный контракт, что у живых
// сообщений: нераспознанный апдейт молча и с переподпиской, закрытый канал —
// без неё (ждать нечего, иначе стена завесила бы мёртвой командой).
func TestWallDeleteUpdateResubscribesUnlessChannelClosed(t *testing.T) {
	m := newConfirmTestModel(t, newWallDeleteTestClient(deleteTestHistory()), 60, 24, deleteTestCards())
	cards, cursor, scrollTop := len(m.cards), m.cursor, m.scrollTop

	next, cmd := m.applyWallDeleteUpdate(wallDeleteUpdateMsg{ok: false, chatID: deleteTestChat.ID})
	if cmd == nil {
		t.Error("на нераспознанный апдейт переподписка не вернулась — удаления перестанут жить")
	}
	assertWallUnchanged(t, next, m)

	next, cmd = m.applyWallDeleteUpdate(wallDeleteUpdateMsg{closed: true})
	if cmd != nil {
		t.Error("на закрытый канал вернулась команда — ждать нечего, она зависнет")
	}
	if len(next.cards) != cards || next.cursor != cursor || next.scrollTop != scrollTop {
		t.Errorf("закрытый канал изменил стену: карточек %d->%d, курсор %d->%d, окно %d->%d",
			cards, len(next.cards), cursor, next.cursor, scrollTop, next.scrollTop)
	}
}

// Апдейт, удаливший не то сообщение, что показано на карточке источника, не
// вызывает перезапроса: удалённого на стене и так нет, а ходить в сеть незачем.
// Сюда же попадает удаление из ЧУЖОГО чата — источника такого на стене может
// вообще не быть.
func TestWallDeleteUpdateOfUnrelatedMessageDoesNotRefetch(t *testing.T) {
	client := newWallDeleteTestClient(deleteTestHistory())
	m := newConfirmTestModel(t, client, 60, 24, deleteTestCards())
	asked, _ := client.historyAsked(deleteTestChat.ID)

	for _, update := range []wallDeleteUpdateMsg{
		// То же сообщение, но в другом чате: у каждой карточки свой чат.
		{ok: true, chatID: 3, messageIDs: []int64{deleteTestGoneID}},
		// Тот же чат, но удалено не то сообщение, что на карточке (на карточке —
		// последнее, а это из её истории).
		{ok: true, chatID: deleteTestChat.ID, messageIDs: []int64{deleteTestLeftID}},
		// Чат, которого на стене нет вовсе.
		{ok: true, chatID: 4242, messageIDs: []int64{deleteTestGoneID}},
		// Пустой список удалённых: удалять всё равно нечего.
		{ok: true, chatID: deleteTestChat.ID, messageIDs: nil},
	} {
		next, _, found := runWallDeleteUpdate(t, m, client, update)
		if found {
			t.Fatalf("апдейт %+v вызвал перезапрос без причины", update)
		}
		assertWallUnchanged(t, next, m)
	}
	if askedAgain, _ := client.historyAsked(deleteTestChat.ID); askedAgain != asked {
		t.Fatalf("история бралась %d раз, ждали ни разы (было %d): апдейт ходил в сеть зря",
			askedAgain, asked)
	}
}
