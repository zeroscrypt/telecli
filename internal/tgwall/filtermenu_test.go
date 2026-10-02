package tgwall

import (
	"context"
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/auth"
	"telecli/internal/config"
)

// Меню фильтра на живой стене: открытие по настроенной клавише, перехват ввода,
// живой предпросмотр (стена перефильтровывается сразу, без «применить»), закрытие
// с сохранением и переживание настроек перезапуском.
//
// Проверяется всё через настоящий Update: поведение, которое не отличимо от «всё
// прошло хорошо», обычно живёт именно в недостающем перехвате клавиши, и прямой
// вызов обработчика такой случай не поймал бы.

// filterTestCards — шесть источников всех трёх типов, два из них заглушены, чаты
// 2 и 5 состоят в папке 10, чат 4 — в папке 20. Состав подобран так, чтобы
// каждое правило фильтра имело что показать и что скрыть.
func filterTestCards() []card {
	return wallCards([]wallMessage{
		{Chat: auth.Chat{ID: 1, Title: "Канал", Kind: auth.ChatChannel}, Message: auth.Message{ID: 11, Text: "канал", Date: 1}},
		{Chat: auth.Chat{ID: 2, Title: "Группа в папке", Kind: auth.ChatGroup}, Message: auth.Message{ID: 22, Text: "группа-10", Date: 2}},
		{Chat: auth.Chat{ID: 3, Title: "Личный", Kind: auth.ChatPrivate}, Message: auth.Message{ID: 33, Text: "личный", Date: 3}},
		{Chat: auth.Chat{ID: 4, Title: "Группа в другой папке", Kind: auth.ChatGroup}, Message: auth.Message{ID: 44, Text: "группа-20", Date: 4}},
		{Chat: auth.Chat{ID: 5, Title: "Заглушённая группа", Kind: auth.ChatGroup, Muted: true}, Message: auth.Message{ID: 55, Text: "группа-заглушена-10", Date: 5}},
		{Chat: auth.Chat{ID: 6, Title: "Заглушённый канал", Kind: auth.ChatChannel, Muted: true}, Message: auth.Message{ID: 66, Text: "канал-заглушён", Date: 6}},
	})
}

// filterTestChats — те же чаты, что и у filterTestCards: без них стена не
// соберёт карточку из живого апдейта (chatsByID).
func filterTestChats() []auth.Chat {
	return []auth.Chat{
		{ID: 1, Title: "Канал", Kind: auth.ChatChannel},
		{ID: 2, Title: "Группа в папке", Kind: auth.ChatGroup},
		{ID: 3, Title: "Личный", Kind: auth.ChatPrivate},
		{ID: 4, Title: "Группа в другой папке", Kind: auth.ChatGroup},
		{ID: 5, Title: "Заглушённая группа", Kind: auth.ChatGroup, Muted: true},
		{ID: 6, Title: "Заглушённый канал", Kind: auth.ChatChannel, Muted: true},
	}
}

func filterTestFoldersList() []auth.Folder {
	return []auth.Folder{{ID: 10, Name: "Работа"}, {ID: 20, Name: "Дом"}}
}

// visibleTexts — тексты карточек, которые стена показывает. Именно они, а не
// полный поток: проверяется результат фильтра, а не то, что он где-то применён.
func visibleTexts(m Model) []string {
	texts := make([]string, 0, len(m.cards))
	for _, item := range m.cards {
		texts = append(texts, item.Text)
	}
	return texts
}

func assertVisible(t *testing.T, m Model, want ...string) {
	t.Helper()
	got := visibleTexts(m)
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("стена показывает %v, ждали %v", got, want)
	}
}

// newFilterTestModel — стена с фильтром из настроек, папками и составом папок.
// Всё подаётся через те же сообщения, что и в живой программе.
func newFilterTestModel(t *testing.T, settings config.Settings) Model {
	t.Helper()
	m := New(context.Background(), nil, "", config.DefaultTgwallKeyBindings(), settings, testAppName, testVersion)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)
	next, _ = m.Update(wallFoldersUpdateMsg{folders: filterTestFoldersList(), valid: true})
	m = next.(Model)
	next, _ = m.Update(wallFolderChatsMsg{
		builtFor: []int32{10, 20},
		byFolder: map[int32][]int64{10: {2, 5}, 20: {4}},
	})
	m = next.(Model)
	next, _ = m.Update(wallLoadedMsg{cards: filterTestCards(), chats: filterTestChats()})
	m = next.(Model)
	_ = m.input.Focus()
	return m
}

// filterCtrlP — нажатие настроенной клавиши открытия меню.
var filterCtrlP = tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}

var (
	// filterSpace — настроенное переключение галки (DefaultTgwallKeyBindings.Toggle).
	filterSpace = tea.KeyPressMsg{Code: tea.KeySpace}
	// filterEnter — keySelect стены, то есть отправка текста. В меню фильтра он
	// ничего не переключает (проверяет TestFilterMenuBlocksWallInput).
	filterEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	filterEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
	filterUp    = tea.KeyPressMsg{Code: tea.KeyUp}
	filterDown  = tea.KeyPressMsg{Code: tea.KeyDown}
)

// openFilter — открыть меню фильтра и дойти до строки с данным индексом в списке.
func openFilter(t *testing.T, m Model) Model {
	t.Helper()
	m, _ = pressWallKey(t, m, filterCtrlP)
	if m.filterMenu == nil {
		t.Fatal("меню фильтра не открылось по настроенной клавише")
	}
	return m
}

// moveFilterTo — встать курсором на строку с индексом index в списке строк меню
// (стрелками, как это делает человек: список строк подтверждается отдельно тестом
// TestFilterMenuRowsLayout).
func moveFilterTo(t *testing.T, m Model, index int) Model {
	t.Helper()
	for m.filterMenu.cursor != index {
		current := m.filterMenu.cursor
		if index > current {
			m, _ = pressWallKey(t, m, filterDown)
		} else {
			m, _ = pressWallKey(t, m, filterUp)
		}
		if m.filterMenu.cursor == current {
			t.Fatalf("курсор меню застрял на %d, ждали %d", current, index)
		}
	}
	return m
}

