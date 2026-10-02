package tgwall

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/auth"
	"telecli/internal/config"
)

// Контур панели широкого режима (задача 0167): у панели, на которой сейчас фокус,
// виден тонкий контур цветом типа ОТКРЫТОГО чата, у другой панели — тот же самый
// контур цветом фона, то есть невидимый. Место под контур зарезервировано всегда:
// иначе переключение фокуса по Tab сдвигало бы содержимое панели на две ячейки и на
// две строки.
//
// Проверки идут по тому, что человек видит на экране: цвета берутся из сырой
// отрисовки с ANSI, а не из вызовов panelOutline — проверка через саму функцию была
// бы слепой к её собственной мутации.

// panelOutlineGlyphs — глифы, которые рисует ТОЛЬКО контур панели. `┃` сюда не
// входит намеренно: им нарисованы границы карточек и блока ввода, и он есть на
// экране всегда, независимо от контура.
var panelOutlineGlyphs = []string{"┏", "┓", "┗", "┛", "━"}

// wallZoneRaw — строки зоны стены с ANSI (между верхними барами и строкой
// системных сообщений, задача 0168). Отдельная функция, а не zoneLines: зонная
// отрисовка нужна здесь с цветами, а zoneLines их снимает.
func wallZoneRaw(t *testing.T, m Model) []string {
	t.Helper()
	return wallZoneLines(t, m, splitLines(m.renderScreen()))
}

// cellStyle — SGR-последовательность, которой открыт глиф в колонке column, и сам
// глиф.
//
// Разбор строки идёт слева направо: SGR-последовательности ячейками не считаются,
// всё остальное — ячейка. Своя функция, а не ansi.Cut: срез по границе клетки
// заново открывает SGR в своей (ansi печатает активный стиль с начала среза), а
// сравнивать надо с тем, ЧЕМ вёрстка покрасила этот глиф именно в этой строке.
//
// Руна считается за одну ячейку: в зоне широких рун нет (глифы рамки, карточки и
// русский текст), и вёрстка всё равно меряет такие строки по cellWidth.
func cellStyle(line string, column int) (style, glyph string) {
	runes := []rune(line)
	position, cell := 0, 0
	for position < len(runes) {
		if runes[position] == 0x1b {
			end := position + 1
			for end < len(runes) && runes[end] != 'm' {
				end++
			}
			if end < len(runes) {
				style = string(runes[position : end+1])
				position = end + 1
				continue
			}
		}
		if cell == column {
			return style, string(runes[position])
		}
		cell++
		position++
	}
	return style, ""
}

// assertOutlineCell — в колонке column стоит именно этот глиф, открытый именно этой
// SGR-последовательностью.
func assertOutlineCell(t *testing.T, line string, column int, glyph, style string) {
	t.Helper()
	gotStyle, gotGlyph := cellStyle(line, column)
	if gotGlyph != glyph {
		t.Fatalf("в колонке %d глиф %q, ждали %q (строка: %q)", column, gotGlyph, glyph, ansi.Strip(line))
	}
	if gotStyle != style {
		t.Fatalf("в колонке %d глиф %q нарисован последовательностью %q, ждали %q (строка: %q)",
			column, glyph, gotStyle, style, ansi.Strip(line))
	}
}

// assertPanelOutline — контур панели, занимающей колонки [start, start+width) во
// всех строках зоны: все четыре стороны и цвет каждой из них.
//
// Проверяются и стороны, и углы, и каждая строка содержимого: контур, нарисованный
// только сверху, был бы другим дефектом на том же месте.
func assertPanelOutline(t *testing.T, lines []string, start, width int, want color.Color) {
	t.Helper()
	if width < 2 || len(lines) < 3 {
		t.Fatalf("проверять нечего: панель шириной %d, зона из %d строк", width, len(lines))
	}
	style := foregroundSGR(want, PaletteBackgroundMain)
	assertOutlineCell(t, lines[0], start, "┏", style)
	assertOutlineCell(t, lines[0], start+width-1, "┓", style)
	assertOutlineCell(t, lines[len(lines)-1], start, "┗", style)
	assertOutlineCell(t, lines[len(lines)-1], start+width-1, "┛", style)
	for index := 1; index < len(lines)-1; index++ {
		assertOutlineCell(t, lines[index], start, "┃", style)
		assertOutlineCell(t, lines[index], start+width-1, "┃", style)
	}
}

