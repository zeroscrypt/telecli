package tgwall

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/auth"
	"telecli/internal/config"
)

// Экран одного источника (задача 0155): переписка, открываемая по Enter на узком
// терминале и всегда показываемая справа на широком.
//
// Проверки идут через настоящий Update и настоящую отрисовку везде, где важен
// именно путь человека: неотличимое от «всё прошло хорошо» поведение обычно
// живёт в недостающем перехвате клавиши или в том, что команда загрузки не
// доехала.

// zoomChats и zoomHistory — три источника всех типов с различимой перепиской.
// Ключи — chat_id, значения — сообщения в хронологическом порядке, ровно так их
// отдаёт auth.GetMessages.
var zoomChats = []auth.Chat{
	{ID: 1, Title: "Новости DevOps", Kind: auth.ChatChannel},
	{ID: 2, Title: "Соседи по подъезду", Kind: auth.ChatGroup},
	{ID: 3, Title: "Андрей", Kind: auth.ChatPrivate},
}

var zoomHistory = map[int64][]auth.Message{
	1: {
		{ID: 11, Text: "релиз собран", Date: 1304},
		{ID: 12, Text: "проверьте логи", Date: 1309},
	},
	2: {
		{ID: 21, Text: "во дворе опять перекопали", Date: 1305},
		{ID: 22, Text: "машину не поставить", Date: 1310},
		{ID: 23, Text: "напишите в личку", Date: 1311},
	},
	3: {
		{ID: 31, Text: "го в субботу на футбол", Date: 1307},
	},
}

// zoomClient — мок TDLib для переписки: getChatHistory отдаёт историю чата
// (добивая до предела, см. wallLoadClient.fullHistory — иначе GetMessages
// повторял бы запрос с растущими задержками), openChat и getUser — успех.
type zoomClient struct {
	*wallLoadClient
	// historyCalls — сколько раз запрашивалась история по каждому чату. Считает
	// и ПОВТОРЫ одного и того же чата: переоткрытие под курсором обязано
	// спрашивать заново, а при смене чата не должно спрашивать старый.
	historyLimits map[int64]int
	broken        map[int64]bool
}

func newZoomClient() *zoomClient {
	client := &zoomClient{
		wallLoadClient: newWallLoadClient(zoomChats, zoomHistory),
		historyLimits:  map[int64]int{},
		broken:         map[int64]bool{},
	}
	// История чатов в моке короткая, а предел переписки — 50: без добивки до него
	// GetMessages повторял бы запрос три раза с реальными задержками в сотни
	// миллисекунд на каждый чат каждого теста.
	client.wallLoadClient.fullHistoryLimit = wallZoomMessagesLimit
	return client
}

func (c *zoomClient) Send(ctx context.Context, request map[string]interface{}) (map[string]interface{}, error) {
	if request["@type"] == "getChatHistory" {
		id, _ := request["chat_id"].(int64)
		c.wallLoadClient.mu.Lock()
		c.historyLimits[id] = request["limit"].(int)
		c.wallLoadClient.mu.Unlock()
		if c.broken[id] {
			return nil, errors.New("Chat history is not available")
		}
	}
	return c.wallLoadClient.Send(ctx, request)
}

func (c *zoomClient) askedHistory(chatID int64) (int, bool) {
	c.wallLoadClient.mu.Lock()
	defer c.wallLoadClient.mu.Unlock()
	limit, ok := c.historyLimits[chatID]
	return limit, ok
}

// zoomModel — загруженная стена под размер терминала с живым моком. Порядок
// тот же, что в программе: сперва размер терминала, потом снимок стены.
func zoomModel(t *testing.T, client auth.TDClientInterface, width, height int) Model {
	t.Helper()
	return zoomModelWith(t, client, width, height)
}

// zoomModelWith — то же самое; отдельное имя нужно, когда модель строится не на
// zoomClient, а на другом моке (например, на моке отправки).
func zoomModelWith(t *testing.T, client auth.TDClientInterface, width, height int) Model {
	t.Helper()
	m := New(context.Background(), client, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	next, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	m = next.(Model)
	next, _ = m.Update(wallLoadedMsg{cards: zoomWallCards(), chats: zoomChats})
	m = next.(Model)
	if m.input == nil {
		t.Fatal("поле ввода не создано")
	}
	_ = m.input.Focus()
	return m
}

// zoomWallCards — карточки стены из той же фикстуры, что и переписка, чтобы
// источник под курсором и открытая переписка были про один и тот же чат.
//
// Порядок карточек задан здесь, а не полагается на сортировку: wallCards
// сохраняет порядок переданных сообщений (сортирует их loadWallMessages на
// загрузке стены, а эта фикстура идёт в модель напрямую). Последняя карточка —
// личный диалог, и курсор после загрузки встаёт на неё.
func zoomWallCards() []card {
	return wallCards([]wallMessage{
		{Chat: zoomChats[0], Message: zoomHistory[1][1]},
		{Chat: zoomChats[1], Message: zoomHistory[2][2]},
		{Chat: zoomChats[2], Message: zoomHistory[3][0]},
	})
}

// cardIndexOfChat — индекс карточки источника на стене, -1 если её нет.
func cardIndexOfChat(m Model, chatID int64) int {
	for index, item := range m.cards {
		if item.ChatID == chatID {
			return index
		}
	}
	return -1
}

// pressZoomKey — нажатие через Update, как это делает программа.
func pressZoomKey(t *testing.T, m Model, msg tea.KeyMsg) (Model, tea.Cmd) {
	t.Helper()
	next, cmd := m.Update(msg)
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("Update вернул %T, ждали Model", next)
	}
	return updated, cmd
}

func keyUp() tea.KeyPressMsg   { return tea.KeyPressMsg{Code: tea.KeyUp} }
func keyDown() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyDown} }
func keyTab() tea.KeyPressMsg  { return tea.KeyPressMsg{Code: tea.KeyTab} }
func keyEsc() tea.KeyPressMsg  { return tea.KeyPressMsg{Code: tea.KeyEscape} }
func keyEnter() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyEnter}
}
func keyShiftTab() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
}
func keyCtrlR() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}
}

// zoneLines — строки зоны стены (то, что между верхними барами и строкой
// сообщений) без ANSI. Зона стены одна на оба хелпера, и этот заведён был раньше:
// проверки широкого режима берут зону через него, проверки прокрутки — через
// wallLines.
func zoneLines(t *testing.T, m Model) []string {
	t.Helper()
	return wallLines(t, m)
}