// settingsPathForFilterTest — временный settings.toml на время теста. Отдельный
// override, а не переменная окружения: тесты в одном процессе идут параллельно
// только по t.Parallel, которого здесь нет, но путь всё равно обязан быть свой,
// иначе тест писал бы в настоящий файл пользователя.
func settingsPathForFilterTest(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.toml")
	config.SetSettingsPathForTest(path)
	t.Cleanup(func() { config.SetSettingsPathForTest("") })
	return path
}

// TestFilterMenuOpensOnConfiguredKey — меню открывается НАСТРОЕННОЙ клавишей, а
// не зашитой: при переназначенной клавише зашитая продолжала бы открывать меню, а
// настроенная — не работала бы, и правка keybindings.toml была бы враньём.
func TestFilterMenuOpensOnConfiguredKey(t *testing.T) {
	settingsPathForFilterTest(t)
	keys := config.DefaultTgwallKeyBindings()
	keys.Filter = []string{"ctrl+g"}

	m := New(context.Background(), nil, "", keys, config.DefaultSettings(), testAppName, testVersion)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)

	if m.filterMenu != nil {
		t.Fatal("до нажатия меню на экране быть не должно")
	}
	m, _ = pressWallKey(t, m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	if m.filterMenu == nil {
		t.Fatal("меню не открылось по настроенной клавише ctrl+g")
	}

	// И зашитой ctrl+p при переназначенной клавише — не открывается.
	m2 := New(context.Background(), nil, "", keys, config.DefaultSettings(), testAppName, testVersion)
	next, _ = m2.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m2, _ = pressWallKey(t, m2, filterCtrlP)
	if m2.filterMenu != nil {
		t.Fatal("зашитая ctrl+p открыла меню, хотя в конфигурации назначена другая клавиша")
	}
}

// TestFilterMenuBlocksWallInput — открытое меню перехватывает ввод раньше стены:
// переключение галки не должно заодно двигать курсор по карточкам, уходить в
// поле ввода или открывать модалку удаления.
func TestFilterMenuBlocksWallInput(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	m.cursor = 0
	m = openFilter(t, m)
	m = moveFilterTo(t, m, 8) // «Приглушённые» — галка, которую есть куда переключить

	// Пробел — настроенное переключение (keyToggle). Он обязан переключить галку и
	// НЕ отправить сообщение: пробел в поле ввода — обычный символ, и без
	// перехвата он напечатался бы прямо в текст.
	before := m.filter
	m, _ = pressWallKey(t, m, filterSpace)
	if m.filter.same(before) {
		t.Fatal("пробел в меню фильтра не переключил галку — ввод ушёл стене, а не меню")
	}
	if m.input.Value() != "" {
		t.Fatalf("пробел в меню фильтра не должен был ничего отправить, в поле: %q", m.input.Value())
	}
	if m.cursor != 0 {
		t.Fatalf("курсор стены уехал на %d, хотя ввод принадлежал меню", m.cursor)
	}

	// И наоборот: Enter — это keySelect стены, то есть отправка. В меню он больше
	// не переключает ничего (задача 0166), но и отправлять не должен: поле пустое,
	// так что «отправка» была бы неотличима от тишины, а вот переключенная галка
	// была бы видна сразу.
	before = m.filter
	m, _ = pressWallKey(t, m, filterEnter)
	if !m.filter.same(before) {
		t.Fatal("Enter в меню фильтра переключил галку — переключение живёт на keyToggle, а не на keySelect")
	}
	if m.input.Value() != "" {
		t.Fatalf("Enter в меню фильтра не должен был ничего отправить, в поле: %q", m.input.Value())
	}
	if m.filterMenu == nil {
		t.Fatal("Enter закрыл меню фильтра — закрывает keyBack, а не keySelect")
	}

	// Delete — keyDeleteMessage, то самое действие стены, ради которого особенно
	// важно не пропустить нажатие мимо меню.
	m, _ = pressWallKey(t, m, tea.KeyPressMsg{Code: tea.KeyDelete})
	if m.showConfirm {
		t.Fatal("Delete в меню фильтра открыл модалку удаления — ввод ушёл стене")
	}
}

// TestFilterCursorFollowsSourceWhenListShifts — снятие галки укорачивает список
// ПЕРЕД источником под курсором, и курсор обязан остаться на том же источнике, а
// не на прежнем номере. Без этого переключение галки молча уводит человека на
// соседнюю карточку — тот же класс ошибки, что был найден у живых апдейтов.
func TestFilterCursorFollowsSourceWhenListShifts(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	m = clampToTestCursor(t, m, 2) // «личный» (chat 3)
	if m.cards[m.cursor].ChatID != 3 {
		t.Fatalf("подготовка: под курсором chat %d, ждали 3", m.cards[m.cursor].ChatID)
	}

	m = openFilter(t, m)
	m = moveFilterTo(t, m, 1) // снимаем «Каналы»: карточка канала (индекс 0) уходит
	m, _ = pressWallKey(t, m, filterSpace)

	if got := m.cards[m.cursor].ChatID; got != 3 {
		t.Fatalf("после снятия типа под курсором chat %d, ждали 3 (тот же источник)", got)
	}
}

