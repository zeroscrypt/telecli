package tgwall

import (
	"strings"

	tea "charm.land/bubbletea/v2"

	"telecli/internal/auth"
	"telecli/internal/config"
)

// Меню фильтра источников. Тот же приём наложения, что у модалки подтверждения
// удаления (см. confirm.go — общий overlayPasteLine и та же вклейка по центру
// готовых строк), но это не вопрос да/нет: список чекбоксов с навигацией, живым
// предпросмотром и без кнопок «применить»/«отмена» — каждое переключение
// применяется сразу, а закрытие только убирает меню с экрана.
//
// Клавиши меню — те же действия стены, а не отдельный набор для оверлея: «закрыть»
// это keyBack, то есть ровно то же действие, что Esc на стене, и второй набор
// означал бы, что оно настраивается двумя полями (довод тот же, что у
// updateConfirm). Исключение — переключение галки: это отдельное действие
// keyToggle, потому что на Enter в стене живёт отправка текста, и переназначив
// отправку, человек невольно переназначил бы ещё и переключение в меню. Отдельное
// поле, наоборот, позволяет держать привычную отправку на Enter и переключать
// галки пробелом, как в файловых менеджерах (задача 0166).

const (
	filterMenuPaddingH = 2
	filterMenuPaddingV = 1
	// filterMenuMaxWidth — потолок ширины блока: на широком терминале меню должно
	// остаться узким списком, а не полосой во весь экран.
	filterMenuMaxWidth = 48
	// filterMenuTitle — заголовок блока.
	filterMenuTitle = "Фильтр источников"
	// filterRule — короткая линейка перед заголовком раздела. Разделитель из
	// согласованной схемы меню; шириной он блок не задаёт (в отличие от линейки
	// во всю ширину, которая заставляла бы растягивать блок).
	filterRule = "──"
	// Подсказка меню. Куски те же по построению, что у keys.hint() модалки
	// подтверждения, по которой считается ширина блока.
	filterHintToggleSuffix = " переключить"
	filterHintCloseSuffix  = " закрыть"
)

// wallFilterMenu — открытое меню: строки и курсор по ним.
type wallFilterMenu struct {
	rows   []filterRow
	cursor int
}

// filterMenuKeys — подписи клавиш в подсказке меню. Меню реагирует на keyToggle,
// keyBack и keyOpenFilter, поэтому и показывать должно те клавиши, что настроены,
// а не зашитые «space»/«esc».
type filterMenuKeys struct {
	toggle string
	close  string
}

func (m Model) filterMenuKeys() filterMenuKeys {
	return filterMenuKeys{
		toggle: m.keymap.label(keyToggle),
		close:  m.keymap.label(keyBack),
	}
}

func (k filterMenuKeys) hint() string {
	return k.toggle + filterHintToggleSuffix + wallHintSeparator + k.close + filterHintCloseSuffix
}

// rowAt — строка под курсором. Второе значение — есть ли она: курсор на
// невыбираемой строке (заголовке раздела) означает «переключать нечего».
func (menu wallFilterMenu) rowAt() (filterRow, bool) {
	if menu.cursor < 0 || menu.cursor >= len(menu.rows) {
		return filterRow{}, false
	}
	return menu.rows[menu.cursor], true
}

// openFilterMenu — открыть меню по текущему состоянию фильтра и списку папок.
//
// Курсор встаёт на первую строку («Все»), а не на ту галку, которая последней
// переключалась: угадывать «где человек остановился» по истории переключений
// значило бы хранить её отдельно, а список всё равно начинается сверху, и
// человек начинает с «Все».
func (m *Model) openFilterMenu() {
	rows := filterMenuRows(m.filter, m.folders)
	m.filterMenu = &wallFilterMenu{rows: rows, cursor: firstSelectableRow(rows)}
}

// closeFilterMenu — убрать меню с экрана. Изменения НЕ откатываются: они уже
// применены к стене (живой предпросмотр) и сохранены, а «отмена» здесь означала
// бы второе состояние фильтра, которого нигде больше нет.
func (m *Model) closeFilterMenu() {
	m.filterMenu = nil
}