// runZoomLoad — выполнить команду загрузки переписки и прогнать её через
// Update. Без этого тест проверял бы намерение модели, а не применившийся ответ.
func runZoomLoad(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		t.Fatal("ожидалась команда загрузки переписки, а её нет")
	}
	msg, ok := cmd().(wallZoomLoadedMsg)
	if !ok {
		t.Fatalf("команда вернула %T, ждали wallZoomLoadedMsg", cmd())
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

// Проверка 1 задачи: на узком терминале Enter с пустым полем открывает
// переписку источника под курсором на всю ширину, а история грузится тем же
// запросом и тем же пределом, что у зума чата ленты tgcli.
func TestNarrowEnterOpensZoomOfCardUnderCursor(t *testing.T) {
	client := newZoomClient()
	m := zoomModel(t, client, 60, 30)
	if m.zoom != nil {
		t.Fatal("на узком терминале переписка не должна быть открыта до Enter")
	}

	// Курсор — на последней карточке (чаты отсортированы по дате), то есть на
	// личном диалоге Андрея.
	want := m.cards[m.cursor].ChatID
	if want != 3 {
		t.Fatalf("под курсором чат %d, в фикстуре последний — 3 (Андрей)", want)
	}

	opened, cmd := pressZoomKey(t, m, keyEnter())
	if opened.zoom == nil {
		t.Fatal("Enter с пустым полем не открыл переписку")
	}
	if opened.zoom.chatID != want {
		t.Fatalf("открыт чат %d, ждали %d (под курсором)", opened.zoom.chatID, want)
	}
	if !opened.zoom.loading {
		t.Fatal("переписка открыта, но loading снят до прихода ответа")
	}
	// Предел истории — ровно тот же, что у зума ленты (wallZoomMessagesLimit).
	opened = runZoomLoad(t, opened, cmd)
	if limit, ok := client.askedHistory(3); !ok || limit != wallZoomMessagesLimit {
		t.Fatalf("история запрошена с пределом %d (запрошена: %v), ждали %d",
			limit, ok, wallZoomMessagesLimit)
	}
	// Мок добивает историю до wallZoomMessagesLimit, как это делает TDLib на живом
	// аккаунте, поэтому здесь ровно 50 сообщений — и именно столько показывает
	// экран: обрезки по этому пределу не происходит, и она не должна была.
	if got, limit := len(opened.zoom.messages), wallZoomMessagesLimit; got != limit {
		t.Fatalf("в переписке %d сообщений, ждали %d", got, limit)
	}
	// Настоящее сообщение фикстуры среди них есть (добивка мока имеет более поздние
	// даты и стоит в хвосте, поэтому проверяется наличие, а не позиция).
	if !containsMessageText(opened.zoom.messages, "го в субботу на футбол") {
		t.Fatalf("в переписке нет сообщения фикстуры, первые: %v", zoomMessageTexts(opened)[:2])
	}
	// Курсор после загрузки — на последнем сообщении: переписку открывают, чтобы
	// прочитать свежее.
	if got, want := opened.zoom.cursor, len(opened.zoom.messages)-1; got != want {
		t.Fatalf("курсор переписки %d, ждали %d (хвост)", got, want)
	}
	zone := strings.Join(zoneLines(t, opened), "\n")
	// Стена на узком терминале уступает экран переписке целиком: карточек в зоне
	// быть не должно вовсе.
	//
	// Проверяются заголовки карточек ДРУГИХ источников: заголовок открытого чата
	// в зоне есть — в шапке переписки, — и его наличие ничего не говорит о стене.
	for _, item := range opened.cards {
		if item.ChatID != opened.zoom.chatID && strings.Contains(zone, item.title()) {
			t.Fatalf("на экране осталась карточка стены %q:\n%s", item.title(), zone)
		}
	}
	// Шапка называет источник, и переписка нарисована под ней.
	if !strings.Contains(zone, opened.zoom.title) {
		t.Fatalf("в зоне нет шапки переписки %q:\n%s", opened.zoom.title, zone)
	}
	// Вся переписка (50 сообщений) в зону не помещается, поэтому в кадре видны
	// последние — те, что ближе к низу. Наличие переписки проверяется по
	// последнему видимому сообщению, а не по заведомо ушедшему за экран.
	last := opened.zoom.messages[len(opened.zoom.messages)-1]
	if !strings.Contains(zone, last.Text) {
		t.Fatalf("в зоне нет последнего сообщения переписки %q:\n%s", last.Text, zone)
	}
}

// Проверка 2 задачи: на широком терминале переписка под курсором открыта СРАЗУ
// после загрузки стены, без всякого Enter, и панель занимает всё, кроме левой
// колонки шириной ровно 30.
func TestWideTerminalOpensZoomRightAfterWallLoad(t *testing.T) {
	client := newZoomClient()
	m := zoomModel(t, client, 100, 30)
	if m.zoom == nil {
		t.Fatal("на широком терминале переписка должна открыться сразу после загрузки стены")
	}
	if m.zoom.chatID != m.cards[m.cursor].ChatID {
		t.Fatalf("открыт чат %d, а под курсором %d", m.zoom.chatID, m.cards[m.cursor].ChatID)
	}
	// Ширины СОДЕРЖИМОГО панелей: каждая на две ячейки уже своей панели, потому
	// что по краям стоит контур (задача 0167). Полные размеры панелей — это они
	// же плюс контур.
	if got, want := m.wallColumnWidth(), wallPanelColumnWidth-wallPanelOutlineInset; got != want {
		t.Fatalf("ширина содержимого колонки стены %d, ждали %d", got, want)
	}
	// Поле справа от панели (wallPanelInsetRight) в формуле обязательно: рамка панели
	// не должна стоять вплотную к краю терминала (задача человека, 2026-10-01).
	if got, want := m.zoomListWidth(), 100-wallPanelColumnWidth-wallPanelGapWidth-wallPanelInsetRight-wallPanelOutlineInset; got != want {
		t.Fatalf("ширина содержимого панели переписки %d, ждали %d", got, want)
	}
	// Колонки в сумме дают ширину терминала, и зазор между ними — одна ячейка:
	// контур перераспределяет уже отведённое содержимому, а не занимает новое.
	zone := zoneLines(t, m)
	if got := cellWidth(zone[0]); got != 100 {
		t.Fatalf("строка зоны шириной %d, ждали 100", got)
	}
	// Панель на месте, и открытая переписка называет себя: в широком режиме не
	// шапкой внутри панели, а верхней строкой экрана (см. zoomHeaderRows) — там
	// она стоит с колонки, с которой начинается сама панель.
	row := splitLines(m.renderScreen())[0]
	if !strings.Contains(ansi.Strip(row), zoomChats[2].Title) {
		t.Fatalf("в верхней строке нет названия открытого источника: %q", ansi.Strip(row))
	}
	if got := strings.TrimSpace(ansi.Strip(ansi.TruncateLeft(ansi.Strip(row), topBarTitleStart, ""))); got != zoomChats[2].Title {
		t.Fatalf("название открытого источника встало с не с той колонки: %q", got)
	}
	// Шапки внутри панели нет: её строка отдана списку сообщений, и первая строка
	// содержимого панели — верхний контур, а вторая уже сообщение, а не название.
	if strings.Contains(zone[1], zoomChats[2].Title) {
		t.Fatalf("в панели осталась шапка с названием источника: %q", zone[1])
	}
}

// Проверка 3 задачи (первая половина): движение курсора стены в широком режиме
// переоткрывает переписку на новый источник.
func TestWideCursorMoveReopensZoomOfNewSource(t *testing.T) {
	client := newZoomClient()
	m := zoomModel(t, client, 100, 30)

	up, cmd := pressZoomKey(t, m, keyUp())
	if up.zoom == nil || up.zoom.chatID == m.zoom.chatID {
		t.Fatalf("↑ не открыл переписку нового источника (было %v)", m.zoom.chatID)
	}
	if up.zoom.chatID != up.cards[up.cursor].ChatID {
		t.Fatalf("открыт чат %d, под курсором %d", up.zoom.chatID, up.cards[up.cursor].ChatID)
	}
	up = runZoomLoad(t, up, cmd)
	if got := zoomMessageTexts(up); len(got) == 0 {
		t.Fatal("переписка нового источника загрузилась пустой")
	}
}

// Проверка 3 задачи (вторая половина): гонка. Быстрое перемещение курсора через
// несколько карточек оставляет в полёте несколько ответов, и примениться должен
// только последний — устаревший ответ не того источника обязан быть отброшен.
//
// Проверяется и сверка по чату, и сверка по номеру попытки: первый тест поймал
// бы только первую, второй — только вторую.
func TestWideStaleZoomResponseIsDiscarded(t *testing.T) {
	client := newZoomClient()
	m := zoomModel(t, client, 100, 30)

	// Запрос на первый источник под курсором.
	first := m.wallZoomLoadedCmd(m.zoom.panel, m.zoom.chatID, m.zoom.loadID)
	// Курсор уходит на другой источник, тот грузится тоже.
	moved, second := pressZoomKey(t, m, keyUp())
	if moved.zoom.chatID == m.zoom.chatID {
		t.Fatal("тест бессмысленен: переписка не сменилась")
	}
	moved = runZoomLoad(t, moved, second)

	// Ответ ПЕРВОГО запроса приходит последним — так и бывает в жизни, когда
	// первый чат отвечает медленнее второго.
	stale, ok := first().(wallZoomLoadedMsg)
	if !ok {
		t.Fatalf("команда вернула не wallZoomLoadedMsg")
	}
	next, _ := moved.Update(stale)
	after := next.(Model)
	if after.zoom.chatID != moved.zoom.chatID {
		t.Fatalf("устаревший ответ сменил источник на %d, ждали %d", after.zoom.chatID, moved.zoom.chatID)
	}
	if got := zoomMessageTexts(after); len(got) == 0 {
		t.Fatal("устаревший ответ подчистил переписку")
	}
	// Тот же источник, но устаревшая попытка — сверка обязана быть и по loadID,
	// иначе ответ предыдущего открытия того же чата применился бы поверх.
	repeat, _ := m.openWallZoom(m.zoom.chatID, m.zoom.title)
	if repeat.zoom.loadID == m.zoom.loadID {
		t.Fatal("номер попытки не вырос при повторном открытии того же чата")
	}
	next, _ = repeat.Update(wallZoomLoadedMsg{chatID: m.zoom.chatID, loadID: m.zoom.loadID, messages: nil})
	if loaded := next.(Model); !loaded.zoom.loading {
		t.Fatal("ответ с устаревшим номером попытки применился поверх текущей загрузки")
	}
}

// В широком режиме у ленты колонка стены узкая, и время последнего сообщения у
// КАЖДОЙ карточки убрано: переписка открыта справа целиком, время там есть, а в
// тесной колонке оно только мешает читать заголовки (решение человека,
// 2026-10-01). Превью текста скрыто только у карточки под курсором — там оно
// заменяло бы собой открытую справа переписку, у остальных карточек текст остаётся.
func TestWideHidesTimeOnEveryCardAndPreviewUnderCursor(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 100, 30)
	m = m.moveCursor(0)

	for index, item := range m.cards {
		underCursor := index == m.cursor
		// Проверяется ровно то, что увидит человек, — колонка стены из ЗОНЫ
		// ЭКРАНА, а не прямой вызов карточки. Проверка прямым вызовом прошла бы
		// и на том коде, где флаг задан, но на экран попадает обычная карточка:
		// ровно такой дефект и был найден на живой отрисовке.
		zone := zoneLines(t, m)
		// Карточки стены начинаются не с первой строки зоны: первую занимает верх
		// контура панели (задача 0167), а нумерация самих карточек (две строки
		// плюс зазор) от этого не меняется.
		headline, text := ansi.Strip(zone[index*3+1]), ansi.Strip(zone[index*3+2])
		if underCursor {
			// Время с задачи 0163 живёт в конце строки ТЕКСТА, поэтому и
			// скрываться оно должно там же, а не на строке заголовка.
			if strings.Contains(text, item.Time) {
				t.Fatalf("у карточки под курсором видно время %q: %q", item.Time, text)
			}
			if strings.Contains(headline, item.Time) {
				t.Fatalf("время %q на строке заголовка (оно и раньше было не здесь): %q", item.Time, headline)
			}
			// Строка 2 у карточки под курсором остаётся на месте, но текста в ней
			// нет: проверяется именно СОДЕРЖИМОЕ, а не пустота строки целиком —
			// строка несёт левую границу и фон выделения, и без неё карточка
			// потеряла бы высоту (а с ней и геометрию окна прокрутки).
			if strings.Contains(text, item.Text) {
				t.Fatalf("у карточки под курсором виден текст сообщения: %q", text)
			}
			// Заголовок при этом остаётся: ради него карточка и нужна.
			if !strings.Contains(headline, namePrefix(item.Name)) {
				t.Fatalf("у карточки под курсором пропал заголовок: %q", headline)
			}
			continue
		}
		if strings.Contains(text, item.Time) {
			t.Fatalf("в широком режиме у карточки %q осталось время %q: %q", item.Name, item.Time, text)
		}
		if !strings.Contains(text, item.Text) {
			t.Fatalf("у невыделенной карточки %q пропал текст: %q", item.Name, text)
		}
	}
	// На узком терминале скрытия нет вовсе: там переписки рядом нет, и время с
	// превью последнего сообщения остаётся единственным, что о чате известно.
	narrow := zoomModel(t, newZoomClient(), 60, 30)
	block := narrow.renderWallCardBlock(narrow.cards[narrow.cursor], narrow.width, true)
	if !strings.Contains(ansi.Strip(block[1]), narrow.cards[narrow.cursor].Text) {
		t.Fatal("на узком терминале текст карточки под курсором пропал зря")
	}
	if !strings.Contains(ansi.Strip(block[1]), narrow.cards[narrow.cursor].Time) {
		t.Fatal("на узком терминале время карточки пропало зря")
	}
}

// В узкой колонке рядом с панелью переписки акцентный цвет источника (левая
// граница ┃) остаётся только у ВЫДЕЛЕННОЙ карточки — десяток разноцветных
// линий подряд в тесной колонке пестрит (человек заметил вживую, 2026-09-30).
// На обычной стене (узкий терминал, панели нет) цвет остаётся у каждой карточки
// как и раньше — этот тест его не трогает.
//
// Цвет проверяется в СВОЕЙ колонке карточки (cardMarginH), а не «где-то в строке»:
// с задачей 0167 в той же строке зоны стоит левая граница контура панели, и
// проверка «в строке есть такая последовательность» удовлетворялась бы ею, а не
// границей карточки, — то есть тест стал бы слепым ровно к тому дефекту, который
// охраняет.
func TestPanelColumnBorderIsMutedExceptSelected(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 100, 30)
	raw := wallZoneLines(t, m, splitLines(m.renderScreen()))
	for index, item := range m.cards {
		selected := index == m.cursor
		// +1 — верх контура панели занимает первую строку зоны (задача 0167).
		headline := raw[index*3+1]
		// Граница карточки — в колонке cardMarginH от начала ЕЁ СОБСТВЕННОЙ строки,
		// а строка карточки в широком режиме начинается на ячейку правее: первую
		// занимает левая сторона контура панели.
		column := 1 + cardMarginH
		// Невыделенная — фон вместо PaletteBorder (человек попросил убрать
		// серые полоски у всего списка, 2026-09-30): граница невидима, а не
		// просто приглушена.
		wantColor, wantBg := PaletteBackgroundMain, PaletteBackgroundMain
		if selected {
			wantColor, wantBg = item.accent(), PaletteBackgroundSelected
		}
		if got := cellAt(t, ansi.Strip(headline), column); got != "┃" {
			t.Fatalf("карточка %q (выбрана=%v): в колонке %d = %q, ждена левая граница",
				item.Name, selected, column, got)
		}
		want := foregroundSGR(wantColor, wantBg) + "┃"
		// Строка режется по началу клетки границы, а не по её концу: срез по
		// концу заново открывает SGR-последовательность в своей, и побайтовое
		// сравнение с тем, что вёрстка писала в строке, ничего бы не значило.
		// Ложного совпадения в таком срезе нет: клеток-вертикалей в нём ровно
		// две — левая сторона контура панели (её цвет другой: контур красится
		// акцентом типа открытого чата, а не цветом выделения карточки) и сама
		// граница.
		if got := ansi.Cut(headline, 0, column+1); !strings.Contains(got, want) {
			t.Fatalf("карточка %q (выбрана=%v): граница нарисована не ожидаемым цветом: %q, ждали содержащую %q",
				item.Name, selected, got, want)
		}
	}
}

