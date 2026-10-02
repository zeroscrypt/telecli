package tgwall

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/config"
)

func TestScreenHasExactlyTerminalHeight(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {100, 30}, {80, 24}, {62, 20}, {50, 12}, {40, 8}, {20, 6}, {200, 60}} {
		m := newTestModel(t, size[0], size[1])
		lines := strings.Split(m.renderScreen(), "\n")
		if len(lines) != size[1] {
			t.Fatalf("терминал %dx%d: строк на экране %d", size[0], size[1], len(lines))
		}
		for index, line := range lines {
			if width := len([]rune(ansi.Strip(line))); width != size[0] {
				t.Fatalf("терминал %dx%d: строка %d шириной %d:\n%q", size[0], size[1], index, width, line)
			}
		}
	}
}

// Экран собран сверху вниз: стена, разделитель, блок ввода, разделитель, нижняя
// строка. Порядок задан макетом, и разделителей ровно два.
func TestScreenZonesOrder(t *testing.T) {
	m := newTestModel(t, 100, 30)
	lines := strings.Split(ansi.Strip(m.renderScreen()), "\n")

	separator := func(index int) bool {
		return strings.TrimSpace(lines[index]) != "" &&
			strings.Trim(strings.TrimSpace(lines[index]), "─") == ""
	}
	var separators []int
	for index := range lines {
		if separator(index) {
			separators = append(separators, index)
		}
	}
	if len(separators) != 2 {
		t.Fatalf("разделителей %d, ждали 2 (строки %v):\n%s", len(separators), separators, strings.Join(lines, "\n"))
	}
	if last := lines[len(lines)-1]; !strings.Contains(last, testAppName) {
		t.Fatalf("последняя строка не нижняя: %q", last)
	}
	// Блок ввода стоит между разделителями, и подсказка — его первая строка.
	// Сравнение по ТЕКСТУ, а не по строке с управляющими последовательностями:
	// подсказка нарисована двумя цветами (сочетания и слова), и в строке с
	// SGR текста hintLine() попросту нет — есть те же слова, разорванные
	// escape-последовательностями.
	block := lines[separators[0]+1:]
	if plain := ansi.Strip(block[0]); !strings.Contains(plain, m.hintLine()) {
		t.Fatalf("первая строка блока ввода — не подсказка:\nв строке %q\nждали     %q", plain, m.hintLine())
	}
}

// При дефолтной конфигурации строка подсказок собирается в тот же текст, что был
// зашит в коде, — кроме «/ поиск», действия которого у стены нет вообще (см.
// Model.hintLine). Проверка текста целиком, а не «строка непустая»: подсказка,
// разъехавшаяся с раскладкой, хуже отсутствующей.
func TestDefaultHintLineIsAssembledFromRealKeys(t *testing.T) {
	m := newTestModel(t, 100, 30)
	want := "ctrl+p фильтр · ctrl+j строка · ↵ отправить · ↑ лента"
	if got := m.hintLine(); got != want {
		t.Fatalf("подсказка = %q, ждали %q", got, want)
	}
}

// Поле растёт ВВЕРХ: нижняя строка и разделитель под ним остаются на месте, а
// стена отдаёт поле строки сверху. Это механика, скопированная из tgcli, и
// именно на ней там сегодня чинили регрессии — проверка здесь не формальность.
func TestInputGrowsUpwards(t *testing.T) {
	m := newTestModel(t, 100, 30)

	before := m.inputRows
	if before != inputMinHeight {
		t.Fatalf("пустое поле занимает %d строк, ждали %d", before, inputMinHeight)
	}
	wallBefore := len(wallLines(t, m))

	for _, key := range []tea.KeyPressMsg{
		{Code: 'a', Text: "a"},
		{Code: 'j', Mod: tea.ModCtrl},
		{Code: 'b', Text: "b"},
		{Code: 'j', Mod: tea.ModCtrl},
		{Code: 'c', Text: "c"},
	} {
		next, _ := m.Update(key)
		m = next.(Model)
	}

	if m.inputRows != 3 {
		t.Fatalf("поле с двумя переносами занимает %d строк, ждали 3", m.inputRows)
	}
	if got := m.input.Value(); got != "a\nb\nc" {
		t.Fatalf("значение поля = %q, ждали %q", got, "a\nb\nc")
	}
	if got := len(wallLines(t, m)); got != wallBefore-2 {
		t.Fatalf("стена отдала %d строк, ждали 2 (было %d)", wallBefore-got, wallBefore)
	}
	// Нижняя строка не сдвинулась: последняя строка экрана — по-прежнему она.
	lines := strings.Split(ansi.Strip(m.renderScreen()), "\n")
	if !strings.Contains(lines[len(lines)-1], testAppName) {
		t.Fatalf("после роста поля последняя строка экрана — не нижняя:\n%s", strings.Join(lines, "\n"))
	}
}

