package tgwall

import (
	"context"
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"telecli/internal/auth"
	"telecli/internal/config"
)

// wallLiveTestClient — мок TDLib для живых апдейтов стены. Загрузку стены он
// умеет сам (встроенный wallLoadClient), а сверху добавляет канал апдейтов:
// сам по себе мок загрузки отдаёт nil в MessageUpdates, и ждать апдейта было бы
// негде.
type wallLiveTestClient struct {
	*wallLoadClient
	updates chan map[string]interface{}
	// unreadUpdates — отдельный канал updateChatReadInbox: в бою TDLib шлёт
	// живые сообщения и числа непрочитанных по разным каналам, и подписки на
	// них не должны путать апдейты друг друга.
	unreadUpdates chan map[string]interface{}
	// groupCountUpdates, userStatusUpdates, userProfileUpdates — отдельные каналы
	// сведений об источнике (задача 0163). Отдельными, а не одним общим, по той
	// же причине, что и unreadUpdates: в бою это три разных апдейта TDLib, и
	// подписки на них не должны путать друг друга.
	groupCountUpdates  chan map[string]interface{}
	userStatusUpdates  chan map[string]interface{}
	userProfileUpdates chan map[string]interface{}
}

func newWallLiveTestClient() *wallLiveTestClient {
	return &wallLiveTestClient{
		wallLoadClient:    newWallLoadClient(wallTestChats(), wallTestHistory()),
		updates:           make(chan map[string]interface{}, 4),
		unreadUpdates:     make(chan map[string]interface{}, 4),
		groupCountUpdates: make(chan map[string]interface{}, 4),
		userStatusUpdates: make(chan map[string]interface{}, 4),
		// Буфер побольше остальных: updateUser на живой загрузке идёт пачками по
		// одному на чат (проверено вживью), и тест на три апдейта подряд не
		// должен упираться в переполнение.
		userProfileUpdates: make(chan map[string]interface{}, 8),
	}
}

func (c *wallLiveTestClient) MessageUpdates() <-chan map[string]interface{} {
	return c.updates
}

func (c *wallLiveTestClient) ChatReadInboxUpdates() <-chan map[string]interface{} {
	return c.unreadUpdates
}

func (c *wallLiveTestClient) GroupMemberCountUpdates() <-chan map[string]interface{} {
	return c.groupCountUpdates
}

func (c *wallLiveTestClient) UserStatusUpdates() <-chan map[string]interface{} {
	return c.userStatusUpdates
}

func (c *wallLiveTestClient) UserUpdates() <-chan map[string]interface{} {
	return c.userProfileUpdates
}

