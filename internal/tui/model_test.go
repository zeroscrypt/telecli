package tui

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"telecli/internal/auth"
	"telecli/internal/config"
	"telecli/internal/update"
)

// TestMain форсирует TrueColor-профиль lipgloss на весь пакетный прогон
// тестов. Без этого lipgloss в среде `go test` (нет TTY на stdout)
// молча не эмитит ANSI-коды вообще — Render() отдаёт голый текст без
// escape-последовательностей. Любой тест, который рендерит стиль и ищет
// в результате ANSI-подстроку цвета (например, chatSelectionColorANSI ниже),
// без этой строки получал бы ПУСТУЮ искомую подстроку и проходил бы
// vacuous-true независимо от реального цвета — обнаружено при добавлении
// TestFolderCursorHighlightUsesTriangleAndBold (правка на внутренний отступ
// панелей и подсветку курсора): страховочная негативная проверка "не мой
// цвет" падала на любой панели, потому что искомая пустая строка входит в
// любой текст. Тесты границ/геометрии (paneBorderStyle.GetHorizontalBorderSize
// и т.п.) эту проблему не имеют — они не рендерят и не парсят ANSI.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	os.Exit(m.Run())
}

// fakeClient — минимальная подмена auth.TDClientInterface: отдаёт заготовленные
// ответы по очереди, как mockTDClient в internal/auth. messageCh (если задан) —
// управляемый тестом канал для MessageUpdates(); folderCh — то же для
// ChatFolderUpdates(); requests — последовательность отправленных запросов
// (@type + поля), по которой тесты проверяют порядок openChat/closeChat/
// getChatHistory и chat_list при выборе папки.
type fakeClient struct {
	responses []map[string]interface{}
	sendCount int
	messageCh chan map[string]interface{}
	folderCh  chan map[string]interface{}
	// chatReadInboxCh/unreadCountCh/unreadChatCountCh — управляемые тестом
	// каналы для ChatReadInboxUpdates()/UnreadCountUpdates()/
	// UnreadChatCountUpdates(), по аналогии с folderCh.
	chatReadInboxCh   chan map[string]interface{}
	chatReadOutboxCh  chan map[string]interface{}
	chatTitleCh       chan map[string]interface{}
	unreadCountCh     chan map[string]interface{}
	unreadChatCountCh chan map[string]interface{}
	requests          []map[string]interface{}
}

func (f *fakeClient) Send(_ context.Context, request map[string]interface{}) (map[string]interface{}, error) {
	f.sendCount++
	f.requests = append(f.requests, request)
	if len(f.responses) > 0 {
		resp := f.responses[0]
		f.responses = f.responses[1:]
		return resp, nil
	}
	return nil, errors.New("no more responses")
}

func (f *fakeClient) Execute(_ map[string]interface{}) (map[string]interface{}, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeClient) AuthUpdates() <-chan map[string]interface{} { return nil }

func (f *fakeClient) MessageUpdates() <-chan map[string]interface{} {
	return f.messageCh
}

func (f *fakeClient) SendStatusUpdates() <-chan map[string]interface{} { return nil }

func (f *fakeClient) ChatFolderUpdates() <-chan map[string]interface{} { return f.folderCh }

func (f *fakeClient) ChatReadInboxUpdates() <-chan map[string]interface{} {
	return f.chatReadInboxCh
}

func (f *fakeClient) ChatReadOutboxUpdates() <-chan map[string]interface{} {
	return f.chatReadOutboxCh
}

func (f *fakeClient) ChatTitleUpdates() <-chan map[string]interface{} {
	return f.chatTitleCh
}

func (f *fakeClient) UnreadCountUpdates() <-chan map[string]interface{} {
	return f.unreadCountCh
}

func (f *fakeClient) UnreadChatCountUpdates() <-chan map[string]interface{} {
	return f.unreadChatCountCh
}

func (f *fakeClient) FileUpdates() <-chan map[string]interface{} { return nil }

func (f *fakeClient) Close() {}

func keyRune(r rune) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}
}

// updateModel вызывает m.Update и сразу приводит результат к конкретному типу
// Model — так тест не разбрасывается тайп-ассертами вокруг каждого вызова.
func updateModel(m Model, msg tea.Msg) (Model, tea.Cmd) {
	mm, cmd := m.Update(msg)
	return mm.(Model), cmd
}

// testModel возвращает модель с уже загруженным списком из двух чатов.
func testModel(t *testing.T, chats []auth.Chat) Model {
	t.Helper()
	m := New(&fakeClient{}, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = updateModel(m, chatsLoadedMsg{chats: chats})
	return m
}

// defaultTheme возвращает тему по умолчанию для тестов.
func defaultTheme() Theme {
	return Themes[DefaultThemeName]
}

// typeText прогоняет каждый rune строки через Update отдельным KeyMsg — так
// ввод идёт «по одному символу» и попадает в активное textinput-поле.
func typeText(m Model, s string) Model {
	for _, r := range s {
		m, _ = updateModel(m, keyRune(r))
	}
	return m
}

// TestFocusNextCyclesThroughThreePanes — с тремя панелями Tab циклит
// folders→chats→messages→folders, три нажатия возвращают фокус в исходное
// состояние (раньше двухпанельная модель переключала между двумя панелями).
func TestFocusNextCyclesThroughThreePanes(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 1, Title: "A"}, {ID: 2, Title: "B"}})
	if m.focus != focusFolders {
		t.Fatalf("expected initial focusFolders, got %v", m.focus)
	}

	for _, step := range []struct {
		want focus
		name string
	}{
		{focusChats, "folders -> chats"},
		{focusMessages, "chats -> messages"},
		{focusFolders, "messages -> folders"},
	} {
		m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab})
		if m.focus != step.want {
			t.Fatalf("%s: expected %v, got %v", step.name, step.want, m.focus)
		}
	}
}

// TestFocusNextCyclesWithoutChats — с третьей панелью прежняя проверка
// len(m.chats) > 0 убрана: пустые панели рендерят понятный плейсхолдер, Tab
// циклит по всем трём независимо от наличия чатов.
func TestFocusNextCyclesWithoutChats(t *testing.T) {
	m := testModel(t, nil)
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusChats {
		t.Fatalf("expected focusChats after tab without chats, got %v", m.focus)
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.focus != focusMessages {
		t.Fatalf("expected focusMessages after second tab without chats, got %v", m.focus)
	}
}

func TestUpdateCursorMovementAndClamp(t *testing.T) {
	chats := []auth.Chat{{ID: 1, Title: "A"}, {ID: 2, Title: "B"}, {ID: 3, Title: "C"}}
	m := testModel(t, chats)
	// Стартовый фокус — панель папок; навигация по списку чатов начинается
	// после Tab (см. TestFocusNextCyclesThroughThreePanes).
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab})

	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyDown})
	assertCursor(t, m, 1)
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyDown})
	assertCursor(t, m, 2)

	// Кламп на верхней границе.
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyUp})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyUp})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyUp})
	assertCursor(t, m, 0)

	// Кламп на нижней границе.
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyDown})
	assertCursor(t, m, 2)

	// Вима-альтернативы j/k тоже работают.
	m, _ = updateModel(m, keyRune('j'))
	m, _ = updateModel(m, keyRune('j'))
	assertCursor(t, m, 2)
	m, _ = updateModel(m, keyRune('k'))
	assertCursor(t, m, 1)
}

func assertCursor(t *testing.T, m Model, want int) {
	t.Helper()
	if m.chatCursor != want {
		t.Errorf("expected chatCursor %d, got %d", want, m.chatCursor)
	}
}

func TestUpdateChatsLoadedErrorSetsStatus(t *testing.T) {
	m := testModel(t, nil)
	m, _ = updateModel(m, chatsLoadedMsg{err: errors.New("boom")})
	if m.status == "" {
		t.Fatal("expected non-empty status on chats load error")
	}
	if len(m.chats) != 0 {
		t.Errorf("expected empty chats on error, got %d", len(m.chats))
	}
}

func TestMessagesLoadedAppliesAndStaleDropped(t *testing.T) {
	chats := []auth.Chat{{ID: 111, Title: "Первый"}, {ID: 222, Title: "Второй"}}
	m := testModel(t, chats)

	// Подмена ответов при выборе чата 111: первым идёт ответ на OpenChat
	// (selectChatCmd отправляет openChat раньше getChatHistory), затем сама
	// история из одного исходящего сообщения.
	fake := &fakeClient{responses: []map[string]interface{}{
		{"@type": "ok"},
		{
			"@type": "messages",
			"messages": []interface{}{
				map[string]interface{}{
					"@type":       "message",
					"id":          float64(5),
					"is_outgoing": true,
					"date":        float64(100),
					"content": map[string]interface{}{
						"@type": "messageText",
						"text": map[string]interface{}{
							"@type": "formattedText",
							"text":  "привет",
						},
					},
				},
			},
		},
	}}
	// Пересоздаём модель с этим фейком, чтобы cmd ниже реально сходил в него.
	m.client = fake

	// Стартовый фокус — панель папок; Tab переводит его в список чатов.
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab})
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd after enter")
	}
	if m.focus != focusMessages {
		t.Fatalf("expected focusMessages after enter, got %v", m.focus)
	}
	if !m.loadingMsgs {
		t.Fatal("expected loadingMsgs after enter")
	}
	if m.displayedChat != 111 {
		t.Fatalf("expected displayedChat 111, got %d", m.displayedChat)
	}

	// Старый/чужой ответ (другой chatID) должен быть молча отброшен.
	m, _ = updateModel(m, messagesLoadedMsg{chatID: 222, messages: []auth.Message{{ID: 9}}})
	if !m.loadingMsgs {
		t.Fatal("stale messagesLoadedMsg must not clear loadingMsgs")
	}
	if len(m.messages) != 0 {
		t.Fatal("stale messagesLoadedMsg must not apply messages")
	}

	// Актуальный ответ применяется.
	msg := cmd() // выполняем tea.Cmd вручную: GetMessages против fake-клиента
	m, _ = updateModel(m, msg)
	if m.loadingMsgs {
		t.Fatal("expected loadingMsgs cleared after applying messages")
	}
	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(m.messages))
	}
	if m.messages[0].Text != "привет" || m.messages[0].SenderName != "Вы" {
		t.Errorf("unexpected message content: %+v", m.messages[0])
	}
}

func TestMessagesLoadedErrorKeepsMessages(t *testing.T) {
	chats := []auth.Chat{{ID: 111, Title: "Первый"}}
	m := testModel(t, chats)

	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab})   // фокус в список чатов
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter}) // loadingMsgs = true
	m.messages = []auth.Message{{ID: 1, Text: "старое", SenderName: "X"}}

	m, _ = updateModel(m, messagesLoadedMsg{chatID: 111, err: errors.New("network")})
	if m.loadingMsgs {
		t.Fatal("expected loadingMsgs cleared after error")
	}
	if len(m.messages) != 1 {
		t.Fatal("existing messages must not be wiped by a load error")
	}
	if m.status == "" {
		t.Fatal("expected non-empty status on messages load error")
	}
}

func TestUpdateQuitKeys(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 1, Title: "A"}})

	for _, key := range []tea.KeyMsg{
		tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}},
		tea.KeyMsg{Type: tea.KeyCtrlC},
	} {
		_, cmd := updateModel(m, key)
		if !isQuitCmd(cmd) {
			t.Errorf("expected quit cmd for %s, got %v", key.String(), cmd)
		}
	}
}

func isQuitCmd(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	msg := cmd()
	_, ok := msg.(tea.QuitMsg)
	return ok
}

func TestUpdateEscReturnsToChats(t *testing.T) {
	chats := []auth.Chat{{ID: 1, Title: "A"}}
	m := testModel(t, chats)

	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab}) // focusMessages
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.focus != focusChats {
		t.Fatalf("expected focusChats after esc, got %v", m.focus)
	}
}

func TestViewRendersPanes(t *testing.T) {
	chats := []auth.Chat{{ID: 111, Title: "Тестовый чат"}, {ID: 222, Title: "Второй"}}
	m := testModel(t, chats)
	m.folders = []auth.Folder{{ID: 7, Name: "Работа"}, {ID: 8, Name: ""}}

	view := m.View()
	for _, want := range []string{"Все чаты", "Работа", "Папка #8", "Тестовый чат", "Второй", "Выберите чат и нажмите Enter"} {
		if !strings.Contains(view, want) {
			t.Errorf("View() missing %q:\n%s", want, view)
		}
	}

	m.loadingMsgs = true
	view = m.View()
	if !strings.Contains(view, "Загрузка сообщений…") {
		t.Errorf("View() missing loading indicator:\n%s", view)
	}

	m.messages = []auth.Message{{ID: 1, SenderName: "Вы", Text: "текст", Date: 100}}
	view, _ = renderMessages(m.messages, 0, false, 0, defaultTheme(), 0)
	if !strings.Contains(view, "текст") {
		t.Errorf("renderMessages missing text:\n%s", view)
	}
}

func TestRenderMessagesWrapsLongText(t *testing.T) {
	longText := "одно два три четыре пять шесть семь восемь девять десять"
	msgs := []auth.Message{{ID: 1, SenderName: "Вы", Text: longText, Date: 100, IsOutgoing: true}}

	got, _ := renderMessages(msgs, 20, false, 0, defaultTheme(), 0)
	lines := strings.Split(got, "\n")
	// Карточка: верхняя рамка + N строк тела + нижняя рамка; длинный текст не
	// умещается в одну строку на 20 колонок, значит N >= 2, итого строк >= 4.
	if len(lines) < 4 {
		t.Fatalf("expected wrapped body to span multiple card rows, got %d lines:\n%s", len(lines), got)
	}
	// Ширина строк <= 20 (не переполняет ленту), но НЕ обязательно ровно 20:
	// по правке человека карточка теперь минимальной ширины по контенту
	// (naturalCardWidth), не всегда растянута на всю ленту — здесь важно
	// только, что перенос действительно произошёл и ничего не вылезло.
	for _, line := range lines {
		if line == "" {
			continue
		}
		if w := lipgloss.Width(line); w > 20 {
			t.Errorf("card line wider than lane: width=%d, line=%q", w, line)
		}
	}
}

func TestRenderMessagesZeroWidthDoesNotWrap(t *testing.T) {
	longText := "одно два три четыре пять шесть семь восемь девять десять"
	msgs := []auth.Message{{ID: 1, SenderName: "Вы", Text: longText, Date: 100, IsOutgoing: true}}

	for _, width := range []int{0, -1} {
		got, _ := renderMessages(msgs, width, false, 0, defaultTheme(), 0)
		// Перенос сохранён: строк такое же число, как и у текста без переноса.
		if lines := strings.Split(got, "\n"); len(lines) != 2 {
			t.Errorf("width=%d: expected single body line (no wrap), got %d lines:\n%s", width, len(lines), got)
		}
		if !strings.Contains(got, longText) {
			t.Errorf("width=%d: expected full long text preserved, got:\n%s", width, got)
		}
	}
}

func TestWindowSizeMsgRewrapsExistingMessages(t *testing.T) {
	longText := "одно два три четыре пять шесть семь восемь девять десять"
	m := testModel(t, nil)
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.displayedChat = 111
	m.messages = []auth.Message{{ID: 1, SenderName: "Вы", Text: longText, Date: 100, IsOutgoing: true}}
	contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
	content, _ := renderMessages(m.messages, contentWidth, false, 0, defaultTheme(), 0)
	m.viewport.SetContent(content)

	// Новый, более узкий размер терминала: лента перерисовывается под него.
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 40, Height: 30})
	view := m.viewport.View()
	// Под ширину ~48 (40 − рамка) длинный текст переносится на несколько строк.
	if strings.Count(view, "\n") < 2 {
		t.Errorf("expected rewrapped (multi-line) viewport after narrower resize, got:\n%s", view)
	}
}

// Цвет ника и рамки детерминированы по хешу имени: одно и то же имя всегда
// даёт один и тот же цвет (то, что в личном чате у собеседника цвет один и
// тот же, получается само собой без отдельной проверки "это группа или нет").
func TestNickColorDeterministicSameNameSameColor(t *testing.T) {
	th := defaultTheme()
	if got, want := nickColor("Ирина", th), nickColor("Ирина", th); got != want {
		t.Fatalf("expected same nick color for the same name, got %v and %v", got, want)
	}
}

// Хеш имени реально разбрасывает имена по разным корзинам палитры, а не
// сводит всё в одну: nickIndex("Ирина") и nickIndex("Игорь") посчитаны
// вручную сложением байт-кодов (208+152+… по модулю 4) и дают разные
// значения (2 и 3).
func TestNickIndexDistributesAcrossPalette(t *testing.T) {
	th := defaultTheme()
	gotIrina, gotIgor := nickIndex("Ирина", len(th.NickPalette)), nickIndex("Игорь", len(th.NickPalette))
	if gotIrina == gotIgor {
		t.Fatalf("expected different nick indices for different names, both got %d", gotIrina)
	}
}

// Имя отправителя встроено в верхнюю линию рамки карточки — первая строка
// рендера содержит его (ANSI-обёртка стиля не мешает strings.Contains: сам
// текст остаётся непрерывной подстрокой, тот же приём, что и по всему файлу).
func TestRenderMessageCardTopLineContainsSenderName(t *testing.T) {
	msgs := []auth.Message{{ID: 1, SenderName: "Ирина", Text: "привет", Date: 100}}

	got, _ := renderMessages(msgs, 20, false, 0, defaultTheme(), 0)
	first := strings.Split(got, "\n")[0]
	if !strings.Contains(first, "Ирина") {
		t.Errorf("top card line must contain sender name, got: %q", first)
	}
}

// alignOwnRight=true прижимает МОИ карточки к правому краю ленты: каждая
// непустая строка карточки имеет ведущие пробелы и полную ширину ленты.
// Фикс фона 0039 добавил в начало строк ANSI-префикс фона панели, поэтому
// первый символ строки — не буква, а ESC-последовательность: для проверки
// ведущего пробела сравниваем ОЧИЩЕННУЮ от ANSI строку.
func TestRenderMessagesAlignOwnRightPadsOwnCardToRightEdge(t *testing.T) {
	msgs := []auth.Message{{ID: 1, SenderName: "Вы", Text: "моё", Date: 100, IsOutgoing: true}}

	got, _ := renderMessages(msgs, 60, true, 0, defaultTheme(), 0)
	for _, line := range strings.Split(got, "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(stripANSI(line), " ") {
			t.Errorf("own card line must be padded to the right edge, got leading non-space: %q", line)
		}
		if w := lipgloss.Width(line); w != 60 {
			t.Errorf("padded own card line width %d != 60: %q", w, line)
		}
	}
}

// alignOwnRight=false (и для исходящего, и для входящего) — карточки на всю
// ширину ленты без ведущих пробелов.
// TestRenderMessagesAlignOwnRightFalseNoRightPadding — alignOwnRight=false
// (и для исходящего, и для входящего) — карточки без ведущих пробелов
// (флеш-лефт, не прижаты к правому краю). Ширина карточки теперь
// МИНИМАЛЬНАЯ по контенту (naturalCardWidth, по правке человека — раньше
// растягивалась на всю ленту), а не всегда равна ленте — короткое "моё"
// (3 буквы) должно дать карточку СУЩЕСТВЕННО у́же ленты (60), это и
// проверяется явно, не только верхняя граница ширины.
func TestRenderMessagesAlignOwnRightFalseNoRightPadding(t *testing.T) {
	msgs := []auth.Message{
		{ID: 1, SenderName: "Вы", Text: "моё", Date: 100, IsOutgoing: true},
		{ID: 2, SenderName: "Ирина", Text: "чужое", Date: 101},
	}

	got, _ := renderMessages(msgs, 60, false, 0, defaultTheme(), 0)
	cards := strings.Split(got, "\n\n")
	if len(cards) != 2 {
		t.Fatalf("expected 2 cards, got %d:\n%s", len(cards), got)
	}
	for _, card := range cards {
		firstLine := ""
		maxLineW := 0
		for _, line := range strings.Split(card, "\n") {
			if line == "" {
				continue
			}
			if firstLine == "" {
				firstLine = line
			}
			if w := lipgloss.Width(line); w > 60 {
				t.Errorf("card line wider than lane: width=%d, line=%q", w, line)
			}
			// Видимая ширина карточки — без хвостовой доливки фона панели до
			// ширины ленты (фикс 0039): доливка добавляет ANSI-префикс и
			// пробелы, они ширину карточки не увеличивают.
			visible := strings.TrimRight(stripANSI(line), " ")
			if vw := lipgloss.Width(visible); vw > maxLineW {
				maxLineW = vw
			}
		}
		if strings.HasPrefix(stripANSI(firstLine), " ") {
			t.Errorf("card must be flush-left (no leading space), got: %q", stripANSI(firstLine))
		}
		if maxLineW >= 60 {
			t.Errorf("short message card should be narrower than the lane (60), got width=%d", maxLineW)
		}
	}
}

// === Задача 0039: непрерывность фона ===

// sgrState — состояние SGR-атрибутов на текущей позиции строки: из него
// uncoveredCols решает, покрыта ли закраской очередная ячейка.
type sgrState struct {
	hasBG bool // установлен ли явный фоновый цвет
	rev   bool // активен ли reverse video (ячейка заливается инверсией, не фона терминала)
}

// apply разбирает один набор параметров SGR-последовательности («…;…;…» из
// `\x1b[…m`). Параметры одной последовательности приходят вперемешку (fg, bg,
// модификаторы) — lipgloss склеивает всё в один CSI. «38»/«39» на hasBG не
// влияют; «48» — только когда за ним реально идёт селектор расширенного цвета
// (2 или 5): синяя компонента TrueColor-фона в склеенной последовательности
// выглядит как голый «48», и трактовать её как установку фона — ложное
// срабатывание.
func (st *sgrState) apply(params []string) {
	for i := 0; i < len(params); i++ {
		switch p := params[i]; p {
		case "", "0":
			st.hasBG = false
			st.rev = false
		case "49":
			st.hasBG = false
		case "7":
			st.rev = true
		case "27":
			st.rev = false
		case "48":
			if n := 1 + sgrColorLen(params[i+1:]); n > 1 {
				st.hasBG = true
				i += n
			}
		case "38", "39":
			i += 1 + sgrColorLen(params[i+1:])
		default:
			if n, err := strconv.Atoi(p); err == nil && (n >= 40 && n <= 47 || n >= 100 && n <= 107) {
				st.hasBG = true
			}
		}
	}
}

// sgrColorLen возвращает число компонент расширенного цвета ПОСЛЕ селектора:
// 3 для «2;R;G;B», 1 для «5;N», 0 если селектора нет.
func sgrColorLen(ps []string) int {
	if len(ps) == 0 {
		return 0
	}
	switch ps[0] {
	case "2":
		return 3
	case "5":
		return 1
	}
	return 0
}

// uncoveredCols возвращает номера колонок строки s (обход по рунам), для
// которых на момент отрисовки НЕ был установлен ни фоновый SGR-код, ни
// reverse video — такие ячейки унаследовали бы цвет фона терминала, а не
// панели/хрома. Именно хвост после внутреннего `\x1b[0m` был багом 0039:
// карточка закрывала свой цвет и вместе с ним сбрасывала фон внешнего слоя.
// Reverse video заливкой НЕ считается «прозрачной» ячейкой (инверсия красит),
// единственное его применение в UI — блок курсора в поле ввода.
func uncoveredCols(s string) []int {
	st := sgrState{}
	var gaps []int
	col := 0
	rest := s
	for len(rest) > 0 {
		if rest[0] == '\x1b' {
			if len(rest) > 1 && rest[1] == '[' {
				end := strings.IndexByte(rest, 'm')
				if end < 0 {
					break
				}
				st.apply(strings.Split(rest[2:end], ";"))
				rest = rest[end+1:]
				continue
			}
			rest = rest[1:]
			continue
		}
		_, sz := utf8.DecodeRuneInString(rest)
		if !st.hasBG && !st.rev {
			gaps = append(gaps, col)
		}
		col++
		rest = rest[sz:]
	}
	return gaps
}

// п.1 задачи 0039: фон панели сообщений не должен обрываться ни на одной
// строке карточки (рамка, имя, время, глифы, текст, паддинги, доливка до
// ширины ленты). Проверяет оба режима выравнивания: ведущие пробелы у
// сдвигаемых к правому краю своих карточек идуворотом тоже закрашены.
func TestRenderMessagesBackgroundsContiguous(t *testing.T) {
	msgs := []auth.Message{
		{ID: 1, SenderName: "Вы", Text: "моё", Date: 100, IsOutgoing: true},
		{ID: 2, SenderName: "Ирина", Text: "привет", Date: 101},
	}
	for _, align := range []bool{false, true} {
		content, _ := renderMessages(msgs, 40, align, 0, defaultTheme(), 1400)
		for _, line := range strings.Split(content, "\n") {
			if line == "" {
				continue
			}
			if cols := uncoveredCols(line); len(cols) > 0 {
				t.Errorf("renderMessages(align=%v): uncovered columns %v (фон терминала): %q", align, cols, line)
			}
		}
	}
}