// Enter переносом строки не вставляет: в макете enter — отправка, а отправки
// ещё нет, и enter не должен втихую разбивать набранный текст.
// Enter — отправка (задача 0151), а не перенос строки: виджет textarea не
// должен получить это нажатие вовсе (перехватывается раньше, в Update), и
// набранный текст не может остаться в поле склеенным с настоящим "\n". Поле
// после Enter с непустым текстом либо пустое (текст ушёл в отправку — на стене
// есть карточка под курсором, есть куда), либо, если бы отправлять было
// некуда, осталось бы ровно тем, что было — в любом случае без вставленного
// переноса строки.
func TestEnterDoesNotInsertNewline(t *testing.T) {
	m := newTestModel(t, 100, 30)

	for _, key := range []tea.KeyPressMsg{{Code: 'a', Text: "a"}, {Code: tea.KeyEnter}} {
		next, _ := m.Update(key)
		m = next.(Model)
	}
	if got := m.input.Value(); strings.Contains(got, "\n") {
		t.Fatalf("значение поля = %q — enter вставил перенос строки", got)
	}
}

// Все карточки стены помещаются в стену на обычном терминале.
func TestWallShowsEveryCard(t *testing.T) {
	// Ширина узкая намеренно: с задачи 0155 на широком терминале стена
	// рисуется суженной колонкой рядом с панелью переписки, и здесь проверялось
	// бы, что влезает в 30 ячеек, а не что стена показывает все карточки.
	m := newTestModel(t, 60, 30)
	wall := strings.Join(wallLines(t, m), "\n")
	for _, item := range m.cards {
		// Заголовки фикстуры короткие и на ширине 100 не обрезаются (задача
		// 0156: колонка заголовка больше не фиксированной ширины, обрезка —
		// только по cardTitleTimeMaxWidth, см. cardTitleText).
		if !strings.Contains(wall, item.title()) {
			t.Fatalf("в стене нет карточки %q", item.title())
		}
	}
}

// Стена заполняет отведённую высоту, когда карточек больше, чем помещается, и не
// обрывается после первой не влезшей. Раньше фикстурой была одна очень длинная
// карточка (на узком терминале она переносилась на десятки строк), но с задачей
// 0154 карточка стены всегда одной высоты, а с задачей 0156 — ровно две строки:
// обрываться больше нечему, и тот сценарий на стене недостижим. Само требование
// живьём: поток из сорока карточек на окне 100x24 обязан заполнить стену целиком,
// а не показать двадцать карточек и двадцать пустых строк под ними.
//
// Что содержимое действительно длиннее окна, проверяется отдельно, иначе тест
// выродился бы в проверку вёрстки впритык.
func TestWallFillsWindowWhenMoreCardsThanFit(t *testing.T) {
	m := loadedModel(t)
	m.cards = wallScrollCards()
	m.width, m.height = scrollWidth, 24
	m.applyLayout()
	m.cursor = len(m.cards) - 1
	m.scrollTop = clampScrollToCursor(wallCardHeight, m.cards, m.scrollTop, m.cursor, m.wallColumnWidth(), m.wallHeight())

	if rows, wallHeight := wallContentRows(m.cards, 0, m.cursor, m.width), m.wallHeight(); rows <= wallHeight {
		t.Fatalf("содержимое стены — %d строк, окно — %d: содержимое обязано быть длиннее окна, иначе тест проверяет не то",
			rows, wallHeight)
	}
	wall := wallLines(t, m)
	if len(wall) != m.wallHeight() {
		t.Fatalf("стена — %d строк, ждали %d (окно под неё)", len(wall), m.wallHeight())
	}
	// Пустого хвоста под последней нарисованной строкой быть не должно — либо
	// совсем нет, либо он короче одной карточки с зазором.
	//
	// Раньше здесь проверялось, что последняя строка стены не пустая. С
	// двухстрочной карточкой (задача 0156) это требование невыполнимо: высоты
	// содержимого равны 2 + 3k (2, 5, 8, …, 23, 26) и высота стены в 25 строк
	// среди них НЕТ, так что 1-2 пустые строки внизу — это минимум, а не
	// недобор. Баг, который тест ловит, выглядит иначе: стена обрывается, и
	// под последней карточкой остаётся десяток-два пустых строк.
	lastDrawn := len(wall) - 1
	for lastDrawn > 0 && strings.TrimSpace(wall[lastDrawn]) == "" {
		lastDrawn--
	}
	block := len(renderCard(m.cards[len(m.cards)-1], m.width, true))
	if tail := len(wall) - 1 - lastDrawn; tail >= wallGap+block {
		t.Fatalf("под последней карточкой стены %d пустых строк, а поместилась бы ещё карточка (%d строк) — стена не набита:\n%s",
			tail, wallGap+block, strings.Join(wall, "\n"))
	}
}