// Проверка 5 задачи: Tab в широком режиме переключает фокус список/чат, и
// стрелки двигают то, что сейчас в фокусе. Курсор стены с переоткрытием переписки
// — на списке; курсор переписки — без.
func TestWideTabSwitchesFocusAndArrowsFollowIt(t *testing.T) {
	client := newZoomClient()
	m := zoomModel(t, client, 100, 30)
	// Переписка загружена той же командой, что её открыла: тест про фокус, а не
	// про загрузку (она проверена отдельно).
	m = runZoomLoad(t, m, m.wallZoomLoadedCmd(m.zoom.panel, m.zoom.chatID, m.zoom.loadID))
	wallCursor, zoomChat := m.cursor, m.zoom.chatID

	if m.focusPanel.isChatPanel() {
		t.Fatal("в широком режиме фокус по умолчанию на списке, а не на переписке")
	}
	// Tab → фокус на переписке.
	focused, _ := pressZoomKey(t, m, keyTab())
	if !focused.focusPanel.isChatPanel() || !focused.zoomHasFocus() {
		t.Fatal("Tab не перевёл фокус на переписку")
	}
	// Стрелка при фокусе на переписке двигает курсор СООБЩЕНИЙ, а не стены.
	moved, _ := pressZoomKey(t, focused, keyUp())
	if moved.cursor != wallCursor {
		t.Fatalf("↑ при фокусе на переписке сдвинул курсор стены на %d, ждали %d", moved.cursor, wallCursor)
	}
	if moved.zoom.cursor == 0 && len(m.zoom.messages) > 0 {
		t.Fatalf("↑ при фокусе на переписке не сдвинул курсор сообщений (был 0, остался %d)", moved.zoom.cursor)
	}
	if moved.zoom.chatID != zoomChat {
		t.Fatalf("↑ при фокусе на переписке сменил источник на %d, ждали %d", moved.zoom.chatID, zoomChat)
	}
	// Tab → фокус обратно на списке, и та же стрелка уже двигает стену (с
	// переоткрытием переписки под новым источником).
	back, _ := pressZoomKey(t, moved, keyTab())
	if back.focusPanel.isChatPanel() {
		t.Fatal("второй Tab не вернул фокус на список")
	}
	after, _ := pressZoomKey(t, back, keyUp())
	if after.cursor == back.cursor {
		t.Fatal("↑ при фокусе на списке не сдвинул курсор стены")
	}
	if after.zoom.chatID != after.cards[after.cursor].ChatID {
		t.Fatalf("переписка не переехала за курсором: чат %d, под курсором %d",
			after.zoom.chatID, after.cards[after.cursor].ChatID)
	}
}

// Tab в узком режиме не перехватывается: переписка там единственный экран, и
// отдельного фокуса не существует.
func TestNarrowTabIsNotHijacked(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 60, 30)
	opened, _ := pressZoomKey(t, m, keyEnter())
	opened = runZoomLoad(t, opened, opened.wallZoomLoadedCmd(opened.zoom.panel, opened.zoom.chatID, opened.zoom.loadID))
	after, _ := pressZoomKey(t, opened, keyTab())
	if !after.zoomHasFocus() {
		t.Fatal("в узком режиме фокус обязан остаться на переписке")
	}
	if after.focusPanel.isChatPanel() {
		t.Fatal("в узком режиме отдельного фокуса быть не должно, Tab его не переключает")
	}
}

// Проверка 6 задачи: узкий режим. Esc при активной цели ответа гасит только её;
// следующий Esc закрывает переписку, и курсор с окном стены при этом не меняются.
func TestNarrowEscCancelsReplyThenClosesZoom(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 60, 30)
	opened, _ := pressZoomKey(t, m, keyEnter())
	opened = runZoomLoad(t, opened, opened.wallZoomLoadedCmd(opened.zoom.panel, opened.zoom.chatID, opened.zoom.loadID))

	replied, _ := pressZoomKey(t, opened, keyCtrlR())
	if replied.replyTarget == nil {
		t.Fatal("Ctrl+R не выбрал цель ответа в переписке")
	}
	wallCursor, wallTop := replied.cursor, replied.scrollTop

	// Первая ступень: гасится только цель ответа, переписка остаётся.
	afterReply, _ := pressZoomKey(t, replied, keyEsc())
	if afterReply.replyTarget != nil {
		t.Fatal("Esc не снял цель ответа")
	}
	if afterReply.zoom == nil {
		t.Fatal("Esc снял цель ответа и заодно закрыл переписку")
	}

	// Вторая ступень: переписка закрывается, стена возвращается как была.
	closed, _ := pressZoomKey(t, afterReply, keyEsc())
	if closed.zoom != nil {
		t.Fatal("Esc не закрыл переписку")
	}
	if closed.cursor != wallCursor || closed.scrollTop != wallTop {
		t.Fatalf("закрытие переписки сдвинуло стену: курсор %d (был %d), окно %d (было %d)",
			closed.cursor, wallCursor, closed.scrollTop, wallTop)
	}
	if !strings.Contains(strings.Join(zoneLines(t, closed), "\n"), closed.cards[closed.cursor].Name) {
		t.Fatal("после закрытия переписки стена не вернулась")
	}
	// Третья ступень на стене без переписки: ничего не происходит, стена цела.
	idle, _ := pressZoomKey(t, closed, keyEsc())
	if idle.cursor != closed.cursor || len(idle.cards) != len(closed.cards) {
		t.Fatal("Esc на стене без переписки что-то сломал")
	}
}

// Проверка 7 задачи: широкий режим. Esc при фокусе на переписке и без цели ответа
// возвращает фокус на список, а сама переписка остаётся открытой.
func TestWideEscReturnsFocusToListWithoutClosingZoom(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 100, 30)
	focused, _ := pressZoomKey(t, m, keyTab())
	if !focused.focusPanel.isChatPanel() {
		t.Fatal("Tab не перевёл фокус на переписку")
	}
	after, _ := pressZoomKey(t, focused, keyEsc())
	if after.focusPanel.isChatPanel() {
		t.Fatal("Esc не вернул фокус на список")
	}
	if after.zoom == nil {
		t.Fatal("Esc закрыл переписку — в широком режиме ей закрываться некуда")
	}
	// Дальше Esc на списке — уже ничего (вне переписки на широком экране).
	idle, _ := pressZoomKey(t, after, keyEsc())
	if idle.focusPanel.isChatPanel() || idle.zoom == nil {
		t.Fatal("Esc на списке в широком режиме изменил состояние")
	}
}

// Проверка 8 задачи: живое сообщение в открытый чат появляется в переписке, и
// курсор ведёт себя по правилу «был в хвосте — остался, читал историю — не
// выдёрнуло».
func TestLiveMessageLandsInOpenZoom(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		step       int
		wantAtTail bool
	}{
		{"был в хвосте — остался в хвосте", 0, true},
		{"читает историю — остался на месте", -1, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			m := zoomModel(t, newZoomClient(), 60, 30)
			m = m.moveCursor(-1) // на чат 2
			if m.cards[m.cursor].ChatID != 2 {
				t.Fatalf("под курсором чат %d, ждали 2", m.cards[m.cursor].ChatID)
			}
			opened, cmd := pressZoomKey(t, m, keyEnter())
			opened = runZoomLoad(t, opened, cmd)
			// Длина переписки запоминается ДО вставки: история чата в моке добивается
			// до предела зума, а проверяется ровно прирост от живого сообщения.
			count := len(opened.zoom.messages)
			if count == 0 {
				t.Fatal("переписка загрузилась пустой, проверять вставку нечего")
			}
			opened.moveWallZoomCursor(testCase.step)
			cursorBefore := opened.zoom.cursor

			// id заведомо выше любого из фикстурных и добивочных: у добивки мока
			// свои id из 1..limit, и с ними живого сообщения не отличить от
			// повторной доставки уже показанного (её глотает дедупликация).
			live := auth.Message{ID: 9001, Text: "новое сообщение", Date: 999999}
			next, _ := opened.Update(wallMessageUpdateMsg{chatID: 2, message: live, valid: true})
			after := next.(Model)
			if len(after.zoom.messages) != count+1 {
				t.Fatalf("после живого сообщения в переписке %d сообщений, ждали %d", len(after.zoom.messages), count+1)
			}
			if got := after.zoom.messages[len(after.zoom.messages)-1].Text; got != "новое сообщение" {
				t.Fatalf("последнее сообщение переписки = %q", got)
			}
			if testCase.wantAtTail && after.zoom.cursor != count {
				t.Fatalf("курсор %d, ждали хвост %d", after.zoom.cursor, count)
			}
			if !testCase.wantAtTail && after.zoom.cursor != cursorBefore {
				t.Fatalf("курсор уехал с %d на %d, хотя человек читал историю", cursorBefore, after.zoom.cursor)
			}
			// Карточка стены обновилась одновременно — обе части экрана живые.
			if got := after.cards[after.cursor].MessageID; got != live.ID {
				t.Fatalf("карточка стены показывает сообщение %d, ждали %d", got, live.ID)
			}
		})
	}
}

// Проверка 9 задачи: живое сообщение в ДРУГОЙ чат, пока открыт зум чата X, —
// карточка стены этого источника обновляется как обычно, переписка X не
// меняется.
func TestLiveMessageOfOtherChatLeavesZoomUntouched(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 60, 30)
	m = m.moveCursor(-1) // на чат 2
	opened, cmd := pressZoomKey(t, m, keyEnter())
	opened = runZoomLoad(t, opened, cmd)
	if opened.zoom.chatID != 2 {
		t.Fatalf("открыт чат %d, ждали 2", opened.zoom.chatID)
	}
	before := append([]auth.Message(nil), opened.zoom.messages...)

	// id заведомо не пересекается с историей чата 2: у неё в моке есть добивочные
	// сообщения с id из 1..limit, и с ними живое сообщение не отличить от
	// повторной доставки уже показанного — его глотала бы дедупликация по id.
	next, _ := opened.Update(wallMessageUpdateMsg{
		chatID:  3,
		valid:   true,
		message: auth.Message{ID: 9101, Text: "привет", Date: 999999},
	})
	after := next.(Model)
	if len(after.zoom.messages) != len(before) {
		t.Fatalf("переписку чата 2 затронуло сообщение из чата 3: %d сообщений вместо %d",
			len(after.zoom.messages), len(before))
	}
	for index := range before {
		if after.zoom.messages[index].ID != before[index].ID {
			t.Fatal("состав переписки чата 2 изменился из-за сообщения из чата 3")
		}
	}
	// Карточка чата 3 обновилась: её ChatID и раньше был на стене.
	updated := false
	for _, item := range after.cards {
		if item.ChatID == 3 && item.MessageID == 9101 {
			updated = true
		}
	}
	if !updated {
		t.Fatal("карточка стены чата 3 не обновилась живым сообщением")
	}
}