// updateFilterMenu — ввод открытого меню. Пока оно открыто, весь остальной ввод
// заблокирован: нажатие, задуманное для стены, не должно уехать в поле ввода или
// сдвинуть курсор по карточкам (тот же контракт, что у updateConfirm).
func (m Model) updateFilterMenu(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// keyOpenFilter — закрытие, ровно как открытие: отдельного действия «закрыть
	// меню фильтра» в конфигурации нет намеренно, см. TgwallKeyBindings.Filter.
	if m.keymap.pressed(keyBack, msg) || m.keymap.pressed(keyOpenFilter, msg) {
		return m.closeFilterMenuAndSave(), nil
	}
	if m.filterMenu == nil {
		return m, nil
	}
	if m.keymap.pressed(keyMoveUp, msg) {
		return m.moveFilterMenuCursor(-1), nil
	}
	if m.keymap.pressed(keyMoveDown, msg) {
		return m.moveFilterMenuCursor(1), nil
	}
	if m.keymap.pressed(keyToggle, msg) {
		return m.toggleFilterMenuRow()
	}
	return m, nil
}

// closeFilterMenuAndSave — закрытие с сохранением фильтра в настройки.
//
// Сохранение именно здесь, а не на каждом переключении: переключений за сеанс
// десятки, а файл один и тот же, и писать его на каждое нажатие незачем — тем
// более что состояние всё равно меняется до закрытия.
//
// Ошибка сохранения у стены негде показать (того же решения, что у отправки
// сообщения и удаления): фильтр при этом уже применён и в следующем запуске
// просто вернётся к дефолту. Молчание здесь — «не сохранилось», а не «сломалось».
func (m Model) closeFilterMenuAndSave() Model {
	m.closeFilterMenu()
	_ = config.SaveWallFilter(m.filter.settings())
	return m
}

// moveFilterMenuCursor — шаг курсора по списку строк. Заголовки разделов
// перескакиваются (см. filterRow.selectable): курсор, вставший на «Папки», по
// нажатию ничего не переключил бы, и человек подумал бы, что меню зависло.
func (m Model) moveFilterMenuCursor(delta int) Model {
	if m.filterMenu == nil {
		return m
	}
	next := m.filterMenu.cursor + delta
	for next >= 0 && next < len(m.filterMenu.rows) && !m.filterMenu.rows[next].selectable() {
		next += delta
	}
	m.filterMenu.cursor = clampFilterMenuCursor(next, len(m.filterMenu.rows))
	return m
}

// clampFilterMenuCursor — курсор меню в границах списка строк. При пустом списке
// остаётся в нуле, чтобы и «переключение» на пустом меню было безопасным no-op,
// а не падением по индексу.
func clampFilterMenuCursor(cursor, count int) int {
	if count <= 0 {
		return 0
	}
	if cursor < 0 {
		return 0
	}
	if cursor >= count {
		return count - 1
	}
	return cursor
}

// toggleFilterMenuRow — переключить галку под курсором и сразу перефильтровать
// стену (живой предпросмотр: изменения применяются ДО закрытия меню, а не
// «применяются по Enter»).
func (m Model) toggleFilterMenuRow() (tea.Model, tea.Cmd) {
	menu := m.filterMenu
	if menu == nil {
		return m, nil
	}
	row, ok := menu.rowAt()
	if !ok || !row.selectable() {
		return m, nil
	}
	next, changed := m.filter.toggleRow(row)
	if !changed {
		// Отказ защиты от пустого состояния типов: стена остаётся как была.
		return m, nil
	}
	m.filter = next
	// Курсор стены удерживается на том же источнике, что и до переключения
	// (rebuildVisible), а меню — на той же строке: пересборка строк заново не
	// должна уводить курсор на «Все» после каждого нажатия.
	m.filterMenu = m.filterMenu.rebuilt(m.filter, m.folders)
	m = m.rebuildVisible(false)
	return m, nil
}