// rowsOf — строки зоны (нумерация как в зоне, с нуля), в которых подстрока стоит в
// колонках [start, start+width).
func rowsOf(lines []string, start, width int, sub string) []int {
	found := make([]int, 0, len(lines))
	for index, line := range lines {
		if strings.Contains(ansi.Strip(ansi.Cut(line, start, start+width)), sub) {
			found = append(found, index)
		}
	}
	return found
}

// assertNoPanelOutlineGlyphs — в строках зоны нет ни одного глифа контура.
func assertNoPanelOutlineGlyphs(t *testing.T, lines []string) {
	t.Helper()
	for index, line := range lines {
		plain := ansi.Strip(line)
		for _, glyph := range panelOutlineGlyphs {
			if strings.Contains(plain, glyph) {
				t.Fatalf("строка зоны %d содержит глиф контура %q: %q", index, glyph, plain)
			}
		}
	}
}

// openChatAccent — акцентный цвет типа ОТКРЫТОГО чата, которым обязана краситься
// панель в фокусе.
//
// Считается здесь, из потока стены, а не вызовом zoomPanelAccent: проверка через
// ту же функцию прошла бы и на коде, где функция сломана.
func openChatAccent(t *testing.T, m Model) color.Color {
	t.Helper()
	if m.zoom == nil {
		t.Fatal("переписка не открыта")
	}
	index := indexOfChatCard(m.source, m.zoom.chatID)
	if index < 0 {
		t.Fatalf("источника %d нет на стене", m.zoom.chatID)
	}
	return m.source[index].accent()
}

// Контур отмечает панель, на которой сейчас фокус, и только её: по умолчанию фокус
// на списке (контур вокруг колонки стены), после Tab — на переписке (контур
// переезжает). У панели без фокуса контур НЕВИДИМЫЙ, а не отсутствующий: место под
// него зарезервировано всегда, иначе содержимое прыгало бы при переключении.
func TestWidePanelOutlineMarksFocusedPanel(t *testing.T) {
	const (
		terminal = 100
		left     = 0
		right    = wallPanelColumnWidth + wallPanelGapWidth
		rightLen = terminal - wallPanelColumnWidth - wallPanelGapWidth - wallPanelInsetRight
	)
	m := zoomModel(t, newZoomClient(), terminal, 30)
	if !m.wallPanelOutlined() {
		t.Fatal("на широком терминале с открытой перепиской контур обязан рисоваться")
	}
	if m.focusPanel.isChatPanel() {
		t.Fatal("в широком режиме фокус по умолчанию на списке, а не на переписке")
	}
	accent := openChatAccent(t, m)
	lines := wallZoneRaw(t, m)
	assertPanelOutline(t, lines, left, wallPanelColumnWidth, accent)
	assertPanelOutline(t, lines, right, rightLen, PalettePanelBorder)

	// Высота зоны не меняется: контур не добавляет экрану строк, он
	// перераспределяет уже отведённые между рамкой и содержимым.
	if got, want := len(lines), m.wallHeight(); got != want {
		t.Fatalf("зона из %d строк, ждали %d (контур не должен менять высоту зоны)", got, want)
	}

	// Tab — фокус на переписке: контуры меняются местами, раскладка та же.
	tabbed, _ := pressZoomKey(t, m, keyTab())
	if !tabbed.focusPanel.isChatPanel() {
		t.Fatal("Tab не перевёл фокус на переписку")
	}
	lines = wallZoneRaw(t, tabbed)
	assertPanelOutline(t, lines, left, wallPanelColumnWidth, PalettePanelBorder)
	assertPanelOutline(t, lines, right, rightLen, accent)
	if got, want := len(lines), tabbed.wallHeight(); got != want {
		t.Fatalf("после Tab зона из %d строк, ждали %d", got, want)
	}

	// Обратный Tab — контур возвращается на список. Обе панели проверяются по
	// цвету, а не «контур вообще есть»: иначе переключение можно было бы
	// проверять одним вызовом на панели в фокусе.
	back, _ := pressZoomKey(t, tabbed, keyTab())
	lines = wallZoneRaw(t, back)
	assertPanelOutline(t, lines, left, wallPanelColumnWidth, accent)
	assertPanelOutline(t, lines, right, rightLen, PalettePanelBorder)
}

