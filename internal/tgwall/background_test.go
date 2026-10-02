package tgwall

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
)

// Фон стены (PaletteBackgroundMain) красится по ячейкам на ВСЁМ экране —
// включая пустые строки, боковые поля и промежутки между карточками. Отдельный
// фон остаётся только там, где он ДЕЙСТВИТЕЛЬНО другой: панель поля ввода
// (PaletteBackgroundPanel) и выделенная курсором карточка
// (PaletteBackgroundSelected).
//
// Раньше контракт был обратным: фон стены не красился вовсе и отдавался
// терминалу (родной фон + OSC 11). OSC 11 не применяется видимо ни в Apple
// Terminal, ни в Warp, и фон из-за этого пропадал — без символов в строке его
// не было видно совсем, а после текста обрывался на боковом поле (человек
// заметил вживую, 2026-09-30).
//
// ЧЕГО ЭТИ ТЕСТЫ НЕ ПРОВЕРЯЮТ. Они разбирают вывод View() — то есть байты,
// которые наш код отдал. Между ними и тем, что терминал нарисует, стоит ещё
// рендерер `ultraviolet`, который может вместо записи пробела с фоном отдать
// стирание (ansi.EL/ECH) и рассчитывать на capability `bce` — без её проверки
// (см. foregroundBackgroundStyle и задачу 0160). Поэтому «у каждой ячейки
// есть фоновый SGR» здесь истина, а «полос на экране не будет» — вопрос
// отдельный, требующий проверки в живом терминале, а не в тесте.
//
// Разбор идёт сырым ANSI по ячейкам, а не «на глаз по картинке»: в SGR отличить
// «есть явный фон» от «фона нет вовсе» однозначно, а на скриншоте — нет.

// lineWithoutBackground ищет первую ячейку без явного фона в полуинтервале
// [from, upto) (upto<=0 — до конца строки).
func lineWithoutBackground(line string, from, upto int) (int, int) {
	var (
		state  byte
		pen    cellbuf.Style
		column int
	)
	parser := ansi.NewParser()
	remaining := line
	for len(remaining) > 0 {
		if upto > 0 && column >= upto {
			break
		}
		sequence, width, read, newState := ansi.DecodeSequence(remaining, state, parser)
		if read <= 0 {
			break
		}
		state = newState
		remaining = remaining[read:]
		switch {
		case ansi.HasCsiPrefix(sequence) && parser.Command() == 'm':
			cellbuf.ReadStyle(parser.Params(), &pen)
		case width > 0:
			// Широкие графемы печатаются в две ячейки, но фон терминал ставит им
			// обеим сразу, поэтому вторая ячейка претензий не вызывает.
			if column >= from && pen.Bg == nil {
				return column, width
			}
			column += width
		}
	}
	return -1, 0
}

// ГЛАВНЫЙ контракт: у каждой ячейки каждой строки экрана есть явный фон.
// Раньше проверялось обратное — что фон стены не красится вовсе и видно родной
// фон терминала; из-за этого фон на экране пропадал там, где не было символов
// (человек заметил вживую, 2026-09-30). Проверяется вывод View(); что с ним
// сделает рендерер ultraviolet дальше — см. о границах этих тестов вверху.
func TestEveryScreenCellHasBackground(t *testing.T) {
	for _, size := range [][2]int{{120, 40}, {100, 30}, {80, 24}, {62, 20}, {40, 8}} {
		m := newTestModel(t, size[0], size[1])
		lines := splitLines(m.renderScreen())
		if len(lines) != size[1] {
			t.Fatalf("терминал %dx%d: экран содержит %d строк, ожидалось %d", size[0], size[1], len(lines), size[1])
		}
		for index, line := range lines {
			if column, _ := lineWithoutBackground(line, 0, 0); column >= 0 {
				t.Fatalf("терминал %dx%d, строка %d: ячейка %d нарисована без явного фона, а фон должен быть везде: %q",
					size[0], size[1], index, column, ansi.Strip(line))
			}
		}
	}
}

// Симптом, который человек описал вживую: «если в строке нет никакого символа,
// то и фона нет». Пустая строка обязана быть закрашена целиком — иначе фон
// обрывается ровно там, где заканчивается содержимое.
func TestBlankRowIsFullyPainted(t *testing.T) {
	m := newTestModel(t, 120, 40)
	lines := splitLines(m.renderScreen())
	blank := 0
	for index, line := range lines {
		if strings.TrimSpace(ansi.Strip(line)) != "" {
			continue
		}
		blank++
		if column, _ := lineWithoutBackground(line, 0, 0); column >= 0 {
			t.Fatalf("пустая строка %d: ячейка %d нарисована без фона: %q", index, column, line)
		}
	}
	if blank == 0 {
		t.Fatalf("в раскладке 120x40 не нашлось ни одной пустой строки — тест ничего не проверяет")
	}
}

// Панель поля ввода — зона экрана, чей фон ДЕЙСТВИТЕЛЬНО отличается от фона
// стены (PaletteBackgroundPanel, а не PaletteBackgroundMain), и потому
// красится своим цветом поверх общего: строка подсказок должна нести именно
// этот фон, а не тот же, что у стены.
func TestInputZoneCellsHaveBackground(t *testing.T) {
	m := newTestModel(t, 100, 30)
	for _, key := range []rune{'a', 'b', 'c'} {
		next, _ := m.Update(tea.KeyPressMsg{Code: key, Text: string(key)})
		m = next.(Model)
	}
	lines := splitLines(m.renderScreen())
	hintIndex := -1
	for index, line := range lines {
		if strings.Contains(ansi.Strip(line), m.newLineLabel()) {
			hintIndex = index
			break
		}
	}
	if hintIndex < 0 {
		t.Fatalf("строка подсказок не найдена: %q", strings.Join(lines, "\n"))
	}
	line := lines[hintIndex]
	// Фон есть на всей строке целиком, включая боковые поля: они залиты
	// PaletteBackgroundMain, а сама панель — PaletteBackgroundPanel.
	if column, _ := lineWithoutBackground(line, 0, 0); column >= 0 {
		t.Fatalf("строка подсказок: ячейка с %d нарисована без фона: %q", column, line)
	}
	want := backgroundSGR(t, PaletteBackgroundPanel)
	if !strings.Contains(line, want) {
		t.Fatalf("строка подсказок не несёт явный фон панели (PaletteBackgroundPanel): %q", line)
	}
}
