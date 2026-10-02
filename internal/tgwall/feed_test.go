package tgwall

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"telecli/internal/auth"
	"telecli/internal/config"
)

// wallLoadClient — мок TDLib для загрузки стены. Отвечает по правилам реального
// клиента: loadChats успешен, потом «Not Found» (конец списка), getChats отдаёт
// чаты, openChat — ok, getChatHistory — сообщения заданного чата. Отдельно умеет
// отдавать ошибку на getChatHistory заданного чата — этим проверяется деградация
// «один сбойный чат не роняет всю загрузку».
//
// Мок защищён мьютексом: загрузка стены идёт горутиной на чат, и без защиты
// append к общему срезу отработал бы как гонка под -race, а не как проверка.
type wallLoadClient struct {
	mu sync.Mutex
	// chats — чаты в порядке ответа getChats.
	chats []auth.Chat
	// history — сообщения по chat_id.
	history map[int64][]auth.Message
	// brokenChats — чаты, у которых getChatHistory падает.
	brokenChats map[int64]bool
	// openBrokenChats — чаты, у которых openChat падает (но история отдаётся).
	openBrokenChats map[int64]bool

	loadChatsCalls int
	getChatsCalls  int
	openChatCalls  int
	historyCalls   map[int64]int
	// historyLimits — с каким пределом запрашивалась история каждого чата. По
	// нему видно, сколько сообщений стена собирается показать на чат: сам ответ
	// обрезан по этому же пределу и ничего о намерениях не говорит.
	historyLimits map[int64]int
	// failGetChats — getChats отдаёт ошибку вместо списка.
	failGetChats bool
	// fullHistoryLimit — нижняя граница добивки ответа getChatHistory, когда она
	// должна быть ВЫШЕ запрошенного limit. Ноль — обычное состояние: добивка идёт
	// ровно до limit. См. fullHistory.
	fullHistoryLimit int
}

func newWallLoadClient(chats []auth.Chat, history map[int64][]auth.Message) *wallLoadClient {
	return &wallLoadClient{
		chats:           chats,
		history:         history,
		brokenChats:     map[int64]bool{},
		openBrokenChats: map[int64]bool{},
		historyCalls:    map[int64]int{},
		historyLimits:   map[int64]int{},
	}
}

func (c *wallLoadClient) Send(ctx context.Context, request map[string]interface{}) (map[string]interface{}, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	switch request["@type"] {
	case "loadChats":
		c.loadChatsCalls++
		// Первый вызов «успешен», второй — «Not Found»: так и ведёт себя TDLib на
		// живом аккаунте, и цикл догружки обязан на этом встать.
		if c.loadChatsCalls >= 2 {
			return nil, errors.New("Not Found")
		}
		return map[string]interface{}{"@type": "ok"}, nil
	case "getChats":
		c.getChatsCalls++
		if c.failGetChats {
			return nil, errors.New("Chat list is loading")
		}
		ids := make([]interface{}, 0, len(c.chats))
		for _, chat := range c.chats {
			ids = append(ids, float64(chat.ID))
		}
		return map[string]interface{}{"@type": "chats", "chat_ids": ids}, nil
	case "getChat":
		id, _ := request["chat_id"].(int64)
		for _, chat := range c.chats {
			if chat.ID == id {
				return map[string]interface{}{
					"@type": "chat",
					"id":    float64(chat.ID),
					"title": chat.Title,
					"type":  chatTypeFor(chat.Kind),
				}, nil
			}
		}
		return nil, errors.New("Chat not found")
	case "openChat":
		c.openChatCalls++
		id, _ := request["chat_id"].(int64)
		if c.openBrokenChats[id] {
			return nil, errors.New("Chat is not accessible")
		}
		return map[string]interface{}{"@type": "ok"}, nil
	case "getChatHistory":
		id, _ := request["chat_id"].(int64)
		c.historyCalls[id]++
		if c.brokenChats[id] {
			return nil, errors.New("Chat history is not available")
		}
		limit, _ := request["limit"].(int)
		c.historyLimits[id] = limit
		messages := c.fullHistory(id, limit)
		// TDLib отдаёт от новых к старым, разворот — на стороне GetMessages.
		raw := make([]interface{}, 0, len(messages))
		for index := len(messages) - 1; index >= 0; index-- {
			message := messages[index]
			raw = append(raw, map[string]interface{}{
				"@type":       "message",
				"id":          float64(message.ID),
				"chat_id":     float64(id),
				"date":        float64(message.Date),
				"is_outgoing": false,
				"sender_id":   map[string]interface{}{"@type": "messageSenderChat", "chat_id": float64(id)},
				"content": map[string]interface{}{
					"@type": "messageText",
					"text":  map[string]interface{}{"@type": "formattedText", "text": message.Text},
				},
			})
		}
		return map[string]interface{}{"@type": "messages", "messages": raw}, nil
	case "getUser":
		return map[string]interface{}{"@type": "user", "id": float64(1), "first_name": "Кто-то"}, nil
	}
	return nil, errors.New("unexpected request " + responseTypeOf(request))
}