// Цвет контура совпадает с цветом типа ОТКРЫТОГО чата — того, чья переписка открыта
// справа, а не карточки под курсором стены.
//
// Все три типа проходятся по очереди, и сверяется не «цвет какой-то», а цвет
// конкретного типа плюс то, что три типа дают три РАЗНЫХ цвета: на одной палитре
// проверка была бы пустой.
func TestWidePanelOutlineColorFollowsOpenChatType(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 100, 30)
	// Курсор после загрузки стоит на последней карточке, дальше идём вверх:
	// личный диалог, чат, канал — все три типа по одному разу.
	seen := make([]color.Color, 0, len(m.cards))
	for step := range len(m.cards) {
		want := openChatAccent(t, m)
		for _, previous := range seen {
			if previous == want {
				t.Fatalf("шаг %d: тип источника дал тот же акцент, что и раньше — "+
					"проверка разных типов ничего не отличает", step)
			}
		}
		seen = append(seen, want)
		// Фокус на списке — контур нарисован у колонки стены, и именно он несёт
		// цвет типа открытого чата.
		style := foregroundSGR(want, PaletteBackgroundMain)
		lines := wallZoneRaw(t, m)
		assertOutlineCell(t, lines[1], 0, "┃", style)
		assertOutlineCell(t, lines[0], 0, "┏", style)
		// Тот же акцент — у контура ПАНЕЛИ ПЕРЕПИСКИ, когда фокус на ней. Без
		// этой проверки цвет панели не охранялся бы ничем: на экране он виден
		// только под фокусом переписки, а колонка стены показывает совсем другой
		// контур. Проверка идёт на КАЖДОМ шаге, потому что мутация «взять акцент
		// у чужой карточки» заметна лишь тогда, когда у открытого чата и у первой
		// карточки стены разные типы.
		chatFocused, _ := pressZoomKey(t, m, keyTab())
		if !chatFocused.focusPanel.isChatPanel() {
			t.Fatal("Tab не перевёл фокус на переписку")
		}
		chatLines := wallZoneRaw(t, chatFocused)
		chatStart := wallPanelColumnWidth + wallPanelGapWidth
		assertOutlineCell(t, chatLines[0], chatStart, "┏", style)
		assertOutlineCell(t, chatLines[1], chatStart, "┃", style)
		// Второй Tab возвращает фокус на список, чтобы ↑ дальше двигал курсор
		// стены, а не переписки.
		back, _ := pressZoomKey(t, chatFocused, keyTab())
		if back.focusPanel.isChatPanel() {
			t.Fatal("второй Tab не вернул фокус на ленту")
		}
		m = back
		if step+1 == len(m.cards) {
			break
		}
		previous := m.zoom.chatID
		moved, _ := pressZoomKey(t, m, keyUp())
		if moved.zoom == nil || moved.zoom.chatID == previous {
			t.Fatal("↑ не открыл переписку следующего источника — тест проверяет один тип")
		}
		m = moved
	}
}

// В узком режиме контура нет НИ В ОДНОМ состоянии: переключать фокус между панелями
// там нечем, переписка занимает весь экран, и рисовать рамку вокруг того, чего нет,
// нельзя. Заодно проверяется, что зона осталась той же ширины и высоты, что была до
// этой задачи (регрессия по узкому режиму).
func TestNarrowModeHasNoPanelOutline(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 60, 30)
	opened, openCmd := pressZoomKey(t, m, keyEnter())
	opened = runZoomLoad(t, opened, openCmd)
	tabbed, _ := pressZoomKey(t, opened, keyTab())
	for name, state := range map[string]Model{"стена": m, "переписка": opened, "фокус на переписке": tabbed} {
		t.Run(name, func(t *testing.T) {
			if state.wallPanelMode() {
				t.Fatal("тест построен на узком терминале, а тут широкий режим")
			}
			lines := wallZoneRaw(t, state)
			assertNoPanelOutlineGlyphs(t, lines)
			if got, want := len(lines), state.wallHeight(); got != want {
				t.Fatalf("зона из %d строк, ждали %d", got, want)
			}
			for index, line := range lines {
				if got := cellWidth(ansi.Strip(line)); got != state.width {
					t.Fatalf("строка зоны %d шириной %d, ждали %d", index, got, state.width)
				}
			}
		})
	}
}