// п.3 задачи 0039: заголовки панелей — сплошной чёрный фон хрома.
func TestPaneTitleBackgroundsContiguous(t *testing.T) {
	th := defaultTheme()
	for _, args := range []struct {
		width   int
		num     int
		text    string
		focused bool
	}{
		{24, 1, "Папки", true},
		{24, 2, "Чаты", false},
		{40, 3, "Чат", true},
	} {
		s := paneTitle(args.width, args.num, args.text, args.focused, th)
		if cols := uncoveredCols(s); len(cols) > 0 {
			t.Errorf("paneTitle(%d, %d, %q, %v): uncovered columns %v: %q", args.width, args.num, args.text, args.focused, cols, s)
		}
	}
}

// п.3 задачи 0039: нижняя строка (лого, теги режимов, хоткей-подсказки) —
// сплошной чёрный фон в обоих режимах, где она рендерится с вложенными
// стилями (Normal и Command с вводом команды).
func TestBottomLineBackgroundsContiguous(t *testing.T) {
	for name, prepare := range map[string]func(*Model){
		"normal": func(*Model) {},
		"command": func(m *Model) {
			*m, _ = updateModel(*m, keyRune(':'))
		},
	} {
		m := testModel(t, nil)
		m.version = "v0.4.2"
		prepare(&m)
		got := m.bottomLine()
		for _, line := range strings.Split(got, "\n") {
			if line == "" {
				continue
			}
			if cols := uncoveredCols(line); len(cols) > 0 {
				t.Errorf("bottomLine (%s): uncovered columns %v (фон терминала): %q", name, cols, line)
			}
		}
	}
}

// Тест бага 0042 живой проверки: фон хрома не покрывал :help/t (about) экраны
// целиком — строки там собраны из НЕСКОЛЬКИХ Render(...)-фрагментов подряд
// (заголовок, keyLine в help, баннер в about), и фон внешнего paneBox
// переживал только первый вложенный \x1b[0m. Проверка — тем же механизмом
// uncoveredCols, что в задачах 0039: ни одна колонка ни одной строки не должна
// остаться без фона.
func TestHelpAboutBackgroundsContiguous(t *testing.T) {
	m := New(&fakeClient{}, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 100})
	m.version = "v0.5.0"

	for name, s := range map[string]string{
		"help":  m.helpScreen(),
		"about": m.aboutScreen(),
	} {
		for i, line := range strings.Split(s, "\n") {
			if line == "" {
				continue
			}
			if cols := uncoveredCols(line); len(cols) > 0 {
				t.Errorf("%s line %d: uncovered columns %v (фон терминала): %q", name, i, cols, line)
			}
		}
	}
}

// TestRenderMessagesAlignOwnRightNarrowWidthDoesNotOverflow — ревью
// оркестратора, два независимых бага в одной и той же функции на узкой
// ширине: (1) max(20, width*3/4) без верхнего предела давал cardWidth >
// width — своя карточка вылезала бы за пределы ленты, исправлено
// min(width, ...); (2) порог "слишком узко для метки" в renderMessageCard
// был занижен (захардкожен 8 вместо реального fixedW+3) — на впритык
// проходящей ширине maxNameW уходил в отрицательные значения и итоговая
// метка вылезала на 1+ колонку. Оба бага проявлялись только на реалистично
// узких терминалах, не на width=60 из уже существующих тестов. Проверяет
// широкий диапазон ширин, в т.ч. с более длинным именем отправителя, чем
// короткое "Вы". Нижняя граница диапазона — 3, не 1: ширины < 3 — уже
// принятый и задокументированный предел renderMessageCard (см. её же
// комментарий и TestRenderMessageCardNarrowWidthLineWidthsMatch выше) — при
// них на текст физически не остаётся ни одной колонки, это не баг.
func TestRenderMessagesAlignOwnRightNarrowWidthDoesNotOverflow(t *testing.T) {
	for _, sender := range []string{"Вы", "Александра"} {
		msgs := []auth.Message{{ID: 1, SenderName: sender, Text: "моё сообщение подлиннее", Date: 100, IsOutgoing: true}}
		for width := 3; width <= 30; width++ {
			got, _ := renderMessages(msgs, width, true, 0, defaultTheme(), 0)
			for _, line := range strings.Split(got, "\n") {
				if line == "" {
					continue
				}
				if w := lipgloss.Width(line); w > width {
					t.Errorf("sender %q, width %d: card line wider than lane (%d): %q", sender, width, w, line)
				}
			}
		}
	}
}

// Регрессия на несовпадение ширины тела и рамки на узких карточках (см.
// формулу pad/textWidth в renderMessageCard): на ширинах {3,4,5,6,8} каждая
// непустая строка карточки ровно этой ширины. width=2 намеренно не входит в
// набор — при нём на текст физически не остаётся ни одной колонки (2 угловых
// символа рамки уже занимают всю ширину), renderMessageCard ниже этого порога
// не гарантирует соблюдение инварианта ширины (осознанный, а не забытый предел).
func TestRenderMessageCardNarrowWidthLineWidthsMatch(t *testing.T) {
	for _, width := range []int{3, 4, 5, 6, 8} {
		got := renderMessageCard(auth.Message{ID: 1, SenderName: "?", Text: "x", Date: 100}, width, false, false, defaultTheme(), 0)
		for _, line := range strings.Split(strings.TrimRight(got, "\n"), "\n") {
			if line == "" {
				continue
			}
			if w := lipgloss.Width(line); w != width {
				t.Errorf("width=%d: card line width %d != %d: %q", width, w, width, line)
			}
		}
	}
}

// Глиф прочтения на своих сообщениях: "✓" (Faint) — отправлено, но ещё не
// прочитано; "✓✓" (OwnColor) — прочитано; на чужих сообщениях глифа нет
// вообще (в реальном Telegram галочки есть только на своих сообщениях).
// Проверяется через renderMessageCard напрямую — глиф живёт в верхней линии
// карточки рядом со временем.
func TestRenderMessageCardReadGlyphs(t *testing.T) {
	th := defaultTheme()
	// ANSI-префикс OwnColor не хардкодим (см. chatSelectionColorANSI выше):
	// берём префикс из рендера эталонной строки тем же стилем.
	ownGlyph, ownGlyphPrefix := readGlyphANSI(t, th)

	cases := []struct {
		name     string
		msg      auth.Message
		lastRead int64
		want     string // обязана присутствовать
		notWant  string // обязана отсутствовать
		colored  bool   // глиф обязан нести ANSI-префикс OwnColor
	}{
		{
			name:     "unread own message shows single check",
			msg:      auth.Message{ID: 1, SenderName: "Вы", Text: "привет", Date: 100, IsOutgoing: true},
			lastRead: 0,
			want:     "✓",
			notWant:  "✓✓",
		},
		{
			name:     "read own message shows double check",
			msg:      auth.Message{ID: 1, SenderName: "Вы", Text: "привет", Date: 100, IsOutgoing: true},
			lastRead: 5,
			want:     "✓✓",
			colored:  true,
		},
		{
			name:     "foreign message shows no check",
			msg:      auth.Message{ID: 2, SenderName: "Ирина", Text: "привет", Date: 101},
			lastRead: 5,
			notWant:  "✓",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := renderMessageCard(tc.msg, 40, false, false, th, tc.lastRead)
			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("expected %q in card:\n%s", tc.want, got)
			}
			if tc.notWant != "" && strings.Contains(got, tc.notWant) {
				t.Errorf("unexpected %q in card:\n%s", tc.notWant, got)
			}
			if tc.colored {
				// Прочитанное — акцентом OwnColor, не приглушённым Faint.
				if !strings.Contains(got, ownGlyphPrefix+ownGlyph) {
					t.Errorf("expected read glyph colored with OwnColor, got:\n%s", got)
				}
			}
		})
	}
}

// readGlyphANSI возвращает глиф "✓✓" и ANSI-префикс, которым lipgloss
// открывает рендер глифа стилем OwnColor (та же идея, что
// chatSelectionColorANSI) — не парсим ANSI-коды руками.
func readGlyphANSI(t *testing.T, th Theme) (string, string) {
	t.Helper()
	glyph := "✓✓"
	// Глиф в карточке рендерится фоном панели (фикс 0039) — эталонный префикс
	// строим тем же двойным стилем, что и карточка: fg=OwnColor + bg=панели.
	ref := lipgloss.NewStyle().Foreground(th.OwnColor).Background(messagePanelBg()).Render(glyph)
	return glyph, ref[:strings.Index(ref, glyph)]
}

func TestModeNormalToCommand(t *testing.T) {
	m := testModel(t, nil)
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal by default, got %v", m.mode)
	}

	m, _ = updateModel(m, keyRune(':'))
	if m.mode != modeCommand {
		t.Fatalf("expected modeCommand after ':', got %v", m.mode)
	}
	if !m.commandInput.Focused() {
		t.Fatal("expected commandInput focused in command mode")
	}
}

func TestModeCommandQuit(t *testing.T) {
	for _, cmdName := range []string{"q", "quit"} {
		m := testModel(t, nil)
		m, _ = updateModel(m, keyRune(':'))
		m = typeText(m, cmdName)
		m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
		if !isQuitCmd(cmd) {
			t.Errorf("expected quit cmd for :%s, got %v", cmdName, cmd)
		}
		if m.mode != modeNormal {
			t.Errorf("expected modeNormal after :%s, got %v", cmdName, m.mode)
		}
	}
}

func TestModeCommandHelpOpensHelpScreen(t *testing.T) {
	m := testModel(t, nil)

	// :help from Normal mode opens help screen
	m, _ = updateModel(m, keyRune(':'))
	m = typeText(m, "help")
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf(":help must not return cmd, got %v", cmd)
	}
	if m.mode != modeHelp {
		t.Fatalf("expected modeHelp after :help, got %v", m.mode)
	}

	// 'h' key (ShowHelp) from help screen returns to Normal (same toggle behavior)
	m, _ = updateModel(m, keyRune('h'))
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after 'h' from help screen, got %v", m.mode)
	}
}

// TestModeAboutHotkeyOpensAndCloses — 't' (About) из Normal открывает modeAbout
// и запускает ПЕРВЫЙ тик анимации (не-nil tea.Cmd), повторный 't' закрывает
// назад в modeNormal (тот же принцип toggle, что у modeHelp) и не возвращает
// команд — анимация больше не планируется.
func TestModeAboutHotkeyOpensAndCloses(t *testing.T) {
	m := testModel(t, nil)
	if m.mode != modeNormal {
		t.Fatalf("setup: expected modeNormal, got %v", m.mode)
	}

	m, cmd := updateModel(m, keyRune('t'))
	if m.mode != modeAbout {
		t.Fatalf("expected modeAbout after 't', got %v", m.mode)
	}
	if cmd == nil {
		t.Fatal("expected non-nil first tick cmd after opening about")
	}

	m, cmd = updateModel(m, keyRune('t'))
	if cmd != nil {
		t.Fatalf("expected nil cmd on closing about by repeated 't', got %v", cmd)
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after repeated 't', got %v", m.mode)
	}
}

// TestModeAboutEscCloses — Esc закрывает modeAbout назад в modeNormal так же,
// как повторный хоткей, и без возвращаемых команд.
func TestModeAboutEscCloses(t *testing.T) {
	m := testModel(t, nil)
	m, _ = updateModel(m, keyRune('t'))
	if m.mode != modeAbout {
		t.Fatalf("setup: expected modeAbout, got %v", m.mode)
	}

	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("expected nil cmd on esc from about, got %v", cmd)
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after esc, got %v", m.mode)
	}
}

// TestAboutTickResubscribesOnlyInAboutMode — tea.Tick в modeAbout сдвигает
// колонку блика (m.aboutTickCol) и переподписывается (не-nil cmd). Тик,
// пришедший вне modeAbout, ничего не меняет и НЕ переподписывается (cmd == nil)
// — при выходе с экрана анимация естественно прекращается.
func TestAboutTickResubscribesOnlyInAboutMode(t *testing.T) {
	m := testModel(t, nil)
	m.mode = modeAbout

	m, cmd := updateModel(m, aboutTickMsg(3))
	if cmd == nil {
		t.Fatal("expected non-nil resubscription cmd while in modeAbout")
	}
	if m.aboutTickCol != 3 {
		t.Fatalf("expected aboutTickCol 3, got %d", m.aboutTickCol)
	}

	m = testModel(t, nil) // сброс в modeNormal
	m, cmd = updateModel(m, aboutTickMsg(7))
	if cmd != nil {
		t.Fatalf("expected nil cmd for about tick outside modeAbout, got %v", cmd)
	}
	if m.aboutTickCol != 0 {
		t.Fatalf("aboutTickCol must stay untouched outside modeAbout, got %d", m.aboutTickCol)
	}
}

// TestAboutScreenShowsLiveVersion — версия на экране «о программе» берётся из
// m.version (живое значение модели), а не из захардкоженной строки: задаём
// заведомо нестандартную версию и проверяем её появление в отрендеренном UI.
// Заодно — ключевые контентные блоки экрана (описание, ссылка, автор,
// благодарность, footer закрытия, глифы логотипа).
func TestAboutScreenShowsLiveVersion(t *testing.T) {
	m := testModel(t, nil)
	m.version = "v9.9.9-rc1"
	m.mode = modeAbout

	view := m.View()
	for _, want := range []string{
		"v9.9.9-rc1",
		"Терминальный клиент Telegram с vim-подобной модальностью ввода",
		"github.com/zeroscrypt/telecli",
		"@zeroscrypt",
		"@hakatao",
		"Esc / t — закрыть",
		"█",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("about screen missing %q:\n%s", want, view)
		}
	}
}

// stripANSI вырезает ANSI-SGR-последовательности (`\x1b[…m`) из строки —
// вспомогательная функция только для тестов: чтобы мерить "чистую" форму
// логотипа поверх цветного рендера lipgloss (TestMain форсирует TrueColor,
// ANSI-коды в рендере присутствуют).
func stripANSI(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		if r == '\x1b' {
			inEscape = true
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// TestRenderAboutBannerShape — геометрия ASCII-логотипа TELECLi: ровно 6 строк
// шириной 41 колонка (7 букв × 5 + 6 разделителей), каждая строка содержит
// только глиф '█' и пробелы; сдвиг блика не меняет саму форму (колонки
// подсветки просто перекрашиваются, набор '█' по столбцам фиксирован).
func TestRenderAboutBannerShape(t *testing.T) {
	m := testModel(t, nil)

	for _, tick := range []int{0, 1, 20, 40, 41, 1000} {
		lines := m.renderAboutBanner(tick)
		if len(lines) != 6 {
			t.Fatalf("tick %d: expected 6 banner lines, got %d", tick, len(lines))
		}
		for _, line := range lines {
			// ANSI-коды цветов убираем, чтобы мерить "чистую" форму логотипа.
			plain := stripANSI(line)
			if lipgloss.Width(plain) != 41 {
				t.Errorf("tick %d: banner line width %d != 41: %q", tick, lipgloss.Width(plain), plain)
			}
			for _, r := range plain {
				if r != '█' && r != ' ' {
					t.Errorf("tick %d: banner line has unexpected rune %q: %q", tick, r, plain)
				}
			}
		}
	}
}

func TestModeCommandThemeValidNameSwitchesTheme(t *testing.T) {
	m := testModel(t, nil)
	if m.theme.Name != DefaultThemeName {
		t.Fatalf("expected default theme %q at start, got %q", DefaultThemeName, m.theme.Name)
	}

	m, _ = updateModel(m, keyRune(':'))
	m = typeText(m, "theme yellow")
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatalf(":theme yellow must not return cmd, got %v", cmd)
	}
	if m.theme.Name != "yellow" {
		t.Fatalf("expected theme %q after :theme yellow, got %q", "yellow", m.theme.Name)
	}
	if m.settings.Theme != "yellow" {
		t.Fatalf("expected m.settings.Theme %q, got %q", "yellow", m.settings.Theme)
	}
	if !strings.Contains(m.status, "yellow") {
		t.Errorf("expected status to mention new theme name, got %q", m.status)
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after :theme, got %v", m.mode)
	}
}

func TestModeCommandThemeUnknownNameKeepsThemeAndShowsError(t *testing.T) {
	m := testModel(t, nil)
	originalTheme := m.theme.Name

	m, _ = updateModel(m, keyRune(':'))
	m = typeText(m, "theme doesnotexist")
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})

	if m.theme.Name != originalTheme {
		t.Fatalf("theme must not change on unknown name: got %q, want unchanged %q", m.theme.Name, originalTheme)
	}
	if !strings.Contains(m.status, "Неизвестная тема") {
		t.Errorf("expected error status for unknown theme, got %q", m.status)
	}
}

func TestModeCommandThemeNoArgShowsUsage(t *testing.T) {
	m := testModel(t, nil)

	m, _ = updateModel(m, keyRune(':'))
	m = typeText(m, "theme")
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})

	if !strings.Contains(m.status, ":theme") {
		t.Errorf("expected usage hint mentioning :theme, got %q", m.status)
	}
}

func TestModeCommandUnknownSetsStatusAndReturnsToNormal(t *testing.T) {
	m := testModel(t, nil)
	m, _ = updateModel(m, keyRune(':'))
	m = typeText(m, "xyz")
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if isQuitCmd(cmd) {
		t.Fatal("unknown command must not quit")
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after unknown command, got %v", m.mode)
	}
	if !strings.Contains(m.status, "Неизвестная команда: xyz") {
		t.Fatalf("expected unknown-command status, got %q", m.status)
	}
}

func TestModeCommandEmptyCommandReturnsToNormal(t *testing.T) {
	m := testModel(t, nil)
	m, _ = updateModel(m, keyRune(':'))
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil || isQuitCmd(cmd) {
		t.Fatalf("empty command must not quit, got cmd %v", cmd)
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after empty command, got %v", m.mode)
	}
}

func TestEscInCommandReturnsToNormalWithoutSideEffects(t *testing.T) {
	m := testModel(t, nil)
	m, _ = updateModel(m, keyRune(':'))
	m = typeText(m, "leftover")
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("expected nil cmd on esc in command mode, got %v", cmd)
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after esc in command mode, got %v", m.mode)
	}
	if m.commandInput.Focused() {
		t.Fatal("expected commandInput blurred after esc")
	}
}

func TestModeNormalToInsertAndTyping(t *testing.T) {
	m := testModel(t, nil)
	m.displayedChat = 111
	m, _ = updateModel(m, keyRune('i'))
	if m.mode != modeInsert {
		t.Fatalf("expected modeInsert after 'i', got %v", m.mode)
	}
	if !m.composeInput.Focused() {
		t.Fatal("expected composeInput focused in insert mode")
	}

	m = typeText(m, "привет")
	if m.composeInput.Value() != "привет" {
		t.Fatalf("expected typed text in composeInput, got %q", m.composeInput.Value())
	}
}

func TestEscInInsertReturnsToNormal(t *testing.T) {
	m := testModel(t, nil)
	m.displayedChat = 111
	m, _ = updateModel(m, keyRune('i'))
	m = typeText(m, "draft")
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("expected nil cmd on esc in insert mode, got %v", cmd)
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after esc in insert mode, got %v", m.mode)
	}
	if m.composeInput.Focused() {
		t.Fatal("expected composeInput blurred after esc")
	}
}

func TestInsertEnterSendsMessage(t *testing.T) {
	fake := &fakeClient{responses: []map[string]interface{}{
		{
			"@type":       "message",
			"id":          float64(1),
			"is_outgoing": true,
			"date":        float64(400),
			"content": map[string]interface{}{
				"@type": "messageText",
				"text": map[string]interface{}{
					"@type": "formattedText",
					"text":  "привет",
				},
			},
		},
	}}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = updateModel(m, chatsLoadedMsg{chats: []auth.Chat{{ID: 111, Title: "Чат"}}})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab})   // фокус в список чатов
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter}) // Select: displayedChat = 111
	if m.displayedChat != 111 {
		t.Fatalf("expected displayedChat 111, got %d", m.displayedChat)
	}

	m, _ = updateModel(m, keyRune('i'))
	m = typeText(m, "привет")

	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd after enter in insert mode")
	}
	if !m.sendingMsg {
		t.Fatal("expected sendingMsg after enter")
	}
	if m.mode != modeInsert {
		t.Fatalf("expected modeInsert while sending, got %v", m.mode)
	}
	if m.composeInput.Value() != "привет" {
		t.Fatalf("expected draft preserved while sending, got %q", m.composeInput.Value())
	}

	// Выполняем tea.Cmd вручную: auth.SendMessage против fake-клиента.
	m, _ = updateModel(m, cmd())
	if m.sendingMsg {
		t.Fatal("expected sendingMsg cleared after sendMessageMsg")
	}
	if m.mode != modeInsert {
		t.Fatalf("expected modeInsert to persist after send, got %v", m.mode)
	}
	if !m.composeInput.Focused() {
		t.Fatal("expected composeInput still focused after send")
	}
	if m.composeInput.Value() != "" {
		t.Fatalf("expected composeInput cleared after send, got %q", m.composeInput.Value())
	}
	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message appended, got %d", len(m.messages))
	}
	if m.messages[0].Text != "привет" || m.messages[0].SenderName != "Вы" {
		t.Errorf("unexpected appended message: %+v", m.messages[0])
	}
}

func TestInsertFrozenWhileSending(t *testing.T) {
	m := testModel(t, nil)
	m.displayedChat = 111
	m, _ = updateModel(m, keyRune('i'))
	m = typeText(m, "первый")
	m.sendingMsg = true

	m, cmd := updateModel(m, keyRune('x'))
	if cmd != nil {
		t.Fatalf("expected nil cmd while sending, got %v", cmd)
	}
	if m.composeInput.Value() != "первый" {
		t.Fatalf("expected draft unchanged while sending, got %q", m.composeInput.Value())
	}
	if m.mode != modeInsert {
		t.Fatalf("expected modeInsert while sending, got %v", m.mode)
	}
}

// TestSendFileHotkeyRequiresSelectedChat — ctrl+f без выбранного чата не
// переводит в modeFile: остаётся modeNormal, показывается статус, поле ввода
// пути не фокусируется (тот же гейт, что у EnterInsert после 0022).
func TestSendFileHotkeyRequiresSelectedChat(t *testing.T) {
	m := testModel(t, nil)

	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlF})
	if cmd != nil {
		t.Fatalf("expected nil cmd without selected chat, got %v", cmd)
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal without selected chat, got %v", m.mode)
	}
	if m.status == "" {
		t.Fatal("expected non-empty status without selected chat")
	}
	if m.fileInput.Focused() {
		t.Fatal("fileInput must not be focused without selected chat")
	}
}

// TestSendFileHotkeyEntersFileMode — ctrl+f с выбранным чатом переводит в
// modeFile, поле ввода пути фокусируется.
func TestSendFileHotkeyEntersFileMode(t *testing.T) {
	m := testModel(t, nil)
	m.displayedChat = 111

	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlF})
	if cmd == nil {
		t.Fatal("expected non-nil focus cmd after ctrl+f")
	}
	if m.mode != modeFile {
		t.Fatalf("expected modeFile after ctrl+f, got %v", m.mode)
	}
	if !m.fileInput.Focused() {
		t.Fatal("expected fileInput focused in file mode")
	}
}

// TestFileInputEscReturnsToNormal — Esc в modeFile возвращает в modeNormal,
// поле ввода пути расфокусируется.
func TestFileInputEscReturnsToNormal(t *testing.T) {
	m := testModel(t, nil)
	m.displayedChat = 111
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlF})

	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("expected nil cmd on esc in file mode, got %v", cmd)
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after esc in file mode, got %v", m.mode)
	}
	if m.fileInput.Focused() {
		t.Fatal("expected fileInput blurred after esc")
	}
}

// TestFileInputEmptyEnterReturnsToNormalWithoutSending — Enter с пустым путём
// (в т.ч. из одних пробелов) возвращает в modeNormal без отправки.
func TestFileInputEmptyEnterReturnsToNormalWithoutSending(t *testing.T) {
	for _, path := range []string{"", "   "} {
		fake := &fakeClient{}
		m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
		m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
		m.displayedChat = 111
		m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlF})
		m = typeText(m, path)

		m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
		if cmd != nil {
			t.Fatalf("path %q: expected nil cmd on empty path enter, got %v", path, cmd)
		}
		if m.mode != modeNormal {
			t.Fatalf("path %q: expected modeNormal, got %v", path, m.mode)
		}
		if m.sendingFile {
			t.Fatalf("path %q: sendingFile must not be set", path)
		}
		if fake.sendCount != 0 {
			t.Fatalf("path %q: expected no sends, got %d", path, fake.sendCount)
		}
	}
}