func responseTypeOf(request map[string]interface{}) string {
	name, _ := request["@type"].(string)
	return name
}

// fillerDateBase — дата, с которой начинаются добитые сообщения. Больше любой даты
// из фикстур, чтобы добивка всегда оказывалась в КОНЦЕ отсортированного потока и не
// мешала проверять начало (то есть то, что задано фикстурой осмысленно).
const fillerDateBase = 90000

// fullHistory — история чата, обрезанная до запрошенного limit и добитая до него.
//
// Обрезка обязательна и повторяет реальное поведение TDLib: getChatHistory с
// limit = N отдаёт НЕ БОЛЕЕ N самых новых сообщений. Мок, отдающий всю историю
// чата сколько угодно, проверял бы не код, а себя — и после перехода стены на
// «одну карточку на источник» (limit = 1) напечатал бы на стене все сообщения
// чата, а ровно одно.
//
// Добавка нужна не для красоты: GetMessages повторяет getChatHistory, пока ответ
// короче limit (чат мог быть не досинхронизирован после openChat — см.
// historyRetryDelays в internal/auth), и каждый повтор стоит реальных задержек в
// сотни миллисекунд. Без добивки каждый тест загрузки стены платил бы за них по
// 2 секунды и суммировался в десятки секунд на пакет. Побочный эффект полезный:
// проверка «openChat ровно один раз на чат» становится строгой, а не «хотя бы раз».
func (c *wallLoadClient) fullHistory(chatID int64, limit int) []auth.Message {
	if limit <= 0 {
		return nil
	}
	// c.fullHistoryLimit — до какого предела добивать ответ: ноль (обычное
	// состояние) означает «до запрошенного клиентом limit». Тесты переписки
	// (задача 0155) задают его пределом зума: их истории короче него, и без добивки
	// каждый запрос повторялся бы три раза с реальными задержками в сотни
	// миллисекунд на каждый чат каждого теста.
	minimum := limit
	if c.fullHistoryLimit > minimum {
		minimum = c.fullHistoryLimit
	}
	messages := append([]auth.Message(nil), c.history[chatID]...)
	if len(messages) > minimum {
		// Последние minimum сообщений истории — самые новые (фикстуры
		// хронологические).
		messages = messages[len(messages)-minimum:]
	}
	for filler := minimum - len(messages); filler > 0; filler-- {
		messages = append(messages, auth.Message{
			ID:   int64(filler),
			Text: "добивка",
			Date: fillerDateBase + int64(limit-filler),
		})
	}
	return messages
}

func (c *wallLoadClient) Execute(request map[string]interface{}) (map[string]interface{}, error) {
	return nil, errors.New("not implemented")
}