// rebuilt — те же строки по новому состоянию фильтра, курсор на той же по
// смыслу строке. Идентичность строки — её вид (все/каналы/чаты/личные/папка/
// приглушённые), а для папок — id: по названию опознавать нельзя, оно меняется
// руками в Telegram.
func (menu wallFilterMenu) rebuilt(filter wallFilter, folders []auth.Folder) *wallFilterMenu {
	anchor, ok := menu.rowAt()
	result := &wallFilterMenu{rows: filterMenuRows(filter, folders), cursor: 0}
	if ok {
		if index := indexOfFilterRow(result.rows, anchor); index >= 0 {
			result.cursor = index
		}
	}
	result.cursor = clampFilterMenuCursor(result.cursor, len(result.rows))
	return result
}

// indexOfFilterRow — номер такой же строки в новом списке, -1 если её больше нет
// (например, папку удалили, пока меню было открыто).
func indexOfFilterRow(rows []filterRow, target filterRow) int {
	for index, row := range rows {
		if row.kind != target.kind {
			continue
		}
		if row.kind == filterRowFolder && row.folderID != target.folderID {
			continue
		}
		return index
	}
	return -1
}

// firstSelectableRow — первая строка, на которую можно встать. При пустом или
// целиком невыбираемом списке — 0, и переключать тогда просто нечего.
func firstSelectableRow(rows []filterRow) int {
	for index, row := range rows {
		if row.selectable() {
			return index
		}
	}
	return 0
}

// overlayFilter — вклеивает блок меню по центру готового экрана, тем же
// способом, что и модалка подтверждения (см. overlayConfirm).
func (m Model) overlayFilter(screen string) string {
	if m.filterMenu == nil || screen == "" || m.width <= 0 || m.height <= 0 {
		return screen
	}
	lines := splitLines(screen)
	block, top, left := filterMenuBlock(m.width, m.height, m.filterMenu, m.filterMenuKeys())
	if len(block) == 0 {
		return screen
	}
	for index, blockLine := range block {
		row := top + index
		if row >= len(lines) {
			break
		}
		lines[row] = overlayPasteLine(lines[row], left, blockLine)
	}
	return strings.Join(lines, "\n")
}

// filterMenuBlock — блок меню границами внутри области: top — номер строки, с
// которой блок начинается, left — колонка. Те же три строки, что у
// confirmModalBlock, и по той же причине: блок вклеивается в готовые строки
// экрана, не перерисовывая их.
func filterMenuBlock(width, height int, menu *wallFilterMenu, keys filterMenuKeys) (lines []string, top, left int) {
	if width <= 0 || height <= 0 || menu == nil {
		return nil, 0, 0
	}
	inner := filterMenuContentWidth(menu, keys)
	if limit := width - 2*filterMenuPaddingH; inner > limit {
		inner = max(0, limit)
	}
	if inner <= 0 {
		return nil, 0, 0
	}

	content, cursorLine := filterMenuContent(inner, menu, keys)
	// Блок выше экрана обрезается не с конца, а окном вокруг курсора: у человека
	// с десятком папок меню в терминал не влезает, и обрезка снизу молча убрала бы
	// «Приглушённые» — последнюю строку, до которой только что можно было дойти.
	content, cursorLine = windowFilterMenuContent(content, cursorLine, max(0, height-2*filterMenuPaddingV))

	padding := renderedFill(filterMenuPaddingH, PaletteBackgroundPanel)
	blank := renderedFill(inner+2*filterMenuPaddingH, PaletteBackgroundPanel)
	block := make([]string, 0, len(content)+2*filterMenuPaddingV)
	for range filterMenuPaddingV {
		block = append(block, blank)
	}
	for _, line := range content {
		block = append(block, padding+fitLine(line, inner, PaletteBackgroundPanel)+padding)
	}
	for range filterMenuPaddingV {
		block = append(block, blank)
	}

	boxWidth := cellWidth(block[0])
	top = max(0, (height-len(block))/2)
	left = max(0, (width-boxWidth)/2)
	return block, top, left
}