// TestFileInputEnterSendsFile — по образцу TestInsertEnterSendsMessage: Enter с
// непустым путём создаёт команду отправки файла, поле замораживается, а после
// успеха режим ЗАКРЫВАЕТСЯ (возврат в modeNormal) — одноразовое действие, в
// отличие от "липкого" modeInsert после 0022. SendFile внутри шлёт sendMessage
// с inputMessageDocument, ответ — та же форма message JSON, что и в текстовой
// отправке; отправленное сообщение добавляется в ленту текущего чата.
func TestFileInputEnterSendsFile(t *testing.T) {
	fake := &fakeClient{responses: []map[string]interface{}{
		{
			"@type":       "message",
			"id":          float64(1),
			"is_outgoing": true,
			"date":        float64(400),
			"content": map[string]interface{}{
				"@type": "messageDocument",
				"document": map[string]interface{}{
					"@type":     "document",
					"file_name": "файл.txt",
				},
			},
		},
	}}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.displayedChat = 111
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlF})
	m = typeText(m, "/tmp/файл.txt")

	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd after enter in file mode")
	}
	if !m.sendingFile {
		t.Fatal("expected sendingFile after enter")
	}
	if m.mode != modeFile {
		t.Fatalf("expected modeFile while sending, got %v", m.mode)
	}

	// Выполняем tea.Cmd вручную: auth.SendFile против fake-клиента.
	m, _ = updateModel(m, cmd())
	if m.sendingFile {
		t.Fatal("expected sendingFile cleared after sendFileMsg")
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after file send (one-shot), got %v", m.mode)
	}
	if m.fileInput.Focused() {
		t.Fatal("expected fileInput blurred after file send")
	}
	if m.fileInput.Value() != "" {
		t.Fatalf("expected fileInput cleared after file send, got %q", m.fileInput.Value())
	}
	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message appended, got %d", len(m.messages))
	}
}

// TestFileInputFrozenWhileSending — пока отправка файла в полёте, ввод в
// modeFile игнорируется: ни одна руна не попадает в поле, команда не рождается
// (та же защита, что у modeInsert/sendingMsg).
func TestFileInputFrozenWhileSending(t *testing.T) {
	m := testModel(t, nil)
	m.displayedChat = 111
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlF})
	m = typeText(m, "/tmp/a")
	m.sendingFile = true
	valueBefore := m.fileInput.Value()

	m, cmd := updateModel(m, keyRune('x'))
	if cmd != nil {
		t.Fatalf("expected nil cmd while sending, got %v", cmd)
	}
	if m.fileInput.Value() != valueBefore {
		t.Fatalf("expected fileInput unchanged while sending, got %q", m.fileInput.Value())
	}
	if m.mode != modeFile {
		t.Fatalf("expected modeFile while sending, got %v", m.mode)
	}
}

func TestSendMessageFailureKeepsDraftAndReturnsError(t *testing.T) {
	m := testModel(t, nil)
	m.displayedChat = 111
	m, _ = updateModel(m, keyRune('i'))
	m = typeText(m, "черновик")
	m.sendingMsg = true

	m, cmd := updateModel(m, sendMessageMsg{err: errors.New("network")})
	if m.sendingMsg {
		t.Fatal("expected sendingMsg cleared after send error")
	}
	if m.mode != modeInsert {
		t.Fatalf("expected modeInsert after send error, got %v", m.mode)
	}
	if m.composeInput.Value() != "черновик" {
		t.Fatalf("expected draft preserved after send error, got %q", m.composeInput.Value())
	}
	if m.status == "" {
		t.Fatal("expected non-empty status on send error")
	}
	if cmd == nil {
		t.Fatal("expected focus cmd on send error")
	}
}

// voiceTestModel возвращает модель в focusMessages с одним голосовым
// сообщением под курсором.
func voiceTestModel(t *testing.T) Model {
	t.Helper()
	m := testModel(t, nil)
	m.focus = focusMessages
	m.messages = []auth.Message{{ID: 1, Text: "▶ голосовое [1:05]", IsVoiceNote: true, VoiceFileID: 12345, VoiceDuration: 65}}
	m.messageCursor = 0
	return m
}

// TestPlayVoiceHotkeyStartsDownloadCmd — p на голосовом под курсором
// возвращает не-nil команду (саму команду не выполняем: она блокируется на
// ожидании файла), статус-ошибки нет.
func TestPlayVoiceHotkeyStartsDownloadCmd(t *testing.T) {
	m := voiceTestModel(t)

	m, cmd := updateModel(m, keyRune('p'))
	if cmd == nil {
		t.Fatal("expected non-nil cmd for voice note under cursor")
	}
	if m.status != "" {
		t.Errorf("expected no status error, got %q", m.status)
	}
}

// TestPlayVoiceHotkeyNonVoiceMessage — p на обычном сообщении: статус про
// не-голосовое, команды нет.
func TestPlayVoiceHotkeyNonVoiceMessage(t *testing.T) {
	m := testModel(t, nil)
	m.focus = focusMessages
	m.messages = []auth.Message{{ID: 1, Text: "обычное", IsOutgoing: true}}
	m.messageCursor = 0

	m, cmd := updateModel(m, keyRune('p'))
	if cmd != nil {
		t.Fatalf("expected nil cmd on non-voice message, got %v", cmd)
	}
	if !strings.Contains(m.status, "не голосовое") {
		t.Errorf("expected status about non-voice message, got %q", m.status)
	}
}

// TestPlayVoiceHotkeyWrongFocus — p вне панели сообщений: статус про панель,
// команды нет.
func TestPlayVoiceHotkeyWrongFocus(t *testing.T) {
	m := testModel(t, nil)
	m.focus = focusChats
	m.messages = []auth.Message{{ID: 1, IsVoiceNote: true, VoiceFileID: 1}}
	m.messageCursor = 0

	m, cmd := updateModel(m, keyRune('p'))
	if cmd != nil {
		t.Fatalf("expected nil cmd outside messages panel, got %v", cmd)
	}
	if !strings.Contains(m.status, "только в панели сообщений") {
		t.Errorf("expected status about messages panel, got %q", m.status)
	}
}

// TestPlayVoiceHotkeyNoMessageUnderCursor — пустая лента/вырожденный курсор:
// статус, команды нет.
func TestPlayVoiceHotkeyNoMessageUnderCursor(t *testing.T) {
	m := testModel(t, nil)
	m.focus = focusMessages
	m.messages = nil
	m.messageCursor = -1

	m, cmd := updateModel(m, keyRune('p'))
	if cmd != nil {
		t.Fatalf("expected nil cmd without message under cursor, got %v", cmd)
	}
	if !strings.Contains(m.status, "нет сообщения под курсором") {
		t.Errorf("expected status about missing message, got %q", m.status)
	}
}

// TestVoiceFileMsgErrorSetsStatus — ошибка скачивания: статус, playingVoice
// остаётся false.
func TestVoiceFileMsgErrorSetsStatus(t *testing.T) {
	m := testModel(t, nil)

	m, cmd := updateModel(m, voiceFileMsg{err: errors.New("download failed")})
	if cmd != nil {
		t.Fatalf("expected nil cmd on download error, got %v", cmd)
	}
	if m.playingVoice {
		t.Error("expected playingVoice=false on download error")
	}
	if !strings.Contains(m.status, "Ошибка скачивания голосового") {
		t.Errorf("expected download error status, got %q", m.status)
	}
}

// TestVoiceFileMsgSuccessStartsPlayer — файл скачан: playingVoice=true и
// возвращается команда запуска плеера (не выполняем — она идёт в child-режим).
func TestVoiceFileMsgSuccessStartsPlayer(t *testing.T) {
	m := testModel(t, nil)

	m, cmd := updateModel(m, voiceFileMsg{fileID: 1, path: "/tmp/x", err: nil})
	if cmd == nil {
		t.Fatal("expected non-nil player command after download")
	}
	if !m.playingVoice {
		t.Error("expected playingVoice=true after successful download")
	}
}

// TestVoicePlayFinishedMsgNilErr — плеер завершился без ошибки: флаг снят,
// статус пустой.
func TestVoicePlayFinishedMsgNilErr(t *testing.T) {
	m := voiceTestModel(t)
	m.playingVoice = true

	m, cmd := updateModel(m, voicePlayFinishedMsg{err: nil})
	if cmd != nil {
		t.Fatalf("expected nil cmd after playback finished, got %v", cmd)
	}
	if m.playingVoice {
		t.Error("expected playingVoice=false after playback finished")
	}
	if m.status != "" {
		t.Errorf("expected empty status after clean playback, got %q", m.status)
	}
}

// TestVoicePlayFinishedMsgError — плеер упал: флаг снят, статус про ошибку.
func TestVoicePlayFinishedMsgError(t *testing.T) {
	m := voiceTestModel(t)
	m.playingVoice = true

	m, cmd := updateModel(m, voicePlayFinishedMsg{err: errors.New("player crashed")})
	if cmd != nil {
		t.Fatalf("expected nil cmd after playback error, got %v", cmd)
	}
	if m.playingVoice {
		t.Error("expected playingVoice=false after playback error")
	}
	if !strings.Contains(m.status, "Ошибка воспроизведения") {
		t.Errorf("expected playback error status, got %q", m.status)
	}
}

// TestPlaybackHintSuffix — индикатор воспроизведения: пуст, когда не играет,
// содержит ▶, когда играет.
func TestPlaybackHintSuffix(t *testing.T) {
	if got := playbackHintSuffix(false); got != "" {
		t.Errorf("expected empty suffix when not playing, got %q", got)
	}
	if got := playbackHintSuffix(true); !strings.Contains(got, "▶") {
		t.Errorf("expected ▶ in suffix when playing, got %q", got)
	}
}

// TestBottomLineShowsPlayingVoiceIndicator — индикатор идущего воспроизведения
// появляется в нижней строке в Normal-режиме и отсутствует, когда не играет.
func TestBottomLineShowsPlayingVoiceIndicator(t *testing.T) {
	m := testModel(t, nil)
	m.version = ""

	if got := m.bottomLine(); strings.Contains(got, "воспроизведение") {
		t.Errorf("did not expect playback indicator when not playing, got %q", got)
	}
	m.playingVoice = true
	if got := m.bottomLine(); !strings.Contains(got, "▶ воспроизведение") {
		t.Errorf("expected playback indicator while playing, got %q", got)
	}
}

// TestBottomLineShowsVoiceHintInMessagesPanel — в нижней строке при фокусе на
// панели сообщений присутствует пара хоткея "p" с меткой «голосовое».
func TestBottomLineShowsVoiceHintInMessagesPanel(t *testing.T) {
	m := testModel(t, nil)
	m.version = ""
	m.focus = focusMessages

	if got := m.bottomLine(); !strings.Contains(got, "голосовое") {
		t.Errorf("expected voice hint in bottom line for focusMessages, got %q", got)
	}
}

// TestInsertEnterEmptyDraftStaysInInsertWithoutSending — Enter на пустом (или
// только из пробелов) черновике остаётся в Insert-режиме и не отправляет:
// Enter зарезервирован под отправку и не закрывает режим сам по себе
// (закрывает только Esc). Раньше эта ветка ошибочно выкидывала в Normal с
// Blur() фокуса.
func TestInsertEnterEmptyDraftStaysInInsertWithoutSending(t *testing.T) {
	for _, draft := range []string{"", "   "} {
		fake := &fakeClient{}
		m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
		m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
		m.displayedChat = 111
		m, _ = updateModel(m, keyRune('i'))
		m = typeText(m, draft)

		m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
		if cmd != nil {
			t.Fatalf("draft %q: expected nil cmd on empty draft enter, got %v", draft, cmd)
		}
		if m.mode != modeInsert {
			t.Fatalf("draft %q: expected modeInsert, got %v", draft, m.mode)
		}
		if !m.composeInput.Focused() {
			t.Fatalf("draft %q: composeInput must stay focused", draft)
		}
		if m.sendingMsg {
			t.Fatalf("draft %q: sendingMsg must not be set", draft)
		}
		if fake.sendCount != 0 {
			t.Fatalf("draft %q: expected no sends, got %d", draft, fake.sendCount)
		}
	}
}

func TestEnterInsertWithoutSelectedChatShowsStatusAndStaysNormal(t *testing.T) {
	m := testModel(t, nil)

	m, cmd := updateModel(m, keyRune('i'))
	if cmd != nil {
		t.Fatalf("expected nil cmd without selected chat, got %v", cmd)
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal without selected chat, got %v", m.mode)
	}
	if m.status == "" {
		t.Fatal("expected non-empty status without selected chat")
	}
	if m.composeInput.Focused() {
		t.Fatal("composeInput must not be focused without selected chat")
	}
	if m.composeInput.Value() != "" {
		t.Fatalf("expected composeInput empty (never entered insert), got %q", m.composeInput.Value())
	}
}

func TestTypingQInInsertDoesNotQuit(t *testing.T) {
	m := testModel(t, nil)
	m.displayedChat = 111
	m, _ = updateModel(m, keyRune('i'))

	m, cmd := updateModel(m, keyRune('q'))
	if isQuitCmd(cmd) {
		t.Fatal("typing q in insert mode must not trigger quit")
	}
	if m.composeInput.Value() != "q" {
		t.Fatalf("expected q in composeInput, got %q", m.composeInput.Value())
	}

	// Выход из Insert по-прежнему возможен через ctrl+c.
	m, cmd = updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !isQuitCmd(cmd) {
		t.Fatal("expected ctrl+c to quit from insert mode")
	}
}

func TestCtrlCQuitsFromEveryMode(t *testing.T) {
	m := testModel(t, nil)
	m.displayedChat = 111
	m, _ = updateModel(m, keyRune('i')) // Insert
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !isQuitCmd(cmd) {
		t.Fatal("ctrl+c must quit from insert mode")
	}

	m = testModel(t, nil)
	m, _ = updateModel(m, keyRune(':')) // Command
	m, cmd = updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !isQuitCmd(cmd) {
		t.Fatal("ctrl+c must quit from command mode")
	}

	m = testModel(t, nil) // Normal
	m, cmd = updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlC})
	if !isQuitCmd(cmd) {
		t.Fatal("ctrl+c must quit from normal mode")
	}
}

func TestViewShowsModeIndicator(t *testing.T) {
	m := testModel(t, nil)
	// "ELECLi" (строчная "i" в конце — фирменное написание "TELECLi"), не
	// "TELECLi" целиком — см. комментарий в TestBottomLineNormalModeShowsPill
	// (буква "T" стилизована отдельно, ANSI-сброс между "T" и "ELECLi").
	if !strings.Contains(m.View(), "ELECLi") {
		t.Errorf("logo missing:\n%s", m.View())
	}
	if !strings.Contains(m.View(), "NAV") {
		t.Errorf("normal mode indicator (NAV) missing:\n%s", m.View())
	}
	if strings.Contains(m.View(), "-- NORMAL --") {
		t.Errorf("legacy '-- NORMAL --' indicator must be gone:\n%s", m.View())
	}

	m.displayedChat = 111
	m, _ = updateModel(m, keyRune('i'))
	if !strings.Contains(m.View(), "отправить") {
		t.Errorf("insert mode draft hint missing:\n%s", m.View())
	}

	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	m, _ = updateModel(m, keyRune(':'))
	if !strings.Contains(m.View(), m.commandInput.View()) {
		t.Errorf("command mode input missing from view:\n%s", m.View())
	}
}

// Цвета своих и чужих сообщений должны быть различимы — прямой тест функции
// стилизации, без парсинга ANSI-кодов из отрендеренной View() (в go test-среде
// профиль цвета lipgloss может отличаться от интерактивного терминала).
func TestMessageColorDiffersByOutgoing(t *testing.T) {
	th := defaultTheme()
	if messageColor(true, th) == messageColor(false, th) {
		t.Fatal("expected different colors for own and other messages")
	}
}

// Подсветка рамки реально меняет цвет только у панели в фокусе: у неактивной
// цвет рамки не задан (NoColor), у активной — акцентный.
func TestPaneBorderStyleFocusedChangesBorderColor(t *testing.T) {
	th := defaultTheme()
	if paneBorderStyle(true, th, 0).GetBorderTopForeground() == paneBorderStyle(false, th, 0).GetBorderTopForeground() {
		t.Fatal("expected focused pane border color to differ from unfocused")
	}
}

// Регрессия на проверку из файла задачи 0020: замена RoundedBorder на
// DoubleBorder у активной панели не должна менять геометрию. Оба стиля задают
// стороны рамки однорунными строками, поэтому GetHorizontalFrameSize() и
// GetVerticalFrameSize() у фокусированной и нефокусированной рамки равны — если
// в будущем рамка сменится на стиль с другой толщиной, этот тест упадёт.
func TestActiveAndInactiveBorderFrameSizesEqual(t *testing.T) {
	th := defaultTheme()
	if got, want := paneBorderStyle(true, th, 0).GetHorizontalFrameSize(), paneBorderStyle(false, th, 0).GetHorizontalFrameSize(); got != want {
		t.Errorf("horizontal frame: focused %d != unfocused %d", got, want)
	}
	if got, want := paneBorderStyle(true, th, 0).GetVerticalFrameSize(), paneBorderStyle(false, th, 0).GetVerticalFrameSize(); got != want {
		t.Errorf("vertical frame: focused %d != unfocused %d", got, want)
	}
}

// chatSelectionColorANSI возвращает ANSI-последовательность, которой lipgloss
// открывает рендер строки с фоном/текстом цвета пилюли курсора чата:
// рендерим эталонную строку теми же атрибутами, что и ряд курсора, и берём
// префикс до самого символа — так тест не парсит ANSI-коды руками и не
// зависит от того, соединяет ли конкретная версия lipgloss атрибуты в одну
// или несколько последовательностей.
func chatSelectionColorANSI(t *testing.T) string {
	t.Helper()
	th := defaultTheme()
	ref := lipgloss.NewStyle().Background(th.ChatSelectionColor).Foreground(pillTextColor).Render("x")
	return ref[:strings.Index(ref, "x")]
}

// Курсор в списке папок — треугольник-маркер "▸" слева + жирное название, БЕЗ
// цветной пилюли (по правке человека, отличает подсветку папок от чатов).
func TestFolderCursorHighlightUsesTriangleAndBold(t *testing.T) {
	m := testModel(t, nil)
	m.folders = []auth.Folder{{ID: 7, Name: "Работа"}}
	m.folderCursor = 1 // курсор на папке "Работа"

	pane := m.foldersPane()
	if !strings.Contains(pane, "▸") {
		t.Errorf("folder cursor row must contain '▸' marker, pane:\n%s", pane)
	}
	boldOn := lipgloss.NewStyle().Bold(true).Render("x")
	boldPrefix := boldOn[:strings.Index(boldOn, "x")]
	if !strings.Contains(pane, boldPrefix) {
		t.Errorf("folder cursor row must be bold, pane:\n%s", pane)
	}
	if strings.Contains(pane, chatSelectionColorANSI(t)) {
		t.Errorf("folder cursor row must NOT use the chat pill color, pane:\n%s", pane)
	}
}

// Ряды БЕЗ курсора в списке папок — без "▸" и без жирности (маркер только на
// строке курсора, не на всех подряд).
func TestFolderNonCursorRowsHaveNoTriangleOrBold(t *testing.T) {
	m := testModel(t, nil)
	m.folders = []auth.Folder{{ID: 7, Name: "Работа"}, {ID: 8, Name: "Друзья"}}
	m.folderCursor = 1 // курсор на "Работа" (индекс 1), "Друзья" (индекс 2) — без курсора

	pane := m.foldersPane()
	lines := strings.Split(pane, "\n")
	var friendsLine string
	for _, l := range lines {
		if strings.Contains(l, "Друзья") {
			friendsLine = l
			break
		}
	}
	if friendsLine == "" {
		t.Fatalf("could not find 'Друзья' row in pane:\n%s", pane)
	}
	if strings.Contains(friendsLine, "▸") {
		t.Errorf("non-cursor folder row must not contain '▸', got: %q", friendsLine)
	}
}

// То же для списка чатов: ряд под курсором — пилюля chatSelectionColor
// (оранжево-жёлтая, по правке человека).
func TestChatCursorHighlightUsesSelectionColors(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 1, Title: "A"}, {ID: 2, Title: "B"}})
	m.chatCursor = 1 // курсор на чате "B"

	if pane := m.chatPane(); !strings.Contains(pane, chatSelectionColorANSI(t)) {
		t.Errorf("chat cursor row must use chatSelectionColor pill, pane:\n%s", pane)
	}
}

// Заголовок панели начинается с номера-хоткея в квадратных скобках.
func TestPaneTitleShowsNumberPrefix(t *testing.T) {
	th := defaultTheme()
	if s := paneTitle(20, 1, "Папки", true, th); !strings.Contains(s, "[1]") {
		t.Errorf("paneTitle(20, 1, 'Папки', true) must contain '[1]', got: %q", s)
	}
	if s := paneTitle(20, 2, "Чаты", false, th); !strings.Contains(s, "[2]") {
		t.Errorf("paneTitle(20, 2, 'Чаты', false) must contain '[2]', got: %q", s)
	}
}

// Название в заголовке приводится к капсу, без разрядки между буквами —
// исходные строчные буквы в рендере не остаются.
func TestPaneTitleUppercasesWithoutSpacingName(t *testing.T) {
	th := defaultTheme()
	got := paneTitle(30, 1, "чаты", true, th)
	if !strings.Contains(got, "ЧАТЫ") {
		t.Errorf("paneTitle(30, 1, 'чаты', true) must contain 'ЧАТЫ', got: %q", got)
	}
	if strings.Contains(got, "Ч А Т Ы") {
		t.Errorf("paneTitle must not space out letters, got: %q", got)
	}
	if strings.Contains(got, "чаты") {
		t.Errorf("paneTitle must not contain the original lowercase name, got: %q", got)
	}
}