// Остальные методы auth.TDClientInterface — заглушки: загрузка стены ходит только
// через Send, каналы апдейтов ей не нужны. Если у интерфейса появится новый
// метод, компилятор здесь об этом напомнит сам.
func (c *wallLoadClient) AuthUpdates() <-chan map[string]interface{} { return nil }
func (c *wallLoadClient) MessageUpdates() <-chan map[string]interface{} {
	return nil
}
func (c *wallLoadClient) SendStatusUpdates() <-chan map[string]interface{} { return nil }
func (c *wallLoadClient) ChatFolderUpdates() <-chan map[string]interface{} { return nil }
func (c *wallLoadClient) ChatReadInboxUpdates() <-chan map[string]interface{} {
	return nil
}
func (c *wallLoadClient) ChatReadOutboxUpdates() <-chan map[string]interface{} {
	return nil
}
func (c *wallLoadClient) ChatTitleUpdates() <-chan map[string]interface{} { return nil }
func (c *wallLoadClient) ChatNotificationSettingsUpdates() <-chan map[string]interface{} {
	return nil
}
func (c *wallLoadClient) UnreadCountUpdates() <-chan map[string]interface{} { return nil }
func (c *wallLoadClient) UnreadChatCountUpdates() <-chan map[string]interface{} {
	return nil
}
func (c *wallLoadClient) MessageInteractionUpdates() <-chan map[string]interface{} {
	return nil
}
func (c *wallLoadClient) UnreadReactionMessageUpdates() <-chan map[string]interface{} {
	return nil
}
func (c *wallLoadClient) DeleteMessagesUpdates() <-chan map[string]interface{} { return nil }
func (c *wallLoadClient) MessageContentUpdates() <-chan map[string]interface{} { return nil }
func (c *wallLoadClient) NewChatUpdates() <-chan map[string]interface{}        { return nil }
func (c *wallLoadClient) ChatAddedToListUpdates() <-chan map[string]interface{} {
	return nil
}
func (c *wallLoadClient) ChatRemovedFromListUpdates() <-chan map[string]interface{} {
	return nil
}
func (c *wallLoadClient) ChatPositionUpdates() <-chan map[string]interface{} { return nil }
func (c *wallLoadClient) ConnectionStateUpdates() <-chan map[string]interface{} {
	return nil
}
func (c *wallLoadClient) UserUpdates() <-chan map[string]interface{}             { return nil }
func (c *wallLoadClient) GroupMemberCountUpdates() <-chan map[string]interface{} { return nil }
func (c *wallLoadClient) UserStatusUpdates() <-chan map[string]interface{}       { return nil }
func (c *wallLoadClient) Close()                                                 {}

func (c *wallLoadClient) calls() (loadChats, getChats, openChat int, history map[int64]int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	history = make(map[int64]int, len(c.historyCalls))
	for id, count := range c.historyCalls {
		history[id] = count
	}
	return c.loadChatsCalls, c.getChatsCalls, c.openChatCalls, history
}

func chatTypeFor(kind auth.ChatKind) map[string]interface{} {
	switch kind {
	case auth.ChatChannel:
		return map[string]interface{}{"@type": "chatTypeSupergroup", "is_channel": true}
	case auth.ChatGroup:
		return map[string]interface{}{"@type": "chatTypeSupergroup"}
	default:
		return map[string]interface{}{"@type": "chatTypePrivate"}
	}
}

// fillerText — текст добитых сообщений (см. fullHistory). Отбрасывается перед
// проверками, где важны заданные фикстурой сообщения, но не там, где проверяется
// сам факт «стена показала переписку».
const fillerText = "добивка"