// Проверка 10 задачи: Enter с текстом при фокусе на переписке отправляет в чат
// переписки, а НЕ в источник карточки под курсором стены.
//
// Именно расхождение и проверяется: на широком терминале после смены фокуса
// карточка под курсором и открытая переписка — разные источники, и отправка по
// инерции ушла бы не туда. Список отправок снимается с мока, а не с модели.
func TestSendGoesToZoomChatNotWallCard(t *testing.T) {
	// Отправка проверяется на настоящем моке отправки (wallSendClient), а не на
	// моке истории: он снимает список ушедших в Telegram запросов, и по нему видно
	// адресата. История при этом добивается до предела зума, иначе загрузка
	// переписки повторяла бы запрос с задержками.
	base := newWallLoadClient(zoomChats, zoomHistory)
	base.fullHistoryLimit = wallZoomMessagesLimit
	sender := &wallSendClient{wallLoadClient: base, sentMessageID: 900, sentMessageDate: 9000}
	// Ширина 60 — узкий режим: переписка занимает весь экран и всегда в фокусе.
	m := zoomModelWith(t, sender, 60, 30)

	// Узкий терминал: переписка чата 3 открыта по Enter, и фокус на ней всегда.
	//
	// Расхождение «переписка ≠ карточка под курсором» достигается НЕ стрелками
	// (в узком режиме они листают переписку), а живым апдейтом: пришедшее сообщение
	// чата 2 переставляет его карточку вниз, и курсор стены, стоявший в хвосте,
	// уезжает на неё. Переписка при этом остаётся чата 3 — так оно и в жизни.
	m = m.moveCursor(2) // на карточку чата 3
	m, openCmd := pressZoomKey(t, m, keyEnter())
	m = runZoomLoad(t, m, openCmd)
	if m.zoom.chatID != 3 {
		t.Fatalf("открыт чат %d, ждали 3", m.zoom.chatID)
	}
	next, _ := m.Update(wallMessageUpdateMsg{
		chatID:  2,
		valid:   true,
		message: auth.Message{ID: 99, Text: "новое", Date: 9999},
	})
	m = next.(Model)
	zoomChat, wallChat := m.zoom.chatID, m.cards[m.cursor].ChatID
	if zoomChat == wallChat {
		t.Fatalf("тест бессмысленен: переписка %d совпала с карточкой %d", zoomChat, wallChat)
	}

	for _, symbol := range "привет" {
		m, _ = pressZoomKey(t, m, tea.KeyPressMsg{Code: symbol, Text: string(symbol)})
	}
	// Команда отправки исполняется по-настоящему: адресат виден только в том, что
	// реально ушло в мок, а модель на это ещё не отвечала.
	m, cmd := pressZoomKey(t, m, keyEnter())
	if cmd == nil {
		t.Fatal("Enter с текстом не отправил сообщение")
	}
	if msg, ok := cmd().(wallSendMessageMsg); !ok {
		t.Fatalf("команда отправки вернула %T", cmd())
	} else if msg.chatID != zoomChat {
		t.Fatalf("команда отправляет в чат %d, ждали чат переписки %d", msg.chatID, zoomChat)
	}
	next, _ = m.Update(wallSendMessageMsg{
		chatID:     zoomChat,
		inputValue: "привет",
		message:    auth.Message{ID: 901, Text: "привет", Date: 999999, IsOutgoing: true},
	})
	m = next.(Model)
	sent := sender.sentRequests()
	if len(sent) != 1 {
		t.Fatalf("отправок %d, ждали 1", len(sent))
	}
	if sent[0].chatID != zoomChat {
		t.Fatalf("сообщение ушло в чат %d, ждали чат переписки %d (под курсором стены был %d)", sent[0].chatID, zoomChat, wallChat)
	}
	if sent[0].text != "привет" {
		t.Fatalf("отправлен текст %q", sent[0].text)
	}
	if sent[0].hasReplyTo {
		t.Fatal("обычная отправка не должна нести reply_to")
	}
	// Возврат фокуса на список переводит отправку обратно на карточку стены: иначе
	// «смотрю в переписку» и «пишу в карточку под курсором» были бы неразличимы.
	back, _ := pressZoomKey(t, m, keyEsc())
	if back.zoom != nil {
		t.Fatal("Esc в узком режиме должен был закрыть переписку")
	}
	for _, symbol := range "пока" {
		back, _ = pressZoomKey(t, back, tea.KeyPressMsg{Code: symbol, Text: string(symbol)})
	}
	_, second := pressZoomKey(t, back, keyEnter())
	if second == nil {
		t.Fatal("Enter с текстом вне переписки не отправил сообщение")
	}
	if msg, ok := second().(wallSendMessageMsg); !ok {
		t.Fatalf("вторая команда отправки вернула %T", second())
	} else if msg.chatID != back.cards[back.cursor].ChatID {
		t.Fatalf("вторая отправка идёт в чат %d, ждали чат карточки под курсором %d",
			msg.chatID, back.cards[back.cursor].ChatID)
	}
}

// Проверка 11 задачи: Ctrl+R при фокусе на переписке берёт цель ответа из
// сообщения под курсором переписки, а не из карточки стены.
func TestReplyTargetComesFromZoomMessage(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 60, 30)
	m = m.moveCursor(-1) // на чат 2
	if m.cards[m.cursor].ChatID != 2 {
		t.Fatalf("под курсором чат %d, ждали 2", m.cards[m.cursor].ChatID)
	}
	opened, cmd := pressZoomKey(t, m, keyEnter())
	opened = runZoomLoad(t, opened, cmd)

	// Курсор на первое сообщение переписки — не то, что показывает карточка
	// стены (последнее сообщение чата).
	opened.moveWallZoomCursor(-2)
	want := opened.zoom.messages[opened.zoom.cursor]
	if want.ID == opened.cards[opened.cursor].MessageID {
		t.Fatalf("тест бессмысленен: сообщение %d совпало с карточкой стены", want.ID)
	}
	replied, _ := pressZoomKey(t, opened, keyCtrlR())
	if replied.replyTarget == nil {
		t.Fatal("Ctrl+R не выбрал цель ответа")
	}
	if replied.replyTarget.MessageID != want.ID {
		t.Fatalf("цель ответа — сообщение %d, ждали %d (из переписки)", replied.replyTarget.MessageID, want.ID)
	}
	if replied.replyTarget.ChatID != opened.zoom.chatID {
		t.Fatalf("цель ответа — чат %d, ждали %d", replied.replyTarget.ChatID, opened.zoom.chatID)
	}
	// Строка контекста ответа называет источник и кусок текста ВЫБРАННОГО
	// сообщения, а не карточки стены (иначе человек отвечает «не туда»).
	if !strings.Contains(replied.replyTarget.Label, zoomChats[1].Title) {
		t.Fatalf("в строке ответа нет названия источника %q: %q", zoomChats[1].Title, replied.replyTarget.Label)
	}
	if !strings.Contains(replied.replyTarget.Label, want.Text) {
		t.Fatalf("в строке ответа нет текста сообщения %q: %q", want.Text, replied.replyTarget.Label)
	}

	// И ответ уходит именно туда — с reply_to на выбранное сообщение.
	for _, symbol := range "ок" {
		replied, _ = pressZoomKey(t, replied, tea.KeyPressMsg{Code: symbol, Text: string(symbol)})
	}
	if replied.replyTarget == nil {
		t.Fatal("цель ответа пропала до отправки")
	}
	_, cmd = pressZoomKey(t, replied, keyEnter())
	if cmd == nil {
		t.Fatal("Enter с ответом не отправил сообщение")
	}
	sent, ok := cmd().(wallSendMessageMsg)
	if !ok {
		t.Fatalf("команда отправки вернула %T", cmd())
	}
	if sent.chatID != opened.zoom.chatID || sent.replyToMessageID != want.ID {
		t.Fatalf("ответ ушёл в чат %d на сообщение %d, ждали чат %d на сообщение %d",
			sent.chatID, sent.replyToMessageID, opened.zoom.chatID, want.ID)
	}
	// Цель ответа снимается ответом сети (applyWallSendMessage), а не самим Enter:
	// пока ответа нет, человек может передумать и снять её Esc. Прогоняем
	// ответ — и проверяем, что цель погашена.
	next, _ := replied.Update(wallSendMessageMsg{
		chatID:           sent.chatID,
		replyToMessageID: sent.replyToMessageID,
		inputValue:       "ок",
		message:          auth.Message{ID: 902, Text: "ок", Date: 999999, IsOutgoing: true},
	})
	answered := next.(Model)
	if answered.replyTarget != nil {
		t.Fatal("цель ответа не снята после ответа сети на отправленный ответ")
	}
}

// Проверка 12 задачи (первая часть): сообщения выровнены по сторонам по
// IsOutgoing — входящие слева, исходящие справа, у каждого своя строка времени.
//
// Проверяется ПОЛОЖЕНИЕ БЛОКА, а не хвост строки: блок сообщения — прямоугольник
// шириной zoomBubbleWidth (70% доступной ширины), и текст внутри него прижат к его
// левому краю. Проверка «строка заканчивается текстом» была бы неверной вдвойне:
// блок добит пробелами до своей ширины, и у входящего он тоже не в конце строки.
func TestZoomMessagesAlignByOutgoingSide(t *testing.T) {
	incoming := zoomCard(auth.Message{ID: 1, Text: "привет", Date: 1304}, 0)
	outgoing := zoomCard(auth.Message{ID: 2, Text: "и тебе", Date: 1305, IsOutgoing: true}, 0)
	const width = 50
	inner := width - 2*cardMarginH
	// Ожидаемая колонка (внутри строки, с нулём) начала ТЕКСТА: у входящего —
	// сразу за боковым полем, у своего — от правого края зоны на ширину именно
	// этого текста (не фиксированного bubbleWidth — короткая строка обязана
	// прижаться к настоящему краю, а не к началу более широкого блока; человек
	// заметил это вживую, 2026-09-30, когда фон-плашку убрали и стало видно).
	outText := "и тебе"
	inColumn, outColumn := cardMarginH, cardMarginH+inner-cellWidth(outText)
	if inColumn >= outColumn {
		t.Fatalf("тест бессмысленен: колонки блоков совпали (%d)", inColumn)
	}

	for _, testCase := range []struct {
		name string
		item card
		text string
		date int64
		// column — колонка начала текста, timeColumn — колонка начала времени.
		// Обе прижаты к одному краю зоны: у входящего это левый (обе с cardMarginH),
		// у своего — правый. У своего хвост строки — время, зазор и галочка
		// прочтения, и прижат он ЦЕЛИКОМ к cardMarginH+inner: правее галочки уже
		// ничего нет. Поэтому колонка времени на одну ячейку зазора и на ширину
		// галочки левее правого края, а не вплотную к нему.
		column     int
		timeColumn int
	}{
		{"входящее", incoming, "привет", 1304, inColumn, inColumn},
		{"своё", outgoing, "и тебе", 1305, outColumn, cardMarginH + inner -
			cellWidth(auth.FormatMessageTime(1305)) - cardSegmentGapWidth - cellWidth(zoomReadCheckSent)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			lines := renderZoomMessage(testCase.item, width, false)
			if len(lines) != 2 {
				t.Fatalf("однострочное сообщение заняло %d строк, ждали 2 (текст + время)", len(lines))
			}
			plain := splitLines(ansi.Strip(strings.Join(lines, "\n")))
			// Текст начинается в ожидаемой колонке — блок прижат к своему краю.
			if got := columnOf(t, plain[0], testCase.text); got != testCase.column {
				t.Fatalf("текст на колонке %d, ждали %d:\n%q", got, testCase.column, plain[0])
			}
			// Время — своей строкой, у того же края, что и текст блока.
			wantTime := auth.FormatMessageTime(testCase.date)
			if got := columnOf(t, plain[1], wantTime); got != testCase.timeColumn {
				t.Fatalf("время на колонке %d, ждали %d:\n%q", got, testCase.timeColumn, plain[1])
			}
			// Ширина строк — ровно ширина зоны: боковые поля плюс блок плюс добивка
			// дают её целиком, иначе строки зоны разъедутся с остальным экраном.
			for index, line := range plain {
				if got := cellWidth(line); got != width {
					t.Fatalf("строка %d шириной %d, ждали %d:\n%q", index, got, width, line)
				}
			}
		})
	}
	// Правый край блока входящего и правый край блока своего совпадают: обе
	// стороны прижаты к краям одной и той же зоны.
	if columnOf(t, ansi.Strip(renderZoomMessage(incoming, width, false)[0]), "привет")+
		cellWidth("привет") > cardMarginH+inner ||
		columnOf(t, ansi.Strip(renderZoomMessage(outgoing, width, false)[0]), "и тебе")+
			cellWidth("и тебе") > cardMarginH+inner {
		t.Fatal("текст сообщения вылез за правый край зоны")
	}
}