// filterMenuContent — строки содержимого меню и номер строки курсора среди них.
// Заголовок блока, пустая строка, строки фильтра, пустая строка и подсказка по
// клавишам.
//
// Строка под курсором заливается PaletteBackgroundSelected — тем же фоном, что
// выбранная карточка стены, потому что акцентного цвета у стены нет, а «какая
// строка выбрана» на этом экране обязано быть видно сразу, без чтения подсказки.
func filterMenuContent(width int, menu *wallFilterMenu, keys filterMenuKeys) ([]string, int) {
	muted := foregroundBackgroundStyle(PaletteTextMuted, PaletteBackgroundPanel)
	title := foregroundBackgroundStyle(PaletteText, PaletteBackgroundPanel)

	lines := []string{muted.Render(truncateVisible(filterMenuTitle, width))}
	lines = append(lines, renderedFill(width, PaletteBackgroundPanel))
	cursorLine := 0
	for index, row := range menu.rows {
		if index == menu.cursor {
			cursorLine = len(lines)
		}
		lines = append(lines, filterMenuRowLine(width, row, index == menu.cursor))
	}
	lines = append(lines, renderedFill(width, PaletteBackgroundPanel))
	// Подсказка по клавишам — теми же кусками, из которых собрана keys.hint(), по
	// которой считается ширина блока.
	lines = append(lines, title.Render(keys.toggle)+muted.Render(filterHintToggleSuffix+wallHintSeparator)+
		title.Render(keys.close)+muted.Render(filterHintCloseSuffix))
	return lines, cursorLine
}

// filterMenuRowLine — строка фильтра: чекбокс и подпись. Заголовок раздела рисуется
// приглушённым и с линейкой, а не чекбоксом: галки у него нет, и рисовать её
// значило бы обещать переключение, которого не существует.
func filterMenuRowLine(width int, row filterRow, underCursor bool) string {
	background := PaletteBackgroundPanel
	if underCursor {
		background = PaletteBackgroundSelected
	}
	label := foregroundBackgroundStyle(PaletteText, background)
	muted := foregroundBackgroundStyle(PaletteTextMuted, background)
	if !row.selectable() {
		return fitLine(muted.Render(filterRule+" "+truncateVisible(row.label, width)), width, background)
	}
	// Чекбокс отмеченной строки типа красится акцентным цветом ЭТОГО типа (тем же,
	// что у карточки такого источника на стене, см. filterRow.accent): так видно,
	// что «Каналы», «Чаты» и «Личные» — те же три цвета, что и сама стена, и галка
	// сразу узнаётся. Подпись строки при этом остаётся обычной: акцент нужен галке,
	// а не всей строке, иначе текст на чужом фоне стал бы читаться хуже.
	//
	// Неотмеченный чекбокс — обычным цветом, как и раньше: красить нечего, внутри
	// скобок пусто, и акцентный цвет там означал бы «это включено» там, где
	// выключено. «Все», папки и «Приглушённые» — тоже обычным: типа у них нет.
	box, boxLabel := filterBoxOff, label
	if row.checked {
		box = filterBoxOn
		if accent, ok := row.accent(); ok {
			boxLabel = foregroundBackgroundStyle(accent, background)
		}
	}
	// Отступ под линейку заголовков: строки чекбоксов выравниваются по левому краю
	// с тем же отступом, иначе список читался бы сдвинутым относительно своих
	// заголовков.
	content := strings.Repeat(" ", filterRowIndent) +
		boxLabel.Render(box) + " " +
		label.Render(truncateVisible(row.label, max(0, width-filterRowPrefix())))
	return fitLine(content, width, background)
}