// realCards — карточки без добивки.
func realCards(cards []card) []card {
	filtered := make([]card, 0, len(cards))
	for _, item := range cards {
		if item.Text == fillerText {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

// wallTestChats — три чата всех типов для проверки маппинга и сортировки.
func wallTestChats() []auth.Chat {
	return []auth.Chat{
		{ID: 1, Title: "Новости DevOps", Kind: auth.ChatChannel},
		{ID: 2, Title: "Соседи по подъезду", Kind: auth.ChatGroup},
		{ID: 3, Title: "Андрей", Kind: auth.ChatPrivate},
	}
}

func wallTestHistory() map[int64][]auth.Message {
	return map[int64][]auth.Message{
		1: {
			{ID: 11, Text: "канал, 13:04", Date: 1304},
			{ID: 12, Text: "канал, 13:09", Date: 1309},
		},
		2: {
			{ID: 21, Text: "чат, 13:05", Date: 1305},
		},
		3: {
			{ID: 31, Text: "личное, 13:07", Date: 1307},
		},
	}
}

// TestWallCardMapsChatAndMessageToCard — маппинг пары (чат, сообщение) в карточку
// для всех трёх типов. Проверяются и тип, и тег, и заголовок, и время, и текст:
// это единственное место, где живые данные TDLib превращаются в то, что видно на
// экране, и ошибка здесь тихо исказила бы всю стену.
func TestWallCardMapsChatAndMessageToCard(t *testing.T) {
	cases := []struct {
		name      string
		chat      auth.Chat
		message   auth.Message
		wantType  cardType
		wantTag   string
		wantTitle string
	}{
		{
			name:      "канал",
			chat:      auth.Chat{Title: "Новости DevOps", Kind: auth.ChatChannel},
			message:   auth.Message{Text: "пост", Date: 1304},
			wantType:  cardChannel,
			wantTag:   "#канал",
			wantTitle: "Новости DevOps",
		},
		{
			name:      "канал с подписью автора",
			chat:      auth.Chat{Title: "Новости DevOps", Kind: auth.ChatChannel},
			message:   auth.Message{Text: "пост", Date: 1304, AuthorSignature: "Дмитрий"},
			wantType:  cardChannel,
			wantTag:   "#канал",
			wantTitle: "Новости DevOps — Дмитрий",
		},
		{
			name:      "чат",
			chat:      auth.Chat{Title: "Соседи по подъезду", Kind: auth.ChatGroup},
			message:   auth.Message{Text: "во дворе перекопали", SenderName: "Марина", Date: 1305},
			wantType:  cardChat,
			wantTag:   "#чат",
			wantTitle: "Соседи по подъезду — Марина",
		},
		{
			name:      "личное",
			chat:      auth.Chat{Title: "Андрей", Kind: auth.ChatPrivate},
			message:   auth.Message{Text: "го в субботу", SenderName: "Андрей", Date: 1307},
			wantType:  cardPersonal,
			wantTag:   "#личное",
			wantTitle: "Андрей",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := wallCard(testCase.chat, testCase.message)
			if got.Type != testCase.wantType {
				t.Errorf("Type = %v, ждали %v", got.Type, testCase.wantType)
			}
			if got.Tag != testCase.wantTag {
				t.Errorf("Tag = %q, ждали %q", got.Tag, testCase.wantTag)
			}
			if got.Name != testCase.chat.Title {
				t.Errorf("Name = %q, ждали %q", got.Name, testCase.chat.Title)
			}
			if title := got.title(); title != testCase.wantTitle {
				t.Errorf("заголовок = %q, ждали %q", title, testCase.wantTitle)
			}
			if got.Text != testCase.message.Text {
				t.Errorf("Text = %q, ждали %q", got.Text, testCase.message.Text)
			}
			if got.Time != auth.FormatMessageTime(testCase.message.Date) {
				t.Errorf("Time = %q, ждали %q", got.Time, auth.FormatMessageTime(testCase.message.Date))
			}
			// Подпись автора в заголовке читается только у канала, но в карточке
			// хранится всегда: у личного и чата она в заголовок не идёт (см. title),
			// но потерять её на разборе значило бы потом не показать.
			if got.AuthorSignature != testCase.message.AuthorSignature {
				t.Errorf("AuthorSignature = %q, ждали %q", got.AuthorSignature, testCase.message.AuthorSignature)
			}
		})
	}
}

// TestWallCardKeepsParagraphsButRowNeverHasRawNewline — сообщения
// ботов-отчётов (замечено на живом аккаунте: "PAWTouch Admin Bot") приходят от
// TDLib с настоящими символами переноса строки внутри ОДНОГО message.Text.
// card.Text эти переносы СОХРАНЯЕТ: структура абзацев нужна экрану одного чата
// (задача 0155), где полный текст читается целиком. А на стене карточка —
// всегда РОВНО две строки renderCard, и текст сообщения лежит на одной из них
// (второй) ровно одной строкой: сырой перенос внутри неё терминал напечатал бы
// буквально, и высота карточки на экране разошлась бы с тем, что посчитал
// wallCardHeight — карточки съезжают, а следующая рисуется поверх хвоста
// предыдущей (тот самый баг, что уже однажды ловили). См. collapsedText в
// wall.go — там и живёт эта защита.
//
// Раньше тест дополнительно проверял, что РАЗВЁРНУТАЯ карточка показывает абзацы
// отдельными строками экрана. Такого поведения на стене больше нет (задача 0154):
// читать целиком — на экране одного чата.
func TestWallCardKeepsParagraphsButRowNeverHasRawNewline(t *testing.T) {
	chat := auth.Chat{Title: "PAWTouch Admin Bot", Kind: auth.ChatPrivate}
	message := auth.Message{Text: "Версия X-UI: 3.8.5\nХост: 0104f0783202\nIPv4: 172.18.0.2", Date: 1630}

	got := wallCard(chat, message)
	want := "Версия X-UI: 3.8.5\nХост: 0104f0783202\nIPv4: 172.18.0.2"
	if got.Text != want {
		t.Fatalf("Text = %q, ждали %q (структура абзацев обязана сохраниться)", got.Text, want)
	}

	// Обе строки — выбранная и невыбранная — обязаны быть без сырого переноса:
	// раньше расходились именно они. Строк теперь две (задача 0156), текст
	// сообщения — на второй из них, но перенос внутри строки по-прежнему
	// недопустим: терминал напечатал бы его буквально, и высота карточки на
	// экране разошлась бы с тем, что посчитал wallCardHeight.
	for _, selected := range []bool{false, true} {
		rows := renderCard(got, 120, selected)
		if len(rows) != 2 {
			t.Fatalf("выбрана=%v: карточка должна быть ровно 2 строками экрана, получили %d: %q", selected, len(rows), rows)
		}
		for index, row := range rows {
			if strings.Contains(row, "\n") {
				t.Fatalf("выбрана=%v: отрисованная строка %d содержит сырой перенос строки: %q", selected, index, row)
			}
		}
	}

	// Текст сообщения — на строке 2 (индекс 1), на ней он обязан показать первую
	// строку отчёта и знак «не поместилось».
	plain := ansi.Strip(renderCard(got, 120, false)[1])
	if !strings.Contains(plain, "Версия X-UI: 3.8.5") || !strings.Contains(plain, "…") {
		t.Fatalf("строка обязана показать первую строку сообщения и знак «не поместилось»: %q", plain)
	}
	// Знака «есть продолжение» на стене больше нет (задача 0156) — иначе он
	// вернулся бы вместе с этой правкой: он жил ровно на этом сценарии. Глиф
	// задан литералом, а не константой: константы больше нет, и её возврат
	// должен быть отдельным решением, а не молчаливым совпадением имён.
	if strings.Contains(plain, "▶") {
		t.Fatalf("в карточке снова появился знак «есть продолжение» %q: %q", "▶", plain)
	}
}

// TestWallCardMarksOutgoingMessage — собственное сообщение помечается «Вы»,
// как в Telegram/WhatsApp/iMessage: у чата (там есть отдельный слот автора в
// заголовке) заменяется сам автор, у канала и личного (там такого слота нет)
// — перед текстом встаёт префикс «Вы: ». Найдено человеком вживую: «непонятно,
// что я писал, что мне писали».
func TestWallCardMarksOutgoingMessage(t *testing.T) {
	cases := []struct {
		name       string
		chat       auth.Chat
		wantAuthor string
		wantText   string
	}{
		{
			name:       "чат — автор в заголовке становится «Вы»",
			chat:       auth.Chat{Title: "Рабочий чат", Kind: auth.ChatGroup},
			wantAuthor: "Вы",
			wantText:   "го в субботу",
		},
		{
			name:       "личное — префикс перед текстом",
			chat:       auth.Chat{Title: "Андрей", Kind: auth.ChatPrivate},
			wantAuthor: "Игорь",
			wantText:   "Вы: го в субботу",
		},
		{
			name:       "канал — префикс перед текстом",
			chat:       auth.Chat{Title: "Новости DevOps", Kind: auth.ChatChannel},
			wantAuthor: "Игорь",
			wantText:   "Вы: го в субботу",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			message := auth.Message{Text: "го в субботу", SenderName: "Игорь", IsOutgoing: true, Date: 1}
			got := wallCard(testCase.chat, message)
			if got.Author != testCase.wantAuthor {
				t.Errorf("Author = %q, ждали %q", got.Author, testCase.wantAuthor)
			}
			if got.Text != testCase.wantText {
				t.Errorf("Text = %q, ждали %q", got.Text, testCase.wantText)
			}
		})
	}
}

// Входящее сообщение (IsOutgoing == false) не получает никакой метки — иначе
// «Вы» появлялось бы у чужих сообщений тоже.
func TestWallCardDoesNotMarkIncomingMessage(t *testing.T) {
	chat := auth.Chat{Title: "Соседи по подъезду", Kind: auth.ChatGroup}
	message := auth.Message{Text: "во дворе перекопали", SenderName: "Марина", Date: 1}
	got := wallCard(chat, message)
	if got.Author != "Марина" {
		t.Fatalf("Author = %q, ждали настоящее имя отправителя", got.Author)
	}
	if got.Text != "во дворе перекопали" {
		t.Fatalf("Text = %q, метка «Вы» не должна появляться у чужого сообщения", got.Text)
	}
}

// Сообщение-ответ помечается знаком wallReplyMarker и короткой цитатой
// оригинала прямо в тексте карточки — иначе на стене никак не видно, что это
// ответ, а не обычное сообщение (человек заметил это вживую, 2026-09-30).
// Метка ставится ПЕРЕД «Вы:», если сообщение к тому же своё: обе метки не
// исключают друг друга.
func TestWallCardMarksReplyWithQuote(t *testing.T) {
	chat := auth.Chat{Title: "Андрей", Kind: auth.ChatPrivate}
	message := auth.Message{
		Text: "го в 11", SenderName: "Андрей", Date: 1,
		ReplyToMessageID: 10, ReplyToChatID: 3, ReplyQuote: "го в субботу",
	}
	got := wallCard(chat, message)
	want := wallReplyMarker + " го в субботу: го в 11"
	if got.Text != want {
		t.Fatalf("Text = %q, ждали %q", got.Text, want)
	}
}

// TestWallMergesChatsSortedByDate — сообщения разных чатов попадают в один поток
// вперемешку и по дате, а не по чату: стена показывает переписку вперемешку, и
// сортировка по чатам читалась бы как «список чатов», а не как лента.
//
// Отдельно проверяется, что на чат приходится ровно одна карточка: у канала в
// фикстуре ДВА сообщения (13:04 и 13:09), и на стене обязано быть только
// последнее — иначе после перехода стены на «одну карточку на источник» старые
// сообщения по-прежнему сыпались бы в поток, просто медленнее.
func TestWallMergesChatsSortedByDate(t *testing.T) {
	client := newWallLoadClient(wallTestChats(), wallTestHistory())
	m := New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)

	msg, ok := m.wallLoadedCmd()().(wallLoadedMsg)
	if !ok {
		t.Fatal("команда загрузки вернула не wallLoadedMsg")
	}
	if len(msg.failedChatIDs) != 0 {
		t.Fatalf("сбойных чатов %v, ждали ни одного", msg.failedChatIDs)
	}

	// Проверяется весь поток: по одному сообщению каждого из трёх чатов, по дате,
	// а добивки (см. fullHistory) на стене вовсе нет — снимок берёт ровно
	// wallSnapshotMessagesPerChat сообщений на чат.
	wantTexts := []string{"чат, 13:05", "личное, 13:07", "канал, 13:09"}
	if len(msg.cards) != len(wantTexts) {
		t.Fatalf("карточек %d, ждали ровно %d (по одной на чат): %+v", len(msg.cards), len(wantTexts), msg.cards)
	}
	for index, want := range wantTexts {
		if msg.cards[index].Text != want {
			t.Fatalf("карточка %d = %q, ждали %q (порядок должен быть по дате)", index, msg.cards[index].Text, want)
		}
	}
	// Старое сообщение канала на стене быть не должно вовсе.
	for _, item := range msg.cards {
		if item.Text == "канал, 13:04" {
			t.Fatal("на стене больше одной карточки на источник: канал показал и 13:04, и 13:09")
		}
	}
	// Типы и теги по чату: после смешивания потока каждая карточка обязана помнить,
	// из какого чата она пришла.
	wantTags := []string{"#чат", "#личное", "#канал"}
	for index, want := range wantTags {
		if msg.cards[index].Tag != want {
			t.Fatalf("тег карточки %d = %q, ждали %q", index, msg.cards[index].Tag, want)
		}
	}
}