// Сообщение переписки заливается тем же фоном, что и экран
// (PaletteBackgroundMain), в том числе сообщение ПОД КУРСОРОМ: сплошная
// заливка цвета выделения красила прямоугольник даже под однословный текст
// («ба», «g»), из-за чего сторона выравнивания терялась за пустым цветным полем
// (человек заметил вживую, 2026-09-30; снял заливку — 2026-10-01). Признак
// «здесь курсор» один и держится на маркере слева от текста, так что заливка
// не различала ничего, кроме собственного прямоугольника.
func TestZoomMessageUsesScreenBackgroundEvenWhenSelected(t *testing.T) {
	short := zoomCard(auth.Message{ID: 1, Text: "ба", Date: 1304}, 0)
	const width = 50
	for _, testCase := range []struct {
		name     string
		selected bool
	}{
		{"невыделенное", false},
		{"под курсором", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			rendered := strings.Join(renderZoomMessage(short, width, testCase.selected), "\n")
			if column, _ := lineWithoutBackground(rendered, 0, 0); column >= 0 {
				t.Fatalf("ячейка %d нарисована без фона, а фон должен быть везде", column)
			}
			if strings.Contains(rendered, backgroundSGR(t, PaletteBackgroundSelected)) {
				t.Fatal("сообщение переписки красится в PaletteBackgroundSelected — заливка выделения убрана, признак фокуса один и он в маркере")
			}
		})
	}
}

// Сообщение-ответ в переписке получает отдельную строку цитаты НАД текстом,
// тем же знаком (wallReplyMarker) и тем же приглушённым цветом, что и строка
// контекста над полем ввода при составлении ответа — иначе в открытом чате
// никак не видно, что это ответ (человек заметил вживую, 2026-09-30). Строка
// цитаты прижата к тому же краю, что и сам текст: сторона не должна расходиться
// между двумя строками одного сообщения.
func TestZoomMessageShowsReplyQuoteAboveText(t *testing.T) {
	const width = 50
	withQuote := card{Text: "го в 11", ReplyQuote: "го в субботу", Outgoing: true}
	without := card{Text: "го в 11", Outgoing: true}

	gotWith := renderZoomMessage(withQuote, width, false)
	gotWithout := renderZoomMessage(without, width, false)
	if len(gotWith) != len(gotWithout)+1 {
		t.Fatalf("строк с цитатой = %d, без цитаты = %d — ждали ровно на одну больше",
			len(gotWith), len(gotWithout))
	}

	quoteLine := ansi.Strip(gotWith[0])
	if !strings.Contains(quoteLine, wallReplyMarker) {
		t.Fatalf("строка цитаты не несёт знак ответа %q: %q", wallReplyMarker, quoteLine)
	}
	if !strings.Contains(quoteLine, "го в субботу") {
		t.Fatalf("строка цитаты не содержит текст оригинала: %q", quoteLine)
	}
	// Своё сообщение — цитата тоже прижата к правому краю, тем же приёмом, что
	// и сама строка текста: сравнивается ПРАВЫЙ край (куда строка заканчивается),
	// а не колонка начала — цитата и текст разной длины, и у выровненных по
	// правому краю строк разной длины СОВПАДАТЬ обязан только конец, не начало
	// (проверено координатно, не только по факту наличия).
	textEnd := columnOf(t, ansi.Strip(gotWith[1]), "го в 11") + cellWidth("го в 11")
	quoteEnd := columnOf(t, quoteLine, "го в субботу") + cellWidth("го в субботу")
	if quoteEnd != textEnd {
		t.Fatalf("цитата и текст заканчиваются в разных колонках: цитата %d, текст %d", quoteEnd, textEnd)
	}

	if strings.Contains(ansi.Strip(gotWithout[0]), wallReplyMarker) {
		t.Fatal("у сообщения без ответа появился знак ответа")
	}
}

// Проверка 12 задачи (вторая часть): многострочные сообщения переносятся по
// абзацам, высота блока растёт, и выделение не меняет высоту — иначе окно
// прокрутки считало бы по одной картине, а рисовало по другой.
func TestZoomMessageWrapsParagraphsAndHeightIgnoresSelection(t *testing.T) {
	multiline := zoomCard(auth.Message{ID: 1, Text: "первый абзац подлиннее\nвторой абзац", Date: 1304}, 0)
	const width = 50
	lines := renderZoomMessage(multiline, width, false)
	// Текст из двух абзацев занимает больше одной строки, плюс строка времени.
	if len(lines) <= 2 {
		t.Fatalf("многострочное сообщение заняло %d строк, ждели больше двух:\n%q", len(lines), lines)
	}
	if !strings.Contains(ansi.Strip(lines[0]), "первый абзац") {
		t.Fatalf("первая строка не начало текста: %q", lines[0])
	}
	if !strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "второй абзац") {
		t.Fatal("второй абзац потерялся при переносе")
	}
	if got, want := zoomCardHeight(multiline, width, true), zoomCardHeight(multiline, width, false); got != want {
		t.Fatalf("выделение изменило высоту блока: %d против %d", got, want)
	}
	// Высота блока — ровно то, что нарисовано: окно прокрутки меряет ту же
	// функцию, что и рисует (никакой второй формулы высоты).
	if got, want := zoomCardHeight(multiline, width, false), len(lines); got != want {
		t.Fatalf("мера высоты %d разошлась с отрисовкой %d", got, want)
	}
	// Настоящий перенос строки сохраняется: это абзацы, а не один поток слов.
	joined := ansi.Strip(strings.Join(lines, "\n"))
	if strings.Contains(joined, "абзац вторая") && !strings.Contains(joined, "второй абзац") {
		t.Fatal("перенос склеил абзацы в одну строку")
	}
}

// PgUp/PgDn в переписке листают сообщения, а не карточки стены, и в обоих
// режимах: в узком фокус на переписке всегда, в широком — после Tab.
func TestPageKeysScrollZoomMessagesInBothModes(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		width  int
		tabbed bool
	}{
		{"узкий", 60, false},
		{"широкий, фокус на переписке", 100, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			m := zoomModel(t, newZoomClient(), testCase.width, 30)
			if m.zoom == nil {
				// Узкий режим: переписка открывается явно, по Enter с пустым полем.
				var openCmd tea.Cmd
				m, openCmd = pressZoomKey(t, m, keyEnter())
				m = runZoomLoad(t, m, openCmd)
			} else {
				// Широкий режим: переписка уже открыта, но ещё грузится — без
				// прогона ответа листать было бы нечего.
				m = runZoomLoad(t, m, m.wallZoomLoadedCmd(m.zoom.panel, m.zoom.chatID, m.zoom.loadID))
			}
			if testCase.tabbed {
				m, _ = pressZoomKey(t, m, keyTab())
				if !m.zoomHasFocus() {
					t.Fatal("Tab не перевёл фокус на переписку")
				}
			}
			if len(m.zoom.messages) < 3 {
				t.Fatalf("в переписке %d сообщений, листать нечего", len(m.zoom.messages))
			}
			wallCursor, wallTop := m.cursor, m.scrollTop
			tail := m.zoom.cursor

			// Вверх на страницу: курсор переписки уходит из хвоста, окно стены не
			// трогается.
			up, _ := pressZoomKey(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
			if up.zoom.cursor >= tail {
				t.Fatalf("PgUp не поднял курсор переписки: %d (был %d)", up.zoom.cursor, tail)
			}
			if up.cursor != wallCursor || up.scrollTop != wallTop {
				t.Fatalf("PgUp в переписке сдвинул стену: курсор %d (был %d), окно %d (было %d)",
					up.cursor, wallCursor, up.scrollTop, wallTop)
			}
			// И обратно вниз доходит до хвоста.
			down, _ := pressZoomKey(t, up, tea.KeyPressMsg{Code: tea.KeyPgDown})
			if down.zoom.cursor != tail {
				t.Fatalf("PgDown вернул курсор на %d, ждали хвост %d", down.zoom.cursor, tail)
			}
			// У края переписки курсор встаёт на самый первый элемент, а не зависает
			// на последнем нарисованном (как в less и vim). Страниц до верха
			// заведомо больше одной, поэтому PgUp жмётся до упора — и именно
			// зажатие у края и есть проверка.
			first := down
			for range len(m.zoom.messages) {
				first, _ = pressZoomKey(t, first, tea.KeyPressMsg{Code: tea.KeyPgUp})
			}
			if first.zoom.cursor != 0 {
				t.Fatalf("у самого начала переписки курсор на %d, ждали 0", first.zoom.cursor)
			}
		})
	}
}

// Вставка живого сообщения в переписку идёт ПО ДАТЕ, а не в конец: сообщение
// «из прошлого» дописанное в хвост читалось бы как следующее по порядку.
func TestInsertSortedWallZoomMessagePlacesByDate(t *testing.T) {
	list := []auth.Message{
		{ID: 1, Text: "старое", Date: 100},
		{ID: 2, Text: "новое", Date: 300},
	}
	got := insertSortedWallZoomMessage(list, auth.Message{ID: 3, Text: "среднее", Date: 200})
	if len(got) != 3 {
		t.Fatalf("после вставки %d сообщений, ждали 3", len(got))
	}
	for index, want := range []int64{1, 3, 2} {
		if got[index].ID != want {
			t.Fatalf("на позиции %d сообщение %d, ждали %d (порядок по дате)", index, got[index].ID, want)
		}
	}
	// Сообщение новее всех встаёт в хвост, а старше всех — в начало.
	if tail := insertSortedWallZoomMessage(got, auth.Message{ID: 4, Date: 400}); tail[3].ID != 4 {
		t.Fatalf("самое новое сообщение не встало в хвост: %+v", tail)
	}
	if head := insertSortedWallZoomMessage(got, auth.Message{ID: 5, Date: 50}); head[0].ID != 5 {
		t.Fatalf("самое старое сообщение не встало в начало: %+v", head)
	}
}

// Повторная доставка того же сообщения в открытую переписку её не меняет: тот же
// id уже показан, и вставка второго экземпляра продублировала бы реплику.
func TestZoomIgnoresRepeatedDeliveryOfSameMessage(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 60, 30)
	opened, cmd := pressZoomKey(t, m, keyEnter())
	opened = runZoomLoad(t, opened, cmd)
	live := auth.Message{ID: 9001, Text: "новое сообщение", Date: 999999}
	next, _ := opened.Update(wallMessageUpdateMsg{chatID: opened.zoom.chatID, message: live, valid: true})
	after := next.(Model)
	count := len(after.zoom.messages)
	next, _ = after.Update(wallMessageUpdateMsg{chatID: opened.zoom.chatID, message: live, valid: true})
	if got := len(next.(Model).zoom.messages); got != count {
		t.Fatalf("повторная доставка изменила переписку: %d сообщений вместо %d", got, count)
	}
}

// Ctrl+R на пустой переписке и на сообщении без id ничего не выбирает: отвечать
// не на что, и подставлять вместо цели сообщение 0 было бы отправкой ответа в
// никуда.
func TestReplyOnEmptyZoomSelectsNothing(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 60, 30)
	opened, cmd := pressZoomKey(t, m, keyEnter())
	opened = runZoomLoad(t, opened, cmd)
	opened.zoom.messages = nil
	opened.zoom.cursor = 0

	empty, _ := pressZoomKey(t, opened, keyCtrlR())
	if empty.replyTarget != nil {
		t.Fatalf("Ctrl+R на пустой переписке выбрал цель: %+v", empty.replyTarget)
	}
	// Сообщение без id — битый ответ TDLib, цель ответа на него не строится.
	opened.zoom.messages = []auth.Message{{ID: 0, Text: "битое", Date: 1304}}
	opened.zoom.cursor = 0
	broken, _ := pressZoomKey(t, opened, keyCtrlR())
	if broken.replyTarget != nil {
		t.Fatalf("Ctrl+R на сообщении без id выбрал цель: %+v", broken.replyTarget)
	}
}