// На широком терминале ДО прихода снимка стены переписки ещё нет, и фокусироваться
// на ней не на что — zoomHasFocus возвращает false, Tab не перехватывается. Но
// колонки на месте: рамки рисуются по геометрии зоны, а не по наличию чата внутри
// (решение человека, 2026-10-01), иначе панель появлялась бы и исчезала вместе с
// загрузкой, дёргая всю вёрстку. Место под рамку вычитается из содержимого, и
// строка зоны остаётся ровно во всю ширину терминала.
func TestWideModeWithoutZoomStillDrawsPanelOutline(t *testing.T) {
	m := New(context.Background(), newZoomClient(), "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = next.(Model)
	if !m.wallPanelMode() {
		t.Fatal("тест построен на широком терминале")
	}
	if m.zoom != nil {
		t.Fatalf("до прихода снимка стены переписка не открыта, а открыт чат %d", m.zoom.chatID)
	}
	if !m.wallPanelOutlined() {
		t.Fatal("на широком терминале рамки колонок рисуются всегда, независимо от чата")
	}
	lines := wallZoneRaw(t, m)
	// Правая панель переписки очерчена бледным цветом неактивной рамки. Левую
	// колонку стены здесь не проверяем: до прихода снимка стены под курсором нет
	// карточки, и её рамка красится цветом фона — проверять её цвет значило бы
	// закреплять за этим тестом частный случай пустой стены.
	chatStart := wallPanelColumnWidth + wallPanelGapWidth
	chatWidth := m.width - chatStart - wallPanelInsetRight
	assertPanelOutline(t, lines, chatStart, chatWidth, PalettePanelBorder)
	if got, want := len(lines), m.wallHeight(); got != want {
		t.Fatalf("зона из %d строк, ждали %d", got, want)
	}
	for index, line := range lines {
		if got := cellWidth(ansi.Strip(line)); got != m.width {
			t.Fatalf("строка зоны %d шириной %d, ждали %d:\n%q", index, got, m.width, ansi.Strip(line))
		}
	}
}

// Окно прокрутки каждой панели считается по высоте её СОДЕРЖИМОГО — зоны минус две
// строки контура, — и место под контур вычитается из содержимого ДО отрисовки.
// Проверяется на том, что блок под курсором помещается в панель ЦЕЛИКОМ: ни одна его
// строка не уходит под нижний контур.
//
// Проверяются обе панели, и у каждой своя примета:
//   - у карточки стены под курсором в широком режиме нет ни времени, ни превью (та
//     же переписка открыта справа), так что её строки видно только по ФОНУ ВЫДЕЛЕНИЯ
//     на левой границе;
//   - у сообщения переписки последняя строка блока — время под текстом, и вот оно
//     должно быть на экране.
//
// Курсор переписки стоит на последнем сообщении: у края списка ошибка высоты
// проявляется вся (см. комментарий в тесте), в середине списка окно доливается
// историей сверху и остаётся тем же самым.
func TestWidePanelKeepsCursorBlockFullyVisible(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 100, 30)
	// Списки длинные: короткий помещается в панель целиком, окно не прокручивается,
	// и проверять было бы нечего.
	m.cards = manyWallCards(40)
	m.cursor, m.scrollTop = 0, 0
	m = m.moveCursor(len(m.cards) / 2)
	m.zoom.messages = tallMessages(40)
	m.zoom.cursor, m.zoom.scrollTop, m.zoom.loading = 0, 0, false
	// Курсор переписки — на ПОСЛЕДНЕМ сообщении. Именно тут ошибка высоты видна:
	// окно, посчитанное на две строки больше, чем видно, добирает сверху лишнее
	// сообщение и сдвигает блок под курсором вниз, а контур срезает у него
	// последние строки. В середине списка то же самое не проявляется: там окно
	// доливается историей сверху (backfillScrollTop) и остаётся тем же самым.
	m.moveWallZoomCursor(len(m.zoom.messages) - 1)

	lines := wallZoneRaw(t, m)
	// Обе строки карточки под курсором обязаны остаться внутри панели. Видно их по
	// ФОНУ ВЫДЕЛЕНИЯ на левой границе карточки: у карточки под курсором в широком
	// режиме нет ни времени, ни превью (та же переписка открыта справа), так что
	// другой приметы у её второй строки и нет.
	//
	// Граница карточки — в колонке cardMarginH от начала ЕЁ строки, а строка
	// карточки начинается на ячейку правее: первую занимает левая сторона контура.
	wantSelection := foregroundSGR(m.cards[m.cursor].accent(), PaletteBackgroundSelected)
	selected := make([]int, 0, 2)
	for index := 1; index < len(lines)-1; index++ {
		if style, _ := cellStyle(lines[index], 1+cardMarginH); style == wantSelection {
			selected = append(selected, index)
		}
	}
	if len(selected) != 2 || selected[1] != selected[0]+1 {
		t.Fatalf("строк с фоном выделения карточки %d (строки %v), ждали ровно две "+
			"подряд — вторую срезал бы контур или окно считалось по высоте зоны:\n%s",
			len(selected), selected, strings.Join(zoneLines(t, m), "\n"))
	}
	// То же о сообщении переписки под курсором: у него три строки текста и своя
	// строка времени, и время обязано быть на экране — оно и есть последняя строка
	// блока.
	cursor := m.zoom.messages[m.zoom.cursor]
	textRows := rowsOf(lines, wallPanelColumnWidth+wallPanelGapWidth, m.width, "разговор "+fmt.Sprint(cursor.ID))
	timeRows := rowsOf(lines, wallPanelColumnWidth+wallPanelGapWidth, m.width, auth.FormatMessageTime(cursor.Date))
	if len(textRows) == 0 {
		t.Fatalf("сообщения под курсором (id %d) нет в панели переписки:\n%s",
			cursor.ID, strings.Join(zoneLines(t, m), "\n"))
	}
	if len(timeRows) == 0 {
		t.Fatalf("строка времени сообщения под курсором (id %d) срезана контуром:\n%s",
			cursor.ID, strings.Join(zoneLines(t, m), "\n"))
	}
	if last := textRows[len(textRows)-1] + 3; timeRows[0] != last {
		t.Fatalf("время сообщения на строке %d, а под текстом (строки %v) оно должно быть на строке %d",
			timeRows[0], textRows, last)
	}
}