// TestWallSurvivesOneBrokenChat — один сбойный чат не роняет всю загрузку: остальные
// карточки доезжают, сбойный помечается в failedChatIDs. Стена из трёх пустых
// строк из-за одного недоступного чата — это деградация, которую человек сразу
// прощения бы не отдал.
func TestWallSurvivesOneBrokenChat(t *testing.T) {
	chats := wallTestChats()
	client := newWallLoadClient(chats, wallTestHistory())
	client.brokenChats[2] = true

	m := New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	msg, ok := m.wallLoadedCmd()().(wallLoadedMsg)
	if !ok {
		t.Fatal("команда загрузки вернула не wallLoadedMsg")
	}
	if len(msg.failedChatIDs) != 1 || msg.failedChatIDs[0] != 2 {
		t.Fatalf("сбойные чаты = %v, ждали [2]", msg.failedChatIDs)
	}
	// Каналы и личный доехали, чата 2 в потоке нет.
	got := map[string]bool{}
	for _, item := range realCards(msg.cards) {
		got[item.Text] = true
	}
	for _, want := range []string{"канал, 13:09", "личное, 13:07"} {
		if !got[want] {
			t.Errorf("карточка %q потеряна из-за сбоя соседнего чата", want)
		}
	}
	for _, item := range realCards(msg.cards) {
		if item.Text == "чат, 13:05" {
			t.Error("карточка сбойного чата попала в стену")
		}
	}
}