// TestFilterLivePreviewWithoutApply — живой предпросмотр: список карточек стены
// меняется сразу после переключения чекбокса, без отдельного «применить» и без
// закрытия меню. Это главное отличие от модалки подтверждения.
func TestFilterLivePreviewWithoutApply(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	// По умолчанию заглушённые скрыты, поэтому видны 1,2,3,4.
	assertVisible(t, m, "канал", "группа-10", "личный", "группа-20")

	m = openFilter(t, m)
	// «Все», «Каналы», «Чаты», «Личные», «Папки», папка 10, папка 20,
	// «Дополнительно», «Приглушённые».
	m = moveFilterTo(t, m, 8)
	m, _ = pressWallKey(t, m, filterSpace)

	if m.filterMenu == nil {
		t.Fatal("переключение галки закрыло меню — предпросмотр обязан работать при открытом меню")
	}
	// Заглушённые появились на стене сразу, меню всё ещё открыто.
	assertVisible(t, m, "канал", "группа-10", "личный", "группа-20", "группа-заглушена-10", "канал-заглушён")
}

// TestFilterLivePreviewByTypeFolderAndMute — предпросмотр по каждому из трёх
// правил, плюс проверка, что стена под оверлеем действительно перерисовалась (а не
// только изменилось состояние модели).
func TestFilterLivePreviewByTypeFolderAndMute(t *testing.T) {
	t.Run("снят тип — его карточки уходят сразу", func(t *testing.T) {
		settingsPathForFilterTest(t)
		m := newFilterTestModel(t, config.DefaultSettings())
		m = openFilter(t, m)
		m = moveFilterTo(t, m, 1) // «Каналы»
		m, _ = pressWallKey(t, m, filterSpace)
		assertVisible(t, m, "группа-10", "личный", "группа-20")
	})

	t.Run("отмечена папка — остаются только её чаты", func(t *testing.T) {
		settingsPathForFilterTest(t)
		m := newFilterTestModel(t, config.DefaultSettings())
		m = openFilter(t, m)
		// Папка 10 содержит чаты 2 и 5, но чат 5 заглушён, а «Приглушённые» по
		// умолчанию снято: остаётся только чат 2. Оба правила работают вместе.
		m = moveFilterTo(t, m, 5) // папка 10 «Работа»
		m, _ = pressWallKey(t, m, filterSpace)
		assertVisible(t, m, "группа-10")
	})

	t.Run("снята отметка «Приглушённые» — заглушённые появляются", func(t *testing.T) {
		settingsPathForFilterTest(t)
		m := newFilterTestModel(t, config.DefaultSettings())
		m = openFilter(t, m)
		m = moveFilterTo(t, m, 8)
		m, _ = pressWallKey(t, m, filterSpace)
		assertVisible(t, m, "канал", "группа-10", "личный", "группа-20", "группа-заглушена-10", "канал-заглушён")
	})

	t.Run("стена под оверлеем перерисована по-настоящему", func(t *testing.T) {
		settingsPathForFilterTest(t)
		m := newFilterTestModel(t, config.DefaultSettings())
		before := ansi.Strip(m.View().Content)
		if !strings.Contains(before, "Канал") {
			t.Fatalf("подготовка: канал должен быть нарисован до переключения:\n%s", before)
		}
		m = openFilter(t, m)
		m = moveFilterTo(t, m, 1)
		m, _ = pressWallKey(t, m, filterSpace)
		after := ansi.Strip(m.View().Content)
		if before == after {
			t.Fatal("экран не изменился — предпросмотр не дошёл до отрисовки")
		}
		// Снятый тип исчез с карточек, но его название осталось в меню — в строке
		// «Каналы». Поэтому проверяем именно область стены, а не весь экран.
		wallOnly := wallZoneText(t, m)
		if strings.Contains(wallOnly, "Канал") {
			t.Fatalf("снятый тип всё ещё нарисован на стене:\n%s", wallOnly)
		}
		if !strings.Contains(wallOnly, "Личный") {
			t.Fatalf("остальные карточки должны остаться на стене:\n%s", wallOnly)
		}
	})
}

// TestFilterCursorStaysOnSameSourceOnToggle — переключение галки не уводит курсор
// стены на другой источник: человек смотрит на конкретную карточку, и смена
// фильтра не должна незаметно переставить его на чужую (тот же принцип, что у
// живого апдейта и у Ctrl+R).
func TestFilterCursorStaysOnSameSourceOnToggle(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	m = clampToTestCursor(t, m, 2) // «личный» (chat 3)
	if m.cards[m.cursor].ChatID != 3 {
		t.Fatalf("подготовка: под курсором chat %d, ждали 3", m.cards[m.cursor].ChatID)
	}

	m = openFilter(t, m)
	m = moveFilterTo(t, m, 8) // «Приглушённые»
	m, _ = pressWallKey(t, m, filterSpace)

	if got := m.cards[m.cursor].ChatID; got != 3 {
		t.Fatalf("после переключения под курсором chat %d, ждали 3 (тот же источник)", got)
	}
}

// TestFilterCursorFallsBackWhenSourceHidden — если источник под курсором фильтр
// скрыл, курсор остаётся на прежнем месте списка (его занимает ближайшая видимая
// карточка), а не уезжает произвольно.
func TestFilterCursorFallsBackWhenSourceHidden(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	m = clampToTestCursor(t, m, 3) // «группа-20» (chat 4)
	if m.cards[m.cursor].ChatID != 4 {
		t.Fatalf("подготовка: под курсором chat %d, ждали 4", m.cards[m.cursor].ChatID)
	}

	// Отмечаем папку 10: chat 4 в неё не входит и с экрана уходит.
	m = openFilter(t, m)
	m = moveFilterTo(t, m, 5)
	m, _ = pressWallKey(t, m, filterSpace)

	if len(m.cards) != 1 {
		t.Fatalf("после отметки папки 10 на стене %v, ждали только «группа-10»", visibleTexts(m))
	}
	if got := m.cards[m.cursor].ChatID; got != 2 {
		t.Fatalf("курсор должен встать на единственную видимую карточку (chat 2), а он на chat %d", got)
	}
}

