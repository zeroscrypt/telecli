package tgwall

import (
	"image/color"

	tea "charm.land/bubbletea/v2"
)

// Нумерация панелей — как у человека (2026-10-01): панель 0 — левая лента со
// списком источников, панель 1 — панель переписки у самой ленты, панель 2 —
// крайняя справа. Панель 2 появляется только в трёхколоночном режиме.
//
// Тип, а не голое int, ровно потому, что по коду панели решается всё поведение
// (что двигают стрелки, куда уходит текст, чью переписку рисуют) и появление
// новой панели без проверки по нему дало бы молчаливый no-op на несуществующем
// номере.

// wallPanel — номер панели экрана.
type wallPanel int

const (
	// panelWall — левая лента со списком источников (панель 0).
	panelWall wallPanel = 0
	// panelChat1 — первая панель переписки, у самой ленты (панель 1).
	panelChat1 wallPanel = 1
	// panelChat2 — вторая панель переписки, крайняя справа (панель 2).
	panelChat2 wallPanel = 2
)

// wallPanelFirstChat — первая из панелей переписки: та, что ближе к ленте.
// Отдельная константа, потому что «первая» в разговоре про панели переписки и
// «первая на экране» — разные вещи: на экране первой стоит лента, и назвать её
// panelFirstChat значило бы поменять смысл имени вместе с добавлением панели.
const wallPanelFirstChat = panelChat1

// isChatPanel — панель ли это переписки (не лента).
func (p wallPanel) isChatPanel() bool {
	return p != panelWall
}

// chatPanelAt — панель переписки по её номеру среди панелей чатов (0 и 1), то
// есть той самой нумерации, которой оперирует openPanelN.
//
// Возвращает panelWall для номера за пределами: вызывающий код уже решил, что
// панели с таким номером нет, и этот откат — «никуда» точнее, чем паника или
// выход за границу среза.
func chatPanelAt(index int) wallPanel {
	switch index {
	case 0:
		return panelChat1
	case 1:
		return panelChat2
	default:
		return panelWall
	}
}

// wallChatPanelsOrder — номера панелей переписки в порядке слева направо, по
// одному на каждую существующую панель.
//
// Порядок задан явно, а не «считай от 1 до N»: слева направо по экрану панель
// с меньшим номером стоит раньше, и обход в этом порядке — то, что нужно и
// отрисовке, и склейке колонок. Любое другое перечисление разошлось бы с
// порядком на экране.
func (m Model) wallChatPanelsOrder() []wallPanel {
	switch m.wallChatPanels() {
	case 2:
		return []wallPanel{panelChat1, panelChat2}
	case 1:
		return []wallPanel{panelChat1}
	default:
		return nil
	}
}