// Снимок берёт РОВНО одно сообщение на чат: чат с историей в десять сообщений
// даёт на стене одну карточку — его последнее сообщение, а не десять. Это ровно
// то, ради чего стена перешла на «одну карточку на источник» (задача 0152), и
// проверяется здесь на настоящей загрузке с TDLib-мо��ком, а не на вызове
// wallCards: иначе тест прошёл бы и при неверном пределе в запросе истории.
func TestWallTakesOnlyLastMessagePerChat(t *testing.T) {
	chats := []auth.Chat{
		{ID: 1, Title: "Много сообщений", Kind: auth.ChatChannel},
		{ID: 2, Title: "Одно сообщение", Kind: auth.ChatGroup},
		{ID: 3, Title: "Пустой чат", Kind: auth.ChatPrivate},
	}
	history := map[int64][]auth.Message{
		1: {
			{ID: 11, Text: "старое", Date: 100},
			{ID: 12, Text: "ещё старше", Date: 200},
			{ID: 13, Text: "последнее", Date: 300},
		},
		2: {{ID: 21, Text: "единственное", Date: 150}},
		// 3 — чат без единого сообщения.
	}
	client := newWallLoadClient(chats, history)
	m := New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)

	msg, ok := m.wallLoadedCmd()().(wallLoadedMsg)
	if !ok {
		t.Fatal("команда загрузки вернула не wallLoadedMsg")
	}

	// Добивка (см. fullHistory) — артефакт мока, а не TDLib: она нужна, чтобы
	// GetMessages не повторял запрос из-за ответа короче предела. Поэтому карточки
	// считаются без неё, иначе проверялось бы поведение мока, а не стены.
	cards := realCards(msg.cards)

	// Только два чата с историей: у каждого по одной карточке, обе — последнее
	// сообщение своего чата. Проверяется именно по содержимому: и «одна на чат»,
	// и «это последняя, а не первая попавшаяся».
	if len(cards) != 2 {
		t.Fatalf("карточек %d, ждали 2 (по одной на чат с историей): %+v", len(cards), cards)
	}
	byText := map[string]card{}
	for _, item := range cards {
		if count := wallCountOfChat(cards, item.ChatID); count != 1 {
			t.Fatalf("карточек источника %d на стене %d, ждали 1", item.ChatID, count)
		}
		byText[item.Text] = item
	}
	if got, ok := byText["последнее"]; !ok || got.MessageID != 13 {
		t.Fatalf("карточки чата с историей = %+v, ждали его последнее сообщение (13, «последнее»)", cards)
	}
	if _, ok := byText["старое"]; ok {
		t.Fatal("на стене осталось старое сообщение чата: снимок берёт больше одного сообщения на чат")
	}
	if got, ok := byText["единственное"]; !ok || got.ChatID != 2 {
		t.Fatalf("карточка чата с одним сообщением потеряна: %+v", cards)
	}
	// Чат без единого сообщения карточки не даёт — как и до перехода стены на
	// одну карточку на источник. Отдельного решения тут не принималось, поведение
	// просто сохранено.
	if count := wallCountOfChat(cards, 3); count != 0 {
		t.Fatalf("у пустого чата на стене %d карточек, ждали 0", count)
	}

	// Проверяется и сам предел в запросе: стена просит у TDLib ровно столько
	// последних сообщений, сколько собирается показать. Мок обрезает историю до
	// limit, как это делает настоящий TDLib, поэтому проверка выше сама по себе
	// «одна карточка на чат» доказать не могла бы — предел мог бы быть любым, а
	// обрезка всё равно оставила бы одну карточку.
	for _, chat := range chats {
		if got := client.historyLimits[chat.ID]; got != wallSnapshotMessagesPerChat {
			t.Errorf("история чата %d запрошена с пределом %d, ждали %d",
				chat.ID, got, wallSnapshotMessagesPerChat)
		}
	}
}