// Строка ответа на сообщение без текста остаётся осмысленной: показывается один
// только источник, без висящей кавычки.
func TestZoomReplyLabelWithoutPreview(t *testing.T) {
	label := zoomReplyLabel(&wallZoom{title: "[Андрей]"}, auth.Message{ID: 1, Date: 1304})
	if label != "[Андрей]" {
		t.Fatalf("подпись без превью = %q, ждали только название источника", label)
	}
}

// Геометрия экрана держится в обоих режимах и на всех размерах: ровно высота
// терминала строк, каждая — ровно шириной терминала в ячейках. Ошибка на одну
// ячейку в строке зоны уехала бы на всю сетку экрана (каркас склеивает зоны
// построчно), поэтому проверяется и высота, и ширина каждой строки.
func TestScreenGeometryHoldsWithOpenZoomInBothModes(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {100, 30}, {80, 24}, {66, 20}, {65, 20}, {62, 20}, {40, 12}} {
		for _, tabbed := range []bool{false, true} {
			m := zoomModel(t, newZoomClient(), size[0], size[1])
			if m.zoom == nil {
				var openCmd tea.Cmd
				m, openCmd = pressZoomKey(t, m, keyEnter())
				m = runZoomLoad(t, m, openCmd)
			} else {
				m = runZoomLoad(t, m, m.wallZoomLoadedCmd(m.zoom.panel, m.zoom.chatID, m.zoom.loadID))
			}
			if tabbed {
				m, _ = pressZoomKey(t, m, keyTab())
			}
			name := fmt.Sprint(size[0])
			if tabbed {
				name += " с фокусом на переписке"
			}
			t.Run(name, func(t *testing.T) {
				lines := splitLines(m.renderScreen())
				if len(lines) != size[1] {
					t.Fatalf("строк на экране %d, ждали %d", len(lines), size[1])
				}
				for index, line := range lines {
					if got := cellWidth(ansi.Strip(line)); got != size[0] {
						t.Fatalf("строка %d шириной %d, ждали %d:\n%q", index, got, size[0], ansi.Strip(line))
					}
				}
			})
		}
	}
}

// Панель переписки на широком терминале стоит вплотную к колонке стены и
// занимает всю оставшуюся ширину: сумма колонок, их контуров и зазора даёт
// ширину терминала, ничего не теряется и ничего не наезжает. Контур (задача
// 0167) не занимает новое место, а вычитается из содержимого обеих панелей, и
// поэтому в сумме widths не хватает ровно двух контуров — настолько же, на
// сколько сузилось содержимое.
func TestWidePanelWidthIsExactlyRemainder(t *testing.T) {
	// Только ширины НИЖЕ порога двойного режима (110): формула «колонка + зазор +
	// панель» описывает одну панель переписки, а на 111+ их две и сумма считается
	// иначе (её проверяет panels_test.go).
	for _, width := range []int{66, 80, 100, 110} {
		m := zoomModel(t, newZoomClient(), width, 30)
		m = runZoomLoad(t, m, m.wallZoomLoadedCmd(m.zoom.panel, m.zoom.chatID, m.zoom.loadID))
		column, panel, gap := m.wallColumnWidth(), m.zoomListWidth(), wallPanelGapWidth
		if column != wallPanelColumnWidth-wallPanelOutlineInset {
			t.Fatalf("ширина %d: колонка стены %d, ждали %d", width, column, wallPanelColumnWidth-wallPanelOutlineInset)
		}
		// Поле справа от панели (wallPanelInsetRight) входит в сумму: рамка панели
		// отстоит от края терминала, и без этих ячеек строка зоны была бы уже
		// ширины экрана.
		if column+gap+panel+2*wallPanelOutlineInset+wallPanelInsetRight != width {
			t.Fatalf("ширина %d: %d + %d + %d + контуры %d + поле %d != %d",
				width, column, gap, panel, 2*wallPanelOutlineInset, wallPanelInsetRight, width)
		}
		// То же самое, но по тому, что увидит человек: строка зоны ровно во всю
		// ширину терминала. Сумма выше — это арифметика наших же констант, и
		// сама по себе она ничего не говорит о нарисованной строке.
		for index, line := range zoneLines(t, m) {
			if got := cellWidth(line); got != width {
				t.Fatalf("ширина %d: строка зоны %d шириной %d", width, index, got)
			}
		}
	}
}

// namePrefix — начало имени источника, по которому заголовок ищется в строке.
//
// Заголовок карточки — «[Имя]» или «[Чат — Автор]», а в колонке шириной 30 длинное
// имя обрезается многоточием, из-за чего полного имени в строке нет. Это нормально
// и не дефект; нечитаемым был бы обрыв до пары символов, поэтому берётся первые
// шесть рун.
func namePrefix(name string) string {
	trimmed := strings.TrimLeft(name, "[")
	runes := []rune(trimmed)
	if len(runes) > 6 {
		return string(runes[:6])
	}
	return trimmed
}

// Заголовки карточек в суженной колонке остаются читаемыми.
//
// Проверка на живом выводе, а не на вызове renderCard, потому что дефект был
// именно в том, КАКОЙ карточкой рисуется колонка: колонка шириной 30 сама по
// себе «узкая», и если тег в ней выводится, он съедает 14 ячеек из 25, а на имя
// остаётся три символа — «[Н…» у каждой карточки вместо названия источника.
//
// Проверяется ВИДИМАЯ ширина имени, а не расстояние от начала имени до какой-то
// границы: за обрезанным именем идёт заполнение блока тега, и оно всегда
// занимает ровно всю отведённую ширину, то есть расстояние до границы от
// обрезки не зависит вовсе. По этой же причине бюджет берётся не из
// отрисовки: после задачи 0163 у вёрстки и у проверки не осталось ни одного
// общего куска в строке заголовка (время ушло в конец строки текста), а
// выражение ожидания через ту же функцию вёрстки делает проверку слепой к
// собственной мутации.
//
// Бюджет имени в суженной колонке — содержимое карточки минус зазор и маркер
// непрочитанных: 30 - 2 (контур панели, задача 0167) - 2*2 - 1 - 1 - 5 = 17 ячеек
// (отступ после границы убран, человек попросил, 2026-09-30). Ровно 12 вместо
// 17 получается при той неисправности, которую этот тест ловит: когда ширина
// колонки тега берётся по ширине ЗОНЫ, а не по признаку панели (cardTagWidth =
// 14 вместо cardUnreadMarkerMaxWidth = 5).
func TestWideCardTitlesStayReadableInPanelColumn(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 100, 30)
	zone := zoneLines(t, m)
	// Бюджет под блок «имя + сведения» в суженной колонке: содержимое карточки
	// минус зазор и маркер непрочитанных (отступа после границы больше нет).
	// Содержимое колонки — на две ячейки уже самой панели: по краям стоит контур
	// (задача 0167), и бюджет обязан считаться от той ширины, по которой
	// карточка реально нарисована.
	const titleBudget = wallPanelColumnWidth - wallPanelOutlineInset - 2*cardMarginH -
		cardBorderWidth - cardSegmentGapWidth - cardUnreadMarkerMaxWidth
	for index, item := range m.cards {
		// Строка 1 карточки: карточка занимает две строки плюс зазор, так что
		// карточки идут через три, и каждая сдвинута на строку: первую строку
		// зоны занимает верх контура панели (задача 0167). Шапка панели
		// переписки рисуется справа от колонки и на нумерацию стенной колонки не
		// влияет.
		// Колонка стены — первые wallPanelColumnWidth ячеек строки зоны: дальше
		// начинается панель переписки, и в её правой части «[Андрей]» из шапки
		// иначе искался бы в заголовке чужой карточки.
		row := ansi.Strip(zone[index*3+1])
		// Короткое имя помещается в бюджет — обязано быть видно целиком, без
		// многоточия. Это и есть «место съедено блоком тега»: при обрезанном
		// бюджете даже короткое имя перестаёт помещаться.
		if cellWidth(item.title()) <= titleBudget {
			if !strings.Contains(row, item.title()) {
				t.Fatalf("имя карточки %q помещается в колонку (%d ячеек из %d), "+
					"но видно не целиком — место съедено блоком тега:\n%s",
					item.Name, cellWidth(item.title()), titleBudget, strings.Join(zone, "\n"))
			}
			continue
		}
		// Длинное имя обрезается — но многоточием ровно по краю бюджета, а не
		// раньше: обрезка сильнее означает, что бюджет урезан блоком тега.
		// Ищется начало имени по его началу (полного в строке нет — это и есть
		// обрезка), а измеряется всё, что стоит до многоточия включительно.
		column := columnOf(t, row, namePrefix(item.Name))
		if got := widthUpToEllipsis(t, row, column); got < titleBudget-1 {
			t.Fatalf("имя карточки %q обрезано до %d ячеек, а бюджет под него %d "+
				"— место съедено блоком тега:\n%s",
				item.Name, got, titleBudget, strings.Join(zone, "\n"))
		}
	}
}

// Открытая переписка в широком режиме рисуется в ПРАВОЙ части зоны: текст
// сообщения находится правее колонки стены, а не поверх неё.
func TestWidePanelIsDrawnRightOfWallColumn(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 100, 30)
	m = runZoomLoad(t, m, m.wallZoomLoadedCmd(m.zoom.panel, m.zoom.chatID, m.zoom.loadID))
	// Сообщение, у которого есть собственные уникальные id в фикстуре: добивка
	// мока даёт чужие тексты, и по ним колонку не найти.
	last := openedWallZoomMessage(m)
	row := -1
	for index, line := range zoneLines(t, m) {
		if strings.Contains(line, last.Text) {
			row = index
			break
		}
	}
	if row < 0 {
		t.Fatalf("сообщение переписки %q не нарисовано", last.Text)
	}
	plain := zoneLines(t, m)[row]
	column := cellWidth(plain[:strings.Index(plain, last.Text)])
	if column < wallPanelColumnWidth+wallPanelGapWidth {
		t.Fatalf("сообщение на колонке %d — левее панели (колонка %d + зазор %d)",
			column, wallPanelColumnWidth, wallPanelGapWidth)
	}
}