// Длинное название обрезается по ширине с многоточием на конце, без разрядки.
func TestPaneTitleTruncatesLongNameWithEllipsis(t *testing.T) {
	th := defaultTheme()
	got := paneTitle(10, 3, "Очень длинное название чата", true, th)
	if w := lipgloss.Width(got); w != 10 {
		t.Fatalf("paneTitle(10, ...) width = %d, want 10: %q", w, got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("paneTitle must truncate long name with '…', got: %q", got)
	}
}

// Цифровые хоткеи переводят фокус на нужную панель из любого состояния —
// в отличие от Tab/стрелок, которым фокус нужен как отправная точка.
func TestFocusPaneHotkeysJumpDirectly(t *testing.T) {
	m := testModel(t, nil)

	// Из focusMessages «1» — в панель папок.
	m.focus = focusMessages
	m, _ = updateModel(m, keyRune('1'))
	if m.focus != focusFolders {
		t.Fatalf("'1' from focusMessages: expected focusFolders, got %v", m.focus)
	}

	// Из focusFolders «3» — в панель сообщений.
	m, _ = updateModel(m, keyRune('3'))
	if m.focus != focusMessages {
		t.Fatalf("'3' from focusFolders: expected focusMessages, got %v", m.focus)
	}

	// «2» из любого состояния — в список чатов.
	for _, start := range []focus{focusFolders, focusChats, focusMessages} {
		m.focus = start
		m, _ = updateModel(m, keyRune('2'))
		if m.focus != focusChats {
			t.Fatalf("'2' from %v: expected focusChats, got %v", start, m.focus)
		}
	}
}

// TestCollapseHotkeyAcrossFocusCombos — матрица из четырёх комбинаций
// фокуса/свёрнутости для каждой из сворачиваемых панелей (1 и 2): цифра
// панели НЕ в фокусе — фокус на неё (+ авторазворот, если свёрнута); цифра
// панели уже в фокусе — toggle свёрнутости.
func TestCollapseHotkeyAcrossFocusCombos(t *testing.T) {
	cases := []struct {
		name         string
		hotkey       rune
		collapsed    func(*Model) bool
		setCollapsed func(*Model, bool)
		matrix       []struct {
			name          string
			setup         func(*Model)
			wantFocus     focus
			wantCollapsed bool
		}
	}{
		{
			name:   "folders",
			hotkey: '1',
			collapsed: func(m *Model) bool {
				return m.foldersCollapsed
			},
			setCollapsed: func(m *Model, v bool) {
				m.foldersCollapsed = v
			},
			matrix: []struct {
				name          string
				setup         func(*Model)
				wantFocus     focus
				wantCollapsed bool
			}{
				{"not focused + expanded", func(m *Model) { m.focus = focusChats }, focusFolders, false},
				{"not focused + collapsed", func(m *Model) { m.focus = focusChats; m.foldersCollapsed = true }, focusFolders, false},
				{"focused + expanded", func(m *Model) { m.focus = focusFolders }, focusFolders, true},
				{"focused + collapsed", func(m *Model) { m.focus = focusFolders; m.foldersCollapsed = true }, focusFolders, false},
			},
		},
		{
			name:   "chats",
			hotkey: '2',
			collapsed: func(m *Model) bool {
				return m.chatsCollapsed
			},
			setCollapsed: func(m *Model, v bool) {
				m.chatsCollapsed = v
			},
			matrix: []struct {
				name          string
				setup         func(*Model)
				wantFocus     focus
				wantCollapsed bool
			}{
				{"not focused + expanded", func(m *Model) { m.focus = focusMessages }, focusChats, false},
				{"not focused + collapsed", func(m *Model) { m.focus = focusMessages; m.chatsCollapsed = true }, focusChats, false},
				{"focused + expanded", func(m *Model) { m.focus = focusChats }, focusChats, true},
				{"focused + collapsed", func(m *Model) { m.focus = focusChats; m.chatsCollapsed = true }, focusChats, false},
			},
		},
	}
	for _, c := range cases {
		for _, tc := range c.matrix {
			t.Run(c.name+"/"+tc.name, func(t *testing.T) {
				m := testModel(t, []auth.Chat{{ID: 1, Title: "A"}})
				tc.setup(&m)
				m, _ = updateModel(m, keyRune(c.hotkey))
				if m.focus != tc.wantFocus {
					t.Errorf("'%c': focus = %v, want %v", c.hotkey, m.focus, tc.wantFocus)
				}
				if c.collapsed(&m) != tc.wantCollapsed {
					t.Errorf("'%c': collapsed = %v, want %v", c.hotkey, c.collapsed(&m), tc.wantCollapsed)
				}
			})
		}
	}
}

// TestFocusPane3NeverTouchesCollapsedState — хоткей панели 3 не трогает
// сворачиваемые панели ни при каких условиях (свёрнутость оставляем как есть
// даже в несфокусированном состоянии).
func TestFocusPane3NeverTouchesCollapsedState(t *testing.T) {
	for _, start := range []focus{focusFolders, focusChats, focusMessages} {
		m := testModel(t, nil)
		m.focus = start
		m.foldersCollapsed = true
		m.chatsCollapsed = true
		m, _ = updateModel(m, keyRune('3'))
		if m.focus != focusMessages {
			t.Fatalf("'3' from %v: expected focusMessages, got %v", start, m.focus)
		}
		if !m.foldersCollapsed || !m.chatsCollapsed {
			t.Errorf("'3' from %v must not touch collapsed state, folders=%v chats=%v", start, m.foldersCollapsed, m.chatsCollapsed)
		}
	}
}

// TestCollapseHotkeyAdjustsViewportWidth — сворачивание панели увеличивает
// m.viewport.Width ровно на её освободившуюся ширину (foldersPaneW-
// collapsedPaneW / chatsPaneW-collapsedPaneW) — сравниваем applyLayout
// до и после переключения, не константы «на глаз».
func TestCollapseHotkeyAdjustsViewportWidth(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 1, Title: "A"}})
	m.applyLayout()
	widthExpanded := m.viewport.Width

	// Панель 1 (папки): testModel начинает с focusFolders, «1» сворачивает её.
	m, _ = updateModel(m, keyRune('1'))
	if !m.foldersCollapsed {
		t.Fatal("'1' with focusFolders must collapse folders pane")
	}
	m.applyLayout()
	if got, want := m.viewport.Width, widthExpanded+foldersPaneW-collapsedPaneW; got != want {
		t.Errorf("folders collapse: viewport.Width = %d, want %d", got, want)
	}

	// Развернуть обратно и то же самое для панели 2 (чаты).
	m, _ = updateModel(m, keyRune('1'))
	if m.foldersCollapsed {
		t.Fatal("'1' again with focusFolders must expand folders pane")
	}
	m.applyLayout()
	widthExpanded = m.viewport.Width
	m.focus = focusChats
	m, _ = updateModel(m, keyRune('2'))
	if !m.chatsCollapsed {
		t.Fatal("'2' with focusChats must collapse chats pane")
	}
	m.applyLayout()
	if got, want := m.viewport.Width, widthExpanded+chatsPaneW-collapsedPaneW; got != want {
		t.Errorf("chats collapse: viewport.Width = %d, want %d", got, want)
	}
}

// TestCollapseHotkeyRepaintsMessageContent — сворачивание панели хоткеем
// '1'/'2' меняет эффективную ширину ленты, и уже загруженный контент обязан
// перерисоваться (перепектись) под новую ширину. Без этого лента остаётся
// "запечённой" под старую ширину: текст перенесён по старому, более узкому
// тракту, а новые освободившиеся колонки справа недорисованы (0043).
// Проверка — число строк в хранимом контенте (viewport.TotalLineCount):
// узкая ширина даёт больше переносов, широкая — меньше; свежий контент
// после сворачивания обязан совпасть с эталонным рендером под НОВУЮ ширину,
// а не остаться с числом строк от старой.
func TestCollapseHotkeyRepaintsMessageContent(t *testing.T) {
	longText := "одно два три четыре пять шесть семь восемь девять десять"
	m := testModel(t, nil)
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 70, Height: 30})
	m.displayedChat = 111
	m.messages = []auth.Message{{ID: 1, SenderName: "Вы", Text: longText, Date: 100}}
	m.messageCursor = 0

	// Имитация уже открытой ленты (тот же путь, что в messagesLoadedMsg):
	// контент печётся под ширину ДО сворачивания панели.
	m.applyLayout()
	contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
	content, _ := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, m.messageCursor, m.theme, m.chatReadOutbox[m.displayedChat])
	m.viewport.SetContent(content)
	staleLines := m.viewport.TotalLineCount()

	// Хоткей '1' со сворачиванием панели папок: лента становится шире на
	// foldersPaneW-collapsedPaneW — контент обязан перерисоваться под новую
	// ширину (строк меньше, чем под старую).
	m, _ = updateModel(m, keyRune('1'))
	if !m.foldersCollapsed {
		t.Fatal("'1' with focusFolders must collapse folders pane")
	}
	assertMessageContentRepainted(t, m, "после '1'", staleLines)

	// Панель чатов: разворачиваем папки обратно, сворачиваем чаты хоткеем '2'.
	m, _ = updateModel(m, keyRune('1')) // развернуть папки (фокус остался там)
	if m.foldersCollapsed {
		t.Fatal("'1' again with focusFolders must expand folders pane")
	}
	stale2 := m.viewport.TotalLineCount() // контент снова под шириной с обоими развёрнутыми панелями
	m.focus = focusChats
	m, _ = updateModel(m, keyRune('2'))
	if !m.chatsCollapsed {
		t.Fatal("'2' with focusChats must collapse chats pane")
	}
	assertMessageContentRepainted(t, m, "после '2'", stale2)
}

// assertMessageContentRepainted — свежий контент ленты обязан совпасть по
// числу строк с эталонным рендером под ТЕКУЩУЮ ширину m.viewport (перепекли
// контент) и быть короче запечённого под старую, более узкую ширину.
func assertMessageContentRepainted(t *testing.T, m Model, label string, staleLines int) {
	t.Helper()
	got := m.viewport.TotalLineCount()
	want := referenceMessageLineCount(m, m.messages)
	if want >= staleLines {
		t.Fatalf("%s: эталонный перенос под новую ширину обязан быть короче старого, stale=%d fresh=%d — тест не различает ширины", label, staleLines, want)
	}
	if got != want {
		t.Errorf("%s: лента не перерисована под новую ширину: viewport хранит %d строк, эталон под новой шириной — %d (было %d до сворачивания)", label, got, want, staleLines)
	}
}

// referenceMessageLineCount — сколько строк вернул бы renderMessages под
// текущую ширину m.viewport: эталон "свежего" контента для сверки с
// хранимым в viewport.
func referenceMessageLineCount(m Model, msgs []auth.Message) int {
	contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
	content, _ := renderMessages(msgs, contentWidth, m.settings.AlignOwnRight, m.messageCursor, m.theme, m.chatReadOutbox[m.displayedChat])
	return strings.Count(content, "\n") + 1
}

// TestCollapsedPaneTitleHeader — заголовок свёрнутой панели: только "[N]"
// (те же аргументы paneTitle, что использует View() для свёрнутого вида), без
// названия и без scroll-индикатора.
func TestCollapsedPaneTitleHeader(t *testing.T) {
	m := testModel(t, nil)
	m.foldersCollapsed = true
	m.chatsCollapsed = true
	titles := []struct {
		name  string
		title string
		want  string
	}{
		{"folders", paneTitle(m.foldersPaneWidth(), 1, "", m.focus == focusFolders, m.theme), "[1]"},
		{"chats", paneTitle(m.chatsPaneWidth(), 2, "", m.focus == focusChats, m.theme), "[2]"},
	}
	for _, tc := range titles {
		if !strings.Contains(tc.title, tc.want) {
			t.Errorf("%s collapsed title must contain %q, got: %q", tc.name, tc.want, tc.title)
		}
		// Для свёрнутой панели text == "" — названия в заголовке быть не
		// может; scroll-индикатор не передаётся вовсе (0 аргументов вариадика).
		for _, forbidden := range []string{"ПАПКИ", "ЧАТЫ", "▲", "▼", "⇅"} {
			if strings.Contains(tc.title, forbidden) {
				t.Errorf("%s collapsed title must not contain %q, got: %q", tc.name, forbidden, tc.title)
			}
		}
	}
}

// TestCollapsedPanelInView — свёрнутая папка в живом View: заголовочная
// строка начинается с "[1]" без названия и без scroll-индикатора, полного
// слова «ПАПКИ» в кадре нет (буквы уходят в тело панели по одной на строку).
func TestCollapsedPanelInView(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 1, Title: "A"}})
	m, _ = updateModel(m, keyRune('1')) // focusFolders + «1» — сворачиваем папки
	if !m.foldersCollapsed {
		t.Fatal("'1' must collapse folders pane")
	}
	view := m.View()
	titleRow := strings.SplitN(view, "\n", 2)[0]
	if !strings.Contains(titleRow, "[1]") {
		t.Errorf("collapsed folders title row must contain [1], got: %q", titleRow)
	}
	if strings.Contains(titleRow, "ПАПКИ") {
		t.Errorf("collapsed folders title row must not contain the name, got: %q", titleRow)
	}
	for _, g := range []string{"▲", "▼", "⇅"} {
		if strings.Contains(titleRow, g) {
			t.Errorf("collapsed folders title row must not contain scroll indicator %q, got: %q", g, titleRow)
		}
	}
	if strings.Contains(view, "ПАПКИ") {
		t.Errorf("collapsed folders frame must not contain the full name, got: %q", view)
	}
}

// TestCollapsedPaneBodyVerticalName — тело свёрнутой панели: первая строка
// содержимого пустая, дальше — по одной букве капсом на строку в исходном
// порядке. Каждая строка добита до collapsedPaneContentW и буквы
// ЦЕНТРИРОВАНЫ по горизонтали (0046): при collapsedPaneContentW=3 буква ровно
// посередине — пробел-буква-пробел (" Ч "), а не прижата к левому краю
// ("Ч  "). Сравниваем точные строки — это явно ловит отсутствие окружения
// пробелами с обеих сторон.
func TestCollapsedPaneBodyVerticalName(t *testing.T) {
	tests := []struct {
		name string
		want []string
	}{
		{"Чаты", []string{"   ", " Ч ", " А ", " Т ", " Ы "}},
		{"Folders", []string{"   ", " F ", " O ", " L ", " D ", " E ", " R ", " S "}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := collapsedPaneBody(tc.name, 20)
			lines := strings.Split(got, "\n")
			if len(lines) != len(tc.want) {
				t.Fatalf("collapsedPaneBody(%q, 20) = %d lines, want %d: %q", tc.name, len(lines), len(tc.want), got)
			}
			for i := range tc.want {
				if lines[i] != tc.want[i] {
					t.Errorf("line %d = %q, want centered %q (letter must be surrounded by spaces)", i, lines[i], tc.want[i])
				}
			}
		})
	}
}

// TestCollapsedPaneBodyHeightTruncation — обрезка по высоте: при
// contentRows меньше 1+len(letters) функции не паникует и возвращает не
// больше contentRows строк (лишние буквы с конца названия обрезаются).
func TestCollapsedPaneBodyHeightTruncation(t *testing.T) {
	for _, rows := range []int{-1, 0, 1, 2, 3, 6, 10} {
		got := collapsedPaneBody("Папки", rows) // 5 букв + 1 пустая = 6 строк
		if rows <= 0 {
			if got != "" {
				t.Errorf("rows=%d: want empty body, got %q", rows, got)
			}
			continue
		}
		lines := strings.Split(got, "\n")
		if len(lines) > rows {
			t.Errorf("rows=%d: %d lines returned, want <= %d: %q", rows, len(lines), rows, got)
		}
	}
}

// TestCollapsedPaneTitleWidthMatchesPaneWidth — ширина заголовка свёрнутой
// панели равна ширине её тела (тот же класс проверки, что у
// TestPaneTitleWidthMatchesPaneWidth) — и та и другая равны collapsedPaneW.
func TestCollapsedPaneTitleWidthMatchesPaneWidth(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 1, Title: "A"}})
	m.foldersCollapsed = true
	m.chatsCollapsed = true

	columns := []struct {
		name  string
		title string
		pane  string
	}{
		{"folders", paneTitle(m.foldersPaneWidth(), 1, "", m.focus == focusFolders, m.theme), m.foldersPane()},
		{"chats", paneTitle(m.chatsPaneWidth(), 2, "", m.focus == focusChats, m.theme), m.chatPane()},
	}
	for _, c := range columns {
		t.Run(c.name, func(t *testing.T) {
			titleW := lipgloss.Width(strings.SplitN(c.title, "\n", 2)[0])
			paneW := lipgloss.Width(strings.SplitN(c.pane, "\n", 2)[0])
			if titleW != paneW {
				t.Errorf("title line width %d != pane line width %d (title=%q, pane=%q)", titleW, paneW, c.title, c.pane)
			}
			if titleW != collapsedPaneW {
				t.Errorf("collapsed %s title width = %d, want collapsedPaneW %d", c.name, titleW, collapsedPaneW)
			}
		})
	}
}

// TestCollapsedPaneBoxWidthNoPadding — свёрнутая панель (collapsedPaneBox)
// рисуется БЕЗ внутреннего паддинга: итоговая ширина ровно
// collapsedPaneContentW+2 (рамка 2 + контент 3), а не
// collapsedPaneContentW+2+2*panePaddingH, как было с общим paneBox (0044, по
// прямому запросу человека — панель должна быть максимально узкой). Рамка не
// должна рваться: каждая строка рендера одной фиксированной ширины.
func TestCollapsedPaneBoxWidthNoPadding(t *testing.T) {
	th := defaultTheme()
	body := collapsedPaneBody("Папки", 3) // реальное тело свёрнутой панели
	for _, focused := range []bool{false, true} {
		t.Run("focused="+strconv.FormatBool(focused), func(t *testing.T) {
			rendered := collapsedPaneBox(collapsedPaneW, 10, body, focused, th, 1)
			lines := strings.Split(rendered, "\n")
			if len(lines) == 0 {
				t.Fatalf("collapsedPaneBox returned no lines: %q", rendered)
			}
			for i, line := range lines {
				if w := lipgloss.Width(line); w != collapsedPaneW {
					t.Errorf("line %d width = %d, want collapsedPaneW %d: %q", i, w, collapsedPaneW, line)
				}
			}
			if collapsedPaneW != collapsedPaneContentW+2 {
				t.Errorf("collapsedPaneW = %d, want collapsedPaneContentW+2 = %d", collapsedPaneW, collapsedPaneContentW+2)
			}
		})
	}

	// Тот же контент в общем paneBox шире ровно на отсутствующий паддинг:
	// свёрнутая панель на 2*panePaddingH уже при той же рамке и содержимом.
	collapsed := strings.SplitN(collapsedPaneBox(collapsedPaneW, 10, "АБВ", false, th, 1), "\n", 2)[0]
	padded := strings.SplitN(paneBox(collapsedPaneW+2*panePaddingH, 10, "АБВ", false, th, 1), "\n", 2)[0]
	if got, want := lipgloss.Width(collapsed), collapsedPaneContentW+2; got != want {
		t.Errorf("collapsedPaneBox width = %d, want %d", got, want)
	}
	if got, want := lipgloss.Width(padded), collapsedPaneContentW+2+2*panePaddingH; got != want {
		t.Errorf("paneBox (padded) width = %d, want %d", got, want)
	}
}

// Статус-строка Normal-режима без статуса — логотип "TELECLi" + синяя метка
// "NAV" + тусклая подсказка (вместо прежнего "-- NORMAL --"/"NORMAL"-пилюли).
func TestBottomLineNormalModeShowsPill(t *testing.T) {
	m := testModel(t, nil) // status == ""
	got := m.bottomLine()
	// "ELECLi", не "TELECLi" целиком: буква "T" в лого стилизована отдельно
	// (свой Render-вызов, см. telecliLogo) — между "T" и "ELECLi" в
	// отрендеренной строке есть ANSI-сброс стиля, "TELECLi" слитной
	// подстрокой там больше нет, хотя визуально буквы стоят подряд.
	if !strings.Contains(got, "ELECLi") {
		t.Errorf("normal mode bottom line must contain the TELECLi logo, got: %q", got)
	}
	if !strings.Contains(got, "NAV") {
		t.Errorf("normal mode bottom line must contain the NAV tag, got: %q", got)
	}
	if !strings.Contains(got, "←/→") {
		t.Errorf("normal mode bottom line must contain the hint with '←/→', got: %q", got)
	}
}

// TestBottomLineNormalHintIsContextual — подсказка Normal-режима собирается
// условно по фокусу: сначала общие хоткеи (tab/←/→/j/k/i/:/t/q), затем
// контекстные для панели в фокусе. Клавиша "/" сама по себе не различитель —
// она входит в состав "←/→" и есть во всех трёх вариантах, поэтому
// «отсутствие» проверяем только по описаниям и по уникальной подстроке
// "ctrl+f".
func TestBottomLineNormalHintIsContextual(t *testing.T) {
	common := []string{"tab", "t", "q", "панели", "фокус", "курсор", "ввод", "команда", "справка", "выход"}
	search := []string{"поиск"}       // ключ "/" есть везде из-за "←/→", различаем по описанию
	delete := []string{"удалить чат"} // ключ "d" проверяем отдельно ниже (mandatory, chats)
	file := []string{"ctrl+f", "файл"}
	voice := []string{"p", "голосовое"}

	for _, tc := range []struct {
		name       string
		focus      focus
		mandatory  []string
		prohibited []string
	}{
		// В chats дополнительно проверяем наличие самих ключей "/" и "d" —
		// «отсутствие» по ним не проверяем (см. комментарии выше).
		{"folders", focusFolders, common, []string{"поиск", "удалить чат", "d", "ctrl+f", "файл", "p", "голосовое"}},
		{"chats", focusChats, append(append(append(append([]string{}, common...), search...), delete...), "/", "d"), append(append([]string{}, file...), voice...)},
		{"messages", focusMessages, append(append(append([]string{}, common...), file...), voice...), []string{"поиск", "удалить чат"}},
	} {
		m := testModel(t, nil)
		m.version = ""
		m.focus = tc.focus
		got := m.bottomLine()
		for _, s := range tc.mandatory {
			if !strings.Contains(got, s) {
				t.Errorf("%s: expected %q in bottom line, got: %q", tc.name, s, got)
			}
		}
		for _, s := range tc.prohibited {
			if strings.Contains(got, s) {
				t.Errorf("%s: did not expect %q in bottom line, got: %q", tc.name, s, got)
			}
		}
	}
}

// rawMessageUpdate строит "сырой" TDLib-апдейт updateNewMessage с исходящим
// текстовым сообщением — как пришёл бы из client.MessageUpdates(). Исходящее
// сообщение берём, чтобы резолв отправителя (parseMessage → "Вы") не делал
// доп. Send-вызовов и не влиял на подсчёт запросов.
func rawMessageUpdate(chatID float64, text string) map[string]interface{} {
	return map[string]interface{}{
		"@type": "updateNewMessage",
		"message": map[string]interface{}{
			"@type":       "message",
			"chat_id":     chatID,
			"id":          float64(1),
			"is_outgoing": true,
			"date":        float64(300),
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

// runCmd выполняет tea.Cmd, возвращённый из Update. Одиночная команда
// (в т.ч. tea.Batch из одной команды — bubbletea возвращает её напрямую)
// исполняется синхронно самим вызовом; многосоставной батч возвращает
// tea.BatchMsg, команды которого в реальном цикле исполняет execBatchMsg —
// здесь исполняем их по очереди вручную (порядок между разными командами
// батча для проверок не существенен, важна только последовательность внутри
// selectChatCmd: openChat раньше getChatHistory).
func runCmd(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected non-nil cmd")
	}
	if batch, ok := cmd().(tea.BatchMsg); ok {
		for _, c := range batch {
			if c != nil {
				c()
			}
		}
	}
}

// TestWaitForMessageUpdateAppendsToCurrentChat — апдейт для открытого чата
// добавляется в ленту, переподписка (не-nil tea.Cmd) происходит.
func TestWaitForMessageUpdateAppendsToCurrentChat(t *testing.T) {
	fake := &fakeClient{messageCh: make(chan map[string]interface{}, 1)}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.displayedChat = 111

	cmd := m.waitForMessageUpdate()
	fake.messageCh <- rawMessageUpdate(111, "новое")
	msg := cmd()

	m, resub := updateModel(m, msg)
	if resub == nil {
		t.Fatal("expected non-nil resubscription cmd after live message")
	}
	if len(m.messages) != 1 {
		t.Fatalf("expected 1 message appended, got %d", len(m.messages))
	}
	if m.messages[0].Text != "новое" {
		t.Errorf("unexpected appended message: %+v", m.messages[0])
	}
}

// TestWaitForMessageUpdateDoesNotJumpToBottomWhileScrolledUp — правка по
// замечанию человека: лента "сама возвращалась вниз", даже пока читаешь
// историю, потому что newMessageUpdateMsg безусловно звал GotoBottom().
// Если человек прокрутил вверх (не AtBottom()) — входящее сообщение не
// должно двигать прокрутку.
func TestWaitForMessageUpdateDoesNotJumpToBottomWhileScrolledUp(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 111, Title: "Чат"}})
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 10})
	m.displayedChat = 111

	longText := strings.Repeat("длинная строка чтобы контент был выше высоты вьюпорта ", 10)
	m.messages = []auth.Message{
		{ID: 1, Text: longText, SenderName: "Вы", Date: 100},
		{ID: 2, Text: longText, SenderName: "Собеседник", Date: 101},
	}
	contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
	content, _ := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, 0, defaultTheme(), 0)
	m.viewport.SetContent(content)
	m.viewport.SetYOffset(0) // прокрутили наверх, читаем историю
	if m.viewport.AtBottom() {
		t.Fatal("test setup invalid: expected viewport not at bottom before the update")
	}

	m, _ = updateModel(m, newMessageUpdateMsg{chatID: 111, message: auth.Message{ID: 3, Text: "новое", SenderName: "Собеседник", Date: 102}})

	if m.viewport.YOffset != 0 {
		t.Errorf("expected YOffset unchanged (0) while scrolled up, got %d", m.viewport.YOffset)
	}
}

// TestWaitForMessageUpdateStillJumpsToBottomWhenAlreadyThere — симметричный
// случай: если человек и так был внизу (следит за перепиской вживую), новое
// сообщение по-прежнему должно показываться сразу — поведение до этой правки
// сохраняется для этого случая.
func TestWaitForMessageUpdateStillJumpsToBottomWhenAlreadyThere(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 111, Title: "Чат"}})
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.displayedChat = 111
	m.messages = []auth.Message{{ID: 1, Text: "первое", SenderName: "Вы", Date: 100}}
	contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
	content, _ := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, 0, defaultTheme(), 0)
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()

	m, _ = updateModel(m, newMessageUpdateMsg{chatID: 111, message: auth.Message{ID: 2, Text: "новое", SenderName: "Собеседник", Date: 101}})

	if !m.viewport.AtBottom() {
		t.Errorf("expected viewport to stay at bottom after update when already there")
	}
}

// Регрессия (найдена при ревью): messagesLoadedMsg и newMessageUpdateMsg —
// два независимых асинхронных потока (разовый снимок истории и постоянная
// подписка на live-апдейты), ничем не упорядоченных друг относительно друга.
// Если сообщение уже попало в снимок истории (messagesLoadedMsg), а следом
// приходит live-апдейт про то же самое сообщение (тот же ID) — оно не должно
// задваиваться в ленте.
func TestWaitForMessageUpdateSkipsDuplicateAlreadyInHistory(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 111, Title: "Чат"}})
	m.displayedChat = 111
	m.messages = []auth.Message{{ID: 1, Text: "уже в истории", SenderName: "Вы"}}

	m, resub := updateModel(m, newMessageUpdateMsg{chatID: 111, message: auth.Message{ID: 1, Text: "уже в истории", SenderName: "Вы"}})
	if resub == nil {
		t.Fatal("expected non-nil resubscription cmd even for a duplicate")
	}
	if len(m.messages) != 1 {
		t.Fatalf("expected duplicate message to be skipped, got %d messages", len(m.messages))
	}
}