// newTestModel — модель под размер терминала с уже сфокусированным полем и
// готовыми карточками.
//
// Фокус в живой программе ставит команда из Init(), а в тесте её никто не
// исполнит, и виджет без фокуса молча игнорирует весь ввод. Карточки кладёт та же
// разметка, что приезжает из TDLib, а не зашитый список: тесты вёрстки должны
// видеть ровно то, что увидит живая стена. Состояние загрузки — выключенное, то
// есть стена уже загружена: иначе тесты вёрстки зависели бы от того, грузится
// что-то или нет, а это им не свойственно.
func newTestModel(t *testing.T, width, height int) Model {
	t.Helper()
	m := loadedModel(t)
	m.width, m.height = width, height
	m.applyLayout()
	if m.input == nil {
		t.Fatal("поле ввода не создано")
	}
	_ = m.input.Focus()
	return m
}

// loadedModel — модель после прихода стены с зашитыми в тест карточками
// (фикстура testCards), то есть ровно то состояние, в котором стена работает
// после wallLoadedMsg.
func loadedModel(t *testing.T) Model {
	t.Helper()
	m := New(context.Background(), nil, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	next, _ := m.Update(wallLoadedMsg{cards: testCards()})
	m = next.(Model)
	if m.loading {
		t.Fatal("после wallLoadedMsg стена осталась в состоянии загрузки")
	}
	return m
}

// countWallLines — номер строки экрана, на которой стоит разделитель под стеной.
// Это конец ВСЕГО, что над разделителем: верхних баров, стены и строки сообщений.
func countWallLines(t *testing.T, m Model) int {
	t.Helper()
	lines := strings.Split(ansi.Strip(m.renderScreen()), "\n")
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.Trim(trimmed, "─") == "" {
			// Первый разделитель — низ стены.
			return index
		}
	}
	t.Fatalf("разделитель под стеной не найден:\n%s", strings.Join(lines, "\n"))
	return 0
}

// wallZoneLines — строки зоны стены из готового экрана (screen): ровно та
// область, которую занимает стена.
//
// Отдельно от «всего до разделителя» потому, что до задачи 0168 стена начиналась
// с первой строки экрана, а теперь над ней два пустых бара, а под ней — строка
// временных сообщений. Ни те, ни другая в зону стены не входят, а на проверки
// геометрии (номер строки карточки, высота панели переписки) попадание лишних
// строк сдвигает всё на три.
func wallZoneLines(t *testing.T, m Model, screen []string) []string {
	t.Helper()
	frame := wallTopBarRows + wallNoticeRows
	end := countWallLines(t, m)
	if end < frame {
		t.Fatalf("разделитель под стеной на строке %d, а каркас занимает %d строк над ним — экран короче каркаса:\n%s",
			end, frame, strings.Join(screen, "\n"))
	}
	return screen[wallTopBarRows : end-wallNoticeRows]
}