// Окно прокрутки ПЕРЕПИСКИ в широком режиме меряется по ширине панели, а не по
// ширине терминала. Здесь разница настоящая и проверяемая: сообщения переписки
// переменной высоты (текст переносится), и при неверной ширине переноса блоки
// занимали бы другое число строк, а вместе с ним другое число сообщений влезло бы
// в экран.
//
// Именно поэтому у стены тот же случай проверяемым НЕ является (и в мутациях его
// нет): карточка стены всегда ровно двухстрочная, её высота не зависит от ширины,
// и расчёт по терминалу дал бы тот же ответ. Ошибка ширины стены видна не в
// прокрутке, а в отрисовке — её ловит TestScreenGeometryHoldsWithOpenZoomInBothModes.
func TestWideZoomScrollWindowMeasuredByPanelWidth(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 100, 30)
	m = runZoomLoad(t, m, m.wallZoomLoadedCmd(m.zoom.panel, m.zoom.chatID, m.zoom.loadID))
	// Многострочные сообщения: перенос по ширине панели даёт каждому несколько
	// строк, и высота списка становится функцией ширины.
	opened := m
	opened.zoom = &wallZoom{
		chatID:   1,
		title:    "[тест]",
		loadID:   1,
		loading:  false,
		messages: zoomTallMessages(40),
		cursor:   len(zoomTallMessages(40)) - 1,
	}
	opened.zoom.scrollTop = scrollTopToBottom(zoomCardHeight, zoomCards(opened.zoom.messages, 0), opened.zoom.cursor, opened.zoomListWidth(), opened.wallZoomListHeight())
	// Расчёт по ширине терминала обязан давать ДРУГОЙ результат — иначе проверка
	// не отличает правильную ширину от неверной.
	if wrong := scrollTopToBottom(zoomCardHeight, zoomCards(opened.zoom.messages, 0), opened.zoom.cursor, opened.width, opened.wallZoomListHeight()); wrong == opened.zoom.scrollTop {
		t.Fatalf("расчёты по панели и по терминалу совпали (%d) — тест не проверяет ничего", opened.zoom.scrollTop)
	}
	// Движение курсора вверх держит окно по арифметике ПАНЕЛИ — тем же расчётом.
	moved := opened
	moved.moveWallZoomCursor(-5)
	want := clampScrollToCursor(zoomCardHeight, zoomCards(moved.zoom.messages, 0), moved.zoom.scrollTop, moved.zoom.cursor, moved.zoomListWidth(), moved.wallZoomListHeight())
	if moved.zoom.scrollTop != want {
		t.Fatalf("окно переписки %d, ждали %d (расчёт по ширине панели %d)",
			moved.zoom.scrollTop, want, moved.zoomListWidth())
	}
}

// zoomTallMessages — count многострочных сообщений одинаковой длины: их блоки
// занимают по несколько строк, и высота списка зависит от ширины переноса.
//
// Текст подобран так, чтобы при ширине блока ПАНЕЛИ (67-4 → 44 ячеек) он
// переносился на три строки, а при ширине блока терминала (96 → 67 ячеек) — на
// две: без такой разницы расчёты по панели и по терминалу сошлись бы и проверка
// ничего не отличала бы от неправильной ширины. Панель здесь уже на две ячейки
// уже своей полной ширины — минус контур (задача 0167).
func zoomTallMessages(count int) []auth.Message {
	const text = "разговор о deploy: сначала починили упавшие тесты, потом пересобрали образ, потом выкатили его в прод вечером"
	messages := make([]auth.Message, 0, count)
	for index := range count {
		messages = append(messages, auth.Message{
			ID:         int64(index + 1),
			Text:       text,
			Date:       int64(1000 + index),
			IsOutgoing: index%2 == 1,
		})
	}
	return messages
}

// openedWallZoomMessage — последнее сообщение открытой переписки. Падает, если
// переписка не открыта.
func openedWallZoomMessage(m Model) auth.Message {
	if m.zoom == nil || len(m.zoom.messages) == 0 {
		panic("переписка не открыта или пуста")
	}
	return m.zoom.messages[len(m.zoom.messages)-1]
}

// Пустая переписка и загрузка показывают заглушку, а не пустоту, и обе занимают
// ровно заданную высоту.
func TestZoomEmptyAndLoadingShowNotice(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 60, 30)
	opened, _ := pressZoomKey(t, m, keyEnter())
	if !strings.Contains(strings.Join(zoneLines(t, opened), "\n"), "Загрузка") {
		t.Fatal("во время загрузки переписки нет заглушки «Загрузка истории…»")
	}
	opened = runZoomLoad(t, opened, opened.wallZoomLoadedCmd(opened.zoom.panel, opened.zoom.chatID, opened.zoom.loadID))
	opened.zoom.messages = nil
	if !strings.Contains(strings.Join(zoneLines(t, opened), "\n"), "Нет сообщений") {
		t.Fatal("у пустой переписки нет заглушки «Нет сообщений»")
	}
	// Обе заглушки занимают всю высоту зоны — экран не может стать короче.
	if got, want := len(zoneLines(t, opened)), opened.wallHeight(); got != want {
		t.Fatalf("зона из %d строк, ждали %d", got, want)
	}
}

// Ошибка загрузки не оставляет переписку «в загрузке» навсегда: экран переходит к
// состоянию «нет сообщений», а модель не зависает с loading = true.
func TestZoomLoadErrorClearsLoading(t *testing.T) {
	client := newZoomClient()
	client.broken[3] = true
	m := zoomModel(t, client, 60, 30)
	opened, cmd := pressZoomKey(t, m, keyEnter())
	loaded := runZoomLoad(t, opened, cmd)
	if loaded.zoom.loading {
		t.Fatal("после ошибки загрузки переписка осталась в состоянии загрузки")
	}
	if len(loaded.zoom.messages) != 0 {
		t.Fatal("после ошибки загрузки в переписке появились сообщения")
	}
}

// containsMessageText — есть ли в переписке сообщение с таким текстом.
func containsMessageText(messages []auth.Message, text string) bool {
	for _, message := range messages {
		if message.Text == text {
			return true
		}
	}
	return false
}

// zoomMessageTexts — тексты сообщений открытой переписки, для коротких
// сообщений об ошибках.
func zoomMessageTexts(m Model) []string {
	if m.zoom == nil {
		return nil
	}
	texts := make([]string, 0, len(m.zoom.messages))
	for _, message := range m.zoom.messages {
		texts = append(texts, message.Text)
	}
	return texts
}

// Enter в ДВОЙНОМ режиме меняет свободную и зафиксированную панели местами —
// по docs/tgwall-help.md:
//
//	Enter на чат А → А в панели 1, свободна панель 2
//	листаем ленту   → чат под курсором появляется в панели 2
//	Enter на чат Б → Б зафиксирован в панели 2, свободна снова панель 1
//
// Курсор двигается ТОЛЬКО через нажатие стрелки (moveFocused), а не прямым
// вызовом moveCursor. Разница не в оформлении теста: реальный путь человека
// после каждого шага зовёт syncWallZoomToCursor, то есть свободная панель
// ПОДХВАТЫВАЕТ чат под курсором. Проверка «чат под курсором уже открыт в
// какой-либо панели» на таком пути срабатывала бы всегда, и Enter был бы мёртвой
// клавишей — а на голом moveCursor проверка обходится, и баг не виден. Именно
// так Enter и оказался мёртвым в бою при зелёном тесте.
func TestTwoPanelEnterSwapsFreePanelAndOpensCursorChat(t *testing.T) {
	client := newZoomClient()
	// Ширина 120 > wallTwoPanelThreshold (110) — двухпанельный режим.
	m := zoomModel(t, client, 120, 30)
	if !m.wallTwoPanelMode() {
		t.Fatalf("при ширине %d ждали двухпанельный режим", m.width)
	}
	// При первом запуске открыты последние два чата: 3 в панели 1, 2 в панели 2.
	if m.previewPanel != panelChat1 {
		t.Fatalf("после загрузки свободна панель %d, ждали %d (первую)", m.previewPanel, panelChat1)
	}

	// Две стрелки вверх — с чата 3 на чат 1. Команда приходит вместе с моделью:
	// свободная панель открывает историю нового источника, и без её прогона в
	// панели лежал бы пустой wallZoom.
	m = pressWallArrow(t, m, keyUp())
	m = pressWallArrow(t, m, keyUp())
	if got := panelChatID(m, m.previewPanel); got != 1 {
		t.Fatalf("после стрелок в свободной панели чат %d, ждали 1", got)
	}

	// Enter: свободная и зафиксированная меняются местами, и чат под курсором
	// открывается в НОВОЙ свободной панели. Безусловно — в том числе когда чат
	// под курсором уже открыт в старой свободной (он там и есть всегда).
	pinnedBefore := panelChatID(m, panelChat1)
	m = pressWallEnter(t, m)
	if m.previewPanel != panelChat2 {
		t.Fatalf("после Enter свободна панель %d, ждали %d (панели поменялись местами)",
			m.previewPanel, panelChat2)
	}
	if got := panelChatID(m, panelChat2); got != 1 {
		t.Fatalf("в новой свободной панели чат %d, ждали 1", got)
	}
	// Та, что стала зафиксированной, не тронута: в ней остался чат, который там
	// был. Именно это и есть «зафиксировать» — Enter не выкидывает то, на что
	// человек смотрит.
	if got := panelChatID(m, panelChat1); got != pinnedBefore {
		t.Fatalf("в зафиксированной панели чат %d, ждали %d (не тронута)", got, pinnedBefore)
	}

	// Второй Enter подряд: панели меняются местами обратно. Ключа обязана быть
	// живой без всякого движения курсора между нажатиями — ровно этот шаг и был
	// мёртвым: чат под курсором уже лежал в свободной панели, и проверка
	// «уже открыт» гасила нажатие.
	m = pressWallEnter(t, m)
	if m.previewPanel != panelChat1 {
		t.Fatalf("после второго Enter свободна панель %d, ждали %d (назад)",
			m.previewPanel, panelChat1)
	}
	if got := panelChatID(m, panelChat1); got != 1 {
		t.Fatalf("во второй раз в свободной панели чат %d, ждали 1", got)
	}

	// Третий Enter — снова вперёд: чередование безусловно.
	m = pressWallEnter(t, m)
	if m.previewPanel != panelChat2 {
		t.Fatalf("после третьего Enter свободна панель %d, ждали %d", m.previewPanel, panelChat2)
	}

	// Круг из справки: листаем ленту — свободная панель едет за курсором и
	// расходится с зафиксированной, — затем Enter. Панели после Enter держат
	// разные чаты: зафиксированная тот, что под курсором, свободная его же
	// открывает заново и разойдётся с ним при следующем движении.
	m = pressWallArrow(t, m, keyDown())
	if got := panelChatID(m, m.previewPanel); got != 2 {
		t.Fatalf("после стрелки вниз в свободной панели чат %d, ждали 2 (следует за курсором)", got)
	}
	if got := panelChatID(m, panelChat1); got != 1 {
		t.Fatalf("панели должны разойтись после движения курсора, в панели 1 чат %d", got)
	}
	m = pressWallEnter(t, m)
	if m.previewPanel != panelChat1 {
		t.Fatalf("свободная панель %d, ждали %d", m.previewPanel, panelChat1)
	}
	// Панель 2 стала зафиксированной и сохранила чат, который в ней был.
	if got := panelChatID(m, panelChat2); got != 2 {
		t.Fatalf("в зафиксированной панели 2 чат %d, ждали 2 (не тронута)", got)
	}
	if got := panelChatID(m, panelChat1); got != 2 {
		t.Fatalf("в новой свободной панели чат %d, ждали 2", got)
	}

	// Дальше панели обязаны разойтись: стрелка вверх уводит свободную панель на
	// чат 1, а зафиксированная держит чат 2. Это и есть разница между
	// «переключилась панель» и «на экране две разные переписки».
	m = pressWallArrow(t, m, keyUp())
	if got := panelChatID(m, panelChat1); got != 1 {
		t.Fatalf("свободная панель не поехала за курсором: чат %d, ждали 1", got)
	}
	if got := panelChatID(m, panelChat2); got != 2 {
		t.Fatalf("зафиксированная панель поехала за курсором: чат %d, ждали 2", got)
	}
}

// pressWallArrow — стрелка на стене через Update с прогоном команды: так курсор
// идёт настоящим путём человека (moveFocused → syncWallZoomToCursor), а панель
// успевает подхватить источник под курсором.
func pressWallArrow(t *testing.T, m Model, key tea.KeyPressMsg) Model {
	t.Helper()
	next, cmd := pressZoomKey(t, m, key)
	return runWallCmd(t, next, cmd)
}

// pressWallEnter — Enter на ленте с прогоном команды. Отдельный хелпер, а не
// вызов submitInput напрямую: поведение, которое здесь проверяется, живёт в
// разборе клавиши, и прямой вызов обработчика не увидит его вовсе.
func pressWallEnter(t *testing.T, m Model) Model {
	t.Helper()
	next, cmd := pressZoomKey(t, m, keyEnter())
	return runWallCmd(t, next, cmd)
}