// Регрессия (найдена человеком при живой проверке: "все мои сообщения
// дублируются и отправляются по два"). Причина — чат уже открыт (openChat,
// задача 0008), поэтому TDLib присылает updateNewMessage и про наше же
// исходящее сообщение: sendMessageMsg (наш собственный ответ на отправку) и
// newMessageUpdateMsg (live-апдейт) оба добавляли одно и то же сообщение в
// ленту — дедуп был только в одном из двух обработчиков. Реальной двойной
// отправки на сервер не было, дублировалось только локальное отображение —
// но выглядело для пользователя как отправка "по два". Проверяем оба порядка
// прихода, гонка ничем не упорядочена.
func TestSendMessageMsgSkipsDuplicateFromLiveUpdate(t *testing.T) {
	sent := auth.Message{ID: 42, Text: "привет", SenderName: "Вы", IsOutgoing: true}

	t.Run("sendMessageMsg затем live-апдейт", func(t *testing.T) {
		m := testModel(t, []auth.Chat{{ID: 111, Title: "Чат"}})
		m.displayedChat = 111
		m, _ = updateModel(m, sendMessageMsg{chatID: 111, message: sent})
		if len(m.messages) != 1 {
			t.Fatalf("expected 1 message after sendMessageMsg, got %d", len(m.messages))
		}
		m, _ = updateModel(m, newMessageUpdateMsg{chatID: 111, message: sent})
		if len(m.messages) != 1 {
			t.Fatalf("expected duplicate live-update to be skipped, got %d messages", len(m.messages))
		}
	})

	t.Run("live-апдейт затем sendMessageMsg", func(t *testing.T) {
		m := testModel(t, []auth.Chat{{ID: 111, Title: "Чат"}})
		m.displayedChat = 111
		m, _ = updateModel(m, newMessageUpdateMsg{chatID: 111, message: sent})
		if len(m.messages) != 1 {
			t.Fatalf("expected 1 message after live-update, got %d", len(m.messages))
		}
		m, _ = updateModel(m, sendMessageMsg{chatID: 111, message: sent})
		if len(m.messages) != 1 {
			t.Fatalf("expected duplicate sendMessageMsg to be skipped, got %d messages", len(m.messages))
		}
	})
}

// TestSendMessageMsgSuccessStaysInInsertMode — регрессия на новое поведение:
// после успешной отправки Insert-режим сохраняется (курсор остаётся в поле
// ввода, можно сразу печатать следующее сообщение), очищается только черновик.
func TestSendMessageMsgSuccessStaysInInsertMode(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 111, Title: "Чат"}})
	m.displayedChat = 111
	m.mode = modeInsert
	m.composeInput.Focus()
	m.composeInput.SetValue("ок")

	m, _ = updateModel(m, sendMessageMsg{chatID: 111, message: auth.Message{ID: 1, Text: "ок"}})

	if m.mode != modeInsert {
		t.Fatalf("expected modeInsert to persist after send, got %v", m.mode)
	}
	if !m.composeInput.Focused() {
		t.Fatal("expected composeInput still focused after send")
	}
	if m.composeInput.Value() != "" {
		t.Fatalf("expected composeInput cleared after send, got %q", m.composeInput.Value())
	}
}

// TestWaitForMessageUpdateIgnoresOtherChat — апдейт для чужого чата не меняет
// ленту, но переподписка всё равно происходит (ключевая проверка на ловушку
// задачи: поток апдейтов не должен обрываться из-за игнорируемого апдейта).
func TestWaitForMessageUpdateIgnoresOtherChat(t *testing.T) {
	fake := &fakeClient{messageCh: make(chan map[string]interface{}, 1)}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.displayedChat = 111

	cmd := m.waitForMessageUpdate()
	fake.messageCh <- rawMessageUpdate(222, "чужое")
	msg := cmd()

	m, resub := updateModel(m, msg)
	if resub == nil {
		t.Fatal("expected non-nil resubscription cmd even for ignored update")
	}
	if len(m.messages) != 0 {
		t.Fatalf("foreign chat update must not append, got %d messages", len(m.messages))
	}
}

// TestWaitForMessageUpdateClosedChannelStopsResubscribing — закрытый канал
// (клиент остановлен/контекст отменён) — единственный случай без переподписки.
func TestWaitForMessageUpdateClosedChannelStopsResubscribing(t *testing.T) {
	fake := &fakeClient{messageCh: make(chan map[string]interface{}, 1)}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")

	cmd := m.waitForMessageUpdate()
	close(fake.messageCh)
	msg := cmd()

	m, resub := updateModel(m, msg)
	if resub != nil {
		t.Fatal("closed channel must not resubscribe, expected nil cmd")
	}
}

// TestTranslateLayout — табличный тест прямой функции: представительная выборка
// пар ЙЦУКЕН→QWERTY (используемые в дефолтных биндингах + пара случайных),
// спецклавиши и уже латинская руна возвращаются без изменений.
func TestTranslateLayout(t *testing.T) {
	translated := []struct {
		cyr, lat rune
	}{
		{'о', 'j'}, {'л', 'k'}, {'ш', 'i'}, {'й', 'q'}, {'Ж', ':'},
		{'я', 'z'}, {'ю', '.'}, {'э', '\''}, {'б', ','}, {'Щ', 'O'},
	}
	for _, tc := range translated {
		got := translateLayout(keyRune(tc.cyr))
		if got.Type != tea.KeyRunes || len(got.Runes) != 1 || got.Runes[0] != tc.lat {
			t.Errorf("translateLayout(%q) = %v, want rune %q", tc.cyr, got, tc.lat)
		}
	}

	// Спецклавиши не трогаются: не KeyRunes — возвращаются как есть.
	for _, msg := range []tea.KeyMsg{
		{Type: tea.KeyEnter},
		{Type: tea.KeyUp},
		{Type: tea.KeyCtrlC},
	} {
		if got := translateLayout(msg); !reflect.DeepEqual(got, msg) {
			t.Errorf("translateLayout(%v) must return special key unchanged, got %v", msg, got)
		}
	}

	// Уже латинская руна, которой нет в таблице, — без изменений.
	j := keyRune('j')
	if got := translateLayout(j); !reflect.DeepEqual(got, j) {
		t.Errorf("translateLayout('j') must be unchanged, got %v", got)
	}

	// Многосимвольный ввод — тоже без изменений.
	multi := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'о', 'л'}}
	if got := translateLayout(multi); !reflect.DeepEqual(got, multi) {
		t.Errorf("translateLayout(multi-rune) must be unchanged, got %v", got)
	}
}

// TestNormalHotkeysWorkWithCyrillicLayout покрывает каждый буквенный дефолтный
// биндинг Normal-режима: кириллический эквивалент той же физической клавиши
// даёт то же поведение, что и латинская буква (задача 0009).
func TestNormalHotkeysWorkWithCyrillicLayout(t *testing.T) {
	chats := []auth.Chat{{ID: 1, Title: "A"}, {ID: 2, Title: "B"}}

	t.Run("MoveDown", func(t *testing.T) {
		m := testModel(t, chats)
		m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab}) // фокус в список чатов
		m, _ = updateModel(m, keyRune('о'))                 // физически 'j'
		assertCursor(t, m, 1)
	})

	t.Run("MoveUp", func(t *testing.T) {
		m := testModel(t, chats)
		m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab}) // фокус в список чатов
		m, _ = updateModel(m, keyRune('j'))
		m, _ = updateModel(m, keyRune('л')) // физически 'k'
		assertCursor(t, m, 0)
	})

	t.Run("EnterInsert", func(t *testing.T) {
		m := testModel(t, nil)
		m.displayedChat = 111
		m, _ = updateModel(m, keyRune('ш')) // физически 'i'
		if m.mode != modeInsert {
			t.Fatalf("expected modeInsert after 'ш', got %v", m.mode)
		}
	})

	t.Run("Quit", func(t *testing.T) {
		m := testModel(t, nil)
		_, cmd := updateModel(m, keyRune('й')) // физически 'q'
		if !isQuitCmd(cmd) {
			t.Fatalf("expected quit cmd for 'й', got %v", cmd)
		}
	})

	t.Run("EnterCommand", func(t *testing.T) {
		m := testModel(t, nil)
		m, _ = updateModel(m, keyRune('Ж')) // физически ':' (Shift+;)
		if m.mode != modeCommand {
			t.Fatalf("expected modeCommand after 'Ж', got %v", m.mode)
		}
		if !m.commandInput.Focused() {
			t.Fatal("expected commandInput focused after 'Ж'")
		}
	})
}

// TestCyrillicTypingInInsertIsNotTranslated — критичный регрессионный тест (по
// важности как TestTypingQInInsertDoesNotQuit): 'о' в Normal-режиме
// транслитерируется в 'j'/move_down, но в Insert это обычный символ кириллицы
// и должен остаться в черновике как есть, не двигая курсор и не меняя режим.
func TestCyrillicTypingInInsertIsNotTranslated(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 1, Title: "A"}, {ID: 2, Title: "B"}})
	m.displayedChat = 111
	m, _ = updateModel(m, keyRune('i'))
	cursorBefore := m.chatCursor
	focusBefore := m.focus

	m, _ = updateModel(m, keyRune('о'))
	if m.composeInput.Value() != "о" {
		t.Fatalf("expected literal 'о' in composeInput, got %q", m.composeInput.Value())
	}
	if m.mode != modeInsert {
		t.Fatalf("expected modeInsert unchanged, got %v", m.mode)
	}
	if m.chatCursor != cursorBefore {
		t.Fatalf("chat cursor must not move in insert mode: %d -> %d", cursorBefore, m.chatCursor)
	}
	if m.focus != focusBefore {
		t.Fatalf("focus must not change in insert mode: %v -> %v", focusBefore, m.focus)
	}
}

// TestCyrillicTypingInCommandIsNotTranslated — тот же регрессионный случай для
// Command-режима: кириллица в командной строке не транслитерируется.
func TestCyrillicTypingInCommandIsNotTranslated(t *testing.T) {
	m := testModel(t, nil)
	m, _ = updateModel(m, keyRune(':'))
	m, _ = updateModel(m, keyRune('о'))

	if m.commandInput.Value() != "о" {
		t.Fatalf("expected literal 'о' in commandInput, got %q", m.commandInput.Value())
	}
	if m.mode != modeCommand {
		t.Fatalf("expected modeCommand unchanged, got %v", m.mode)
	}
}

// TestSelectChatCallsOpenThenGetHistory — выбор чата отправляет openChat РАНЬШЕ
// getChatHistory (не только оба, но именно в этом порядке) — это и есть баг,
// который чинит задача: без openChat история может не отдаться целиком.
func TestSelectChatCallsOpenThenGetHistory(t *testing.T) {
	chats := []auth.Chat{{ID: 111, Title: "Первый"}}
	fake := &fakeClient{}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = updateModel(m, chatsLoadedMsg{chats: chats})

	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab}) // фокус в список чатов
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd after select")
	}
	runCmd(t, cmd)

	if len(fake.requests) < 2 {
		t.Fatalf("expected at least openChat + getChatHistory, got %d requests: %v", len(fake.requests), fake.requests)
	}
	if fake.requests[0]["@type"] != "openChat" || fake.requests[0]["chat_id"] != int64(111) {
		t.Errorf("expected first request openChat(111), got %v", fake.requests[0])
	}
	if fake.requests[1]["@type"] != "getChatHistory" || fake.requests[1]["chat_id"] != int64(111) {
		t.Errorf("expected second request getChatHistory(111), got %v", fake.requests[1])
	}
}

// TestSelectingAnotherChatClosesPrevious — выбор второго чата закрывает первый
// (closeChat с его id), а выбор ПЕРВОГО чата (displayedChat ещё 0) — не закрывает
// ничего.
func TestSelectingAnotherChatClosesPrevious(t *testing.T) {
	chats := []auth.Chat{{ID: 111, Title: "Первый"}, {ID: 222, Title: "Второй"}}
	fake := &fakeClient{}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = updateModel(m, chatsLoadedMsg{chats: chats})

	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab})      // фокус в список чатов
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter}) // выбор чата A (111)
	if cmd == nil {
		t.Fatal("expected non-nil cmd after first select")
	}
	runCmd(t, cmd)

	for _, r := range fake.requests {
		if r["@type"] == "closeChat" {
			t.Fatal("first chat selection (displayedChat == 0) must not close anything")
		}
	}

	// Back (esc) из ленты сообщений возвращает в список чатов (Tab из
	// focusMessages ушёл бы в панель папок — с трёхпанельным циклом).
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyDown}) // курсор на чат B (222)
	m, cmd = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd after second select")
	}
	runCmd(t, cmd)

	found := false
	for _, r := range fake.requests {
		if r["@type"] == "closeChat" && r["chat_id"] == int64(111) {
			found = true
		}
	}
	if !found {
		t.Errorf("expected closeChat(111) after switching to chat B, requests: %v", fake.requests)
	}
	if m.displayedChat != 222 {
		t.Errorf("expected displayedChat 222, got %d", m.displayedChat)
	}
}

// TestCtrlJInsertsNewlineNotSend — ctrl+j в Insert-режиме добавляет перенос
// строки в черновик и НЕ отправляет сообщение (отправка остаётся за Enter).
func TestCtrlJInsertsNewlineNotSend(t *testing.T) {
	fake := &fakeClient{}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.displayedChat = 111
	m, _ = updateModel(m, keyRune('i'))
	m = typeText(m, "строка1")

	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlJ})
	if m.sendingMsg {
		t.Fatal("ctrl+j must not trigger sending")
	}
	if fake.sendCount != 0 {
		t.Fatalf("ctrl+j must not send, got %d sends", fake.sendCount)
	}
	if cmd != nil {
		if _, ok := cmd().(sendMessageMsg); ok {
			t.Fatal("ctrl+j must not return a send command")
		}
	}
	if m.mode != modeInsert {
		t.Fatalf("expected modeInsert after ctrl+j, got %v", m.mode)
	}
	if m.composeInput.Value() != "строка1\n" {
		t.Fatalf("expected newline appended to draft, got %q", m.composeInput.Value())
	}
}

// TestEnterSendsMultilineMessage — черновик с уже вставленным переносом строки
// отправляется ЦЕЛИКОМ, а не обрезается до первой строки.
func TestEnterSendsMultilineMessage(t *testing.T) {
	const draft = "первая\nвторая"
	fake := &fakeClient{responses: []map[string]interface{}{
		{
			"@type":       "message",
			"id":          float64(1),
			"is_outgoing": true,
			"date":        float64(400),
			"content": map[string]interface{}{
				"@type": "messageText",
				"text": map[string]interface{}{
					"@type": "formattedText",
					"text":  draft,
				},
			},
		},
	}}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = updateModel(m, chatsLoadedMsg{chats: []auth.Chat{{ID: 111, Title: "Чат"}}})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab})   // фокус в список чатов
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter}) // Select: displayedChat = 111
	m, _ = updateModel(m, keyRune('i'))
	m.composeInput.SetValue(draft)

	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd after enter in insert mode")
	}
	if !m.sendingMsg {
		t.Fatal("expected sendingMsg after enter")
	}
	m, _ = updateModel(m, cmd())

	var sent string
	for _, r := range fake.requests {
		if r["@type"] != "sendMessage" {
			continue
		}
		content, ok := r["input_message_content"].(map[string]interface{})
		if !ok {
			continue
		}
		textObj, ok := content["text"].(map[string]interface{})
		if !ok {
			continue
		}
		sent, _ = textObj["text"].(string)
	}
	if sent != draft {
		t.Fatalf("expected full multiline text %q sent, got %q", draft, sent)
	}
	if len(m.messages) != 1 || m.messages[0].Text != draft {
		t.Fatalf("expected appended multiline message, got %+v", m.messages)
	}
}

// TestApplyLayoutShrinksBodyInInsertMode — вход в Insert-режим уменьшает высоту
// viewport ровно на разницу бюджетов (composeAreaHeight+1 вместо statusReserve).
// По правке человека (задача 0047) черновик больше не резервирует отдельную
// область ПОД всеми тремя панелями (bottomReserve/statusReserve не меняется по
// режиму вовсе), а встроен чёрной областью внутрь единой рамки панели
// сообщений: вьюпорт сжимается на paneFrameV (бордюр+паддинги единой рамки)
// плюс высоту поля ввода composeCardHeight(), ставшую просто composeInput.Height().
func TestApplyLayoutShrinksBodyInInsertMode(t *testing.T) {
	m := testModel(t, nil)
	normalHeight := m.viewport.Height

	m.displayedChat = 111
	m, _ = updateModel(m, keyRune('i'))
	want := normalHeight - paneFrameV - m.composeCardHeight()
	if m.viewport.Height != want {
		t.Fatalf("expected viewport.Height %d in insert mode, got %d", want, m.viewport.Height)
	}
	// paneRowHeight (общий бюджет для всех панелей) НЕ должен меняться —
	// в отличие от m.viewport.Height, папки/чаты не сжимаются.
	if m.paneRowHeight != normalHeight {
		t.Fatalf("expected paneRowHeight unchanged (%d), got %d", normalHeight, m.paneRowHeight)
	}
}

// TestApplyLayoutRestoresBodyOnExitInsert — выход из Insert-режима (Esc)
// возвращает высоту viewport к значению в Normal-режиме.
func TestApplyLayoutRestoresBodyOnExitInsert(t *testing.T) {
	m := testModel(t, nil)
	normalHeight := m.viewport.Height

	m.displayedChat = 111
	m, _ = updateModel(m, keyRune('i'))
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.viewport.Height != normalHeight {
		t.Fatalf("expected viewport.Height restored to %d, got %d", normalHeight, m.viewport.Height)
	}
}

// rawChatFoldersUpdate строит "сырой" TDLib-апдейт updateChatFolders —
// как пришёл бы из client.ChatFolderUpdates(). Структура элемента зеркалит
// то, что адекватно разбирает auth.ParseChatFoldersUpdate (id + name.text.text).
func rawChatFoldersUpdate(folders ...map[string]interface{}) map[string]interface{} {
	list := make([]interface{}, 0, len(folders))
	for _, f := range folders {
		list = append(list, f)
	}
	return map[string]interface{}{
		"@type":        "updateChatFolders",
		"chat_folders": list,
	}
}

// TestFolderNavigationClamps — MoveDown/MoveUp в focusFolders не выходят за
// границы [0, len(m.folders)]: синтетический пункт "Все чаты" (0) — нижняя
// граница, len(m.folders) (последняя папка) — верхняя.
func TestFolderNavigationClamps(t *testing.T) {
	m := testModel(t, nil)
	m.folders = []auth.Folder{{ID: 1, Name: "A"}, {ID: 2, Name: "B"}}
	if m.focus != focusFolders {
		t.Fatalf("expected initial focusFolders, got %v", m.focus)
	}

	for i := 0; i < 5; i++ {
		m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.folderCursor != len(m.folders) {
		t.Fatalf("expected folderCursor clamped to %d, got %d", len(m.folders), m.folderCursor)
	}

	for i := 0; i < 5; i++ {
		m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.folderCursor != 0 {
		t.Fatalf("expected folderCursor clamped to 0, got %d", m.folderCursor)
	}
}

// TestSelectFolderReloadsChatsWithCorrectChatList — выбор пункта 0 («Все
// чаты») грузит чаты из chatListMain, выбор папки с индексом 1 — из
// chatListFolder с правильным chat_folder_id. Фейковый клиент фиксирует
// отправленные запросы.
func TestSelectFolderReloadsChatsWithCorrectChatList(t *testing.T) {
	fake := &fakeClient{}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.folders = []auth.Folder{{ID: 7, Name: "Работа"}}
	m.folderCursor = 1

	// Выбор папки (cursor 1) — chatListFolder(7).
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd after selecting folder")
	}
	runCmd(t, cmd)

	// Возврат к папкам: после выбора фокус уходит в список чатов, обратно —
	// двумя Tab (chats -> messages -> folders). Cursor на "Все чаты" (0).
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab}) // chats -> messages
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab}) // messages -> folders
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyUp})  // cursor 1 -> 0
	m, cmd = updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd after selecting 'Все чаты'")
	}
	runCmd(t, cmd)

	var loadMain, loadFolder bool
	for _, r := range fake.requests {
		chatList, _ := r["chat_list"].(map[string]interface{})
		switch chatList["@type"] {
		case "chatListMain":
			loadMain = true
		case "chatListFolder":
			// f.ID — int32 напрямую в запросе (не float64, как в сыром JSON).
			if id, ok := chatList["chat_folder_id"].(int32); ok && id == 7 {
				loadFolder = true
			}
		}
	}
	if !loadMain {
		t.Errorf("expected loadChats/getChats with chatListMain, requests: %v", fake.requests)
	}
	if !loadFolder {
		t.Errorf("expected loadChats/getChats with chatListFolder(7), requests: %v", fake.requests)
	}
}

// TestSelectFolderClosesPreviousChatAndResetsMessages — если перед переключением
// папки был открыт чат (displayedChat != 0), после переключения displayedChat
// сбрасывается в 0, лента очищается, а closeChatCmd для старого чата вызван
// (тот же паттерн проверки через fakeClient, что в
// TestSelectingAnotherChatClosesPrevious).
func TestSelectFolderClosesPreviousChatAndResetsMessages(t *testing.T) {
	fake := &fakeClient{}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.folders = []auth.Folder{{ID: 7, Name: "Работа"}}
	m.folderCursor = 1
	m.displayedChat = 111
	m.messages = []auth.Message{{ID: 1, Text: "старое", SenderName: "X"}}

	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd after selecting folder")
	}
	runCmd(t, cmd)

	if m.displayedChat != 0 {
		t.Errorf("expected displayedChat reset to 0, got %d", m.displayedChat)
	}
	if len(m.messages) != 0 {
		t.Errorf("expected messages cleared, got %d", len(m.messages))
	}
	if m.focus != focusChats {
		t.Errorf("expected focusChats after selecting folder, got %v", m.focus)
	}

	closed := false
	for _, r := range fake.requests {
		if r["@type"] == "closeChat" && r["chat_id"] == int64(111) {
			closed = true
		}
	}
	if !closed {
		t.Errorf("expected closeChat(111) after switching folder, requests: %v", fake.requests)
	}
}

// TestChatsLoadedMsgIgnoresStaleFolderResponse — если пользователь успел
// переключиться на другую папку до того, как долетел ответ на запрос чатов
// для ранее выбранной, устаревший ответ должен молча отбрасываться (тот же
// класс гонки, что уже покрыт для messagesLoadedMsg.chatID).
func TestChatsLoadedMsgIgnoresStaleFolderResponse(t *testing.T) {
	fake := &fakeClient{}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	// Пользователь уже в папке 9 (актуальный выбор).
	m.selectedFolderID = 9
	m.chats = []auth.Chat{{ID: 1, Title: "актуальный чат"}}

	// Прилетает устаревший ответ от ранее выбранной папки 7.
	m, cmd := updateModel(m, chatsLoadedMsg{folderID: 7, chats: []auth.Chat{{ID: 2, Title: "устаревший чат"}}})
	if cmd != nil {
		t.Errorf("expected nil cmd for stale chatsLoadedMsg, got %v", cmd)
	}
	if len(m.chats) != 1 || m.chats[0].ID != 1 {
		t.Errorf("expected chats unchanged by stale response, got %v", m.chats)
	}

	// Актуальный ответ для папки 9 применяется как обычно.
	m, _ = updateModel(m, chatsLoadedMsg{folderID: 9, chats: []auth.Chat{{ID: 3, Title: "новый чат"}}})
	if len(m.chats) != 1 || m.chats[0].ID != 3 {
		t.Errorf("expected chats updated by matching response, got %v", m.chats)
	}
}

// TestChatFoldersUpdateMsgUpdatesFoldersAndResubscribes — апдейт папок через
// waitForChatFolders применяется к m.folders, возвращённый tea.Cmd не nil
// (переподписка). По образцу TestWaitForMessageUpdateAppendsToCurrentChat.
func TestChatFoldersUpdateMsgUpdatesFoldersAndResubscribes(t *testing.T) {
	fake := &fakeClient{folderCh: make(chan map[string]interface{}, 1)}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	cmd := m.waitForChatFolders()
	fake.folderCh <- rawChatFoldersUpdate(
		map[string]interface{}{"id": float64(7), "name": map[string]interface{}{"text": map[string]interface{}{"text": "Работа"}}},
	)
	msg := cmd()

	m, resub := updateModel(m, msg)
	if resub == nil {
		t.Fatal("expected non-nil resubscription cmd after folders update")
	}
	if len(m.folders) != 1 || m.folders[0].ID != 7 || m.folders[0].Name != "Работа" {
		t.Fatalf("unexpected folders after update: %+v", m.folders)
	}
}

// TestChatFoldersUpdateClampsCursorWhenFoldersShrink — folderCursor указывал на
// папку, которая пропала из нового апдейта → курсор клампится в допустимый
// диапазон [0, len(m.folders)], не остаётся «за концом».
func TestChatFoldersUpdateClampsCursorWhenFoldersShrink(t *testing.T) {
	m := testModel(t, nil)
	m.folders = []auth.Folder{{ID: 1}, {ID: 2}, {ID: 3}}
	m.folderCursor = 3

	m, resub := updateModel(m, chatFoldersUpdateMsg{folders: []auth.Folder{{ID: 1}}})
	if resub == nil {
		t.Fatal("expected non-nil resubscription cmd after folders update")
	}
	if m.folderCursor != 1 {
		t.Fatalf("expected folderCursor clamped to 1, got %d", m.folderCursor)
	}
}