// Размеры содержимого панелей — единое место, откуда берутся и окно прокрутки, и
// отрисовка, и они обязаны быть на две ячейки и на две строки меньше самой панели.
//
// Проверяются значения, а не экран, и это осознанный выбор: окно, посчитанное на две
// строки выше, чем видно, на экране почти нигде не проявляется — отрисовка пересчитывает
// окно сама (clampScrollToCursor в renderWallZoomMessages) уже по правильной высоте и
// сходится к тому же ответу. Ловить такую ошибку можно было бы только счётом «сколько
// пустых строк осталось у края списка», а эта арифметика поедет от любой правки
// вёрстки сообщения, даже когда всё на месте. Значение — то, что и обязано быть
// верным, и по нему же считаются все восемь мест окна стены.
func TestPanelContentSizeIsPanelSizeMinusOutline(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 100, 30)
	if got, want := m.wallColumnWidth(), wallPanelColumnWidth-wallPanelOutlineInset; got != want {
		t.Fatalf("ширина содержимого колонки стены %d, ждали %d", got, want)
	}
	// Ширина содержимого панели: терминал минус колонка стены, зазор у её края,
	// поле справа от панели (wallPanelInsetRight) и контур по ячейке с каждой
	// стороны. Поле справа в формуле обязательно — без него рамка панели
	// прижималась к краю терминала вплотную.
	if got, want := m.zoomListWidth(), m.width-wallPanelColumnWidth-wallPanelGapWidth-wallPanelInsetRight-wallPanelOutlineInset; got != want {
		t.Fatalf("ширина содержимого панели переписки %d, ждали %d", got, want)
	}
	if got, want := m.wallContentHeight(), m.wallHeight()-wallPanelOutlineInset; got != want {
		t.Fatalf("высота содержимого панелей %d, ждали %d", got, want)
	}
	if got, want := m.wallZoomListHeight(), m.wallContentHeight()-m.zoomHeaderRows(); got != want {
		t.Fatalf("высота списка переписки %d, ждали %d", got, want)
	}
	// Узкий режим: контура нет, и содержимое занимает зону целиком — иначе панель,
	// которой нет, отняла бы место у стены.
	narrow := zoomModel(t, newZoomClient(), 60, 30)
	if got, want := narrow.wallColumnWidth(), narrow.width; got != want {
		t.Fatalf("на узком терминале ширина колонки %d, ждали %d", got, want)
	}
	if got, want := narrow.zoomListWidth(), narrow.width; got != want {
		t.Fatalf("на узком терминале ширина панели %d, ждали %d", got, want)
	}
	if got, want := narrow.wallContentHeight(), narrow.wallHeight(); got != want {
		t.Fatalf("на узком терминале высота содержимого %d, ждали %d", got, want)
	}
}