// runWallCmd — прогнать команду через Update, если она есть. nil — обычное
// дело: не каждый шаг открывает новую переписку.
func runWallCmd(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	msg, ok := cmd().(wallZoomLoadedMsg)
	if !ok {
		t.Fatalf("команда вернула %T, ждали wallZoomLoadedMsg", cmd())
	}
	next, _ := m.Update(msg)
	return next.(Model)
}

// panelChatID — чат в указанной панели переписки, 0 если панель пуста.
func panelChatID(m Model, panel wallPanel) int64 {
	zoom := m.panelZoom(panel)
	if zoom == nil {
		return 0
	}
	return zoom.chatID
}

// Стрелки и PgUp/PgDn двигают курсор ТОЙ панели, в которой фокус, а не всегда
// первой.
//
// Здесь стояло m.zoom — то есть первая панель безусловно. При двух панелях фокус
// стоит на второй, а курсор ездил по первому чату: человек смотрел на вторую
// переписку и листал первую. Тот же дефект был в цели ответа (Ctrl+R), в
// удалении и в цвете границы поля ввода.
//
// Проверяется настоящим путём: Tab переводит фокус, стрелки и PgUp идут через
// Update. Ключевое утверждение — что вторая панель едет, а первая стоит.
func TestArrowsMoveCursorInFocusedPanelNotFirst(t *testing.T) {
	client := newZoomClient()
	m := zoomModel(t, client, 120, 30)
	if !m.wallTwoPanelMode() {
		t.Fatalf("при ширине %d ждали двухпанельный режим", m.width)
	}
	// При запуске открыты последние два чата: 3 в панели 1, 2 в панели 2.
	if got := panelChatID(m, panelChat1); got != 3 {
		t.Fatalf("в панели 1 чат %d, ждали 3", got)
	}
	if got := panelChatID(m, panelChat2); got != 2 {
		t.Fatalf("в панели 2 чат %d, ждали 2", got)
	}
	// zoomModel отбрасывает команду загрузки, поэтому истории обеих панелей
	// подгружаются здесь: без сообщений курсором переписки не поездишь.
	m = loadWallPanelHistories(t, m)
	firstPinned := m.panelZoom(panelChat1).cursor
	secondBefore := m.panelZoom(panelChat2).cursor
	if len(m.panelZoom(panelChat2).messages) == 0 {
		t.Fatal("в панели 2 нет сообщений — история не подгрузилась")
	}
	if firstPinned != secondBefore {
		t.Fatalf("подготовка не сходится: курсоры панелей %d и %d", firstPinned, secondBefore)
	}
	// Курсор после загрузки — на последнем сообщении, иначе стрелка вверх
	// упёрлась бы в край и не показала бы, какая панель едет.
	if want := len(m.panelZoom(panelChat2).messages) - 1; secondBefore != want {
		t.Fatalf("курсор панели 2 на %d, ждали %d (последнее сообщение)", secondBefore, want)
	}

	// Фокус с ленты на панель 1, потом на панель 2.
	m = pressWallKeyModel(t, m, keyTab())
	if m.focusPanel != panelChat1 {
		t.Fatalf("после первого Tab фокус на панели %d, ждали 1", m.focusPanel)
	}
	m = pressWallKeyModel(t, m, keyTab())
	if m.focusPanel != panelChat2 {
		t.Fatalf("после второго Tab фокус на панели %d, ждали 2 — весь тест про неё", m.focusPanel)
	}

	// Стрелка вверх: курсор панели 2 уходит вверх, панель 1 не тронута. До
	// правки стрелка ездила именно по панели 1, и этот шаг падал.
	m = pressWallKeyModel(t, m, keyUp())
	if got := m.panelZoom(panelChat2).cursor; got != secondBefore-1 {
		t.Fatalf("стрелка вверх не сдвинула курсор в панели 2: был %d, стал %d (ждали %d)",
			secondBefore, got, secondBefore-1)
	}
	if got := m.panelZoom(panelChat1).cursor; got != firstPinned {
		t.Fatalf("стрелка при фокусе на панели 2 сдвинула курсор в панели 1: %d вместо %d",
			got, firstPinned)
	}

	// PgUp — та же панель: курсор панели 2 уезжает вверх, панель 1 стоит.
	beforePage := m.panelZoom(panelChat2).cursor
	m = pressWallKeyModel(t, m, tea.KeyPressMsg{Code: tea.KeyPgUp})
	if got := m.panelZoom(panelChat2).cursor; got >= beforePage {
		t.Fatalf("PgUp не поднял курсор панели 2: было %d, стало %d", beforePage, got)
	}
	if got := m.panelZoom(panelChat1).cursor; got != firstPinned {
		t.Fatalf("PgUp при фокусе на панели 2 сдвинул курсор в панели 1: %d вместо %d",
			got, firstPinned)
	}

	// Стрелка вниз — тоже панель 2, и ровно на одно сообщение.
	beforeDown := m.panelZoom(panelChat2).cursor
	m = pressWallKeyModel(t, m, keyDown())
	if got := m.panelZoom(panelChat2).cursor; got != beforeDown+1 {
		t.Fatalf("стрелка вниз: курсор панели 2 %d вместо %d", got, beforeDown+1)
	}
	if got := m.panelZoom(panelChat1).cursor; got != firstPinned {
		t.Fatalf("стрелка вниз сдвинула курсор в панели 1: %d вместо %d", got, firstPinned)
	}

	// Цель ответа Ctrl+R — из панели в фокусе, а не из первой.
	m = pressWallKeyModel(t, m, keyCtrlR())
	if m.replyTarget == nil {
		t.Fatal("Ctrl+R на панели 2 не выбрал сообщение для ответа")
	}
	if want := panelChatID(m, panelChat2); m.replyTarget.ChatID != want {
		t.Fatalf("цель ответа — чат %d, ждали чат панели в фокусе (%d)", m.replyTarget.ChatID, want)
	}
}

// pressWallKeyModel — нажатие с прогоном команды через Update: стрелки и PgUp
// в переписке команд не дают, но на всякий случай nil не должен ронять разбор.
func pressWallKeyModel(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, cmd := pressWallKey(t, m, msg)
	if cmd == nil {
		return next
	}
	updated, _ := next.Update(cmd())
	return updated.(Model)
}

// loadWallPanelHistories — прогнать команды загрузки истории открытых панелей
// через Update. Каждая команда выполняется ровно один раз, поэтому пакет
// разворачивается в его сообщения (как в delete_test.go).
func loadWallPanelHistories(t *testing.T, m Model) Model {
	t.Helper()
	// Модель из openLatestChats обязательна: именно в ней создаются переписки,
	// и ответы загрузки сверяются с ними по номеру попытки.
	next, cmd := m.openLatestChats()
	m = next
	if cmd == nil {
		t.Fatal("openLatestChats не вернул команду загрузки истории")
	}
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
	for _, message := range results {
		if _, isZoom := message.(wallZoomLoadedMsg); !isZoom {
			t.Fatalf("команда вернула %T, ждали wallZoomLoadedMsg", message)
		}
		next, _ := m.Update(message)
		m = next.(Model)
	}
	return m
}

// Shift+Tab идёт по кругу фокуса в обратную сторону.
//
// Проверяется настоящим нажатием shift+tab, а не прямым вызовом stepFocusPanel:
// само расхождение было бы не в арифметике круга, а в том, что сочетание не
// доходит до обработки вовсе — раньше KeyName отдавал для него то же имя, что и
// для обычного tab, и нажатие уходило в поле символом.
func TestShiftTabGoesBackAroundTheFocusCycle(t *testing.T) {
	client := newZoomClient()
	m := zoomModel(t, client, 120, 30)
	if !m.wallTwoPanelMode() {
		t.Fatalf("при ширине %d ждали двухпанельный режим", m.width)
	}
	m = loadWallPanelHistories(t, m)
	if got := panelChatID(m, panelChat1); got == 0 || got == panelChatID(m, panelChat2) {
		t.Fatalf("подготовка не сходится: панели %d и %d", got, panelChatID(m, panelChat2))
	}
	if m.focusPanel != panelWall {
		t.Fatalf("фокус на панели %d, ждали ленту", m.focusPanel)
	}

	// Вперёд: с ленты на панель 1, потом на панель 2.
	m = pressWallKeyModel(t, m, keyTab())
	if m.focusPanel != panelChat1 {
		t.Fatalf("после tab фокус на панели %d, ждали 1", m.focusPanel)
	}
	m = pressWallKeyModel(t, m, keyTab())
	if m.focusPanel != panelChat2 {
		t.Fatalf("после второго tab фокус на панели %d, ждали 2", m.focusPanel)
	}

	// Назад: shift+tab возвращает на панель 1, потом на ленту. Именно этого
	// хода не было вообще: круг был односторонним.
	m = pressWallKeyModel(t, m, keyShiftTab())
	if m.focusPanel != panelChat1 {
		t.Fatalf("после shift+tab фокус на панели %d, ждали 1 (назад)", m.focusPanel)
	}
	m = pressWallKeyModel(t, m, keyShiftTab())
	if m.focusPanel != panelWall {
		t.Fatalf("после второго shift+tab фокус на панели %d, ждали ленту", m.focusPanel)
	}

	// С ленты назад — на ПОСЛЕДНЮЮ панель с перепиской, а не на первую:
	// иначе shift+tab выкидывал бы человека из стены не туда.
	m = pressWallKeyModel(t, m, keyShiftTab())
	if m.focusPanel != panelChat2 {
		t.Fatalf("shift+tab с ленты ушёл на панель %d, ждали 2 (последнюю)", m.focusPanel)
	}

	// Обычный tab из той же точки идёт вперёд, то есть назад к ленте: круг
	// замкнулся и в одну, и в другую сторону.
	m = pressWallKeyModel(t, m, keyTab())
	if m.focusPanel != panelWall {
		t.Fatalf("tab с панели 2 ушёл на %d, ждали ленту (круг замкнулся)", m.focusPanel)
	}
}

// Shift+Tab не должен проглатывать остальные клавиши: обработка фокуса стоит
// раньше стрелок, Enter и Delete, и ошибка в её условии тихо отдала бы весь
// ввод в никуда.
func TestShiftTabDoesNotSwallowOtherKeys(t *testing.T) {
	client := newZoomClient()
	m := zoomModel(t, client, 120, 30)
	m = loadWallPanelHistories(t, m)
	m = pressWallKeyModel(t, m, keyTab())
	if m.focusPanel != panelChat1 {
		t.Fatalf("подготовка: фокус на панели %d, ждали 1", m.focusPanel)
	}
	// При фокусе на панели стрелка листает ПЕРЕПИСКУ — так и обещает подсказка
	// («↑ чат»), и именно это должно происходить, а не проглатывание.
	before := m.panelZoom(panelChat1).cursor
	m = pressWallKeyModel(t, m, keyUp())
	if got := m.panelZoom(panelChat1).cursor; got != before-1 {
		t.Fatalf("стрелка вверх не сдвинула переписку: %d вместо %d", got, before-1)
	}
	if m.focusPanel != panelChat1 {
		t.Fatalf("стрелка сменила фокус на панель %d", m.focusPanel)
	}

	// И shift+tab после этого по-прежнему работает: проверка фокуса не
	// «залипла» на предыдущем нажатии.
	m = pressWallKeyModel(t, m, keyShiftTab())
	if m.focusPanel != panelWall {
		t.Fatalf("shift+tab не сработал после стрелки: фокус на панели %d", m.focusPanel)
	}
}