// TestChatFoldersUpdateMsgClosedStopsResubscribing — закрытый канал (клиент
// остановлен/контекст отменён) — единственный случай без переподписки, тот же
// контракт, что у newMessageUpdateMsg.
func TestChatFoldersUpdateMsgClosedStopsResubscribing(t *testing.T) {
	fake := &fakeClient{folderCh: make(chan map[string]interface{}, 1)}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")

	cmd := m.waitForChatFolders()
	close(fake.folderCh)
	msg := cmd()

	m, resub := updateModel(m, msg)
	if resub != nil {
		t.Fatal("closed channel must not resubscribe, expected nil cmd")
	}
}

// TestViewTotalWidthFitsTerminal — регрессия на класс бага, который ловили
// четыре раза (0004/0005/0013/0015) и который эта задача расширяет до трёх
// панелей + строки заголовков: суммарная ширина колонок панелей (папки +
// список чатов + лента) и строка заголовков НАД ними не должны превышать
// ширину терминала — иначе строка переносится и раскладка едет.
// Задача 0020 добавила обычной строке Normal-режима фиксированную подсказку
// (логотип+метка режима + текст подсказки, ~85 колонок) — на узких терминалах
// она по замыслу шире экрана и переносится терминалом (это не класс бага
// раскладки, который ловит этот тест), поэтому строгая проверка ширины
// применяется к заголовкам и панелям, а нижняя строка — только на наличие
// метки режима.
func TestViewTotalWidthFitsTerminal(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 1, Title: "A"}})
	m.folders = []auth.Folder{{ID: 7, Name: "Работа"}}

	for _, width := range []int{100, 80, 60} {
		m.width = width
		m.applyLayout()
		lines := strings.Split(m.View(), "\n")
		if len(lines) < 3 {
			t.Fatalf("width %d: expected titles+panes+bottom rows in View, got %d", width, len(lines))
		}
		// Первая строка — заголовки панелей, строки между ней и последней —
		// сами панели: обе группы должны укладываться в ширину терминала.
		if w := lipgloss.Width(lines[0]); w > width {
			t.Errorf("width %d: titles row wider than terminal (%d): %q", width, w, lines[0])
		}
		for _, line := range lines[1 : len(lines)-1] {
			if w := lipgloss.Width(line); w > width {
				t.Errorf("width %d: pane line wider than terminal (%d): %q", width, w, line)
			}
		}
		// Последняя строка — нижняя область Normal-режима (пилюля + подсказка):
		// длинная фиксированная подсказка, на узких терминалах переносится
		// терминалом по замыслу — проверяем только её наличие с пилюлей.
		bottom := lines[len(lines)-1]
		if !strings.Contains(bottom, "NAV") {
			t.Errorf("width %d: bottom line missing NAV tag: %q", width, bottom)
		}
	}
}

// TestBottomLineInsertHeightMatchesBudget — регрессия на рассинхронизацию
// бюджета высоты (тот же класс бага, что чинили в 0004/0005/0013): нижняя
// область в Insert-режиме занимает ровно composeAreaHeight+1 строк — независимо
// от числа строк и длины черновика (высота поля фиксирована, длинный черновик
// прокручивается внутренним viewport, а не растягивает бюджет).
// TestBottomLineInsertAlwaysOneLine — по правке человека черновик переехал
// в msgPane() отдельной карточкой; bottomLine() в Insert-режиме теперь
// ВСЕГДА ровно 1 строка (логотип+метка+подсказка), независимо от длины
// черновика — в отличие от старого поведения, где сам черновик рисовался
// прямо в bottomLine() и её высота росла вместе с ним.
func TestBottomLineInsertAlwaysOneLine(t *testing.T) {
	m := testModel(t, nil)
	m.displayedChat = 111
	m, _ = updateModel(m, keyRune('i'))

	for _, draft := range []string{"", "одна строка", "первая\nвторая\nтретья\nчетвёртая\nпятая", strings.Repeat("длинный ", 50)} {
		m.composeInput.SetValue(draft)
		m.syncComposeHeight()
		got := m.bottomLine()
		if lines := strings.Split(got, "\n"); len(lines) != 1 {
			t.Fatalf("draft %q: expected exactly 1 row in insert bottom line, got %d:\n%q", draft, len(lines), got)
		}
	}
}

// TestFocusRightMovesThroughPanesWithoutWrapping — «→» переносит фокус на
// панель правее: folders → chats → messages. В отличие от циклического Tab,
// на крайней правой панели (messages) третий «→» ничего не меняет: у
// направленного движения есть естественная граница, в то время как Tab
// вернул бы фокус в folders.
func TestFocusRightMovesThroughPanesWithoutWrapping(t *testing.T) {
	m := testModel(t, nil)
	if m.focus != focusFolders {
		t.Fatalf("expected initial focusFolders, got %v", m.focus)
	}

	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.focus != focusChats {
		t.Fatalf("expected focusChats after first right, got %v", m.focus)
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.focus != focusMessages {
		t.Fatalf("expected focusMessages after second right, got %v", m.focus)
	}

	// Граница: третий right не зацикливает в folders (в отличие от Tab).
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyRight})
	if m.focus != focusMessages {
		t.Fatalf("expected focusMessages unchanged on right boundary, got %v", m.focus)
	}
}

// TestFocusLeftMovesThroughPanesWithoutWrapping — зеркально к правому тесту:
// «←» переносит фокус на панель левее, на крайней левой панели (folders)
// ничего не меняет.
func TestFocusLeftMovesThroughPanesWithoutWrapping(t *testing.T) {
	m := testModel(t, nil)
	m.focus = focusMessages

	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.focus != focusChats {
		t.Fatalf("expected focusChats after first left, got %v", m.focus)
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.focus != focusFolders {
		t.Fatalf("expected focusFolders after second left, got %v", m.focus)
	}

	// Граница: третий left на крайней левой панели ничего не меняет.
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyLeft})
	if m.focus != focusFolders {
		t.Fatalf("expected focusFolders unchanged on left boundary, got %v", m.focus)
	}
}

// TestViewIncludesPaneTitles — у каждой панели есть строка-заголовок НАД ней
// (первая строка View): «[1] ПАПКИ», «[2] ЧАТЫ», а у панели сообщений —
// название открытого чата (или «Сообщения», пока ничего не выбрано); названия
// капсом, без разрядки между буквами.
func TestViewIncludesPaneTitles(t *testing.T) {
	chats := []auth.Chat{{ID: 111, Title: "Тестовый чат"}}
	m := testModel(t, chats)

	// displayedChat == 0 — заголовок панели сообщений статичный «Сообщения».
	titleRow := strings.SplitN(m.View(), "\n", 2)[0]
	for _, want := range []string{"[1] ПАПКИ", "[2] ЧАТЫ", "СООБЩЕНИЯ"} {
		if !strings.Contains(titleRow, want) {
			t.Errorf("title row missing %q, got:\n%s", want, titleRow)
		}
	}

	// После выбора чата заголовок панели сообщений — название этого чата.
	m.displayedChat = 111
	m.messages = []auth.Message{{ID: 1, SenderName: "Вы", Text: "hi", Date: 100}}
	titleRow = strings.SplitN(m.View(), "\n", 2)[0]
	if !strings.Contains(titleRow, "ТЕСТОВЫЙ") {
		t.Errorf("title row must show open chat name after selection, got:\n%s", titleRow)
	}
}

// TestPaneTitleWidthMatchesPaneWidth — заголовок каждой колонки той же ширины,
// что и сама панель под ним (тот же класс проверки, что в
// TestViewTotalWidthFitsTerminal, только для пары заголовок/панель) — иначе
// при JoinHorizontal заголовки разъедутся с панелями.
func TestPaneTitleWidthMatchesPaneWidth(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 1, Title: "A"}})
	m.folders = []auth.Folder{{ID: 7, Name: "Работа"}}

	columns := []struct {
		name  string
		title string
		pane  string
	}{
		{"folders", paneTitle(foldersPaneW, 1, "Папки", m.focus == focusFolders, m.theme), m.foldersPane()},
		{"chats", paneTitle(chatsPaneW, 2, "Чаты", m.focus == focusChats, m.theme), m.chatPane()},
		{"messages", paneTitle(m.viewport.Width, 3, m.currentChatTitle(), m.focus == focusMessages, m.theme), m.msgPane()},
	}
	for _, c := range columns {
		titleW := lipgloss.Width(strings.SplitN(c.title, "\n", 2)[0])
		paneW := lipgloss.Width(strings.SplitN(c.pane, "\n", 2)[0])
		if titleW != paneW {
			t.Errorf("%s: title line width %d != pane line width %d (title=%q, pane=%q)", c.name, titleW, paneW, c.title, c.pane)
		}
	}
}

// TestBottomLineShowsVersionRightAligned — в Normal-режиме без статуса версия
// прижимается к правому краю нижней строки: текст версии физически правее
// метки NAV (а не вписана в подсказку).
func TestBottomLineShowsVersionRightAligned(t *testing.T) {
	m := testModel(t, nil)
	m.version = "v1.2.3"
	m.width = 140

	got := m.bottomLine()
	if !strings.Contains(got, "v1.2.3") {
		t.Fatalf("bottom line must contain the version, got: %q", got)
	}
	if !strings.Contains(got, "NAV") {
		t.Fatalf("bottom line must contain the NAV tag, got: %q", got)
	}
	if strings.Index(got, "v1.2.3") <= strings.Index(got, "NAV") {
		t.Fatalf("version must be physically to the right of the NAV tag, got: %q", got)
	}
}

// TestBottomLineShowsUpdateAvailable — при найденном обновлении правая часть
// нижней строки показывает "текущая → новая (:update)". Ширина 140 (измерено
// эмпирически после добавления пары "j/k/↑↓ — курсор" в подсказку — при
// меньшей ширине версия по замыслу bottomLine (pad < 1) опускается целиком,
// и тест проверял бы противоречащий себе случай).
func TestBottomLineShowsUpdateAvailable(t *testing.T) {
	m := testModel(t, nil)
	m.version = "v1.2.3"
	m.updateAvailable = "v1.3.0"
	m.width = 140

	got := m.bottomLine()
	if !strings.Contains(got, "v1.2.3 → v1.3.0") {
		t.Errorf("bottom line must show 'current → new', got: %q", got)
	}
	if !strings.Contains(got, ":update") {
		t.Errorf("bottom line must hint the :update command, got: %q", got)
	}
}

// TestBottomLineNarrowWidthOmitsVersionGracefully — на терминале, куда версия
// рядом с пилюлей+подсказкой не помещается, bottomLine не паникует и НЕ
// показывает оборванную версию: либо версии нет вовсе, либо (в ширину
// умещается и показана целиком) — строка не длиннее m.width.
func TestBottomLineNarrowWidthOmitsVersionGracefully(t *testing.T) {
	m := testModel(t, nil)
	m.version = "v1.2.3"
	m.width = 40

	got := m.bottomLine() // не должно паниковать
	if strings.Contains(got, "v1.2.3") {
		if w := lipgloss.Width(got); w > m.width {
			t.Errorf("bottom line wider than terminal (%d > %d) while showing the version: %q", w, m.width, got)
		}
	}
}

// TestUpdateCommandTriggersCheck — `:update` + Enter ставит статус "Проверка
// обновлений…" и возвращает команду фактической проверки.
func TestUpdateCommandTriggersCheck(t *testing.T) {
	m := testModel(t, nil)
	m, _ = updateModel(m, keyRune(':'))
	m = typeText(m, "update")

	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd after :update")
	}
	if m.status != "Проверка обновлений…" {
		t.Fatalf("expected status 'Проверка обновлений…', got %q", m.status)
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after :update, got %v", m.mode)
	}
}

// TestUpdateCheckMsgBackgroundSilentOnError — фоновая проверка (explicit=false)
// при ошибке сети молча игнорируется: статус не меняется, пользователь не
// дёргается тем, чего не просил.
func TestUpdateCheckMsgBackgroundSilentOnError(t *testing.T) {
	m := testModel(t, nil)
	m, _ = updateModel(m, updateCheckMsg{err: errors.New("boom"), explicit: false})
	if m.status != "" {
		t.Fatalf("background check error must not change status, got %q", m.status)
	}
	if m.updateAvailable != "" {
		t.Fatalf("background check error must not set updateAvailable, got %q", m.updateAvailable)
	}
}

// TestUpdateCheckMsgExplicitShowsErrorStatus — явная проверка (:update) при
// ошибке показывает её в статусе.
func TestUpdateCheckMsgExplicitShowsErrorStatus(t *testing.T) {
	m := testModel(t, nil)
	m, _ = updateModel(m, updateCheckMsg{err: errors.New("boom"), explicit: true})
	if !strings.Contains(m.status, "Не удалось проверить обновления") {
		t.Fatalf("expected error status on explicit check, got %q", m.status)
	}
	if !strings.Contains(m.status, "boom") {
		t.Fatalf("expected error detail in status, got %q", m.status)
	}
}

// TestUpdateCheckMsgSetsUpdateAvailable — фоновая проверка с более новой
// версией запоминает тег обновления (показывается в нижней строке).
func TestUpdateCheckMsgSetsUpdateAvailable(t *testing.T) {
	m := testModel(t, nil)
	m.version = "v1.0.0"

	m, _ = updateModel(m, updateCheckMsg{release: update.Release{TagName: "v9.9.9"}, explicit: false})
	if m.updateAvailable != "v9.9.9" {
		t.Fatalf("expected updateAvailable v9.9.9, got %q", m.updateAvailable)
	}
	// Фоновая проверка статус не трогает — только indicator.
	if m.status != "" {
		t.Fatalf("background success must not set status, got %q", m.status)
	}
}

// TestUpdateCheckMsgOlderDoesNotSetUpdateAvailable — версия, не новее текущей,
// не считается обновлением: updateAvailable остаётся пустым.
func TestUpdateCheckMsgOlderDoesNotSetUpdateAvailable(t *testing.T) {
	m := testModel(t, nil)
	m.version = "v1.0.0"

	m, _ = updateModel(m, updateCheckMsg{release: update.Release{TagName: "v0.1.0"}, explicit: false})
	if m.updateAvailable != "" {
		t.Fatalf("expected updateAvailable empty for older release, got %q", m.updateAvailable)
	}
}

// --- 0027: поиск чатов/каналов/контактов ---

// tuiSearchChatsResponse строит {"@type":"chats","chat_ids":[...]} — форма
// ответа searchChatsOnServer/searchPublicChats.
func tuiSearchChatsResponse(ids ...int64) map[string]interface{} {
	list := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		list = append(list, float64(id))
	}
	return map[string]interface{}{"@type": "chats", "chat_ids": list}
}

// tuiGetChatResponse — ответ getChat с заголовком.
func tuiGetChatResponse(chatID int64, title string) map[string]interface{} {
	return map[string]interface{}{"@type": "chat", "id": float64(chatID), "title": title}
}

// tuiSearchContactsResponse — ответ searchContacts (users + user_ids).
func tuiSearchContactsResponse(ids ...int64) map[string]interface{} {
	list := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		list = append(list, float64(id))
	}
	return map[string]interface{}{"@type": "users", "user_ids": list}
}

// tuiGetUserResponse — ответ getUser с именем.
func tuiGetUserResponse(userID int64, first, last string) map[string]interface{} {
	return map[string]interface{}{"@type": "user", "id": float64(userID), "first_name": first, "last_name": last}
}

// TestSearchHotkeyEntersSearchMode — "/" в Normal-режиме переводит в modeSearch
// и фокусирует поле поиска. Гейта на выбранный чат НЕТ (в отличие от i/ctrl+f):
// поиск не требует открытого чата.
func TestSearchHotkeyEntersSearchMode(t *testing.T) {
	m := testModel(t, nil)

	m, cmd := updateModel(m, keyRune('/'))
	if cmd == nil {
		t.Fatal("expected non-nil focus cmd after /")
	}
	if m.mode != modeSearch {
		t.Fatalf("expected modeSearch after /, got %v", m.mode)
	}
	if !m.searchInput.Focused() {
		t.Fatal("expected searchInput focused in search mode")
	}
}

// TestSearchEscReturnsToNormal — Esc в modeSearch возвращает modeNormal и
// сбрасывает активные результаты поиска.
func TestSearchEscReturnsToNormal(t *testing.T) {
	m := testModel(t, nil)
	m.searchActive = true
	m.searchResults = auth.SearchResults{Chats: []auth.SearchResultChat{{ID: 1, Title: "X"}}}

	m, _ = updateModel(m, keyRune('/'))
	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("expected nil cmd on esc in search mode, got %v", cmd)
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after esc, got %v", m.mode)
	}
	if m.searchInput.Focused() {
		t.Fatal("expected searchInput blurred after esc")
	}
	if m.searchActive {
		t.Fatal("expected searchActive cleared after esc")
	}
}

// TestSearchEmptyEnterReturnsToNormalWithoutSearching — Enter с пустым (или из
// одних пробелов) запросом возвращает modeNormal без запуска поиска.
func TestSearchEmptyEnterReturnsToNormalWithoutSearching(t *testing.T) {
	for _, q := range []string{"", "   "} {
		fake := &fakeClient{}
		m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
		m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
		m, _ = updateModel(m, keyRune('/'))
		m = typeText(m, q)

		m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
		if cmd != nil {
			t.Fatalf("query %q: expected nil cmd on empty enter, got %v", q, cmd)
		}
		if m.mode != modeNormal {
			t.Fatalf("query %q: expected modeNormal, got %v", q, m.mode)
		}
		if m.searchingNow {
			t.Fatalf("query %q: searchingNow must not be set", q)
		}
		if fake.sendCount != 0 {
			t.Fatalf("query %q: expected no sends, got %d", q, fake.sendCount)
		}
	}
}

// TestSearchEnterRunsSearchAndAppliesResults — Enter с непустым запросом
// запускает SearchAll (searchingNow + статус "Поиск…", режим заморожен), а
// успешный результат переводит searchActive=true, фокус в список чатов,
// modeNormal; результаты видны в chatPane (заголовки чатов + "👤 " перед
// контактами).
func TestSearchEnterRunsSearchAndAppliesResults(t *testing.T) {
	fake := &fakeClient{responses: []map[string]interface{}{
		tuiSearchChatsResponse(1),
		tuiGetChatResponse(1, "Известный чат"),
		tuiSearchChatsResponse(2),
		tuiGetChatResponse(2, "Публичный канал"),
		tuiSearchContactsResponse(100),
		tuiGetUserResponse(100, "Иван", "Петров"),
	}}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = updateModel(m, keyRune('/'))
	m = typeText(m, "запрос")

	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil search cmd after enter")
	}
	if !m.searchingNow {
		t.Fatal("expected searchingNow after enter")
	}
	if m.status != "Поиск…" {
		t.Fatalf("expected status 'Поиск…', got %q", m.status)
	}
	if m.mode != modeSearch {
		t.Fatalf("expected modeSearch while searching, got %v", m.mode)
	}

	m, _ = updateModel(m, cmd())
	if m.searchingNow {
		t.Fatal("expected searchingNow cleared after searchResultMsg")
	}
	if !m.searchActive {
		t.Fatal("expected searchActive after successful search")
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after search, got %v", m.mode)
	}
	if m.focus != focusChats {
		t.Fatalf("expected focusChats after search, got %v", m.focus)
	}
	if m.searchInput.Focused() {
		t.Fatal("expected searchInput blurred after search result")
	}

	pane := m.chatPane()
	for _, want := range []string{"Известный чат", "Публичный канал", "👤 Иван Петров"} {
		if !strings.Contains(pane, want) {
			t.Errorf("chatPane missing %q:\n%s", want, pane)
		}
	}
}

// TestSearchResultMsgErrorShowsStatusAndReturnsToNormal — ошибка поиска
// показывает статус и возвращает modeNormal без результатов.
func TestSearchResultMsgErrorShowsStatusAndReturnsToNormal(t *testing.T) {
	m := testModel(t, nil)
	m, _ = updateModel(m, keyRune('/'))
	m.searchingNow = true

	m, cmd := updateModel(m, searchResultMsg{err: errors.New("boom")})
	if cmd != nil {
		t.Fatalf("expected nil cmd on search error, got %v", cmd)
	}
	if m.searchingNow {
		t.Fatal("expected searchingNow cleared")
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after search error, got %v", m.mode)
	}
	if m.searchActive {
		t.Fatal("expected searchActive false after search error")
	}
	if !strings.Contains(m.status, "Поиск не удался") {
		t.Fatalf("expected error status, got %q", m.status)
	}
}

// TestSearchResultsNavigationClamps — ↑/↓ внутри объединённого списка
// (чаты+контакты) не выходят за его границы.
func TestSearchResultsNavigationClamps(t *testing.T) {
	m := testModel(t, nil)
	m.focus = focusChats
	m.searchActive = true
	m.searchResults = auth.SearchResults{
		Chats:    []auth.SearchResultChat{{ID: 1, Title: "Ч1"}, {ID: 2, Title: "Ч2"}},
		Contacts: []auth.SearchResultContact{{UserID: 100, Name: "Контакт"}},
	}
	m.searchCursor = 0

	for i := 0; i < 10; i++ {
		m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyDown})
	}
	if m.searchCursor != 2 {
		t.Fatalf("expected searchCursor clamped to 2, got %d", m.searchCursor)
	}
	for i := 0; i < 10; i++ {
		m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyUp})
	}
	if m.searchCursor != 0 {
		t.Fatalf("expected searchCursor clamped to 0, got %d", m.searchCursor)
	}
}

// TestSearchBackKeyExitsResultsToNormalChatList — Esc (Back) при активных
// результатах в focusChats сбрасывает результаты и возвращает обычный список
// чатов, курсор обычного списка не трогает.
func TestSearchBackKeyExitsResultsToNormalChatList(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 5, Title: "Обычный"}})
	m.focus = focusChats
	m.searchActive = true
	m.searchResults = auth.SearchResults{Chats: []auth.SearchResultChat{{ID: 1, Title: "Найденный"}}}
	m.searchCursor = 1

	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("expected nil cmd on esc in search results, got %v", cmd)
	}
	if m.searchActive {
		t.Fatal("expected searchActive cleared")
	}
	if len(m.searchResults.Chats) != 0 {
		t.Fatalf("expected searchResults cleared, got %+v", m.searchResults)
	}
	if m.searchCursor != 0 {
		t.Fatalf("expected searchCursor reset to 0, got %d", m.searchCursor)
	}
	if m.focus != focusChats {
		t.Fatalf("expected focusChats unchanged, got %v", m.focus)
	}
}

// TestSearchSelectChatFromResults — выбор чата из результатов ведёт себя как
// обычный selectChatCmd (openChat раньше getChatHistory), при переключении с
// уже открытого чата закрывает прежний, displayedChat сбрасывается на новый.
func TestSearchSelectChatFromResults(t *testing.T) {
	fake := &fakeClient{}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.focus = focusChats
	m.searchActive = true
	m.searchResults = auth.SearchResults{Chats: []auth.SearchResultChat{{ID: 111, Title: "Найденный"}}}
	m.displayedChat = 222 // уже открыт другой чат — должен закрыться
	m.searchCursor = 0

	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd after selecting search chat")
	}
	if m.searchActive {
		t.Fatal("expected searchActive cleared after selecting chat")
	}
	if m.displayedChat != 111 {
		t.Fatalf("expected displayedChat 111, got %d", m.displayedChat)
	}
	if !m.loadingMsgs {
		t.Fatal("expected loadingMsgs after selecting chat")
	}
	if m.focus != focusMessages {
		t.Fatalf("expected focusMessages, got %v", m.focus)
	}
	runCmd(t, cmd)

	var sawOpen, sawHistory, sawClose bool
	for _, r := range fake.requests {
		if r["@type"] == "openChat" && r["chat_id"] == int64(111) {
			sawOpen = true
		}
		if r["@type"] == "getChatHistory" && r["chat_id"] == int64(111) {
			sawHistory = true
		}
		if r["@type"] == "closeChat" && r["chat_id"] == int64(222) {
			sawClose = true
		}
	}
	if !sawOpen {
		t.Errorf("expected openChat(111), requests: %v", fake.requests)
	}
	if !sawHistory {
		t.Errorf("expected getChatHistory(111), requests: %v", fake.requests)
	}
	if !sawClose {
		t.Errorf("expected closeChat(222), requests: %v", fake.requests)
	}
}