// openLatestChats — первый запуск: открыть последние чаты потока в панели, по
// одному на каждую.
//
// Сколько панелей — столько и чатов: в обычном широком режиме панель одна и
// открывается последний чат, в трёхколоночном — последние два (решение человека,
// 2026-10-01). Больше панелей, чем чатов, быть не может — недостающие остаются
// пустыми, и это обычное состояние на пустой стене.
//
// Фокус при этом остаётся на ЛЕНТЕ: открытые панели — это то, что человек видит
// при запуске, а не то, куда он уже вошёл. Фокус на панели означал бы, что Enter
// отправит написанное в чат, который человек ещё не выбирал.
func (m Model) openLatestChats() (Model, tea.Cmd) {
	order := m.wallChatPanelsOrder()
	cmds := make([]tea.Cmd, 0, len(order))
	// С конца потока: карточки отсортированы по возрастанию даты, то есть свежие
	// внизу.
	for index := range order {
		position := len(m.cards) - 1 - index
		if position < 0 {
			break
		}
		next, cmd := m.openWallZoomIn(order[index], m.cards[position].ChatID, m.cards[position].title())
		m = next
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	if len(cmds) == 0 {
		return m, nil
	}
	return m, tea.Batch(cmds...)
}

// wallAccent — цвет контура колонки стены: тип чата в ФОКУСНОЙ панели. У ленты
// своей подписи нет, и её контур красится тем же, что и у панели с фокусом, —
// иначе на экране было бы два разных цвета контура при одном фокусе.
func (m Model) wallAccent() color.Color {
	if m.focusPanel.isChatPanel() {
		return m.panelAccent(m.focusPanel)
	}
	// Фокус на ЛЕНТЕ: цвет типа источника под курсором. Без этого контур ленты
	// был бы цвета фона, то есть невидим, — а фокус на ней самое обычное
	// состояние при запуске, и «где я» должно читаться сразу.
	if len(m.cards) == 0 {
		return PaletteBackgroundMain
	}
	return m.cards[clampWallCursor(m.cursor, len(m.cards))].accent()
}

// panelFocused — в фокусе ли эта панель. Функцией, а не сравнением в отрисовке:
// правило «фокус совпадает с панелью» должно быть одно, иначе рамка и поведение
// Tab разойдутся в каком-нибудь из режимов.
func (m Model) panelFocused(panel wallPanel) bool {
	return m.focusPanel == panel
}

// otherChatPanel — вторая из двух панелей переписки: той, что сейчас свободна,
// становится зафиксированная, а эта — свободной (docs/tgwall-help.md).
//
// Переключение безусловное и ровно на другую панель: при одной панели её
// «вторая» — она же, и Enter на ленте ничего переключать не должен.
func otherChatPanel(panel wallPanel) wallPanel {
	if panel == panelChat1 {
		return panelChat2
	}
	return panelChat1
}

// wallChatPanels — сколько панелей переписки есть при текущей ширине терминала:
// одна в обычном широком режиме, две в трёхколоночном, ноль в узком (там
// переписка занимает весь ввод, и отдельных панелей нет).
func (m Model) wallChatPanels() int {
	switch {
	case m.wallTwoPanelMode():
		return 2
	case m.wallPanelMode():
		return 1
	default:
		return 0
	}
}

// wallTwoPanelMode — трёхколоночный режим: ширина терминала больше
// wallTwoPanelThreshold. Левая колонка стены при этом не меняется (см.
// wallPanelColumnWidth), а правая зона делится на две равные панели.
//
// Порог задан человеком явно (2026-10-01) и не выводится из прочей геометрии:
// при 111 колонках панели по 40 ячеек, бабл сообщения занимает 26 — столько же,
// сколько уже занимает в обычном широком режиме при 66 колонках, то есть
// читаемость от деления не падает. Выводить порог из минимальной ширины панели
// значило бы ввести число, которого человек не называл.
func (m Model) wallTwoPanelMode() bool {
	return m.width > wallTwoPanelThreshold
}

// focusedZoom — переписка, в которой сейчас фокус, или nil.
//
// В узком режиме панелей нет вовсе, и переписка лежит в m.zoom: там
// panelZoom(panelWall) вернул бы nil, потому что у ленты переписки не бывает.
// Поэтому единого обращения через panelZoom(focusPanel) НЕДОСТАТОЧНО — был
// случай, когда отправка в компактном режиме падала на nil (тест
// TestSendGoesToZoomChatNotWallCard).
func (m Model) focusedZoom() *wallZoom {
	if !m.wallPanelMode() {
		return m.zoom
	}
	return m.panelZoom(m.focusPanel)
}

// panelZoom — переписка в указанной панели или nil, если её там нет. Панель
// ленты (panelWall) переписку не имеет, поэтому у неё всегда nil.
func (m Model) panelZoom(panel wallPanel) *wallZoom {
	if !panel.isChatPanel() {
		return nil
	}
	if panel == panelChat2 {
		return m.zoomPinned
	}
	return m.zoom
}

// setPanelZoom — положить переписку в панель (nil — убрать её оттуда).
func (m *Model) setPanelZoom(panel wallPanel, zoom *wallZoom) {
	if panel == panelChat2 {
		m.zoomPinned = zoom
		return
	}
	m.zoom = zoom
}

// twoPanelPinned — открыта ли вторая панель. Одна проверка вместо «zoomPinned !=
// nil» в каждом из многих мест: правило «вторая панель есть, если в ней что-то
// открыто» не должно расходиться по форме записи.
func (m Model) twoPanelPinned() bool {
	return m.zoomPinned != nil
}

// panelListWidth — ширина СОДЕРЖИМОГО панели по её номеру. Обёртка над
// wallPanelListWidth для мест, где ширина нужна вместе с самой перепиской:
// берётся ширина той панели, в которой переписка лежит, а не фокусной.
func (m Model) panelListWidth(panel wallPanel) int {
	return m.wallPanelListWidth(panel)
}

// wallPanelWidths — ширина каждой панели ПЕРЕПИСКИ по порядку, без зазоров и без
// контуров.
//
// Деление с остатком в пользу первой панели: при нечётном остатке она забирает
// лишнюю ячейку, а не вторая (правило человека, 2026-10-01: «если остаётся 81,
// первой достаётся 41, второй 40»). Отдача остатка второй панели дала бы то же
// самое распределение, но переставило бы его при каждом ресайзе на ячейку, и
// рамка бы прыгала.
//
// В обычном широком режиме возвращается одна панель на всю правую зону, в узком —
// ни одной: там переписка не панель, а весь экран.
func (m Model) wallPanelWidths() []int {
	switch {
	case m.wallTwoPanelMode():
		remainder := m.wallChatZoneWidth()
		first := remainder - remainder/2
		return []int{first, remainder / 2}
	case m.wallPanelMode():
		return []int{m.wallChatZoneWidth()}
	default:
		return nil
	}
}

// wallChatZoneWidth — ширина правой зоны целиком, из которой делятся панели
// переписки, БЕЗ зазоров между ними.
//
// Зазоров вычитается столько же, сколько панелей: один у ленты и ещё по одному
// между каждой парой панелей. Вычесть один зазор на всю зону (как было при одной
// панели) нельзя: в двухпанельном режиме renderWallZone склеивает колонки через
// wallPanelGapWidth, и вторая панель уезжала на ячейку за правый край терминала —
// рамка обрезалась краем экрана.
//
// Именно поэтому ширина делится на 32, а не на 31: 30 колонка ленты, 1 зазор у
// её правого края и ещё 1 между панелями.
func (m Model) wallChatZoneWidth() int {
	gaps := m.wallChatPanels()
	if gaps == 0 {
		return max(0, m.width-wallPanelColumnWidth-wallPanelGapWidth-wallPanelInsetRight)
	}
	return max(0, m.width-wallPanelColumnWidth-gaps*wallPanelGapWidth-wallPanelInsetRight)
}

// wallPanelWidth — ширина панели переписки по её номеру на экране. Ноль для
// ленты и для несуществующей панели: вызывающий код рисует на такой ширине
// пустой фон, и это ровно то, что должно быть на месте панели, которой нет.
func (m Model) wallPanelWidth(panel wallPanel) int {
	widths := m.wallPanelWidths()
	if !panel.isChatPanel() || int(panel) > len(widths) {
		return 0
	}
	return widths[int(panel)-1]
}

// wallPanelInsetRight — поле СПРАВА от крайней панели переписки, до края
// терминала. Едино, а не по ячейке у каждой панели: внутренние зазоры между
// панелями уже съедаются wallPanelGapWidth, и повторное поле у каждой съедало бы
// по ячейке зоны на панель.
//
// Без этого рамка крайней панели стояла вплотную к краю экрана: последним
// нарисованным знаком был ┓ или ┃, и панель читалась обрезанной (решение
// человека, 2026-10-01). Зазор нужен и у ЛЕНТЫ справа — там боковое поле экрана
// шире, но рамка панели стены отстоит от края так же, как рамки чатов.
const wallPanelInsetRight = 2

// wallPanelListWidth — ширина СОДЕРЖИМОГО панели по её номеру: та же ширина минус
// контур (wallPanelOutlineInset берёт по ячейке с каждой стороны, место
// зарезервировано всегда — см. его же комментарий).
func (m Model) wallPanelListWidth(panel wallPanel) int {
	return max(0, m.wallPanelWidth(panel)-wallPanelOutlineInset)
}

// wallPanelStart — колонка, с которой начинается панель. Считается от ПЕРВОЙ
// панели переписки, а не от края терминала: без этого верхняя строка экрана
// назвала бы чат не над той колонкой, в которой он нарисован.
func (m Model) wallPanelStart(panel wallPanel) int {
	if !panel.isChatPanel() {
		return 0
	}
	start := wallPanelColumnWidth + wallPanelGapWidth
	for current := panelChat1; current < panel; current++ {
		start += m.wallPanelWidth(current) + wallPanelGapWidth
	}
	return start
}

// wallPanelListHeight — высота содержимого панели. Одинакова для всех панелей:
// высоту zone считает каркас экрана, и различаться между панелями она не может.
// Плоская функция от панели — ровно ради этого: одна высота на все места
// (прокрутка и отрисовка берут её отсюда), а не своя формула у каждой.
func (m Model) wallPanelListHeight() int {
	return max(0, m.wallContentHeight()-m.zoomHeaderRows())
}