// TestFilterHiddenSourceReturnsAfterUnmark — обратная сторона: снятие отметки
// возвращает скрытые карточки на стену. Без этого переключение галки было бы
// односторонним, и полный поток m.source накапливал бы мусор.
func TestFilterHiddenSourceReturnsAfterUnmark(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	m = openFilter(t, m)
	m = moveFilterTo(t, m, 5) // папка 10
	m, _ = pressWallKey(t, m, filterSpace)
	// В папке 10 два чата, но заглушённый по умолчанию скрыт: правила фильтра
	// работают вместе, а не по очереди.
	assertVisible(t, m, "группа-10")

	m, _ = pressWallKey(t, m, filterSpace) // сняли ту же галку
	assertVisible(t, m, "канал", "группа-10", "личный", "группа-20")
}

// TestFilterGuardOnWall — защита от пустого состояния типов проверяется и на живой
// стене: снятие последней галки типа не оставляет стену вовсе пустой.
func TestFilterGuardOnWall(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	m = openFilter(t, m)
	// Снимаем типы по одному: «Каналы», «Чаты», «Личные».
	for _, row := range []int{1, 2, 3} {
		m = moveFilterTo(t, m, row)
		m, _ = pressWallKey(t, m, filterSpace)
	}
	if !m.filter.allTypes() {
		t.Fatalf("после снятия всех типов «Все» обязано восстановиться: %+v", m.filter)
	}
	if len(m.cards) == 0 {
		t.Fatal("стена осталась пустой — защита от пустого состояния не сработала")
	}
	// Откат касается только типов: «Приглушённые» он не трогает, поэтому
	// заглушённые источники на стене не появились.
	assertVisible(t, m, "канал", "группа-10", "личный", "группа-20")
}

// TestFilterPersistsAcrossRestart — сохранённый фильтр переживает пересоздание
// модели, то есть «перезапуск». Это и есть персистентность: без неё состояние,
// нарисованное в меню, существовало бы только до выхода из программы.
func TestFilterPersistsAcrossRestart(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	m = openFilter(t, m)
	m = moveFilterTo(t, m, 1) // сняли «Каналы»
	m, _ = pressWallKey(t, m, filterSpace)
	m = moveFilterTo(t, m, 5) // отметили папку 10
	m, _ = pressWallKey(t, m, filterSpace)
	m = moveFilterTo(t, m, 8) // отметили «Приглушённые»
	m, _ = pressWallKey(t, m, filterSpace)
	visibleBefore := visibleTexts(m)

	// Закрытие меню — единственное место, где состояние пишется в файл.
	m, _ = pressWallKey(t, m, filterEsc)
	if m.filterMenu != nil {
		t.Fatal("меню не закрылось по Esc")
	}
	// Закрытие не откатывает сделанное: изменения уже применены.
	assertVisible(t, m, visibleBefore...)

	// «Перезапуск»: настройки читаются заново, модель создаётся заново.
	settings, err := config.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings после закрытия меню: %v", err)
	}
	restarted := newFilterTestModel(t, settings)
	if !restarted.filter.same(m.filter) {
		t.Fatalf("после перезапуска фильтр %+v, а до закрытия был %+v", restarted.filter, m.filter)
	}
	assertVisible(t, restarted, visibleBefore...)
	if len(settings.WallFilter.Folders) != 1 || settings.WallFilter.Folders[0] != 10 {
		t.Fatalf("в настройках должны быть отмечены папки [10], а там %v", settings.WallFilter.Folders)
	}
	if settings.WallFilter.ShowChannels || !settings.WallFilter.ShowMuted {
		t.Fatalf("сохранённый фильтр не сходится с состоянием меню: %+v", settings.WallFilter)
	}
}

// TestFilterPersistsOnSameKeyClose — то же нажатие, что открывает меню, и
// закрывает его, и состояние при этом сохраняется: отдельного действия «закрыть
// меню фильтра» в конфигурации нет намеренно.
func TestFilterPersistsOnSameKeyClose(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	m = openFilter(t, m)
	m = moveFilterTo(t, m, 8)
	m, _ = pressWallKey(t, m, filterSpace)
	m, _ = pressWallKey(t, m, filterCtrlP)
	if m.filterMenu != nil {
		t.Fatal("повторное нажатие той же клавиши не закрыло меню")
	}
	settings, err := config.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if !settings.WallFilter.ShowMuted {
		t.Fatalf("состояние должно было сохраниться при закрытии той же клавишей: %+v", settings.WallFilter)
	}
}

// TestFilterCloseDoesNotRollBack — закрытие меню не откатывает применённые
// изменения: предпросмотр уже применил их к стене, а «отмена» означала бы второе
// состояние фильтра, которого нигде больше нет.
func TestFilterCloseDoesNotRollBack(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	m = openFilter(t, m)
	m = moveFilterTo(t, m, 1) // сняли «Каналы»
	m, _ = pressWallKey(t, m, filterSpace)
	assertVisible(t, m, "группа-10", "личный", "группа-20")

	m, _ = pressWallKey(t, m, filterEsc)
	assertVisible(t, m, "группа-10", "личный", "группа-20")
}