// openChat, не ответивший, историю не отменяет: getChatHistory после него вполне
// может ответить, и тогда чат вправе попасть на стену. Помечать его сбойным
// означало бы молча выкидывать переписку из-за предупреждения TDLib.
func TestWallKeepsChatWhoseOpenChatFailed(t *testing.T) {
	chats := wallTestChats()
	client := newWallLoadClient(chats, wallTestHistory())
	client.openBrokenChats[1] = true

	m := New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	msg, ok := m.wallLoadedCmd()().(wallLoadedMsg)
	if !ok {
		t.Fatal("команда загрузки вернула не wallLoadedMsg")
	}
	if len(msg.failedChatIDs) != 0 {
		t.Fatalf("сбой openChat сделал чат сбойным: %v", msg.failedChatIDs)
	}
	// Все чаты на месте, включая тот, чей openChat не ответил: по одной карточке
	// на каждый.
	if got := realCards(msg.cards); len(got) != len(chats) {
		t.Fatalf("карточек %d, ждали %d: сбой openChat не должен убирать переписку (%v)", len(got), len(chats), got)
	}
}

// Сортировка устойчива: при равных датах порядок карточек не «прыгает» между
// прогонами из-за того, что горутины отработали в разном порядке. Без этого стена
// переставляла бы сообщения одной и той же минуты при каждой перезагрузке.
func TestWallKeepsChatOrderForEqualDates(t *testing.T) {
	chats := []auth.Chat{
		{ID: 1, Title: "Первый", Kind: auth.ChatPrivate},
		{ID: 2, Title: "Второй", Kind: auth.ChatPrivate},
	}
	history := map[int64][]auth.Message{
		1: {{ID: 1, Text: "первый чат", Date: 500}},
		2: {{ID: 2, Text: "второй чат", Date: 500}},
	}
	client := newWallLoadClient(chats, history)

	firstRun := realCards(New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion).wallLoadedCmd()().(wallLoadedMsg).cards)
	if len(firstRun) != 2 {
		t.Fatalf("заданных сообщений %d, ждали 2", len(firstRun))
	}
	if firstRun[0].Text != "первый чат" || firstRun[1].Text != "второй чат" {
		t.Fatalf("при равных датах порядок = %q, %q; ждали порядок чатов",
			firstRun[0].Text, firstRun[1].Text)
	}
	// Пять прогонов подряд: горутины отрабатывают в разном порядке, и при
	// неустойчивой сортировке карточки скакали бы между прогонами — ровно тот
	// «прыгающий» поток, который человек заметил бы при каждом перезапуске.
	for range 5 {
		got := realCards(New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion).wallLoadedCmd()().(wallLoadedMsg).cards)
		if len(got) != len(firstRun) {
			t.Fatalf("прогон вернул %d карточек, ждали %d", len(got), len(firstRun))
		}
		for index := range got {
			if got[index].Text != firstRun[index].Text {
				t.Fatalf("порядок при равных датах нестабилен: карточка %d = %q, ждали %q",
					index, got[index].Text, firstRun[index].Text)
			}
		}
	}
}