// wallLiveMessageUpdate — updateNewMessage в той форме, которую отдаёт TDLib.
// Формат скопирован из internal/tgclitui/live_test.go (liveMessageUpdate) и
// повторять разбор TDLib в тесте вместо боевого не стоит: проверяется вставка
// карточки, а не разбор JSON.
func wallLiveMessageUpdate(chatID, messageID, date int64, text string) map[string]interface{} {
	return map[string]interface{}{
		"@type": "updateNewMessage",
		"message": map[string]interface{}{
			"chat_id": float64(chatID),
			"id":      float64(messageID),
			"date":    float64(date),
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

// Фикстуры живых апдейтов. Поток здесь длиннее экрана (тот же размер, что у
// wallScrollCards): показывать новое сообщение у нижнего края и держать курсор
// на месте при вставке выше — проверяемо только на стене, которую есть что
// прокручивать.

// liveWallFreshChat — чат снимка, КОТОРОГО нет в потоке карточек: его история
// пуста, и живое сообщение в нём — первое появление этого чата на стене.
// Отдельный чат выбран потому, что его заголовок на стене уникален: поиск
// «на какой строке нарисована карточка» (wallRowOf) идёт по заголовку, и
// карточка с именем уже показанного чата попала бы в кадр задолго до своего
// появления.
func liveWallFreshChat() auth.Chat {
	return auth.Chat{ID: 90, Title: "Свежие", Kind: auth.ChatGroup}
}

// liveWallChat — чат снимка с историей. Имена чатов в потоке различимы, иначе
// wallRowOf находит не ту карточку (см. liveWallFreshChat).
func liveWallChat(index int) auth.Chat {
	return auth.Chat{ID: int64(index + 1), Title: fmt.Sprintf("Чат%02d", index), Kind: auth.ChatPrivate}
}

// liveWallChats — чаты снимка: count чатов с историей плюс чат без истории.
func liveWallChats(count int) []auth.Chat {
	chats := make([]auth.Chat, 0, count+1)
	for index := range count {
		chats = append(chats, liveWallChat(index))
	}
	return append(chats, liveWallFreshChat())
}

// liveWallMessages — по одному сообщению на каждый чат с историей: даты идут
// подряд с единицы (с ними работает бинарный поиск вставки), id совпадают с
// порядком карточек (по ним проверяется, какая именно карточка осталась под
// курсором).
func liveWallMessages(count int) []wallMessage {
	messages := make([]wallMessage, 0, count)
	for index := range count {
		messages = append(messages, wallMessage{
			Chat: liveWallChat(index),
			Message: auth.Message{
				ID:   int64(index + 1),
				Text: "сообщение",
				Date: int64(index + 1),
			},
		})
	}
	return messages
}

// liveWallModel — стена с потоком из count карточек, пришедшим через
// wallLoadedMsg, то есть ровно то состояние, в котором живые апдейты
// начинают приходить.
func liveWallModel(t *testing.T, count int) Model {
	t.Helper()
	m := New(context.Background(), nil, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	next, _ := m.Update(tea.WindowSizeMsg{Width: scrollWidth, Height: 30})
	m = next.(Model)
	messages := liveWallMessages(count)
	next, _ = m.Update(wallLoadedMsg{cards: wallCards(messages), chats: liveWallChats(count)})
	m = next.(Model)
	if m.loading {
		t.Fatal("после wallLoadedMsg стена осталась в состоянии загрузки")
	}
	return m
}

// selectedCard — карточка под курсором.
func selectedCard(t *testing.T, m Model) card {
	t.Helper()
	if m.cursor < 0 || m.cursor >= len(m.cards) {
		t.Fatalf("курсор %d вне потока из %d карточек", m.cursor, len(m.cards))
	}
	return m.cards[m.cursor]
}

// applyLiveUpdate — прогон живого апдейта через настоящий Update, как это делает
// программа, и возврат модели и команды.
func applyLiveUpdate(t *testing.T, m Model, update wallMessageUpdateMsg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(update)
	return next.(Model), cmd
}

// wallIndexOfChat — индекс карточки источника на стене, падение теста если её
// нет. Считается в тесте, а не вызовом одноимённого хелпера продкода: проверка
// «курсор нашёл карточку после перестройки» не должна опираться на тот же код,
// который она проверяет.
func wallIndexOfChat(t *testing.T, m Model, chatID int64) int {
	t.Helper()
	for index, item := range m.cards {
		if item.ChatID == chatID {
			return index
		}
	}
	t.Fatalf("на стене нет карточки источника %d (поток из %d карточек)", chatID, len(m.cards))
	return -1
}

// wallCountOfChat — сколько карточек этого источника в потоке. Больше одной быть
// не может: одна карточка на источник и есть модель стены. Принимает срез, а не
// Model, чтобы рабо��ать и со снимком загрузки, где модели ещё нет.
func wallCountOfChat(cards []card, chatID int64) int {
	count := 0
	for _, item := range cards {
		if item.ChatID == chatID {
			count++
		}
	}
	return count
}

// assertSameWall — стена не изменилась ни по одной карточке. Сравнивается весь
// поток, а не его длина: «карточек столько же» ничего не говорит, если у одной
// карточки сменился текст или адрес.
func assertSameWall(t *testing.T, before []card, m Model) {
	t.Helper()
	if len(before) != len(m.cards) {
		t.Fatalf("число карточек изменилось: было %d, стало %d", len(before), len(m.cards))
	}
	for index := range before {
		if before[index] != m.cards[index] {
			t.Fatalf("карточка %d изменилась: было %+v, стало %+v", index, before[index], m.cards[index])
		}
	}
}

// assertWallMessageOrder — порядок карточек потока по id сообщений.
func assertWallMessageOrder(t *testing.T, m Model, want []int) {
	t.Helper()
	if len(m.cards) != len(want) {
		t.Fatalf("карточек %d, ждали %d: %v", len(m.cards), len(want), wallMessageIDs(m))
	}
	for index, wantID := range want {
		if m.cards[index].MessageID != int64(wantID) {
			t.Fatalf("порядок карточек = %v, ждали %v", wallMessageIDs(m), want)
		}
	}
}

// wallMessageIDs — id сообщений карточек по порядку, для сообщения об ошибке.
func wallMessageIDs(m Model) []int {
	ids := make([]int, 0, len(m.cards))
	for _, item := range m.cards {
		ids = append(ids, int(item.MessageID))
	}
	return ids
}

// Новое сообщение в чате, который был в снимке, добавляет карточку. Проверяется
// и сам факт появления, и содержимое карточки (тип, тег, текст) — карточка
// обязана собираться той же wallCard, что и весь снимок, иначе живая карточка
// выглядела бы иначе, чем все остальные.
func TestLiveMessageAddsCardForKnownChat(t *testing.T) {
	m := liveWallModel(t, 3)
	before := len(m.cards)
	chat := liveWallFreshChat()

	next, cmd := applyLiveUpdate(t, m, wallMessageUpdateMsg{
		valid:   true,
		chatID:  chat.ID,
		message: auth.Message{ID: 90, Date: 900, Text: "живое сообщение"},
	})
	m = next

	if len(m.cards) != before+1 {
		t.Fatalf("карточек после живого сообщения %d, ждали %d", len(m.cards), before+1)
	}
	got := m.cards[len(m.cards)-1]
	if got.Type != cardChat || got.Tag != "#чат" {
		t.Errorf("тип/тег живой карточки = %v/%q, ждали %v/#чат", got.Type, got.Tag, cardChat)
	}
	if got.Text != "живое сообщение" || got.Name != chat.Title {
		t.Errorf("живая карточка = %q из %q, ждали «живое сообщение» из %q", got.Text, got.Name, chat.Title)
	}
	if got.Time != auth.FormatMessageTime(900) {
		t.Errorf("время живой карточки = %q, ждали %q", got.Time, auth.FormatMessageTime(900))
	}
	// Адрес карточки заполнен — без него дедуп и удержание курсора не работают.
	if got.ChatID != chat.ID || got.MessageID != 90 || got.Date != 900 {
		t.Errorf("адрес живой карточки = (%d, %d, %d), ждали (%d, 90, 900)",
			got.ChatID, got.MessageID, got.Date, chat.ID)
	}
	if cmd == nil {
		t.Error("после живого сообщения команда переподписки не вернулась")
	}
}

// Вставка живого сообщения в позицию, вычисленную по дате: сообщение с датой
// МЕЖДУ двумя уже существующими карточками обязано оказаться между ними, а не
// уехать в конец. Сортировка по дате — единственный порядок стены, и живая
// вставка не имеет права его ломать.
func TestLiveMessageKeepsDateOrder(t *testing.T) {
	// Даты снимка — 10, 20, 30: 25 строго между второй и третьей карточкой, и
	// равных дат рядом нет, чтобы результат вставки был однозначным.
	m := liveWallModelWithDates(t, 3, 10, 20, 30)
	next, _ := applyLiveUpdate(t, m, wallMessageUpdateMsg{
		valid:   true,
		chatID:  liveWallFreshChat().ID,
		message: auth.Message{ID: 90, Date: 25, Text: "между"},
	})
	m = next

	want := []int64{1, 2, 90, 3}
	if len(m.cards) != len(want) {
		t.Fatalf("карточек после вставки %d, ждали %d", len(m.cards), len(want))
	}
	for index, wantID := range want {
		if m.cards[index].MessageID != wantID {
			var got []int64
			for _, item := range m.cards {
				got = append(got, item.MessageID)
			}
			t.Fatalf("порядок после вставки = %v, ждали %v — вставка по дате сортировку нарушила", got, want)
		}
	}
}

// liveWallModelWithDates — та же стена, но с заданными датами карточек: с
// датами 1, 2, 3 «между двумя карточками» не вставить, не задев третью.
func liveWallModelWithDates(t *testing.T, count int, dates ...int64) Model {
	t.Helper()
	m := New(context.Background(), nil, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	next, _ := m.Update(tea.WindowSizeMsg{Width: scrollWidth, Height: 30})
	m = next.(Model)
	messages := liveWallMessages(count)
	for index := range messages {
		if index < len(dates) {
			messages[index].Message.Date = dates[index]
		}
	}
	next, _ = m.Update(wallLoadedMsg{cards: wallCards(messages), chats: liveWallChats(count)})
	return next.(Model)
}

// insertSortedCard проверяется сам по себе, а не только через живые апдейты.
// После перехода стены на одну карточку на источник живой путь приходит сюда
// уже без карточки этого чата, и собственная дедупликация хелпера на нём больше
// не срабатывает: единственный её тест был бы тестом недостижимой ветки, а сам
// контракт («тот же чат и то же сообщение — дубль») остался бы непроверенным.
//
// Здесь карточки одного чата на стене задаются напрямую — как раз то состояние,
// которое снимок и живой путь уже не создают.
func TestInsertSortedCardRejectsSameMessageOfSameChat(t *testing.T) {
	cards := []card{
		{ChatID: 1, MessageID: 10, Date: 100},
		{ChatID: 2, MessageID: 20, Date: 200},
	}
	// Тот же чат и то же сообщение: дубль, -1 и поток без изменений.
	position, got := insertSortedCard(cards, card{ChatID: 1, MessageID: 10, Date: 100})
	if position != -1 {
		t.Fatalf("позиция дубля = %d, ждали -1", position)
	}
	if len(got) != len(cards) || got[0] != cards[0] || got[1] != cards[1] {
		t.Fatalf("дубль изменил поток: %+v", got)
	}
	// Тот же id, но ДРУГОЙ чат: не дубль — вставляется по дате.
	position, got = insertSortedCard(cards, card{ChatID: 3, MessageID: 10, Date: 150})
	if position != 1 {
		t.Fatalf("позиция вставки = %d, ждали 1 (по дате 150 между 100 и 200)", position)
	}
	assertWallMessageOrder(t, Model{cards: got}, []int{10, 10, 20})
}

// Сообщение с id, который уже есть на стене у ДРУГОГО чата, добавляется как
// новая карточка: дедуп у insertSortedCard по паре (чат, id), а не по одному
// id. На живой модели стены это означает «у этого источника ещё не было
// карточки» (иначе апдейт заменил бы её), то есть самый частый случай появления
// карточки — первый.
func TestLiveMessageFromOtherChatWithSameIDIsNotDuplicate(t *testing.T) {
	m := liveWallModel(t, 3)

	// Тот же id, что у третьей карточки снимка (даты 1, 2, 3) — но из другого
	// чата, у которого на стене карточки нет.
	next, _ := applyLiveUpdate(t, m, wallMessageUpdateMsg{
		valid:   true,
		chatID:  liveWallFreshChat().ID,
		message: auth.Message{ID: 3, Date: 3, Text: "тот же id из другого чата"},
	})
	m = next
	if len(m.cards) != 4 {
		t.Fatalf("сообщение с чужим id выкинуто или задвоено: на стене %d, ждали 4", len(m.cards))
	}

	// Настоящий дубль — тот же чат И тот же id, что у карточки этого чата.
	// Карточка источника на стене одна, и она уже показывает это сообщение:
	// ни вторая карточка, ни перестановка тут неуместны.
	before := append([]card(nil), m.cards...)
	next, _ = applyLiveUpdate(t, m, wallMessageUpdateMsg{
		valid:   true,
		chatID:  liveWallFreshChat().ID,
		message: auth.Message{ID: 3, Date: 3, Text: "повтор"},
	})
	m = next
	assertSameWall(t, before, m)
}

// Повторная доставка одного и того же апдейта (TDLib присылает
// updateNewMessage повторно при переподключении) не меняет стену ни на одну
// карточку. Отдельно от предыдущего теста проверяется и содержимое: карточка
// обязана остаться ровно той же, какой была до повтора, — иначе «повтор» тихо
// перерисовал бы то, что человек уже читает.
func TestLiveMessageRepeatOfSameUpdateChangesNothing(t *testing.T) {
	m := liveWallModel(t, scrollCardsCount)
	chat := liveWallChat(1)
	repeat := wallMessageUpdateMsg{
		valid:   true,
		chatID:  chat.ID,
		message: auth.Message{ID: 77, Date: 7000, Text: "живое"},
	}

	next, _ := applyLiveUpdate(t, m, repeat)
	m = next
	before := append([]card(nil), m.cards...)
	cursor, scrollTop := m.cursor, m.scrollTop
	if len(before) != len(m.cards) {
		t.Fatalf("подготовка: карточек %d, ждали на одну больше", len(m.cards))
	}

	next, _ = applyLiveUpdate(t, m, repeat)
	m = next
	assertSameWall(t, before, m)
	if m.cursor != cursor || m.scrollTop != scrollTop {
		t.Errorf("повтор апдейта сдвинул курсор/окно: %d->%d, %d->%d",
			cursor, m.cursor, scrollTop, m.scrollTop)
	}
}

// messageIsNewer — единственное место, где решается, заменять ли карточку
// источника. Проверяется напрямую, а не через Update: правило «тот же MessageID
// — не новее» и «младше по дате — не новее» иначе пришлось бы вылавливать
// косвенно, через формулировку «а что в итоге на стене».
func TestMessageIsNewer(t *testing.T) {
	existing := card{ChatID: 1, MessageID: 10, Date: 500}
	cases := []struct {
		name string
		next card
		want bool
		why  string
	}{
		{
			name: "тот же апдейт",
			next: card{ChatID: 1, MessageID: 10, Date: 500},
			want: false,
			why:  "повторная доставка того же сообщения",
		},
		{
			name: "та же дата, но другое сообщение",
			next: card{ChatID: 1, MessageID: 11, Date: 500},
			want: true,
			why:  "равенство дат не запрещает замену: новое сообщение может прийти с тем же временем",
		},
		{
			name: "свежее",
			next: card{ChatID: 1, MessageID: 11, Date: 600},
			want: true,
			why:  "обычное новое сообщение источника",
		},
		{
			name: "старее",
			next: card{ChatID: 1, MessageID: 9, Date: 400},
			want: false,
			why:  "апдейт не по порядку не должен уводить карточку назад по времени",
		},
		{
			name: "нулевой id не считается повтором",
			next: card{ChatID: 1, MessageID: 0, Date: 600},
			want: true,
			why:  "у тестовых карточек без id иначе любое сообщение считалось бы дублем",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := messageIsNewer(testCase.next, existing); got != testCase.want {
				t.Fatalf("messageIsNewer = %v, ждали %v (%s)", got, testCase.want, testCase.why)
			}
		})
	}
}

// Апдейт с датой МЕНЬШЕ, чем у уже показанной карточки источника, отбрасывается:
// карточка не заменяется и на новое место не уезжает. Проверяется и через Update
// (стена не меняется вообще), и напрямую (messageIsNewer), потому что это
// ровно то правило, из-за которого на стене в принципе не может появиться
// «второе, но более старое» сообщение того же чата.
func TestLiveMessageOlderThanShownCardIsDropped(t *testing.T) {
	m := liveWallModel(t, 3)
	chat := liveWallChat(1) // карточка с датой 2
	before := append([]card(nil), m.cards...)

	next, _ := applyLiveUpdate(t, m, wallMessageUpdateMsg{
		valid:   true,
		chatID:  chat.ID,
		message: auth.Message{ID: 77, Date: 1, Text: "из прошлого"},
	})
	m = next
	assertSameWall(t, before, m)
}

// Главное поведение задачи: у источника, у которого карточка на стене уже есть,
// новое сообщение ЗАМЕНЯЕТ её, а не добавляет вторую. Проверяются все три следствия
// сразу — число карточек, новое содержимое и отсутствие старой карточки этого
// источника где-либо в потоке (иначе на стене жил бы дубль источника, и человек
// отличил бы его от настоящего нового сообщения разве что по времени).
func TestLiveMessageReplacesCardOfSourceItUpdates(t *testing.T) {
	// Даты 10, 20, 30 — карточка среднего чата (20) стоит в середине потока.
	m := liveWallModelWithDates(t, 3, 10, 20, 30)
	m = m.moveCursor(-1) // курсор на первой карточке, чтобы перестановка не мешала проверке
	chat := liveWallChat(1)
	if index := wallIndexOfChat(t, m, chat.ID); index != 1 {
		t.Fatalf("подготовка: карточка чата на индексе %d, ждали 1", index)
	}

	next, _ := applyLiveUpdate(t, m, wallMessageUpdateMsg{
		valid:   true,
		chatID:  chat.ID,
		message: auth.Message{ID: 900, Date: 5000, Text: "новое сообщение"},
	})
	m = next

	if len(m.cards) != 3 {
		t.Fatalf("после нового сообщения источника карточек %d, ждали те же 3 (карточка заменяется, а не добавляется)", len(m.cards))
	}
	if count := wallCountOfChat(m.cards, chat.ID); count != 1 {
		t.Fatalf("карточек чата %d на стене %d, ждали ровно 1 — старая осталась дублем", chat.ID, count)
	}
	got := m.cards[wallIndexOfChat(t, m, chat.ID)]
	if got.Text != "новое сообщение" || got.MessageID != 900 || got.Date != 5000 {
		t.Fatalf("карточка источника = %q (%d, %d), ждали «новое сообщение» (900, 5000)",
			got.Text, got.MessageID, got.Date)
	}
	// Замена не должна оставить от источника ничего от прежней карточки.
	for _, item := range m.cards {
		if item.MessageID == 2 {
			t.Fatalf("старое сообщение источника осталось на стене отдельной карточкой: %+v", item)
		}
	}
}

// Заменённая карточка встаёт на место по своему НОВОМУ времени, а не остаётся
// там, где была: поток стены отсортирован по дате, и оставь она старую позицию —
// «свежая» карточка стояла бы выше более старых. Свежая дата уезжает в конец,
// промежуточная — в середину между соседями.
func TestLiveMessageReplacedCardMovesToItsNewDatePlace(t *testing.T) {
	m := liveWallModelWithDates(t, 3, 10, 20, 30)
	chat := liveWallChat(0) // карточка с датой 10, самая старая
	// Курсор вниз от последней карточки — чтобы апдейт считался «слежкой за
	// свежим» и проверка шла именно про место карточки в потоке, а не про
	// удержание курсора (оно проверяется отдельно).
	m = m.moveCursor(1)

	// Промежуточная дата: строго между второй (20) и третьей (30).
	next, _ := applyLiveUpdate(t, m, wallMessageUpdateMsg{
		valid:   true,
		chatID:  chat.ID,
		message: auth.Message{ID: 900, Date: 25, Text: "между"},
	})
	m = next
	want := []int{2, 900, 3} // чаты 2 и 3 остались на своих местах по дате
	assertWallMessageOrder(t, m, want)
	if index := wallIndexOfChat(t, m, chat.ID); index != 1 {
		t.Fatalf("переставленная карточка на индексе %d, ждали 1 (по дате 25 между 20 и 30)", index)
	}
}

// Курсор стоял НЕ на обновляемом источнике: перестановка чужой карточки сдвигает
// индексы, и курсор всё равно обязан остаться на своей — по источнику, а не по
// прежнему номеру. Раньше (до 0152) это держалось на паре (чат, сообщение); теперь
// карточка представляет источник, поэтому и держится она по ChatID.
func TestLiveMessageKeepsCursorOnOtherSourceWhenItMoves(t *testing.T) {
	m := liveWallModel(t, scrollCardsCount)
	for range 2 {
		m = m.moveCursor(-1)
	}
	before := selectedCard(t, m)
	beforeIndex := m.cursor

	// Карточка чата 5 (индекс 4, дата 5) уезжает в самый конец потока: все
	// индексы ПОД ней сдвигаются на единицу, и прежний номер курсора указывает
	// уже на другую карточку.
	next, _ := applyLiveUpdate(t, m, wallMessageUpdateMsg{
		valid:   true,
		chatID:  liveWallChat(4).ID,
		message: auth.Message{ID: 900, Date: 9000, Text: "переехало"},
	})
	m = next

	after := selectedCard(t, m)
	if after.ChatID != before.ChatID {
		t.Fatalf("курсор уехал с источника %d на %d, хотя обновлялся другой (%d)",
			before.ChatID, after.ChatID, liveWallChat(4).ID)
	}
	if m.cursor == beforeIndex {
		t.Fatal("индекс курсора не сдвинулся после перестановки чужой карточки выше — проверка по индексу ничего бы не значила")
	}
	if m.cursor != beforeIndex-1 {
		t.Fatalf("курсор = %d, ждали %d (перестановка карточки выше сдвинула его на единицу)",
			m.cursor, beforeIndex-1)
	}
	if m.cursor == len(m.cards)-1 {
		t.Fatalf("курсор уехал на последнюю карточку (%d): человек читал не конец, и стена не должна его перехватывать", m.cursor)
	}
	if got := wallRowOf(t, m, m.cursor); got < 0 {
		t.Fatalf("после перестановки курсор %d вне окна (scrollTop %d)", m.cursor, m.scrollTop)
	}
	assertExactlyOneSelected(t, m)
}

// Курсор стоял ИМЕННО на карточке, которая обновилась и переставилась. По
// прежней модели карточка была привязана к сообщению, и её MessageID меняется
// при каждом апдейте — привязка к старому id потеряла бы курсор ровно тогда,
// когда карточка обновилась. Здесь он обязан переехать вместе с ней: проверяется
// индекс источника ПОСЛЕ обновления, а не «индекс не изменился».
func TestLiveMessageFollowsCursorToMovedCard(t *testing.T) {
	m := liveWallModel(t, scrollCardsCount)
	for range 19 {
		m = m.moveCursor(-1)
	}
	chat := liveWallChat(20)
	before := selectedCard(t, m)
	if before.ChatID != chat.ID {
		t.Fatalf("подготовка: под курсором источник %d, ждали %d", before.ChatID, chat.ID)
	}

	next, _ := applyLiveUpdate(t, m, wallMessageUpdateMsg{
		valid:   true,
		chatID:  chat.ID,
		message: auth.Message{ID: 900, Date: 9000, Text: "обновилось"},
	})
	m = next

	index := wallIndexOfChat(t, m, chat.ID)
	if index != len(m.cards)-1 {
		t.Fatalf("обновлённая карточка на индексе %d, ждали %d (последняя)", index, len(m.cards)-1)
	}
	if m.cursor != index {
		t.Fatalf("курсор = %d, ждали %d: он обязан уехать вместе с карточкой источника", m.cursor, index)
	}
	if got := m.cards[m.cursor]; got.MessageID != 900 || got.Text != "обновилось" {
		t.Fatalf("под курсором = %q (%d), ждали обновлённую карточку источника", got.Text, got.MessageID)
	}
	// Окно подтянулось ровно настолько, чтобы карточка осталась в кадре: человек
	// читал выше конца, поэтому стена не прижимается к низу, а просто держит
	// выбранное в кадре.
	if got := wallRowOf(t, m, m.cursor); got < 0 {
		t.Fatalf("переехавшая карточка вне окна (scrollTop %d)", m.scrollTop)
	}
	assertExactlyOneSelected(t, m)
}

// Курсор был на самой свежей карточке, и обновляется ДРУГОЙ источник: его карточка
// уезжает вниз и становится новой последней, а та, на которой стоял курсор, уходит
// выше. Курсор в этом случае следует за «самым свежим» — он и был на нём секунду
// назад, — а окно прижимается к низу. Тот же приём, что у TestLiveMessageScrollsToBottomWhenAtLatest,
// но для перестановки существующей карточки, а не для появления новой.
func TestLiveMessageScrollsToBottomWhenSourceUpdatesWhileAtLatest(t *testing.T) {
	m := liveWallModel(t, scrollCardsCount)
	if m.cursor != len(m.cards)-1 {
		t.Fatalf("подготовка: курсор = %d, ждали %d", m.cursor, len(m.cards)-1)
	}
	// Самый старый источник: его карточка переедет с самого верха в самый низ,
	// сдвинув всё между ними.
	moving := liveWallChat(0)

	next, _ := applyLiveUpdate(t, m, wallMessageUpdateMsg{
		valid:   true,
		chatID:  moving.ID,
		message: auth.Message{ID: 900, Date: 9000, Text: "переехало вниз"},
	})
	m = next

	last := len(m.cards) - 1
	if m.cursor != last {
		t.Fatalf("курсор = %d, ждали %d (новая последняя карточка)", m.cursor, last)
	}
	if m.cards[last].ChatID != moving.ID {
		t.Fatalf("под курсором источник %d, ждали обновившийся %d", m.cards[last].ChatID, moving.ID)
	}
	assertNewestCardFullyAtBottom(t, m, last)
	if got := wallRowOf(t, m, 0); got != -1 {
		t.Fatalf("окно уехало в начало потока: самая старая карточка на строке %d", got)
	}
	assertExactlyOneSelected(t, m)
}

// Курсор стоял на самой свежей карточке: новое сообщение уводит его на новое
// самое свежее, и оно стоит у НИЖНЕГО края стены. Не «где-то видно» — именно
// последняя отрисованная строка, тем же приёмом, что у
// TestFirstLoadPutsNewestCardAtBottom: прижимание к низу и есть то, за чем
// человек следит, открывая стену.
func TestLiveMessageScrollsToBottomWhenAtLatest(t *testing.T) {
	m := liveWallModel(t, scrollCardsCount)
	if m.cursor != len(m.cards)-1 {
		t.Fatalf("подготовка: курсор = %d, ждали %d", m.cursor, len(m.cards)-1)
	}

	next, _ := applyLiveUpdate(t, m, wallMessageUpdateMsg{
		valid:   true,
		chatID:  liveWallFreshChat().ID,
		message: auth.Message{ID: 500, Date: 5000, Text: "только что"},
	})
	m = next

	last := len(m.cards) - 1
	if m.cursor != last {
		t.Fatalf("после живого сообщения курсор = %d, ждали %d (новая свежая карточка)", m.cursor, last)
	}
	if m.cards[last].MessageID != 500 {
		t.Fatalf("под курсором карточка %d, ждали живое сообщение 500", m.cards[last].MessageID)
	}
	assertNewestCardFullyAtBottom(t, m, last)
	// Окно не уехало в самое начало: стена показывает конец, а не древний хвост.
	if got := wallRowOf(t, m, 0); got != -1 {
		t.Fatalf("самая старая карточка нарисована на строке %d, ждали её за пределами окна", got)
	}
	assertExactlyOneSelected(t, m)
}

// Найдено вживую человеком: Андрей пишет "привет" (последняя карточка, курсор
// на ней), человек начинает набирать ответ — и в этот момент приходит сообщение
// от СОВСЕМ другого источника (например, от учителя ребёнка). Без этой защиты
// курсор молча перескочил бы на новое сообщение, а набранный текст ушёл бы по
// Enter не туда, куда человек смотрел, когда начал печатать. Печатающий текст —
// это и есть "человек сейчас взаимодействует с этой карточкой", то же самое
// правило, что и "не убегать от читающего выше" в соседнем тесте.
func TestLiveMessageDoesNotStealCursorWhileComposingPlainText(t *testing.T) {
	m := liveWallModel(t, scrollCardsCount)
	if m.cursor != len(m.cards)-1 {
		t.Fatalf("подготовка: курсор = %d, ждали последнюю карточку %d", m.cursor, len(m.cards)-1)
	}
	before := selectedCard(t, m)
	_ = m.input.Focus()
	m = typeInWallField(t, m, "здорова")

	next, _ := applyLiveUpdate(t, m, wallMessageUpdateMsg{
		valid:   true,
		chatID:  liveWallFreshChat().ID,
		message: auth.Message{ID: 500, Date: 5000, Text: "от учителя"},
	})
	m = next

	after := selectedCard(t, m)
	if after.ChatID != before.ChatID || after.MessageID != before.MessageID {
		t.Fatalf("курсор уехал с (%d, %d) на (%d, %d), пока человек печатал ответ",
			before.ChatID, before.MessageID, after.ChatID, after.MessageID)
	}
	if got := m.input.Value(); got != "здорова" {
		t.Fatalf("набранный текст пострадал: %q, ждали %q", got, "здорова")
	}
	// Новое сообщение всё равно должно попасть на стену — оно просто не должно
	// красть курсор, а не пропасть вовсе.
	found := false
	for _, item := range m.cards {
		if item.MessageID == 500 {
			found = true
		}
	}
	if !found {
		t.Fatal("новое сообщение не появилось на стене вовсе")
	}
}

// Тот же сценарий, но человек уже нажал Ctrl+R (зафиксировал цель ответа), текст
// ещё не начал печатать — поле пустое. Само нажатие Ctrl+R уже выбор, и живое
// сообщение не должно его отменить сменой курсора.
func TestLiveMessageDoesNotStealCursorWithActiveReplyTarget(t *testing.T) {
	m := liveWallModel(t, scrollCardsCount)
	before := selectedCard(t, m)
	m = pressKey(t, m, ctrlRKey)
	if m.replyTarget == nil {
		t.Fatal("подготовка: Ctrl+R не выбрал цель ответа")
	}

	next, _ := applyLiveUpdate(t, m, wallMessageUpdateMsg{
		valid:   true,
		chatID:  liveWallFreshChat().ID,
		message: auth.Message{ID: 500, Date: 5000, Text: "от учителя"},
	})
	m = next

	after := selectedCard(t, m)
	if after.ChatID != before.ChatID || after.MessageID != before.MessageID {
		t.Fatalf("курсор уехал с (%d, %d) на (%d, %d), хотя цель ответа была зафиксирована",
			before.ChatID, before.MessageID, after.ChatID, after.MessageID)
	}
	if m.replyTarget == nil || m.replyTarget.ChatID != before.ChatID || m.replyTarget.MessageID != before.MessageID {
		t.Fatalf("цель ответа изменилась: %+v, ждали (%d, %d)", m.replyTarget, before.ChatID, before.MessageID)
	}
}

// Человек читает выше конца потока: новое сообщение уходит вниз молча, а курсор
// остаётся на ТОМ ЖЕ сообщении. Проверяется по паре (чат, id), а не по индексу:
// вставка выше курсора обязана сдвинуть его индекс, и по номеру проверка была
// бы фиктивной.
func TestLiveMessageKeepsSelectedCardWhenReadingAbove(t *testing.T) {
	m := liveWallModel(t, scrollCardsCount)
	m = m.moveCursor(-1)
	m = m.moveCursor(-1)
	before := selectedCard(t, m)
	beforeIndex := m.cursor
	if beforeIndex == len(m.cards)-1 {
		t.Fatalf("подготовка: курсор на последней карточке (%d)", beforeIndex)
	}
	// Сообщение с датой МЕЖДУ первой и второй карточками (даты 1..40) — вставка
	// строго выше курсора, сдвигающая все индексы под ним.
	next, _ := applyLiveUpdate(t, m, wallMessageUpdateMsg{
		valid:   true,
		chatID:  liveWallFreshChat().ID,
		message: auth.Message{ID: 900, Date: 1, Text: "пришло в середину"},
	})
	m = next

	after := selectedCard(t, m)
	if after.ChatID != before.ChatID || after.MessageID != before.MessageID {
		t.Fatalf("после вставки выбрана карточка (%d, %d), ждали ту же (%d, %d)",
			after.ChatID, after.MessageID, before.ChatID, before.MessageID)
	}
	if m.cursor == beforeIndex {
		t.Fatal("индекс курсора не сдвинулся после вставки выше — проверка по индексу ничего бы не значила")
	}
	if m.cursor == len(m.cards)-1 {
		t.Fatalf("курсор уехал на последнюю карточку (%d): человек читал не самый свежий конец, и стена не должна его перехватывать",
			m.cursor)
	}
	// Окно не должно уехать так, чтобы курсор пропал из кадра.
	if got := wallRowOf(t, m, m.cursor); got < 0 {
		t.Fatalf("после вставки выше курсор %d вне окна (scrollTop %d)", m.cursor, m.scrollTop)
	}
	assertExactlyOneSelected(t, m)
}

// Апдейт по чату, которого не было в снимке, молча игнорируется: ни одной
// карточки, ни сдвига курсора, ни сдвига окна. Живость списка чатов (чат,
// появившийся после старта) — отдельная задача, и молча тянуть сюда его
// обработку значило бы раздуть эту вдвое.
func TestLiveMessageFromUnknownChatChangesNothing(t *testing.T) {
	m := liveWallModel(t, scrollCardsCount)
	m = m.moveCursor(-1)
	cards, cursor, scrollTop := len(m.cards), m.cursor, m.scrollTop

	next, _ := applyLiveUpdate(t, m, wallMessageUpdateMsg{
		valid:   true,
		chatID:  liveWallFreshChat().ID + 1000,
		message: auth.Message{ID: 700, Date: 7000, Text: "из чужого чата"},
	})
	m = next

	if len(m.cards) != cards || m.cursor != cursor || m.scrollTop != scrollTop {
		t.Fatalf("апдейт по неизвестному чату изменил стену: карточек %d->%d, курсор %d->%d, окно %d->%d",
			cards, len(m.cards), cursor, m.cursor, scrollTop, m.scrollTop)
	}
}

// Апдейт по чату, которого в карте нет, но снимок ещё не пришёл (гонка на
// старте) — тот же случай, и он тоже обязан быть тихим: пустая карта в Go
// читается безопасно, специальной проверки «карта ещё не создана» не нужно.
func TestLiveMessageBeforeSnapshotIsDropped(t *testing.T) {
	m := New(context.Background(), nil, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	if m.chatsByID != nil && len(m.chatsByID) != 0 {
		t.Fatalf("до снимка карта чатов не должна быть заполнена: %v", m.chatsByID)
	}

	next, cmd := applyLiveUpdate(t, m, wallMessageUpdateMsg{
		valid:   true,
		chatID:  liveWallFreshChat().ID,
		message: auth.Message{ID: 700, Date: 7000, Text: "раннее"},
	})
	m = next
	if len(m.cards) != 0 {
		t.Fatalf("апдейт до снимка добавил карточек: %d", len(m.cards))
	}
	if cmd == nil {
		t.Error("после апдейта до снимка команда переподписки не вернулась")
	}
}

// Переподписка: нераспознанный апдейт — молча и с переподпиской, закрытый канал
// — без неё. Если бы переподписка была обязательной всегда, стена завесила бы
// мёртвой командой, ждущей канал, которого уже нет.
func TestLiveUpdateResubscribesUnlessChannelClosed(t *testing.T) {
	m := liveWallModel(t, 3)
	cards, cursor, scrollTop := len(m.cards), m.cursor, m.scrollTop

	next, cmd := applyLiveUpdate(t, m, wallMessageUpdateMsg{valid: false, chatID: liveWallFreshChat().ID})
	if cmd == nil {
		t.Error("на нераспознанный апдейт переподписка не вернулась — стена перестанет жить")
	}
	if len(next.cards) != cards || next.cursor != cursor || next.scrollTop != scrollTop {
		t.Errorf("нераспознанный апдейт изменил стену: карточек %d->%d, курсор %d->%d, окно %d->%d",
			cards, len(next.cards), cursor, next.cursor, scrollTop, next.scrollTop)
	}

	next, cmd = applyLiveUpdate(t, m, wallMessageUpdateMsg{closed: true})
	if cmd != nil {
		t.Error("на закрытый канал вернулась команда — ждать нечего, она зависнет")
	}
	if len(next.cards) != cards || next.cursor != cursor {
		t.Errorf("закрытый канал изменил стену: карточек %d->%d, курсор %d->%d",
			cards, len(next.cards), cursor, next.cursor)
	}
}

// Сам waitForWallMessageUpdate: читает апдейт из канала и заворачивает его в
// wallMessageUpdateMsg. Без отдельного теста проверялся бы только разбор уже
// готового сообщения, а на command-функцию можно было бы и не позвать вовсе.
func TestWaitForWallMessageUpdateParsesTDLibEvent(t *testing.T) {
	client := newWallLiveTestClient()
	client.updates <- wallLiveMessageUpdate(7, 42, 100, "новое")
	m := New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)

	raw := m.waitForWallMessageUpdate()()
	update, ok := raw.(wallMessageUpdateMsg)
	if !ok {
		t.Fatalf("ожидание вернуло %T, ждали wallMessageUpdateMsg", raw)
	}
	if update.closed || !update.valid || update.chatID != 7 || update.message.ID != 42 || update.message.Text != "новое" {
		t.Fatalf("разобранный апдейт = %+v", update)
	}
}

// Отменённый контекст закрывает ожидание: иначе команда из Init() висела бы
// вечно на выходе из программы.
func TestWaitForWallMessageUpdateStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := New(ctx, newWallLiveTestClient(), "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	cancel()

	raw := m.waitForWallMessageUpdate()()
	update, ok := raw.(wallMessageUpdateMsg)
	if !ok || !update.closed {
		t.Fatalf("ожидание по отменённому контексту = %#v, ждали closed", raw)
	}
}

// Клиента нет и канала апдейтов нет — оба случая обязаны отдавать closed сразу:
// ждать в канале, которого не будет никогда, значило бы зависнуть в Init().
func TestWaitForWallMessageUpdateWithoutClientOrChannel(t *testing.T) {
	assertClosed := func(name string, m Model) {
		t.Helper()
		update, ok := m.waitForWallMessageUpdate()().(wallMessageUpdateMsg)
		if !ok || !update.closed || update.valid {
			t.Fatalf("%s: ожидание = %#v, ждали closed", name, update)
		}
	}
	// wallLoadClient в MessageUpdates() отдаёт nil — так же ведёт себя клиент,
	// у которого поток апдейтов не подключён.
	assertClosed("клиент без канала апдейтов", New(context.Background(), newWallLoadClient(wallTestChats(), wallTestHistory()), "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion))
	assertClosed("клиент без клиента", New(context.Background(), nil, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion))
}

// Init() обязан запустить и загрузку снимка, и подписку на живые сообщения:
// без второго стена осталась бы разовым снимком, ради чего задача и открыта.
func TestInitSubscribesToLiveMessages(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	client := newWallLiveTestClient()
	m := New(ctx, client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	cancel()

	raw := m.Init()()
	batch, ok := raw.(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init вернул %T, ждали tea.BatchMsg", raw)
	}
	sawLoad, sawLive := false, false
	for _, cmd := range batch {
		if cmd == nil {
			continue
		}
		switch msg := cmd().(type) {
		case wallLoadedErrorMsg:
			sawLoad = true
		case wallMessageUpdateMsg:
			sawLive = msg.closed
		}
	}
	if !sawLoad {
		t.Error("Init не запустил загрузку снимка")
	}
	if !sawLive {
		t.Error("Init не запустил подписку на живые сообщения")
	}
}

// Состав чатов в снимке обязателен: без него живой апдейт не опознан и стена
// молчала бы вечно. Проверяется и смена состава: карта строится заново на
// каждом приходе, а не один раз на программу.
func TestSnapshotRebuildsChatIndex(t *testing.T) {
	m := liveWallModel(t, 1)
	if _, ok := m.chatsByID[liveWallChat(0).ID]; !ok {
		t.Fatalf("чат снимка не попал в карту: %v", m.chatsByID)
	}

	next, _ := m.Update(wallLoadedMsg{
		cards: wallCards(liveWallMessages(1)),
		chats: []auth.Chat{{ID: 55, Title: "Другой", Kind: auth.ChatChannel}},
	})
	m = next.(Model)
	if _, ok := m.chatsByID[55]; !ok {
		t.Fatalf("повторный снимок не обновил карту чатов: %v", m.chatsByID)
	}
	if _, ok := m.chatsByID[liveWallChat(0).ID]; ok {
		t.Fatalf("в карте остался чат ушедшего снимка: %v", m.chatsByID)
	}
}