// panelContentHeightFor — арифметика внутри renderWallZone (локальная panelHeight,
// см. там же) и Model.wallContentHeight ОБЯЗАНЫ считать её одной и той же функцией
// (см. её комментарий), но это стоит проверить и по значению напрямую: экранная
// проверка «блок под курсором виден целиком» (TestWidePanelKeepsCursorBlockFullyVisible)
// эту мутацию не ловит НИ ПРИ КАКОМ найденном сценарии — panelOutline защитно
// обрезает содержимое панели по ХВОСТУ до правильной высоты кадра (её высота
// приходит отдельным, немутированным параметром), и лишние строки, которые
// сгенерировала мутированная (большая) высота, всегда оказываются ровно в этом
// обрезаемом хвосте, а не там, где стоит курсор. Отсюда — проверка по значению,
// а не по экрану.
func TestPanelContentHeightForSubtractsOutlineInset(t *testing.T) {
	for _, height := range []int{0, 1, 2, 3, 22, 100} {
		if got, want := panelContentHeightFor(height), max(0, height-wallPanelOutlineInset); got != want {
			t.Fatalf("panelContentHeightFor(%d) = %d, ждали %d", height, got, want)
		}
	}
}

// manyWallCards — длинная стена: карточек больше, чем влезает в панель, иначе окно не
// прокручивается и проверка «блок под курсором виден целиком» ничего не значит.
func manyWallCards(count int) []card {
	cards := make([]card, 0, count)
	for index := range count {
		cards = append(cards, card{
			ChatID: int64(index + 1),
			Type:   cardChannel,
			Name:   fmt.Sprintf("Канал %d", index+1),
			Time:   "13:04",
			Text:   "пост",
			Tag:    "#канал",
			Date:   int64(1000 + index),
		})
	}
	return cards
}

// tallMessages — многострочные сообщения с РАЗНЫМИ текстом и временем: проверка
// «блок под курсором виден целиком» ищет текст и время ИМЕННО его сообщения, а
// одинаковые у всех сообщений подошли бы любой строке списка.
//
// Текст такой, что переносится на три строки при ширине блока панели, — иначе блок
// был бы двухстрочным и обрезалась бы другая строка. Время разведено на час, а не на
// секунду: в пределах одной минуты оно совпало бы у соседних сообщений.
func tallMessages(count int) []auth.Message {
	messages := make([]auth.Message, 0, count)
	for index := range count {
		messages = append(messages, auth.Message{
			ID:   int64(index + 1),
			Date: int64(1000 + index*3600),
			Text: fmt.Sprintf("разговор %d: сначала починили упавшие тесты, потом пересобрали образ, потом выкатили его в прод",
				index+1),
		})
	}
	return messages
}
