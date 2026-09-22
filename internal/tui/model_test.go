package tui

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

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
	requests  []map[string]interface{}
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
	view = renderMessages(m.messages, 0, false)
	if !strings.Contains(view, "текст") {
		t.Errorf("renderMessages missing text:\n%s", view)
	}
}

func TestRenderMessagesWrapsLongText(t *testing.T) {
	longText := "одно два три четыре пять шесть семь восемь девять десять"
	msgs := []auth.Message{{ID: 1, SenderName: "Вы", Text: longText, Date: 100, IsOutgoing: true}}

	got := renderMessages(msgs, 20, false)
	lines := strings.Split(got, "\n")
	// Карточка: верхняя рамка + N строк тела + нижняя рамка; длинный текст не
	// умещается в одну строку на 20 колонок, значит N >= 2, итого строк >= 4.
	if len(lines) < 4 {
		t.Fatalf("expected wrapped body to span multiple card rows, got %d lines:\n%s", len(lines), got)
	}
	for _, line := range lines {
		if line == "" {
			continue
		}
		if w := lipgloss.Width(line); w != 20 {
			t.Errorf("card line not exactly 20 cols wide: width=%d, line=%q", w, line)
		}
	}
}

func TestRenderMessagesZeroWidthDoesNotWrap(t *testing.T) {
	longText := "одно два три четыре пять шесть семь восемь девять десять"
	msgs := []auth.Message{{ID: 1, SenderName: "Вы", Text: longText, Date: 100, IsOutgoing: true}}

	for _, width := range []int{0, -1} {
		got := renderMessages(msgs, width, false)
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
	m.viewport.SetContent(renderMessages(m.messages, contentWidth, false))

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
	if got, want := nickColor("Ирина"), nickColor("Ирина"); got != want {
		t.Fatalf("expected same nick color for the same name, got %v and %v", got, want)
	}
}

// Хеш имени реально разбрасывает имена по разным корзинам палитры, а не
// сводит всё в одну: nickIndex("Ирина") и nickIndex("Игорь") посчитаны
// вручную сложением байт-кодов (208+152+… по модулю 4) и дают разные
// значения (2 и 3).
func TestNickIndexDistributesAcrossPalette(t *testing.T) {
	gotIrina, gotIgor := nickIndex("Ирина"), nickIndex("Игорь")
	if gotIrina == gotIgor {
		t.Fatalf("expected different nick indices for different names, both got %d", gotIrina)
	}
}

// Имя отправителя встроено в верхнюю линию рамки карточки — первая строка
// рендера содержит его (ANSI-обёртка стиля не мешает strings.Contains: сам
// текст остаётся непрерывной подстрокой, тот же приём, что и по всему файлу).
func TestRenderMessageCardTopLineContainsSenderName(t *testing.T) {
	msgs := []auth.Message{{ID: 1, SenderName: "Ирина", Text: "привет", Date: 100}}

	got := renderMessages(msgs, 20, false)
	first := strings.Split(got, "\n")[0]
	if !strings.Contains(first, "Ирина") {
		t.Errorf("top card line must contain sender name, got: %q", first)
	}
}

// alignOwnRight=true прижимает МОИ карточки к правому краю ленты: каждая
// непустая строка карточки имеет ведущие пробелы и полную ширину ленты.
func TestRenderMessagesAlignOwnRightPadsOwnCardToRightEdge(t *testing.T) {
	msgs := []auth.Message{{ID: 1, SenderName: "Вы", Text: "моё", Date: 100, IsOutgoing: true}}

	got := renderMessages(msgs, 60, true)
	for _, line := range strings.Split(got, "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			t.Errorf("own card line must be padded to the right edge, got leading non-space: %q", line)
		}
		if w := lipgloss.Width(line); w != 60 {
			t.Errorf("padded own card line width %d != 60: %q", w, line)
		}
	}
}