// TestSearchSelectContactOpensPrivateChat — выбор контакта из результатов
// шлёт createPrivateChat(user_id, force=true), затем openChat и getChatHistory
// за чат, id которого пришёл в ответе createPrivateChat.
func TestSearchSelectContactOpensPrivateChat(t *testing.T) {
	fake := &fakeClient{responses: []map[string]interface{}{
		{"@type": "chat", "id": float64(500)},
	}}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.focus = focusChats
	m.searchActive = true
	m.searchResults = auth.SearchResults{
		Contacts: []auth.SearchResultContact{{UserID: 100, Name: "Иван Петров"}},
	}
	m.searchCursor = 0 // единственный элемент — контакт (чатов нет)

	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("expected non-nil cmd after selecting contact")
	}
	if m.searchActive {
		t.Fatal("expected searchActive cleared after selecting contact")
	}
	if !m.loadingMsgs {
		t.Fatal("expected loadingMsgs after selecting contact")
	}
	if m.focus != focusMessages {
		t.Fatalf("expected focusMessages, got %v", m.focus)
	}
	runCmd(t, cmd)

	var sawCreate, sawOpen, sawHistory bool
	for _, r := range fake.requests {
		if r["@type"] == "createPrivateChat" && r["user_id"] == int64(100) && r["force"] == true {
			sawCreate = true
		}
		if r["@type"] == "openChat" && r["chat_id"] == int64(500) {
			sawOpen = true
		}
		if r["@type"] == "getChatHistory" && r["chat_id"] == int64(500) {
			sawHistory = true
		}
	}
	if !sawCreate {
		t.Errorf("expected createPrivateChat(user_id=100, force=true), requests: %v", fake.requests)
	}
	if !sawOpen {
		t.Errorf("expected openChat(500), requests: %v", fake.requests)
	}
	if !sawHistory {
		t.Errorf("expected getChatHistory(500), requests: %v", fake.requests)
	}
}

// TestSearchChatPaneEmptyShowsPlaceholder — при активном поиске с пустыми
// результатами chatPane показывает "Ничего не найдено", а не обычный список.
func TestSearchChatPaneEmptyShowsPlaceholder(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 1, Title: "Обычный"}})
	m.searchActive = true
	m.searchResults = auth.SearchResults{}

	pane := m.chatPane()
	if !strings.Contains(pane, "Ничего не найдено") {
		t.Errorf("expected 'Ничего не найдено' placeholder, pane:\n%s", pane)
	}
	if strings.Contains(pane, "Обычный") {
		t.Errorf("normal chats must not be shown while searchActive, pane:\n%s", pane)
	}
}

// TestMessageCursorMovesWithUpDown — ↑/↓ в focusMessages двигают messageCursor
// по ОТДЕЛЬНЫМ сообщениям (не по строкам, как раньше): старт с последнего
// индекса (см. п.5 — после загрузки курсор на последнем сообщении), ↑
// уменьшает на 1, ↓ увеличивает; на границах (0 и len-1) курсор не выходит за
// пределы (по образцу TestFolderNavigationClamps).
func TestMessageCursorMovesWithUpDown(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 111, Title: "Чат"}})
	m.focus = focusMessages
	m.messages = []auth.Message{
		{ID: 1, SenderName: "А", Text: "один", Date: 100},
		{ID: 2, SenderName: "Б", Text: "два", Date: 101},
		{ID: 3, SenderName: "Вы", Text: "три", Date: 102},
	}
	m.messageCursor = len(m.messages) - 1 // после загрузки — последний индекс

	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyUp})
	if cmd != nil {
		t.Fatalf("expected nil cmd when moving cursor, got %v", cmd)
	}
	if m.messageCursor != 1 {
		t.Fatalf("expected cursor 1 after up, got %d", m.messageCursor)
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.messageCursor != 0 {
		t.Fatalf("expected cursor 0 after second up, got %d", m.messageCursor)
	}

	// Верхняя граница: ещё один up не уводит курсор в отрицательные значения.
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyUp})
	if m.messageCursor != 0 {
		t.Fatalf("expected cursor clamped to 0, got %d", m.messageCursor)
	}

	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.messageCursor != 1 {
		t.Fatalf("expected cursor 1 after down, got %d", m.messageCursor)
	}

	// Нижняя граница: на последнем индексе не выходим за конец списка.
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyDown})
	if m.messageCursor != len(m.messages)-1 {
		t.Fatalf("expected cursor clamped to %d, got %d", len(m.messages)-1, m.messageCursor)
	}
}

// TestMessageCursorSelectedCardUsesDoubleBorder — карточка сообщения под
// курсором рисуется ДВОЙНОЙ рамкой (╔/╗/╚/╝), остальные — одинарной
// скруглённой (╭/╮/╰/╯): тот же визуальный язык "это выделено", что у
// активной панели.
func TestMessageCursorSelectedCardUsesDoubleBorder(t *testing.T) {
	msgs := []auth.Message{
		{ID: 1, SenderName: "А", Text: "первое", Date: 100},
		{ID: 2, SenderName: "Б", Text: "второе", Date: 101},
		{ID: 3, SenderName: "В", Text: "третье", Date: 102},
	}

	content, _ := renderMessages(msgs, 60, false, 1, defaultTheme(), 0)
	cards := strings.Split(content, "\n\n")
	if len(cards) != 3 {
		t.Fatalf("expected 3 cards, got %d:\n%s", len(cards), content)
	}

	for _, ch := range []rune{'╔', '╗', '╚', '╝'} {
		if !strings.ContainsRune(cards[1], ch) {
			t.Errorf("selected card (index 1) missing double-border char %q, card:\n%s", ch, cards[1])
		}
	}
	for _, idx := range []int{0, 2} {
		for _, ch := range []rune{'╭', '╮', '╰', '╯'} {
			if !strings.ContainsRune(cards[idx], ch) {
				t.Errorf("card %d missing rounded-border char %q, card:\n%s", idx, ch, cards[idx])
			}
		}
		if strings.ContainsRune(cards[idx], '╔') {
			t.Errorf("card %d must NOT use the double border, card:\n%s", idx, cards[idx])
		}
	}
}

// TestRenderMessagesLineOffsetsMatchActualLines — регрессия на точность
// lineOffsets: для каждого i строка content с номером lineOffsets[i] должна
// быть ДЕЙСТВИТЕЛЬНО первой строкой верхней рамки карточки i (содержит ╭ или
// ╔ — верхний левый угол одной из двух рамок). Карточки разной высоты (тексты
// разной длины), чтобы смещения не совпадали между сообщениями.
func TestRenderMessagesLineOffsetsMatchActualLines(t *testing.T) {
	msgs := []auth.Message{
		{ID: 1, SenderName: "А", Text: "короткое", Date: 100},
		{ID: 2, SenderName: "Б", Text: "одно два три четыре пять шесть семь восемь девять десять", Date: 101},
		{ID: 3, SenderName: "Вы", Text: "ещё одно", Date: 102},
	}

	content, offsets := renderMessages(msgs, 60, false, 1, defaultTheme(), 0)
	lines := strings.Split(content, "\n")
	if len(offsets) != len(msgs) {
		t.Fatalf("expected %d offsets, got %d", len(msgs), len(offsets))
	}
	for i := range msgs {
		if offsets[i] < 0 || offsets[i] >= len(lines) {
			t.Fatalf("offsets[%d]=%d out of range (lines=%d)", i, offsets[i], len(lines))
		}
		first := lines[offsets[i]]
		if !strings.Contains(first, "╭") && !strings.Contains(first, "╔") {
			t.Errorf("line at offsets[%d]=%d is not a card top border: %q", i, offsets[i], first)
		}
		if i > 0 && offsets[i] <= offsets[i-1] {
			t.Errorf("offsets not strictly increasing: offsets[%d]=%d <= offsets[%d]=%d", i, offsets[i], i-1, offsets[i-1])
		}
	}
}

// TestRerenderScrollsToCursorWhenAboveView — курсор уходит вверх за пределы
// текущей видимой области: rerenderMessagesAndScrollToCursor прокручивает
// viewport вверх ровно до верхней строки выбранной карточки.
func TestRerenderScrollsToCursorWhenAboveView(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 111, Title: "Чат"}})
	m.viewport.Height = 5 // узкий viewport — не всё помещается
	m.messages = []auth.Message{
		{ID: 1, SenderName: "А", Text: "сообщение один", Date: 100},
		{ID: 2, SenderName: "Б", Text: strings.Repeat("длинный текст ", 8), Date: 101},
		{ID: 3, SenderName: "Вы", Text: strings.Repeat("длинный текст ", 8), Date: 102},
		{ID: 4, SenderName: "А", Text: "сообщение четыре", Date: 103},
	}
	contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
	content, offsets := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, 0, defaultTheme(), 0)
	m.viewport.SetContent(content)
	m.viewport.GotoBottom()
	if m.viewport.YOffset == 0 {
		t.Fatal("test setup invalid: expected viewport scrolled off the top")
	}

	// Курсор на самом первом сообщении — оно выше видимой области.
	m.messageCursor = 0
	m.rerenderMessagesAndScrollToCursor()
	if m.viewport.YOffset != offsets[m.messageCursor] {
		t.Fatalf("expected YOffset %d (top of selected card), got %d", offsets[m.messageCursor], m.viewport.YOffset)
	}
}

// TestRerenderScrollsToCursorWhenBelowView — симметрично: курсор уходит вниз
// за пределы видимой области — viewport прокручивается вниз ровно настолько,
// чтобы верхняя строка выбранной карточки стала последней видимой строкой
// (либо YOffset == 0, если разница отрицательна — SetYOffset сама клампит,
// тест естественно проходит в обоих случаях).
func TestRerenderScrollsToCursorWhenBelowView(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 111, Title: "Чат"}})
	m.viewport.Height = 5
	m.messages = []auth.Message{
		{ID: 1, SenderName: "А", Text: "короткое", Date: 100},
		{ID: 2, SenderName: "Б", Text: "короткое", Date: 101},
		{ID: 3, SenderName: "Вы", Text: strings.Repeat("длинный текст ", 8), Date: 102},
		{ID: 4, SenderName: "А", Text: strings.Repeat("длинный текст ", 8), Date: 103},
	}
	contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
	content, offsets := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, len(m.messages)-1, defaultTheme(), 0)
	m.viewport.SetContent(content)
	m.viewport.SetYOffset(0)
	if m.viewport.AtBottom() {
		t.Fatal("test setup invalid: expected viewport not at bottom")
	}

	// Курсор на последнем сообщении — оно ниже видимой области.
	m.messageCursor = len(m.messages) - 1
	m.rerenderMessagesAndScrollToCursor()
	want := max(0, offsets[m.messageCursor]-m.viewport.Height+1)
	if m.viewport.YOffset != want {
		t.Fatalf("expected YOffset %d, got %d", want, m.viewport.YOffset)
	}
}

// TestMessageCursorResetsToLastOnFreshLoad — при любой замене m.messages
// (здесь — messagesLoadedMsg) messageCursor сбрасывается на последнее
// сообщение (п.5): курсор всегда синхронизирован с "последнее видимое
// сообщение", то же поведение, что и автопрокрутка вниз.
func TestMessageCursorResetsToLastOnFreshLoad(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 111, Title: "Чат"}})
	m.displayedChat = 111

	msgs := []auth.Message{
		{ID: 1, Text: "первое", SenderName: "Вы", Date: 100},
		{ID: 2, Text: "второе", SenderName: "Собеседник", Date: 101},
		{ID: 3, Text: "третье", SenderName: "Вы", Date: 102},
	}
	m, _ = updateModel(m, messagesLoadedMsg{chatID: 111, messages: msgs})

	if m.messageCursor != len(msgs)-1 {
		t.Fatalf("expected messageCursor %d after fresh load, got %d", len(msgs)-1, m.messageCursor)
	}
}

// TestUnreadSuffix — суффикс бейджа непрочитанных: " [N]" только при N > 0,
// при 0 и отрицательном — пустая строка (не " [0]").
func TestUnreadSuffix(t *testing.T) {
	if got := unreadSuffix(5); got != "[5]" {
		t.Errorf("unreadSuffix(5) = %q, want \"[5]\"", got)
	}
	if got := unreadSuffix(0); got != "" {
		t.Errorf("unreadSuffix(0) = %q, want \"\"", got)
	}
	if got := unreadSuffix(-3); got != "" {
		t.Errorf("unreadSuffix(-3) = %q, want \"\"", got)
	}
}

// rawChatReadInboxUpdate строит "сырой" TDLib-апдейт updateChatReadInbox.
func rawChatReadInboxUpdate(chatID float64, unreadCount float64) map[string]interface{} {
	return map[string]interface{}{
		"@type":                      "updateChatReadInbox",
		"chat_id":                    chatID,
		"last_read_inbox_message_id": float64(1),
		"unread_count":               unreadCount,
	}
}

// rawChatTitleUpdate строит "сырой" TDLib-апдейт updateChatTitle (настоящее
// имя приватного чата после асинхронного резолва собеседника).
func rawChatTitleUpdate(chatID float64, title string) map[string]interface{} {
	return map[string]interface{}{
		"@type":   "updateChatTitle",
		"chat_id": chatID,
		"title":   title,
	}
}

// rawUnreadCountUpdate строит "сырой" апдейт updateUnreadMessageCount для
// chatListFolder (folderID) или chatListMain (folderID < 0 → main).
func rawUnreadCountUpdate(folderID int, unreadCount float64) map[string]interface{} {
	var chatList map[string]interface{}
	if folderID < 0 {
		chatList = map[string]interface{}{"@type": "chatListMain"}
	} else {
		chatList = map[string]interface{}{"@type": "chatListFolder", "chat_folder_id": float64(folderID)}
	}
	return map[string]interface{}{
		"@type":                "updateUnreadMessageCount",
		"chat_list":            chatList,
		"unread_count":         unreadCount,
		"unread_unmuted_count": unreadCount,
	}
}

// rawUnreadChatCountUpdate строит "сырой" апдейт updateUnreadChatCount
// (число ЧАТОВ, не сообщений) для chatListFolder (folderID) или
// chatListMain (folderID < 0 → main) — тот же формат аргументов, что у
// rawUnreadCountUpdate, но другой @type/поля.
func rawUnreadChatCountUpdate(folderID int, chatCount float64) map[string]interface{} {
	var chatList map[string]interface{}
	if folderID < 0 {
		chatList = map[string]interface{}{"@type": "chatListMain"}
	} else {
		chatList = map[string]interface{}{"@type": "chatListFolder", "chat_folder_id": float64(folderID)}
	}
	return map[string]interface{}{
		"@type":        "updateUnreadChatCount",
		"chat_list":    chatList,
		"unread_count": chatCount,
	}
}

// TestWaitForChatReadInboxUpdateUpdatesMatchingChat — живой апдейт через канал
// обновляет UnreadCount у совпадающего чата в m.chats и переподписывается.
func TestWaitForChatReadInboxUpdateUpdatesMatchingChat(t *testing.T) {
	fake := &fakeClient{chatReadInboxCh: make(chan map[string]interface{}, 1)}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.chats = []auth.Chat{
		{ID: 1, Title: "А", UnreadCount: 0},
		{ID: 2, Title: "Б", UnreadCount: 9},
	}

	cmd := m.waitForChatReadInboxUpdate()
	fake.chatReadInboxCh <- rawChatReadInboxUpdate(2, 3)
	msg := cmd()

	m, resub := updateModel(m, msg)
	if resub == nil {
		t.Fatal("expected non-nil resubscription cmd after live update")
	}
	if m.chats[0].UnreadCount != 0 {
		t.Errorf("chat 1 must not change, got %d", m.chats[0].UnreadCount)
	}
	if m.chats[1].UnreadCount != 3 {
		t.Errorf("expected chat 2 UnreadCount 3, got %d", m.chats[1].UnreadCount)
	}
}

// TestChatReadInboxUpdateMsgIgnoresUnknownChat — валидный апдейт для чата,
// которого нет в списке, безвреден и всё равно переподписывается.
func TestChatReadInboxUpdateMsgIgnoresUnknownChat(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 1, Title: "А"}})
	m, resub := updateModel(m, chatReadInboxUpdateMsg{chatID: 999, unreadCount: 5, valid: true})
	if resub == nil {
		t.Fatal("expected non-nil resubscription cmd for unknown chat")
	}
	if m.chats[0].UnreadCount != 0 {
		t.Errorf("unknown chat must not touch existing chats, got %d", m.chats[0].UnreadCount)
	}
}

// TestChatReadInboxUpdateMsgInvalidStillResubscribes — нераспознанный апдейт
// (valid == false) не портит данные и всё равно переподписывается.
func TestChatReadInboxUpdateMsgInvalidStillResubscribes(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 1, Title: "А", UnreadCount: 4}})
	m, resub := updateModel(m, chatReadInboxUpdateMsg{})
	if resub == nil {
		t.Fatal("expected non-nil resubscription cmd for invalid update")
	}
	if m.chats[0].UnreadCount != 4 {
		t.Errorf("invalid update must not change UnreadCount, got %d", m.chats[0].UnreadCount)
	}
}

// TestChatReadInboxUpdateMsgClosedStopsResubscribing — закрытый канал —
// единственный случай без переподписки.
func TestChatReadInboxUpdateMsgClosedStopsResubscribing(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 1, Title: "А"}})
	m, resub := updateModel(m, chatReadInboxUpdateMsg{closed: true})
	if resub != nil {
		t.Fatal("closed channel must not resubscribe, expected nil cmd")
	}
}

// TestWaitForChatTitleUpdateUpdatesMatchingChat — живой апдейт updateChatTitle
// через канал заменяет title у совпадающего чата и переподписывается
// (настоящее имя приватного чата приходит после асинхронного резолва
// собеседника — в момент getChat title был пустым, см. задачу 0045).
func TestWaitForChatTitleUpdateUpdatesMatchingChat(t *testing.T) {
	fake := &fakeClient{chatTitleCh: make(chan map[string]interface{}, 1)}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.chats = []auth.Chat{
		{ID: 1, Title: "chat#1"},
		{ID: 2, Title: "chat#2"},
	}

	cmd := m.waitForChatTitleUpdate()
	fake.chatTitleCh <- rawChatTitleUpdate(2, "Иван Петров")
	msg := cmd()

	m, resub := updateModel(m, msg)
	if resub == nil {
		t.Fatal("expected non-nil resubscription cmd after live update")
	}
	if m.chats[0].Title != "chat#1" {
		t.Errorf("chat 1 must not change, got %q", m.chats[0].Title)
	}
	if m.chats[1].Title != "Иван Петров" {
		t.Errorf("expected chat 2 Title \"Иван Петров\", got %q", m.chats[1].Title)
	}
}

// TestChatTitleUpdateMsgIgnoresUnknownChat — валидный апдейт для чата, которого
// нет в списке, безвреден и всё равно переподписывается.
func TestChatTitleUpdateMsgIgnoresUnknownChat(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 1, Title: "А"}})
	m, resub := updateModel(m, chatTitleUpdateMsg{chatID: 999, title: "Иван", valid: true})
	if resub == nil {
		t.Fatal("expected non-nil resubscription cmd for unknown chat")
	}
	if m.chats[0].Title != "А" {
		t.Errorf("unknown chat must not touch existing chats, got %q", m.chats[0].Title)
	}
}

// TestChatTitleUpdateMsgInvalidStillResubscribes — нераспознанный апдейт
// (valid == false) не портит данные и всё равно переподписывается.
func TestChatTitleUpdateMsgInvalidStillResubscribes(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 1, Title: "А"}})
	m, resub := updateModel(m, chatTitleUpdateMsg{})
	if resub == nil {
		t.Fatal("expected non-nil resubscription cmd for invalid update")
	}
	if m.chats[0].Title != "А" {
		t.Errorf("invalid update must not change Title, got %q", m.chats[0].Title)
	}
}

// TestChatTitleUpdateMsgClosedStopsResubscribing — закрытый канал —
// единственный случай без переподписки.
func TestChatTitleUpdateMsgClosedStopsResubscribing(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 1, Title: "А"}})
	m, resub := updateModel(m, chatTitleUpdateMsg{closed: true})
	if resub != nil {
		t.Fatal("closed channel must not resubscribe, expected nil cmd")
	}
}

// TestWaitForUnreadCountUpdateUpdatesFolder — живой апдейт через канал
// записывает счётчик папки в folderUnread и переподписывается.
// TestWaitForUnreadCountUpdateIgnoresFolder — unreadCountUpdateMsg (сумма
// непрочитанных СООБЩЕНИЙ, updateUnreadMessageCount) применяется ТОЛЬКО к
// folderID==0 ("Все чаты"); апдейт для конкретной папки должен быть
// проигнорирован — эта метрика для папок не подходит (см.
// unreadChatCountUpdateMsg ниже, другой источник, другая метрика).
func TestWaitForUnreadCountUpdateIgnoresFolder(t *testing.T) {
	fake := &fakeClient{unreadCountCh: make(chan map[string]interface{}, 1)}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.folderUnread[7] = 1
	m.folderUnread[0] = 100

	cmd := m.waitForUnreadCountUpdate()
	fake.unreadCountCh <- rawUnreadCountUpdate(7, 12)
	msg := cmd()

	m, resub := updateModel(m, msg)
	if resub == nil {
		t.Fatal("expected non-nil resubscription cmd after live update")
	}
	if m.folderUnread[7] != 1 {
		t.Errorf("folder 7 must be unchanged by unreadCountUpdateMsg (wrong metric), got %d", m.folderUnread[7])
	}
	if m.folderUnread[0] != 100 {
		t.Errorf("folder 0 (Все чаты) must not change, got %d", m.folderUnread[0])
	}
}

// TestWaitForUnreadChatCountUpdateUpdatesFolder — unreadChatCountUpdateMsg
// (число ЧАТОВ, updateUnreadChatCount) применяется ТОЛЬКО к folderID != 0
// (конкретным папкам); "Все чаты" эту метрику не использует.
func TestWaitForUnreadChatCountUpdateUpdatesFolder(t *testing.T) {
	fake := &fakeClient{unreadChatCountCh: make(chan map[string]interface{}, 1)}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.folderUnread[7] = 1
	m.folderUnread[0] = 100

	cmd := m.waitForUnreadChatCountUpdate()
	fake.unreadChatCountCh <- rawUnreadChatCountUpdate(7, 13)
	msg := cmd()

	m, resub := updateModel(m, msg)
	if resub == nil {
		t.Fatal("expected non-nil resubscription cmd after live update")
	}
	if m.folderUnread[7] != 13 {
		t.Errorf("expected folder 7 chat count 13, got %d", m.folderUnread[7])
	}
	if m.folderUnread[0] != 100 {
		t.Errorf("folder 0 (Все чаты) must not change, got %d", m.folderUnread[0])
	}
}

// TestWaitForUnreadChatCountUpdateIgnoresMain — симметричный случай: апдейт
// для chatListMain через ЭТОТ канал/метрику должен быть проигнорирован —
// "Все чаты" берёт бейдж из unreadCountUpdateMsg (сумма сообщений), не
// отсюда (число чатов).
func TestWaitForUnreadChatCountUpdateIgnoresMain(t *testing.T) {
	fake := &fakeClient{unreadChatCountCh: make(chan map[string]interface{}, 1)}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m.folderUnread[0] = 100

	cmd := m.waitForUnreadChatCountUpdate()
	fake.unreadChatCountCh <- rawUnreadChatCountUpdate(-1, 5)
	msg := cmd()

	m, resub := updateModel(m, msg)
	if resub == nil {
		t.Fatal("expected non-nil resubscription cmd after live update")
	}
	if m.folderUnread[0] != 100 {
		t.Errorf("folder 0 (Все чаты) must not change via unreadChatCountUpdateMsg, got %d", m.folderUnread[0])
	}
}

// TestWaitForUnreadCountUpdateUpdatesMain — то же для chatListMain: счётчик
// идёт в folderUnread[0] (синтетическая "Все чаты").
func TestWaitForUnreadCountUpdateUpdatesMain(t *testing.T) {
	fake := &fakeClient{unreadCountCh: make(chan map[string]interface{}, 1)}
	m := New(fake, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})

	cmd := m.waitForUnreadCountUpdate()
	fake.unreadCountCh <- rawUnreadCountUpdate(-1, 7)
	msg := cmd()

	m, resub := updateModel(m, msg)
	if resub == nil {
		t.Fatal("expected non-nil resubscription cmd for main list update")
	}
	if m.folderUnread[0] != 7 {
		t.Errorf("expected folder 0 unread 7, got %d", m.folderUnread[0])
	}
}

// TestUnreadCountUpdateMsgInvalidStillResubscribes — нераспознанный апдейт
// (valid == false) НЕ пишет шум в folderUnread[0] (иначе ломается счётчик
// "Все чаты" — см. п.5.4 файла задачи) и всё равно переподписывается.
func TestUnreadCountUpdateMsgInvalidStillResubscribes(t *testing.T) {
	m := testModel(t, nil)
	m.folderUnread[0] = 100
	m.folderUnread[9] = 1

	m, resub := updateModel(m, unreadCountUpdateMsg{})
	if resub == nil {
		t.Fatal("expected non-nil resubscription cmd for invalid update")
	}
	if m.folderUnread[0] != 100 {
		t.Errorf("invalid update must not touch folderUnread[0], got %d", m.folderUnread[0])
	}
	if m.folderUnread[9] != 1 {
		t.Errorf("invalid update must not touch folder 9, got %d", m.folderUnread[9])
	}
}