// filterMenuContentWidth — ширина СОДЕРЖИМОГО, которое блок просит у области:
// самая широкая строка списка, но не уже заголовка и подсказки по клавишам.
// Потолок filterMenuMaxWidth задаётся обрезкой каждой строки в
// filterMenuContent, а не здесь: и ширина, и её реальный рендер тогда считаются
// по одному и тому же значению. Горизонтальные отступы добавляет filterMenuBlock.
func filterMenuContentWidth(menu *wallFilterMenu, keys filterMenuKeys) int {
	inner := max(cellWidth(truncateVisible(filterMenuTitle, filterMenuMaxWidth)), cellWidth(keys.hint()))
	for _, row := range menu.rows {
		inner = max(inner, cellWidth(filterRowPlainText(row)))
	}
	return inner
}

// filterRowPlainText — строка фильтра БЕЗ оформления: отступ, чекбокс, подпись.
// Возвращается текстом, а не числом ячеек, потому что шириной меряется он — так
// расчёт блока идёт по ровно той же строке, что и её рендер, и разойтись им не
// может. Заголовки разделов считаются без чекбокса: лишние три ячейки в ширине
// блока не видны, а совпадение с реальным рендером строки важнее.
func filterRowPlainText(row filterRow) string {
	if !row.selectable() {
		return truncateVisible(filterRule+" "+row.label, filterMenuMaxWidth)
	}
	return strings.Repeat(" ", filterRowIndent) + filterBoxOn + " " +
		truncateVisible(row.label, filterMenuMaxWidth)
}

// Признаки чекбокса. Оба варианта — ровно три ячейки, и ширина строки от галки не
// зависит, поэтому список не «прыгает» при переключении.
//
// Отмеченный вариант — галочка, а не буква x: буква читалась как «здесь что-то не
// то», а галочка — как «включено», и ни с чем не путается. Ширина при этом обязана
// остаться прежней, поэтому она считается в ячейках (cellWidth), а не в байтах: у
// ✓ в UTF-8 три байта, и подсчёт по len молча отдавал бы строке лишние две ячейки —
// ровно на разницу между длиной галки в байтах и её длиной в ячейках.
const (
	filterBoxOn  = "[✓]"
	filterBoxOff = "[ ]"
)

// filterRowIndent — отступ строк-чекбоксов под линейку заголовков. Число, а не
// ширина в ячейках: столько именно пробелов рисуется, и столько же занимает
// первая ячейка подписи.
const filterRowIndent = len(filterRule) + 1

// filterRowPrefix — ширина, которую занимает начало строки чекбокса: отступ под
// линейку заголовков, сам чекбокс и пробел после него. Считается из тех же
// констант, что и рисуются, и применяется в обоих местах (рендер строки и
// расчёт ширины блока) — иначе подпись обрезалась бы по одной ширине, а мерилась
// по другой.
//
// Функция, а не константа, по двум причинам сразу. Первая: ширина галки нужна в
// ячейках, а у ✓ три байта в UTF-8, и подсчёт по len дал бы на две ячейки больше
// нарисованного. Вторая: cellWidth меряет тем методом, который согласован с
// терминалом (см. activeWidthMethod), а он может смениться уже после
// инициализации пакета, и вычисленное на старте значение разошлось бы с
// отрисовкой ровно там, где ширины считаются.
func filterRowPrefix() int {
	return filterRowIndent + cellWidth(filterBoxOn) + 1
}

// windowFilterMenuContent — окно строк, в котором курсор остаётся видимым, с
// пересчётом его номера внутри окна. Список короче предела возвращается целиком.
func windowFilterMenuContent(content []string, cursorLine, limit int) ([]string, int) {
	if limit <= 0 || len(content) <= limit {
		return content, cursorLine
	}
	// Окно центрируется на курсоре, но не выходит за края списка: у края
	// содержимое должно доезжать до конца, а не висеть пустыми строками.
	start := clampFilterMenuCursor(cursorLine-limit/2, len(content))
	if maxStart := len(content) - limit; start > maxStart {
		start = maxStart
	}
	return content[start : start+limit], cursorLine - start
}