// alignOwnRight=false (и для исходящего, и для входящего) — карточки на всю
// ширину ленты без ведущих пробелов.
func TestRenderMessagesAlignOwnRightFalseUsesFullWidthNoPadding(t *testing.T) {
	msgs := []auth.Message{
		{ID: 1, SenderName: "Вы", Text: "моё", Date: 100, IsOutgoing: true},
		{ID: 2, SenderName: "Ирина", Text: "чужое", Date: 101},
	}

	got := renderMessages(msgs, 60, false)
	cards := strings.Split(got, "\n\n")
	if len(cards) != 2 {
		t.Fatalf("expected 2 cards, got %d:\n%s", len(cards), got)
	}
	for _, card := range cards {
		firstLine := ""
		for _, line := range strings.Split(card, "\n") {
			if line == "" {
				continue
			}
			firstLine = line
			break
		}
		if strings.HasPrefix(firstLine, " ") {
			t.Errorf("full-width card must not be padded, got leading space: %q", firstLine)
		}
		for _, line := range strings.Split(card, "\n") {
			if line == "" {
				continue
			}
			if w := lipgloss.Width(line); w != 60 {
				t.Errorf("full-width card line width %d != 60: %q", w, line)
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
			got := renderMessages(msgs, width, true)
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
		got := renderMessageCard(auth.Message{ID: 1, SenderName: "?", Text: "x", Date: 100}, width, false)
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

func TestInsertEnterEmptyDraftReturnsToNormalWithoutSending(t *testing.T) {
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
		if m.mode != modeNormal {
			t.Fatalf("draft %q: expected modeNormal, got %v", draft, m.mode)
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

func TestWriteAndReadDraftTempFile(t *testing.T) {
	for _, want := range []string{"привет мир", "строка1\nстрока2"} {
		path, err := writeDraftTempFile(want)
		if err != nil {
			t.Fatalf("writeDraftTempFile(%q) failed: %v", want, err)
		}
		got, err := readDraftTempFile(path)
		if err != nil {
			t.Fatalf("readDraftTempFile(%q) failed: %v", path, err)
		}
		if got != want {
			t.Errorf("round-trip mismatch: want %q, got %q", want, got)
		}
		os.Remove(path)
	}

	// Редакторы добавляют при сохранении финальный перевод строки — он не
	// считается частью текста и должен обрезаться.
	path, err := writeDraftTempFile("текст\n\n")
	if err != nil {
		t.Fatalf("writeDraftTempFile failed: %v", err)
	}
	defer os.Remove(path)
	got, err := readDraftTempFile(path)
	if err != nil {
		t.Fatalf("readDraftTempFile failed: %v", err)
	}
	if got != "текст" {
		t.Errorf("expected trailing newlines trimmed, got %q", got)
	}
}

func TestOpenEditorKeyReturnsCmd(t *testing.T) {
	m := testModel(t, nil)
	m.displayedChat = 111
	m, _ = updateModel(m, keyRune('i'))
	m = typeText(m, "черновик")

	m, cmd := updateModel(m, tea.KeyMsg{Type: tea.KeyCtrlE})
	if cmd == nil {
		t.Fatal("expected non-nil cmd for open_editor key")
	}
	if m.mode != modeInsert {
		t.Fatalf("expected modeInsert after open editor, got %v", m.mode)
	}
	if m.composeInput.Value() != "черновик" {
		t.Fatalf("expected draft preserved after open editor, got %q", m.composeInput.Value())
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
	if !strings.Contains(m.View(), "NORMAL") {
		t.Errorf("normal mode indicator missing:\n%s", m.View())
	}
	if strings.Contains(m.View(), "-- NORMAL --") {
		t.Errorf("legacy '-- NORMAL --' indicator must be gone:\n%s", m.View())
	}

	m.displayedChat = 111
	m, _ = updateModel(m, keyRune('i'))
	if !strings.Contains(m.View(), "редактор") {
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
	if messageColor(true) == messageColor(false) {
		t.Fatal("expected different colors for own and other messages")
	}
}

// Подсветка рамки реально меняет цвет только у панели в фокусе: у неактивной
// цвет рамки не задан (NoColor), у активной — акцентный.
func TestPaneBorderStyleFocusedChangesBorderColor(t *testing.T) {
	if paneBorderStyle(true).GetBorderTopForeground() == paneBorderStyle(false).GetBorderTopForeground() {
		t.Fatal("expected focused pane border color to differ from unfocused")
	}
}

// Регрессия на проверку из файла задачи 0020: замена RoundedBorder на
// DoubleBorder у активной панели не должна менять геометрию. Оба стиля задают
// стороны рамки однорунными строками, поэтому GetHorizontalFrameSize() и
// GetVerticalFrameSize() у фокусированной и нефокусированной рамки равны — если
// в будущем рамка сменится на стиль с другой толщиной, этот тест упадёт.
func TestActiveAndInactiveBorderFrameSizesEqual(t *testing.T) {
	if got, want := paneBorderStyle(true).GetHorizontalFrameSize(), paneBorderStyle(false).GetHorizontalFrameSize(); got != want {
		t.Errorf("horizontal frame: focused %d != unfocused %d", got, want)
	}
	if got, want := paneBorderStyle(true).GetVerticalFrameSize(), paneBorderStyle(false).GetVerticalFrameSize(); got != want {
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
	ref := lipgloss.NewStyle().Background(chatSelectionColor).Foreground(pillTextColor).Render("x")
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

// spaceOutRunes вставляет ровно один пробел между каждая парой рун: "ABC" с
// достаточным бюджетом становится "A B C".
func TestSpaceOutRunesInsertsSingleSpaces(t *testing.T) {
	if got := spaceOutRunes("ABC", 10); got != "A B C" {
		t.Fatalf("spaceOutRunes('ABC', 10) = %q, want 'A B C'", got)
	}
}

// Выход за бюджет — обрезка с многоточием на месте последнего разделителя:
// результат не шире бюджета и заканчивается на "…".
func TestSpaceOutRunesTruncatesWithEllipsis(t *testing.T) {
	got := spaceOutRunes("ПАПКИ", 5)
	if w := lipgloss.Width(got); w > 5 {
		t.Fatalf("spaceOutRunes('ПАПКИ', 5) width %d > 5: %q", w, got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("spaceOutRunes('ПАПКИ', 5) must end with '…', got: %q", got)
	}
}

// Заголовок панели начинается с номера-хоткея в квадратных скобках.
func TestPaneTitleShowsNumberPrefix(t *testing.T) {
	if s := paneTitle(20, 1, "Папки", true); !strings.Contains(s, "[1]") {
		t.Errorf("paneTitle(20, 1, 'Папки', true) must contain '[1]', got: %q", s)
	}
	if s := paneTitle(20, 2, "Чаты", false); !strings.Contains(s, "[2]") {
		t.Errorf("paneTitle(20, 2, 'Чаты', false) must contain '[2]', got: %q", s)
	}
}

// Название в заголовке приводится к капсу с разрядкой — исходные строчные
// буквы в рендере не остаются.
func TestPaneTitleUppercasesAndSpacesName(t *testing.T) {
	got := paneTitle(30, 1, "чаты", true)
	if !strings.Contains(got, "Ч А Т Ы") {
		t.Errorf("paneTitle(30, 1, 'чаты', true) must contain 'Ч А Т Ы', got: %q", got)
	}
	if strings.Contains(got, "чаты") {
		t.Errorf("paneTitle must not contain the original lowercase name, got: %q", got)
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

// Статус-строка Normal-режима без статуса — синяя пилюля "NORMAL" + тусклая
// подсказка (вместо прежнего "-- NORMAL --").
func TestBottomLineNormalModeShowsPill(t *testing.T) {
	m := testModel(t, nil) // status == ""
	got := m.bottomLine()
	if !strings.Contains(got, "NORMAL") {
		t.Errorf("normal mode bottom line must contain the NORMAL pill, got: %q", got)
	}
	if !strings.Contains(got, "←/→") {
		t.Errorf("normal mode bottom line must contain the hint with '←/→', got: %q", got)
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
func TestApplyLayoutShrinksBodyInInsertMode(t *testing.T) {
	m := testModel(t, nil)
	normalHeight := m.viewport.Height

	m.displayedChat = 111
	m, _ = updateModel(m, keyRune('i'))
	want := normalHeight - (composeAreaHeight + 1 - statusReserve)
	if m.viewport.Height != want {
		t.Fatalf("expected viewport.Height %d in insert mode, got %d", want, m.viewport.Height)
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
// (пилюля "NORMAL" + текст подсказки, ~85 колонок) — на узких терминалах она
// по замыслу шире экрана и переносится терминалом (это не класс бага раскладки,
// который ловит этот тест), поэтому строгая проверка ширины применяется к
// заголовкам и панелям, а нижняя строка проверяется только на наличие пилюли.
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
		if !strings.Contains(bottom, "NORMAL") {
			t.Errorf("width %d: bottom line missing NORMAL pill: %q", width, bottom)
		}
	}
}

// TestBottomLineInsertHeightMatchesBudget — регрессия на рассинхронизацию
// бюджета высоты (тот же класс бага, что чинили в 0004/0005/0013): нижняя
// область в Insert-режиме занимает ровно composeAreaHeight+1 строк — независимо
// от числа строк и длины черновика (высота поля фиксирована, длинный черновик
// прокручивается внутренним viewport, а не растягивает бюджет).
func TestBottomLineInsertHeightMatchesBudget(t *testing.T) {
	m := testModel(t, nil)
	m.displayedChat = 111
	m, _ = updateModel(m, keyRune('i'))

	for _, draft := range []string{"", "одна строка", "первая\nвторая\nтретья\nчетвёртая\nпятая", strings.Repeat("длинный ", 50)} {
		m.composeInput.SetValue(draft)
		got := m.bottomLine()
		if lines := strings.Split(got, "\n"); len(lines) != composeAreaHeight+1 {
			t.Fatalf("draft %q: expected %d rows in insert bottom line, got %d:\n%q", draft, composeAreaHeight+1, len(lines), got)
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
// (первая строка View): «[1] П А П К И», «[2] Ч А Т Ы», а у панели сообщений —
// название открытого чата (или «Сообщения», пока ничего не выбрано); названия
// капсом с разрядкой (формат из 0023).
func TestViewIncludesPaneTitles(t *testing.T) {
	chats := []auth.Chat{{ID: 111, Title: "Тестовый чат"}}
	m := testModel(t, chats)

	// displayedChat == 0 — заголовок панели сообщений статичный «Сообщения».
	titleRow := strings.SplitN(m.View(), "\n", 2)[0]
	// Заголовки — капсом с разрядкой («[1] П А П К И» и т.п.), проверяем
	// разряженные подстроки вместо исходных слов.
	for _, want := range []string{"[1] П А П К И", "[2] Ч А Т Ы", "С О О Б Щ Е Н И Я"} {
		if !strings.Contains(titleRow, want) {
			t.Errorf("title row missing %q, got:\n%s", want, titleRow)
		}
	}

	// После выбора чата заголовок панели сообщений — название этого чата.
	m.displayedChat = 111
	m.messages = []auth.Message{{ID: 1, SenderName: "Вы", Text: "hi", Date: 100}}
	titleRow = strings.SplitN(m.View(), "\n", 2)[0]
	if !strings.Contains(titleRow, "Т Е С Т О В Ы Й") {
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
		{"folders", paneTitle(foldersPaneW, 1, "Папки", m.focus == focusFolders), m.foldersPane()},
		{"chats", paneTitle(chatsPaneW, 2, "Чаты", m.focus == focusChats), m.chatPane()},
		{"messages", paneTitle(m.viewport.Width, 3, m.currentChatTitle(), m.focus == focusMessages), m.msgPane()},
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
// пилюли NORMAL (а не вписана в подсказку).
func TestBottomLineShowsVersionRightAligned(t *testing.T) {
	m := testModel(t, nil)
	m.version = "v1.2.3"
	m.width = 100

	got := m.bottomLine()
	if !strings.Contains(got, "v1.2.3") {
		t.Fatalf("bottom line must contain the version, got: %q", got)
	}
	if !strings.Contains(got, "NORMAL") {
		t.Fatalf("bottom line must contain the NORMAL pill, got: %q", got)
	}
	if strings.Index(got, "v1.2.3") <= strings.Index(got, "NORMAL") {
		t.Fatalf("version must be physically to the right of the NORMAL pill, got: %q", got)
	}
}

// TestBottomLineShowsUpdateAvailable — при найденном обновлении правая часть
// нижней строки показывает "текущая → новая (:update)". Ширина 120, а не 100
// (как в файле задачи): измерено, что левая часть (пилюля NORMAL + подсказка)
// занимает 85 колонок, а вся правая часть "v1.2.3 → v1.3.0 (:update)" — ещё 25
// (итого 110) — на 100 версия по замыслу bottomLine (pad < 1) опускается целиком,
// и тест проверял бы противоречащий себе случай.
func TestBottomLineShowsUpdateAvailable(t *testing.T) {
	m := testModel(t, nil)
	m.version = "v1.2.3"
	m.updateAvailable = "v1.3.0"
	m.width = 120

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