// TestUnreadCountUpdateMsgClosedStopsResubscribing — закрытый канал —
// единственный случай без переподписки.
func TestUnreadCountUpdateMsgClosedStopsResubscribing(t *testing.T) {
	m := testModel(t, nil)
	m, resub := updateModel(m, unreadCountUpdateMsg{closed: true})
	if resub != nil {
		t.Fatal("closed channel must not resubscribe, expected nil cmd")
	}
}

// TestFoldersPaneShowsUnreadSuffix — панель «Папки» показывает " [N]" справа
// от имени при N > 0 (в т.ч. у синтетической "Все чаты"), и не показывает
// "[0]" при нуле.
func TestFoldersPaneShowsUnreadSuffix(t *testing.T) {
	m := testModel(t, nil)
	m.folders = []auth.Folder{{ID: 7, Name: "Работа"}, {ID: 8, Name: "Друзья"}}
	m.folderUnread = map[int32]int32{0: 3, 7: 12}

	pane := m.foldersPane()
	// "Все чаты [3]" — ровно 1 пробел: имя+бейдж вместе заполняют всю
	// доступную ширину строки, зазору взяться неоткуда (см. alignBadge).
	if !strings.Contains(pane, "Все чаты [3]") {
		t.Errorf("main list must show '[3]' right after the name, pane:\n%s", pane)
	}
	// "Работа  [12]" — 2 пробела: короче доступной ширины, alignBadge
	// прижимает бейдж к правому краю строки (по правке человека), а не
	// дописывает его сразу после имени.
	if !strings.Contains(pane, "Работа  [12]") {
		t.Errorf("folder 7 must show '[12]' right-aligned (2 spaces before it), pane:\n%s", pane)
	}
	if strings.Contains(pane, "Друзья [0]") || strings.Contains(pane, "Друзья  [0]") {
		t.Errorf("folder with 0 unread must not show '[0]', pane:\n%s", pane)
	}

	// Нулевые счётчики вообще не дают суффикса.
	m.folderUnread = map[int32]int32{0: 0, 7: 0}
	pane = m.foldersPane()
	if strings.Contains(pane, "[0]") {
		t.Errorf("all-zero counts must not render any suffix, pane:\n%s", pane)
	}
}

// TestChatTitlesAppendUnreadSuffix — chatTitles добавляет " [N]" к названию
// чата только при N > 0.
func TestChatTitlesAppendUnreadSuffix(t *testing.T) {
	chats := []auth.Chat{
		{ID: 1, Title: "А", UnreadCount: 0},
		{ID: 2, Title: "Б", UnreadCount: 4},
		{ID: 3, Title: "В", UnreadCount: -1},
	}
	// chatTitles больше НЕ дописывает бейдж в название (по правке
	// человека — бейдж выравнивается по правому краю строки, а не
	// приклеивается к названию) — названия остаются как есть, бейджи
	// отдельно в chatBadges, тем же порядком/длиной.
	got := chatTitles(chats)
	want := []string{"А", "Б", "В"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("chatTitles = %#v, want %#v", got, want)
	}
	gotBadges := chatBadges(chats)
	wantBadges := []string{"", "[4]", ""}
	if !reflect.DeepEqual(gotBadges, wantBadges) {
		t.Errorf("chatBadges = %#v, want %#v", gotBadges, wantBadges)
	}
}

// TestChatPaneRightAlignsUnreadBadge — бейдж непрочитанных в панели чатов
// прижат к правому краю строки, а не дописан сразу после названия (по
// прямому запросу человека: "количество непрочитанных должно выравниваться
// по правой стороне").
func TestChatPaneRightAlignsUnreadBadge(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 1, Title: "Ирина", UnreadCount: 7}})
	pane := m.chatPane()
	if strings.Contains(pane, "Ирина [7]") {
		t.Errorf("badge must NOT be glued right after the name, pane:\n%s", pane)
	}
	if !strings.Contains(pane, "[7]") {
		t.Errorf("badge must still be present somewhere in the pane, pane:\n%s", pane)
	}
	lines := strings.Split(pane, "\n")
	found := false
	for _, line := range lines {
		if strings.Contains(line, "Ирина") && strings.Contains(line, "[7]") {
			found = true
			// Между именем и бейджем — минимум 2 пробела (chatsPaneW=30
			// заведомо шире "Ирина"+"[7]", есть куда прижимать вправо).
			if !strings.Contains(line, "Ирина  ") {
				t.Errorf("badge must be right-aligned with a visible gap, line: %q", line)
			}
		}
	}
	if !found {
		t.Fatalf("no line contains both the name and the badge, pane:\n%s", pane)
	}
}

// cancellingCheckClient — fakeClient, у которого Send с leaveChat/
// deleteChatHistory вызывает t.Fatal: в сценариях ОТМЕНЫ удаления эти
// необратимые запросы не должны уходить в TDLib вообще. Реальная проверка
// ОТСУТСТВИЯ вызова (смена mode сама по себе этого не доказывает).
type cancellingCheckClient struct {
	fakeClient
	t *testing.T
}

func (c *cancellingCheckClient) Send(ctx context.Context, request map[string]interface{}) (map[string]interface{}, error) {
	switch request["@type"] {
	case "leaveChat", "deleteChatHistory":
		c.t.Fatal("expected no leaveChat/deleteChatHistory call in cancellation scenario")
	}
	return c.fakeClient.Send(ctx, request)
}

// confirmDeleteModel строит модель в режиме подтверждения удаления (modeConfirmDelete)
// для первого чата переданного списка — общий setup для confirm-сценариев.
func confirmDeleteModel(t *testing.T, client auth.TDClientInterface, chats []auth.Chat) Model {
	t.Helper()
	m := New(client, context.Background(), config.DefaultKeyBindings(), config.DefaultSettings(), "dev")
	m, _ = updateModel(m, tea.WindowSizeMsg{Width: 100, Height: 30})
	m, _ = updateModel(m, chatsLoadedMsg{chats: chats})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab}) // фокус в список чатов
	m, _ = updateModel(m, keyRune('d'))
	if m.mode != modeConfirmDelete {
		t.Fatalf("setup: expected modeConfirmDelete, got %v", m.mode)
	}
	return m
}

// TestDeleteChatHotkeyIgnoredOutsideChatList — хоткей DeleteChat ничего не
// делает (мод не меняется, команды не возвращаются), когда фокус вне панели
// чатов, активны результаты поиска или список чатов пуст.
func TestDeleteChatHotkeyIgnoredOutsideChatList(t *testing.T) {
	// Фокус не на списке чатов (стартовый фокус — панель папок).
	m := testModel(t, []auth.Chat{{ID: 1, Title: "A", IsGroup: true}})
	m, cmd := updateModel(m, keyRune('d'))
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal outside chat pane, got %v", m.mode)
	}
	if cmd != nil {
		t.Fatalf("expected nil cmd outside chat pane, got %v", cmd)
	}
	if m.mode != modeNormal {
		t.Fatalf("expected unchanged mode, got %v", m.mode)
	}

	// Активный поиск: хоткей в списке чатов, но searchActive=true.
	m = testModel(t, []auth.Chat{{ID: 2, Title: "B"}})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab})
	m.searchActive = true
	m.searchResults = auth.SearchResults{Chats: []auth.SearchResultChat{{ID: 3, Title: "X"}}}
	m, cmd = updateModel(m, keyRune('d'))
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal with searchActive, got %v", m.mode)
	}
	if cmd != nil {
		t.Fatalf("expected nil cmd with searchActive, got %v", cmd)
	}

	// Пустой список чатов.
	m = testModel(t, nil)
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab})
	m, cmd = updateModel(m, keyRune('d'))
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal with empty chats, got %v", m.mode)
	}
	if cmd != nil {
		t.Fatalf("expected nil cmd with empty chats, got %v", cmd)
	}
}

// TestDeleteChatHotkeyEntersConfirmMode — хоткей в обычном состоянии переводит
// в modeConfirmDelete и сохраняет корректные deleteTarget* (ID, группа/личный,
// название), deleteStep=0.
func TestDeleteChatHotkeyEntersConfirmMode(t *testing.T) {
	m := testModel(t, []auth.Chat{
		{ID: 111, Title: "Группа", IsGroup: true},
		{ID: 222, Title: "Личный"},
	})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyTab}) // фокус в список чатов

	m, cmd := updateModel(m, keyRune('d'))
	if cmd != nil {
		t.Fatalf("expected nil cmd on entering confirm mode, got %v", cmd)
	}
	if m.mode != modeConfirmDelete {
		t.Fatalf("expected modeConfirmDelete, got %v", m.mode)
	}
	if m.deleteTargetChatID != 111 {
		t.Fatalf("expected deleteTargetChatID 111, got %d", m.deleteTargetChatID)
	}
	if m.deleteTargetTitle != "Группа" {
		t.Fatalf("expected deleteTargetTitle Группа, got %q", m.deleteTargetTitle)
	}
	if !m.deleteTargetGroup {
		t.Fatal("expected deleteTargetGroup true for group chat")
	}
	if m.deleteStep != 0 {
		t.Fatalf("expected deleteStep 0, got %d", m.deleteStep)
	}

	// Отмена (Esc) возвращает в Normal, затем курсор вниз — и второй чат
	// (личный) заполняет цель с IsGroup=false.
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after Esc, got %v", m.mode)
	}
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyDown})
	m, _ = updateModel(m, keyRune('d'))
	if m.mode != modeConfirmDelete {
		t.Fatalf("expected modeConfirmDelete for personal chat, got %v", m.mode)
	}
	if m.deleteTargetChatID != 222 {
		t.Fatalf("expected deleteTargetChatID 222, got %d", m.deleteTargetChatID)
	}
	if m.deleteTargetGroup {
		t.Fatal("expected deleteTargetGroup false for personal chat")
	}
	if m.deleteTargetTitle != "Личный" {
		t.Fatalf("expected deleteTargetTitle Личный, got %q", m.deleteTargetTitle)
	}
}

// TestDeleteConfirmGroupCancel — на единственном шаге группы n (или Esc) —
// полная отмена: возврат в modeNormal БЕЗ вызова leaveChatCmd (cancellingCheckClient
// упадёт с t.Fatal, если запрос всё же уйдёт).
func TestDeleteConfirmGroupCancel(t *testing.T) {
	for _, key := range []tea.KeyMsg{keyRune('n'), {Type: tea.KeyEsc}} {
		m := confirmDeleteModel(t, &cancellingCheckClient{t: t}, []auth.Chat{{ID: 111, Title: "Группа", IsGroup: true}})
		m, cmd := updateModel(m, key)
		if m.mode != modeNormal {
			t.Fatalf("key %s: expected modeNormal after cancel, got %v", key.String(), m.mode)
		}
		if cmd != nil {
			t.Fatalf("key %s: expected nil cmd on cancel, got %v", key.String(), cmd)
		}
	}
}

// TestDeleteConfirmGroupYesLeavesAndRemoves — y на группе запускает
// leaveChatCmd(111); chatActionDoneMsg{err:nil} убирает чат из m.chats.
func TestDeleteConfirmGroupYesLeavesAndRemoves(t *testing.T) {
	fake := &fakeClient{responses: []map[string]interface{}{{"@type": "ok"}}}
	m := confirmDeleteModel(t, fake, []auth.Chat{{ID: 111, Title: "Группа", IsGroup: true}})

	m, cmd := updateModel(m, keyRune('y'))
	if cmd == nil {
		t.Fatal("expected non-nil leaveChat cmd")
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after confirm, got %v", m.mode)
	}
	if m.status != "Покидаем чат…" {
		t.Fatalf("expected status Покидаем чат…, got %q", m.status)
	}

	done, ok := cmd().(chatActionDoneMsg)
	if !ok {
		t.Fatalf("expected chatActionDoneMsg, got %T", cmd())
	}
	if done.chatID != 111 || done.err != nil {
		t.Fatalf("unexpected chatActionDoneMsg: %+v", done)
	}

	m, cmd = updateModel(m, done)
	if cmd != nil {
		t.Fatalf("expected nil cmd after chatActionDoneMsg, got %v", cmd)
	}
	if len(m.chats) != 0 {
		t.Fatalf("expected chat removed from list, got %v", m.chats)
	}
	if m.status != "Готово" {
		t.Fatalf("expected status Готово, got %q", m.status)
	}

	foundLeave := false
	for _, req := range fake.requests {
		if req["@type"] == "leaveChat" && req["chat_id"] == int64(111) {
			foundLeave = true
		}
	}
	if !foundLeave {
		t.Fatalf("expected leaveChat(111) request, got %v", fake.requests)
	}
}

// TestDeleteConfirmPrivateStep0YesAdvancesNotSends — y на шаге 0 личного чата
// переводит deleteStep в 1, НЕ меняет mode и НЕ вызывает deleteChatCmd
// (cancellingCheckClient докажет t.Fatal'ом, если запрос всё же уйдёт).
func TestDeleteConfirmPrivateStep0YesAdvancesNotSends(t *testing.T) {
	m := confirmDeleteModel(t, &cancellingCheckClient{t: t}, []auth.Chat{{ID: 222, Title: "Личный"}})

	m, cmd := updateModel(m, keyRune('y'))
	if cmd != nil {
		t.Fatalf("expected no cmd on step 0 yes, got %v", cmd)
	}
	if m.mode != modeConfirmDelete {
		t.Fatalf("expected modeConfirmDelete still, got %v", m.mode)
	}
	if m.deleteStep != 1 {
		t.Fatalf("expected deleteStep 1, got %d", m.deleteStep)
	}
}

// TestDeleteConfirmPrivateStep1RevokeAndCancel — шаг 1 личного чата: y →
// deleteChatHistory(revoke=true), n → deleteChatHistory(revoke=false), Esc —
// полная отмена БЕЗ вызова.
func TestDeleteConfirmPrivateStep1RevokeAndCancel(t *testing.T) {
	// y → revoke=true.
	fake := &fakeClient{}
	m := confirmDeleteModel(t, fake, []auth.Chat{{ID: 222, Title: "Личный"}})
	m, _ = updateModel(m, keyRune('y')) // шаг 0 подтверждён
	if m.mode != modeConfirmDelete || m.deleteStep != 1 {
		t.Fatalf("setup: expected step 1 confirm mode, got mode=%v step=%d", m.mode, m.deleteStep)
	}
	m, cmd := updateModel(m, keyRune('y'))
	if cmd == nil {
		t.Fatal("expected non-nil deleteChat cmd (revoke=true)")
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after step 1 yes, got %v", m.mode)
	}
	if m.status != "Удаляем чат…" {
		t.Fatalf("expected status Удаляем чат…, got %q", m.status)
	}
	if _, ok := cmd().(chatActionDoneMsg); !ok {
		t.Fatalf("expected chatActionDoneMsg, got %T", cmd())
	}
	assertDeleteChatRequest(t, fake.requests, 222, true)

	// n → revoke=false.
	fake = &fakeClient{responses: []map[string]interface{}{{"@type": "ok"}}}
	m = confirmDeleteModel(t, fake, []auth.Chat{{ID: 222, Title: "Личный"}})
	m, _ = updateModel(m, keyRune('y'))
	m, cmd = updateModel(m, keyRune('n'))
	if cmd == nil {
		t.Fatal("expected non-nil deleteChat cmd (revoke=false)")
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after step 1 no, got %v", m.mode)
	}
	if _, ok := cmd().(chatActionDoneMsg); !ok {
		t.Fatalf("expected chatActionDoneMsg, got %T", cmd())
	}
	assertDeleteChatRequest(t, fake.requests, 222, false)

	// Esc на шаге 1 — отмена без вызова DeleteChatHistory.
	m = confirmDeleteModel(t, &cancellingCheckClient{t: t}, []auth.Chat{{ID: 222, Title: "Личный"}})
	m, _ = updateModel(m, keyRune('y'))
	m, cmd = updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil {
		t.Fatalf("expected nil cmd on step 1 Esc, got %v", cmd)
	}
	if m.mode != modeNormal {
		t.Fatalf("expected modeNormal after step 1 Esc, got %v", m.mode)
	}
}

// assertDeleteChatRequest проверяет, что среди запросов есть ровно один
// deleteChatHistory с нужным chat_id/revoke (и remove_from_chat_list=true).
func assertDeleteChatRequest(t *testing.T, requests []map[string]interface{}, chatID int64, revoke bool) {
	t.Helper()
	for _, req := range requests {
		if req["@type"] == "deleteChatHistory" {
			if req["chat_id"] != chatID {
				t.Fatalf("expected deleteChatHistory chat_id %d, got %v", chatID, req["chat_id"])
			}
			if got, ok := req["revoke"].(bool); !ok || got != revoke {
				t.Fatalf("expected deleteChatHistory revoke=%v, got %v", revoke, req["revoke"])
			}
			if got, ok := req["remove_from_chat_list"].(bool); !ok || !got {
				t.Fatalf("expected deleteChatHistory remove_from_chat_list=true, got %v", req["remove_from_chat_list"])
			}
			return
		}
	}
	t.Fatalf("expected deleteChatHistory(%d, revoke=%v) request, got %v", chatID, revoke, requests)
}

// TestChatActionDoneMsgErrorDoesNotTouchChats — история с ошибкой не меняет
// m.chats и показывает статус с текстом ошибки.
func TestChatActionDoneMsgErrorDoesNotTouchChats(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 111, Title: "A"}})
	m.chatCursor = 0

	m, _ = updateModel(m, chatActionDoneMsg{chatID: 111, err: errors.New("network")})
	if len(m.chats) != 1 {
		t.Fatalf("expected chats untouched on error, got %v", m.chats)
	}
	if !strings.Contains(m.status, "network") {
		t.Fatalf("expected status with error text, got %q", m.status)
	}
}

// TestChatActionDoneMsgClosesDisplayedChat — успешное удаление чата, который
// был открыт в msgPane, сбрасывает displayedChat/messages и клампит курсор.
func TestChatActionDoneMsgClosesDisplayedChat(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 111, Title: "Чат"}, {ID: 222, Title: "Другой"}})
	m.displayedChat = 111
	m.messages = []auth.Message{{ID: 1, Text: "текст", SenderName: "Вы"}}
	m.messageCursor = 0
	m.chatCursor = 1 // после удаления 111 из 2-элементного списка нужно заклампить к 0

	m, _ = updateModel(m, chatActionDoneMsg{chatID: 111, err: nil})
	if len(m.chats) != 1 || m.chats[0].ID != 222 {
		t.Fatalf("expected chat 111 removed, got %v", m.chats)
	}
	if m.displayedChat != 0 {
		t.Fatalf("expected displayedChat reset to 0, got %d", m.displayedChat)
	}
	if len(m.messages) != 0 {
		t.Fatalf("expected messages cleared, got %v", m.messages)
	}
	if m.chatCursor != 0 {
		t.Fatalf("expected chatCursor clamped to 0, got %d", m.chatCursor)
	}
	if m.status != "Готово" {
		t.Fatalf("expected status Готово, got %q", m.status)
	}
}

// TestPerChatDraftsAreIndependent — в задаче 0047 введены отдельные черновики
// для каждого открытого чата: смена чата сохраняет текст одного и загружает
// черновик другого (пусто, если его ещё нет), возврат в исходный чат
// восстанавливает его текст. Реальные клавиши, не прямой вызов
// switchDisplayedChat.
func TestPerChatDraftsAreIndependent(t *testing.T) {
	m := testModel(t, []auth.Chat{{ID: 111, Title: "A"}, {ID: 222, Title: "B"}, {ID: 333, Title: "C"}})
	m.focus = focusChats
	m.chatCursor = 0

	selectChatAt := func(cur int) Model {
		m.chatCursor = cur
		m2, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyEnter})
		if cmd == nil {
			t.Fatal("expected non-nil cmd after selecting chat")
		}
		runCmd(t, cmd)
		return m2
	}

	// Чаты переключаются только из списка: из Insert выходим Esc (в Normal,
	// текст черновика НЕ чистится — см. ветку KeyEsc в modeInsert), ещё раз
	// Esc назад в focusChats.
	m = selectChatAt(0) // A (111)
	if m.displayedChat != 111 {
		t.Fatalf("expected displayedChat 111, got %d", m.displayedChat)
	}
	m.focus = focusMessages
	m, _ = updateModel(m, keyRune('i'))
	m = typeText(m, "черновик для A")
	if got := m.composeInput.Value(); got != "черновик для A" {
		t.Fatalf("expected draft A typed, got %q", got)
	}

	// Переключаемся на B (222) — черновик A должен сохраниться.
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEsc}) // Insert → Normal
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEsc}) // focusMessages → focusChats
	m = selectChatAt(1)                                 // B (222)
	if m.displayedChat != 222 {
		t.Fatalf("expected displayedChat 222, got %d", m.displayedChat)
	}
	if got := m.composeInput.Value(); got != "" {
		t.Fatalf("expected empty draft for B, got %q", got)
	}
	if _, ok := m.chatDrafts[111]; !ok {
		t.Fatalf("expected draft for chat A saved on switch")
	}

	// Набираем черновик для B.
	m.focus = focusMessages
	m, _ = updateModel(m, keyRune('i'))
	m = typeText(m, "черновик для B")

	// Переключаемся на C (333) — черновик B сохраняется, у C пусто.
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = selectChatAt(2) // C (333)
	if m.displayedChat != 333 {
		t.Fatalf("expected displayedChat 333, got %d", m.displayedChat)
	}
	if got := m.composeInput.Value(); got != "" {
		t.Fatalf("expected empty draft for C, got %q", got)
	}
	if m.chatDrafts[222] != "черновик для B" {
		t.Fatalf("expected draft %q saved for B, got %q", "черновик для B", m.chatDrafts[222])
	}

	// Возврат в A — восстанавливается его собственный черновик.
	m.focus = focusMessages
	m, _ = updateModel(m, keyRune('i'))
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	m, _ = updateModel(m, tea.KeyMsg{Type: tea.KeyEsc})
	m = selectChatAt(0) // A (111)
	if m.displayedChat != 111 {
		t.Fatalf("expected displayedChat 111, got %d", m.displayedChat)
	}
	if got := m.composeInput.Value(); got != "черновик для A" {
		t.Fatalf("expected draft for A restored, got %q", got)
	}
}

// TestMsgPaneInsertLayoutInvariant — единая рамка панели сообщений в
// Insert-режиме: панель целиком шириной paneW и высотой paneRowHeight,
// лента+чёрная область поля ввода ТОЧНО заполняют внутреннюю площадь
// рамки (feedH+composeH == paneRowHeight-paneFrameV), и ни одна строка не
// оставляет фоновых "дыр" в цвет панели (методика uncoveredCols из 0039).
func TestMsgPaneInsertLayoutInvariant(t *testing.T) {
	for _, tc := range []struct{ w, h int }{
		{100, 30},
		{60, 20},
	} {
		m := testModel(t, []auth.Chat{{ID: 111, Title: "Чат"}})
		m.displayedChat = 111
		m.messages = []auth.Message{
			{ID: 1, SenderName: "Ирина", Text: "привет как дела", Date: 100},
			{ID: 2, SenderName: "Вы", Text: "нормально", Date: 101, IsOutgoing: true},
		}
		m, _ = updateModel(m, tea.WindowSizeMsg{Width: tc.w, Height: tc.h})
		m.refreshMessagesContent()
		m.viewport.GotoBottom()
		m, _ = updateModel(m, keyRune('i'))
		m.composeInput.SetValue("черновик")
		m.syncComposeHeight()

		got := m.msgPane()
		lines := strings.Split(got, "\n")
		paneW := m.viewport.Width
		if len(lines) != m.paneRowHeight {
			t.Fatalf("%dx%d: expected %d rows, got %d", tc.w, tc.h, m.paneRowHeight, len(lines))
		}
		for i, l := range lines {
			if w := lipgloss.Width(l); w != paneW {
				t.Fatalf("%dx%d row %d: expected width %d, got %d", tc.w, tc.h, i, paneW, w)
			}
			if cols := uncoveredCols(l); len(cols) > 0 {
				t.Fatalf("%dx%d row %d: uncovered columns %v (терминальный фон вместо цвета панели)", tc.w, tc.h, i, cols)
			}
		}
		if m.viewport.Height+m.composeInput.Height() != m.paneRowHeight-paneFrameV {
			t.Fatalf("%dx%d: invariant broken: feedH(%d)+composeH(%d) != paneRowHeight(%d)-paneFrameV(%d)",
				tc.w, tc.h, m.viewport.Height, m.composeInput.Height(), m.paneRowHeight, paneFrameV)
		}
		// чёрная область поля ввода — последние composeH строк контента рамки,
		// все несут chromeBackground (0;0;0).
		feedH := m.viewport.Height
		for r := 2 + feedH; r < 2+feedH+m.composeInput.Height(); r++ {
			if !strings.Contains(lines[r], "48;2;0;0;0") {
				t.Fatalf("%dx%d row %d: compose area must be solid chromeBackground, got %q", tc.w, tc.h, r, lines[r])
			}
		}
	}
}