// Клавиша переключения — отдельное настроенное действие (keyToggle, по умолчанию
// space), а не keySelect стены. Отдельное поле в конфигурации: Enter в стене
// означает отправку текста, и если бы переключение галки жило на нём же, то
// переназначив отправку, человек невольно переназначил бы ещё и меню.
func TestFilterMenuTogglesByConfiguredToggleKey(t *testing.T) {
	t.Run("по умолчанию переключает space, а Enter молчит", func(t *testing.T) {
		settingsPathForFilterTest(t)
		m := newFilterTestModel(t, config.DefaultSettings())
		m = openFilter(t, m)
		m = moveFilterTo(t, m, 1) // «Каналы»

		before := m.filter
		m, _ = pressWallKey(t, m, filterEnter)
		if !m.filter.same(before) {
			t.Fatal("Enter переключил галку в меню фильтра — переключение живёт на keyToggle, не на keySelect")
		}

		m, _ = pressWallKey(t, m, filterSpace)
		if m.filter.same(before) {
			t.Fatal("пробел не переключил галку, хотя он и есть настроенное переключение по умолчанию")
		}
	})

	t.Run("переназначенная клавиша работает, дефолтная нет", func(t *testing.T) {
		settingsPathForFilterTest(t)
		keys := config.DefaultTgwallKeyBindings()
		keys.Toggle = []string{"ctrl+g"}
		m := newFilterTestModel(t, config.DefaultSettings())
		m.keymap = keyMap{keys: keys}
		m = openFilter(t, m)
		m = moveFilterTo(t, m, 1) // «Каналы»

		// Пробел в этой модели больше не переключает: он убран из конфигурации.
		before := m.filter
		m, _ = pressWallKey(t, m, filterSpace)
		if !m.filter.same(before) {
			t.Fatal("пробел переключил галку, хотя в конфигурации назначен ctrl+g — конфигурация декорация")
		}

		m, _ = pressWallKey(t, m, tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
		if m.filter.same(before) {
			t.Fatal("настроенная клавиша ctrl+g не переключила галку")
		}
		// И подсказка обязана называть ту же клавишу, которой человек и жмёт:
		// обещать ctrl+g, а ждать пробел (или наоборот) — враньё на экране.
		// Подпись берётся из keymap'а, а не пишется руками: префикс сокращён
		// (см. wallDisplayKey), и зашитое здесь «ctrl+g» разошлось бы с ней при
		// первой же правке.
		if hint := m.filterMenuKeys().hint(); !strings.Contains(hint, m.keymap.label(keyToggle)) {
			t.Fatalf("подсказка меню = %q, а переключение показывается как %q",
				hint, m.keymap.label(keyToggle))
		}
	})
}

// Подсказка меню показывает настроенное переключение. Отдельно от поведения:
// подсказка собирается из keymap'а, а поведение читает keymap в updateFilterMenu,
// и разойтись они могут только если одна из двух сторон забудет про конфигурацию.
func TestFilterMenuHintShowsTheConfiguredToggleKey(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	if hint := m.filterMenuKeys().hint(); !strings.Contains(hint, "space переключить") {
		t.Fatalf("подсказка меню = %q, а по умолчанию переключение на пробеле", hint)
	}
	if strings.Contains(m.filterMenuKeys().hint(), "enter переключить") {
		t.Fatal("подсказка обещает переключение по Enter — Enter в меню больше ничего не делает")
	}
}

// TestFilterMenuCursorSkipsHeaders — курсор перескакивает заголовки разделов:
// встать на «Папки» или «Дополнительно» бессмысленно, там нечего переключать, и
// человек подумал бы, что меню зависло.
func TestFilterMenuCursorSkipsHeaders(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	m = openFilter(t, m)

	// Вниз от «Личные» (3) сразу перескакивает заголовок «Папки» (4).
	m = moveFilterTo(t, m, 3)
	m, _ = pressWallKey(t, m, filterDown)
	if m.filterMenu.cursor != 5 {
		t.Fatalf("курсор встал на %d, ждали 5 (первую папку, а не заголовок)", m.filterMenu.cursor)
	}
	// И обратно вверх: с папки 20 (6) перескакивает заголовок обратно на «Личные».
	m = moveFilterTo(t, m, 6)
	m, _ = pressWallKey(t, m, filterUp)
	if m.filterMenu.cursor != 5 {
		t.Fatalf("курсор встал на %d, ждали 5", m.filterMenu.cursor)
	}
	// Через заголовок «Дополнительно» (7) вниз: с папки 20 сразу на «Приглушённые».
	m = moveFilterTo(t, m, 6)
	m, _ = pressWallKey(t, m, filterDown)
	if m.filterMenu.cursor != 8 {
		t.Fatalf("курсор встал на %d, ждали 8 («Приглушённые», а не заголовок)", m.filterMenu.cursor)
	}
}

// TestFilterMenuCursorStaysOnRowAfterToggle — переключение галки не уводит курсор
// меню на другую строку: строки пересобираются после каждого нажатия, и без
// удержания курсора каждое нажатие возвращало бы человека на «Все».
func TestFilterMenuCursorStaysOnRowAfterToggle(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	m = openFilter(t, m)

	m = moveFilterTo(t, m, 6) // папка 20
	m, _ = pressWallKey(t, m, filterSpace)
	if m.filterMenu.cursor != 6 {
		t.Fatalf("после переключения курсор меню уехал на %d, ждали 6", m.filterMenu.cursor)
	}
	row, _ := m.filterMenu.rowAt()
	if row.kind != filterRowFolder || row.folderID != 20 {
		t.Fatalf("под курсором %+v, ждали папку 20", row)
	}
	if !row.checked {
		t.Fatal("галка в пересобранной строке обязана показать новое состояние")
	}

	// То же для типа: переключение «Каналы» должно пересчитать «Все» над ним, но
	// курсор остаться на «Каналы».
	m = moveFilterTo(t, m, 1)
	m, _ = pressWallKey(t, m, filterSpace)
	if m.filterMenu.cursor != 1 {
		t.Fatalf("после переключения типа курсор уехал на %d, ждали 1", m.filterMenu.cursor)
	}
	all, _ := m.filterMenu.rows[0], true
	if all.checked {
		t.Fatal("«Все» обязана пересчитаться в снятое, когда один тип снят")
	}
}

// TestFilterMenuOverlayRendered — меню действительно нарисовано поверх стены, а не
// только живёт в состоянии: заголовок, подписи и чекбоксы должны быть на экране.
func TestFilterMenuOverlayRendered(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	before := m.View().Content
	if strings.Contains(before, filterMenuTitle) {
		t.Fatal("меню нарисовано до того, как его открыли")
	}

	m = openFilter(t, m)
	screen := ansi.Strip(m.View().Content)
	for _, want := range []string{
		filterMenuTitle,
		filterLabelAll,
		filterLabelChannels,
		filterLabelChats,
		filterLabelPersonal,
		filterLabelFolders,
		"Работа",
		"Дом",
		filterLabelExtra,
		filterLabelMuted,
		filterBoxOn,
		filterBoxOff,
		// Подсказка по клавишам собирается из конфигурации, как у модалки, и
		// показывает настроенное переключение (пробел), а не Enter: подсказка,
		// обещающая не ту клавишу, вреднее отсутствия подсказки.
		"space переключить",
		"esc закрыть",
	} {
		if !strings.Contains(screen, want) {
			t.Fatalf("на экране меню нет %q:\n%s", want, screen)
		}
	}
	// Содержимое стены под меню остаётся: меню накладывается, а не заменяет.
	if !strings.Contains(screen, "личный") {
		t.Fatal("стена под меню пропала — меню должно накладываться поверх неё")
	}
}

// ГЛАВНОЕ требование задачи 0166: отмеченная галка строки типа красится акцентным
// цветом ЭТОГО типа — тем же, что левая граница карточки такого источника на стене
// (см. TestCardAccentFollowsTheSourceType). Иначе человек сверял бы строку меню с
// карточкой как разные сущности: в меню «Каналы» были бы серыми, а на стене
// каналы — синими.
//
// Акцент достаётся ТОЛЬКО галке: подпись строки остаётся обычным PaletteText.
// Проверяется и это отдельно — иначе «покрасили строку» прошло бы тест, оставив
// подпись нечитаемой на чужом фоне.
func TestFilterMenuCheckedBoxTakesItsTypeAccent(t *testing.T) {
	const width = 40
	for _, test := range []struct {
		kind   filterRowKind
		accent color.Color
		typed  bool
	}{
		{kind: filterRowChannels, accent: PaletteChannel, typed: true},
		{kind: filterRowChats, accent: PaletteChat, typed: true},
		{kind: filterRowPersonal, accent: PalettePersonal, typed: true},
		// Типа нет — у «Все», папок и «Приглушённых»: это набор или правило, а не
		// один источник. Их отмеченная галка обязана остаться обычной, иначе им
		// приписывался бы чужой тип.
		{kind: filterRowAll, accent: PaletteText},
		{kind: filterRowFolder, accent: PaletteText},
		{kind: filterRowMuted, accent: PaletteText},
	} {
		row := filterRow{kind: test.kind, label: "строка", folderID: 10, checked: true}
		// Сам признак «цвет есть» проверяется на filterRow, а не только на
		// отрисованной строке. Возврат для строк без типа «цвета по умолчанию»
		// нарисовал бы ровно ту же картинку, что и правильный код, и проверка по
		// строке его бы не поймала.
		accent, typed := row.accent()
		if typed != test.typed {
			t.Fatalf("строка %d: «цвет есть» = %v, а ждали %v", test.kind, typed, test.typed)
		}
		if test.typed && accent != test.accent {
			t.Fatalf("строка %d: accent() = %v, а ждали %v", test.kind, accent, test.accent)
		}

		line := filterMenuRowLine(width, row, false)
		if want := foregroundSGR(test.accent, PaletteBackgroundPanel) + filterBoxOn; !strings.Contains(line, want) {
			t.Fatalf("отмеченная галка строки %d нарисована не цветом %v:\n%q", test.kind, test.accent, line)
		}
		// Подпись строки — обычным цветом, даже когда галка акцентная: акцент нужен
		// галке, чтобы её узнать, а не всей строке, иначе текст на чужом фоне
		// читался бы хуже.
		if !strings.Contains(line, foregroundSGR(PaletteText, PaletteBackgroundPanel)+row.label) {
			t.Fatalf("подпись строки %d должна остаться PaletteText, а строка нарисована так:\n%q", test.kind, line)
		}
	}
}

// Неотмеченный чекбокс — обычным цветом, у ЛЮБОЙ строки. Акцентный цвет внутри
// пустых скобок означал бы «включено» там, где выключено, — и по цвету нельзя было
// бы отличить снятую галку от снятой подписи.
func TestFilterMenuUncheckedBoxStaysPlain(t *testing.T) {
	const width = 40
	for _, kind := range []filterRowKind{
		filterRowAll, filterRowChannels, filterRowChats,
		filterRowPersonal, filterRowFolder, filterRowMuted,
	} {
		row := filterRow{kind: kind, label: "строка", folderID: 10, checked: false}
		line := filterMenuRowLine(width, row, false)
		if want := foregroundSGR(PaletteText, PaletteBackgroundPanel) + filterBoxOff; !strings.Contains(line, want) {
			t.Fatalf("снятая галка строки %d нарисована не обычным цветом:\n%q", kind, line)
		}
		// Акцентного цвета в строки быть не должно вовсе: красить пустые скобки
		// нечего, и цвет внутри них значил бы «включено» там, где выключено.
		if accent, ok := row.accent(); ok {
			if strings.Contains(line, foregroundSGR(accent, PaletteBackgroundPanel)) {
				t.Fatalf("у снятой галки строки %d не должно быть цвета %v:\n%q", kind, accent, line)
			}
		}
	}
}

// Признак отмеченной галки — галочка, а не буква x, и ширина обоих вариантов по
// прежнему равна: список не должен «прыгать» при переключении, а это возможно
// только при равной ширине. Ширина меряется в ЯЧЕЙКАХ (cellWidth), а не в len: у
// ✓ три байта в UTF-8, и подсчёт по байтам отдал бы каждой строке лишние две
// ячейки — на узком меню подпись уезжала бы за поле, а посчитанная ширина блока
// разошлась бы с нарисованной.
func TestFilterBoxOnIsACheckOfTheSameWidthAsTheEmptyOne(t *testing.T) {
	if !strings.Contains(filterBoxOn, "✓") {
		t.Fatalf("отмеченный чекбокс = %q, ждали галочку ✓", filterBoxOn)
	}
	if strings.Contains(filterBoxOn, "x") {
		t.Fatalf("отмеченный чекбокс = %q, буква x выглядит как «здесь что-то не то»", filterBoxOn)
	}
	if got := cellWidth(filterBoxOn); got != 3 {
		t.Fatalf("галка шириной %d ячеек, а ждали 3 — как у пустой: строка «прыгала» бы при переключении", got)
	}
	if cellWidth(filterBoxOn) != cellWidth(filterBoxOff) {
		t.Fatalf("галки разной ширины: %d и %d ячеек", cellWidth(filterBoxOn), cellWidth(filterBoxOff))
	}

	// Ширина префикса строки обязана совпадать с тем, что реально нарисовано: при
	// подсчёте в байтах обрезка подписи отставала бы от отрисовки на две ячейки.
	// filterRowPlainText — ровно та строка без оформления, по которой считается
	// ширина блока, поэтому сверка идёт с ней, а не с от руки собранным куском.
	plain := filterRowPlainText(filterRow{kind: filterRowChats, label: "Чаты"})
	if got, want := cellWidth(plain)-cellWidth("Чаты"), filterRowPrefix(); got != want {
		t.Fatalf("префикс строки занял %d ячеек, а filterRowPrefix = %d: обрезка подписи поехала бы на %d",
			got, want, got-want)
	}
}

// TestFilterMenuHiddenWithoutFolders — у человека без папок раздел «Папки» не
// рисуется вовсе: заголовок с пустым списком под ним читался бы как оборванный.
func TestFilterMenuHiddenWithoutFolders(t *testing.T) {
	settingsPathForFilterTest(t)
	m := New(context.Background(), nil, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m = next.(Model)
	next, _ = m.Update(wallLoadedMsg{cards: filterTestCards(), chats: filterTestChats()})
	m = next.(Model)

	m = openFilter(t, m)
	screen := ansi.Strip(m.View().Content)
	if strings.Contains(screen, filterLabelFolders) {
		t.Fatalf("без папок раздел «Папки» не должен рисоваться:\n%s", screen)
	}
}

// TestFilterStaleFolderChatsIgnored — ответ на запрос состава папок, пришедший уже
// после смены списка папок, молча отбрасывается: иначе чаты удалённой папки
// остались бы в фильтре, а чаты новой потерялись бы до следующего апдейта.
func TestFilterStaleFolderChatsIgnored(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	if !m.chatFolders.inFolder(2, 10) {
		t.Fatal("подготовка: чат 2 должен состоять в папке 10")
	}

	// Пришёл апдейт папок: папка 10 переименована (id тот же), папка 20 удалена.
	next, _ := m.Update(wallFoldersUpdateMsg{folders: []auth.Folder{{ID: 10, Name: "Работа (новое)"}}, valid: true})
	m = next.(Model)
	// Ответ, собранный ДО этого апдейта.
	next, _ = m.Update(wallFolderChatsMsg{builtFor: []int32{10, 20}, byFolder: map[int32][]int64{10: {1}, 20: {1, 2, 3, 4}}})
	m = next.(Model)

	if m.chatFolders.inFolder(4, 20) {
		t.Fatal("устаревший ответ оживил удалённую папку 20")
	}
	if m.chatFolders.inFolder(1, 10) {
		t.Fatal("устаревший ответ применился: чат 1 попал в папку 10, хотя её состав не менялся")
	}
}

// TestFilterKeepsMarksAcrossFolderRename — переименование папки (id тот же) не
// стирает выбор человека, а удаление папки убирает её отметку: отметка без живой
// папки ограничивает стену на несуществующее.
func TestFilterKeepsMarksAcrossFolderRename(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	m = openFilter(t, m)
	m = moveFilterTo(t, m, 5) // отметили папку 10
	m, _ = pressWallKey(t, m, filterSpace)
	m, _ = pressWallKey(t, m, filterEsc)

	next, _ := m.Update(wallFoldersUpdateMsg{folders: []auth.Folder{{ID: 10, Name: "Работа (новое)"}}, valid: true})
	m = next.(Model)
	if !m.filter.folders[10] {
		t.Fatal("переименование папки стёрло отметку человека")
	}

	next, _ = m.Update(wallFoldersUpdateMsg{folders: []auth.Folder{{ID: 30, Name: "Другая"}}, valid: true})
	m = next.(Model)
	if m.filter.folders[10] {
		t.Fatalf("отметка удалённой папки обязана исчезнуть: %+v", m.filter.folders)
	}
	// Ограничение по исчезнувшей папке снято — вернулись все не-заглушённые.
	assertVisible(t, m, "канал", "группа-10", "личный", "группа-20")
}

// TestFilterOpenMenuDropsDeletedFolderRow — открытое меню пересобирается по новому
// списку папок: строка удалённой папки, на которой стоял курсор, исчезает, и
// нажатие на неё уже не включает несуществующее ограничение.
func TestFilterOpenMenuDropsDeletedFolderRow(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	m = openFilter(t, m)
	m = moveFilterTo(t, m, 5) // папка 10
	m, _ = pressWallKey(t, m, filterSpace)

	// Пока меню открыто, папка 20 удаляется из Telegram.
	next, _ := m.Update(wallFoldersUpdateMsg{folders: []auth.Folder{{ID: 10, Name: "Работа"}}, valid: true})
	m = next.(Model)

	for _, row := range m.filterMenu.rows {
		if row.folderID == 20 {
			t.Fatalf("удалённая папка всё ещё в меню: %+v", m.filterMenu.rows)
		}
	}
	// Курсор не вышел за границы нового списка строк.
	row, ok := m.filterMenu.rowAt()
	if !ok || !row.selectable() {
		t.Fatalf("под курсором оказалась невыбираемая строка: %+v", row)
	}
	// Нажатие на оставшуюся строку работает и не включает ничего лишнего.
	before := m.filter
	m, _ = pressWallKey(t, m, filterSpace)
	if m.filter.same(before) {
		t.Fatal("переключение в пересобранном меню перестало работать")
	}
}

// TestFilterLiveMessageGoesThroughSource — живое сообщение в источник, скрытый
// фильтром, попадает в полный поток, и снятие галки его показывает. Иначе
// переключение галки было бы односторонним: скрытые источники тихо теряли бы
// последнее сообщение.
func TestFilterLiveMessageGoesThroughSource(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	m = openFilter(t, m)
	m = moveFilterTo(t, m, 5) // папка 10: виден только её незаглушённый чат 2
	m, _ = pressWallKey(t, m, filterSpace)
	assertVisible(t, m, "группа-10")

	// Живое сообщение в чат 1 — он скрыт фильтром.
	m = m.applyWallMessageUpdate(1, auth.Message{ID: 111, Text: "новое в канале", Date: 7})
	if len(visibleTexts(m)) != 1 {
		t.Fatalf("скрытому источнику нельзя появиться на стене: %v", visibleTexts(m))
	}
	found := false
	for _, item := range m.source {
		if item.ChatID == 1 && item.Text == "новое в канале" {
			found = true
		}
	}
	if !found {
		t.Fatal("живое сообщение в скрытый источник не попало в полный поток")
	}

	// Сняли отметку папки — источник вернулся вместе со своим новым сообщением.
	// Новое сообщение уехало вниз потока: карточки стены отсортированы по дате.
	m, _ = pressWallKey(t, m, filterSpace)
	assertVisible(t, m, "группа-10", "личный", "группа-20", "новое в канале")
}

// TestFilterUnreadReachesHiddenSource — счётчик непрочитанных доходит до карточки,
// скрытой фильтром: иначе после снятия галки человек увидел бы устаревший счётчик,
// будто апдейта не было.
func TestFilterUnreadReachesHiddenSource(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	m = openFilter(t, m)
	m = moveFilterTo(t, m, 5) // папка 10
	m, _ = pressWallKey(t, m, filterSpace)
	assertVisible(t, m, "группа-10")

	m = m.applyWallUnreadUpdate(1, 42) // чат 1 скрыт
	m, _ = pressWallKey(t, m, filterSpace)

	for _, item := range m.cards {
		if item.ChatID == 1 {
			if item.UnreadCount != 42 {
				t.Fatalf("счётчик непрочитанных не дошёл до карточки: %d", item.UnreadCount)
			}
			return
		}
	}
	t.Fatal("карточка чата 1 не появилась после снятия отметки папки")
}

// TestFilterDeleteRemovesFromSource — удаление убирает карточку и из полного
// потока: иначе снятие галки вернуло бы на стену уже удалённое сообщение.
func TestFilterDeleteRemovesFromSource(t *testing.T) {
	settingsPathForFilterTest(t)
	m := newFilterTestModel(t, config.DefaultSettings())
	m = openFilter(t, m)
	m = moveFilterTo(t, m, 5) // папка 10
	m, _ = pressWallKey(t, m, filterSpace)
	assertVisible(t, m, "группа-10")

	m = m.applyDeleteMessage(deleteMessageMsg{chatID: 1, messageID: 11})
	// Снимаем отметку папки: удалённый источник вернуться не должен.
	m, _ = pressWallKey(t, m, filterSpace)
	for _, item := range m.cards {
		if item.ChatID == 1 {
			t.Fatal("удалённый источник вернулся на стену после снятия отметки папки")
		}
	}
	for _, item := range m.source {
		if item.ChatID == 1 {
			t.Fatal("удалённый источник остался в полном потоке")
		}
	}
}

// TestFilterNoFileKeepsDefault — без settings.toml фильтр остаётся умолчанием
// («Все» включено, папки не отмечены, «Приглушённые» выключено), то есть стена
// показывает ровно то, что показывала до появления фильтра. Ошибки при этом нет:
// файла настроек может не быть вовсе.
func TestFilterNoFileKeepsDefault(t *testing.T) {
	path := settingsPathForFilterTest(t)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("подготовка: файла настроек быть не должно, а он есть (%v)", err)
	}
	settings, err := config.LoadSettings()
	if err != nil {
		t.Fatalf("LoadSettings без файла: %v", err)
	}
	m := newFilterTestModel(t, settings)
	if !m.filter.allTypes() || len(m.filter.folders) != 0 || m.filter.showMuted {
		t.Fatalf("без файла настроек фильтр обязан остаться умолчанием: %+v", m.filter)
	}
	assertVisible(t, m, "канал", "группа-10", "личный", "группа-20")
}

// clampToTestCursor — поставить курсор стены на карточку с указанным индексом.
func clampToTestCursor(t *testing.T, m Model, index int) Model {
	t.Helper()
	if index < 0 || index >= len(m.cards) {
		t.Fatalf("подготовка: индекса %d нет среди %d карточек", index, len(m.cards))
	}
	m.cursor = index
	m.scrollTop = clampScrollToCursor(wallCardHeight, m.cards, m.scrollTop, m.cursor, m.wallColumnWidth(), m.wallHeight())
	return m
}

// wallZoneText — текст зоны стены без оверлея меню. Собирается вручную тем же
// способом, что и renderWallZone в renderScreen, но БЕЗ наложения меню: иначе
// подпись «Каналы» из самого меню попадала бы в проверку и делала её
// неразличимой.
func wallZoneText(t *testing.T, m Model) string {
	t.Helper()
	zone := m.renderWallZone(m.width, m.wallHeight())
	texts := make([]string, 0, len(zone))
	for _, line := range zone {
		texts = append(texts, ansi.Strip(line))
	}
	return strings.Join(texts, "\n")
}