// Без клиента команда обязана сообщить об этом, а не вернуть пустую стену молча:
// тихая пустота неотличима от «у аккаунта нет чатов».
func TestWallReportsNilClient(t *testing.T) {
	m := New(context.Background(), nil, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	msg, ok := m.wallLoadedCmd()().(wallLoadedErrorMsg)
	if !ok {
		t.Fatal("команда с nil-клиентом вернула не wallLoadedErrorMsg")
	}
	if msg.err == nil {
		t.Fatal("ошибка загрузки без клиента пустая")
	}
}

// Сбой загрузки списка чатов доходит до интерфейса отдельным типом: это не «часть
// чатов не показана», а «не показано ничего», и стена обязана остановить спиннер.
func TestWallReportsChatListFailure(t *testing.T) {
	client := newWallLoadClient(wallTestChats(), wallTestHistory())
	client.failGetChats = true

	m := New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	if _, ok := m.wallLoadedCmd()().(wallLoadedErrorMsg); !ok {
		t.Fatal("сбой getChats вернулся не как wallLoadedErrorMsg")
	}
}

// Каждый чат открывается ровно один раз, и история каждого запрашивается хотя бы
// один раз: openChat на каждый чат дважды съедал бы лимит TDLib на сотне чатов.
func TestWallAsksEachChatOnce(t *testing.T) {
	chats := wallTestChats()
	client := newWallLoadClient(chats, wallTestHistory())

	m := New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	m.wallLoadedCmd()()

	loadChats, getChats, openChat, history := client.calls()
	if loadChats < 2 {
		t.Errorf("loadChats вызван %d раз, ждали догружку циклом", loadChats)
	}
	if getChats != 1 {
		t.Errorf("getChats вызван %d раз, ждали 1", getChats)
	}
	if openChat != len(chats) {
		t.Errorf("openChat вызван %d раз, ждали по одному на чат (%d)", openChat, len(chats))
	}
	for _, chat := range chats {
		// «Хотя бы один раз», а не «ровно один»: GetMessages сама повторяет
		// getChatHistory, если ответ короче запрошенного (чат мог быть ещё не
		// досинхронизирован после openChat — см. historyRetryDelays в
		// internal/auth). Это существующее поведение общей функции, повторов на
		// стороне стены тут не добавляется, и запрещать их здесь нельзя.
		if history[chat.ID] == 0 {
			t.Errorf("история чата %d не запрошена вовсе", chat.ID)
		}
	}
}
