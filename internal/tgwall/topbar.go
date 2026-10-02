package tgwall

import (
	"strings"
)

// Верхняя строка экрана: одна, во всю ширину терминала, без полей и без рамки.
//
// Слева — что показывает фильтр источников (та же строка, что и в меню ctrl+p),
// справа — название открытой переписки, выровненное по левому краю СВОЕЙ панели.
// В трёхколоночном режиме панелей переписки две, и верхняя строка называет обе:
// каждое название стоит над своей панелью, то есть по её якорю, а не по краю
// терминала (см. topBarParts). Фон основной (PaletteBackgroundMain), а не
// панельный: строка сама по себе ни чему не отделяется от стены, отделяет её
// только текст.

// topBarTitleStart — колонка, с которой начинается название открытой переписки:
// левее идёт колонка стены, между ними зазор. Ровно те два числа, что и у
// renderWallZone, взятые оттуда же: заголовок должен встать над своей панелью,
// иначе он читался бы как относящийся к колонке стены.
const topBarTitleStart = wallPanelColumnWidth + wallPanelGapWidth

// topBarSourceSeparator — чем перечислены выбранные источники в подписи слева.
// Точка по центру без пробелов: список короткий, и на полях строки места
// на « · » с обоих боков жалко.
const topBarSourceSeparator = "·"

// wallSourceLabel — что показывает фильтр источников, одной строкой.
//
// Собирается из filterMenuRows, а не отдельным перечислением подписей: меню
// фильтра и эта строка обязаны рассказывать одно и то же, и набор строк у них
// общий — перечисление типов в двух местах разъехалось бы при первой же правке
// формулировки, и строка тихо показывала бы не то, что на самом деле включено.
//
// Два отступления от меню, оба намеренные:
//
//   - «Все» заменяет собою три типа: при всех трёх включённых строка «Все» уже
//     стоит первой, и без замены подпись выходила бы «Все·Каналы·Чаты·Личные» —
//     одно и то же дважды.
//   - «Приглушённые» не выводится: это не источник, а правило показа, и по
//     умолчанию оно включено всегда — слово висело бы в строке постоянно и
//     ничего бы не сообщало.
func (m Model) wallSourceLabel() string {
	allTypes := m.filter.allTypes()
	labels := make([]string, 0, len(m.folders)+1)
	for _, row := range filterMenuRows(m.filter, m.folders) {
		if !row.selectable() || !row.checked {
			continue
		}
		if allTypes && row.kind.isSourceType() {
			continue
		}
		if row.kind == filterRowMuted {
			continue
		}
		labels = append(labels, row.label)
	}
	return strings.Join(labels, topBarSourceSeparator)
}

// isSourceType — строка меню, отбирающая один из трёх типов источников. Именно их
// (и только их) заменяет собой «Все», поэтому проверка живёт на виде строки, а не
// повторяется перечислением в wallSourceLabel.
func (k filterRowKind) isSourceType() bool {
	return k == filterRowChannels || k == filterRowChats || k == filterRowPersonal
}

// topBarTitle — что показывается справа: название открытой переписки и колонка,
// с которой оно начинается. В компактном режиме правая часть строки не рисуется
// вовсе: переписка там занимает весь экран и называет себя сама шапкой
// (см. zoomHeaderRows), а вторая подпись в строке шириной во весь экран только
// мешала бы.
//
// Одна панель переписки — то есть обычный широкий режим; при двух панелях
// названия собирает topBarParts.
func (m Model) topBarTitle() (string, int) {
	if !m.wallPanelMode() || m.zoom == nil {
		return "", 0
	}
	return m.zoom.title, topBarTitleStart
}

// topBarPart — подпись верхней строки, которой есть что показать: её текст и
// колонка, с которой он начинается.
type topBarPart struct {
	// text — что показывается.
	text string
	// anchor — колонка начала подписи. У названия переписки это якорь СВОЕЙ
	// панели (wallPanelStart), то есть её левая граница: по правому краю
	// терминала название выравнивать нельзя, см. topBarParts.
	anchor int
}

// topBarParts — названия открытых переписок, которые верхняя строка называет: по
// одному на панель, в которой переписка открыта, в порядке панелей на экране.
//
// Каждое название начинается с якоря СВОЕЙ панели, а не с края терминала. В
// обычном широком режиме это одна и та же колонка (topBarTitleStart — левая
// граница единственной панели), а в трёхколоночном две панели стоят одна за
// другой и края терминала у них разные: название, прижатое к краю строки, встало
// бы над соседней панелью (или над её зазором) и читалось бы как заголовок чужого
// чата — ровно то, чего строка не должна говорить. По той же причине не
// «догоняется» и правая граница: у панели своя ширина, и правее её названию
// нечего рисоваться.
//
// Панель без открытой переписки названия не получает: панель на экране есть, а
// переписки в ней нет, и называть её нечем.
func (m Model) topBarParts() []topBarPart {
	order := m.wallChatPanelsOrder()
	if len(order) == 1 {
		// Обычный широкий режим: панель одна, и её название с той же колонкой
		// приходит из topBarTitle — того места, где решается, рисуется ли
		// название вообще.
		title, anchor := m.topBarTitle()
		if title == "" {
			return nil
		}
		return []topBarPart{{text: title, anchor: anchor}}
	}
	parts := make([]topBarPart, 0, len(order))
	for _, panel := range order {
		zoom := m.panelZoom(panel)
		if zoom == nil {
			continue
		}
		parts = append(parts, topBarPart{text: zoom.title, anchor: m.wallPanelStart(panel)})
	}
	return parts
}

// topBarWidths — сколько ячеек достаётся каждой части строки.
//
// Правая часть начинается с anchor, то есть забирает всё, что правее него, и левая
// живёт в колонках до anchor. Если обе подписи в свои полосы помещаются, берут
// свою естественную ширину, а промежуток между ними забивается фоном.
//
// Если не помещаются — режутся ПРОПОРЦИОНАЛЬНО своим естественным ширинам, а каждая
// всё равно не залезает в свою полосу: длинное название чата не должно вытеснять
// подпись фильтра и наоборот.
//
// Это правило для двух частей строки, записанное буквально. Общий случай на любое
// число названий — topBarPartsWidths; при одном названии полосы совпадают с этими
// ячейка в ячейку (проверяет тест), а сама функция осталась отдельной не по
// нужде, а потому что для двух частей правило читается без разбора индексов по
// полосам.
func topBarWidths(width, anchor, sourceWidth, titleWidth int) (int, int) {
	if width <= 0 {
		return 0, 0
	}
	if titleWidth <= 0 || anchor <= 0 || anchor >= width {
		// Правой части нет: вся строка отдана подписи фильтра.
		return width, 0
	}
	if sourceWidth+titleWidth <= width {
		return min(sourceWidth, anchor), min(titleWidth, width-anchor)
	}
	need := sourceWidth + titleWidth
	return min(width*sourceWidth/need, anchor), min(width*titleWidth/need, width-anchor)
}

// topBarPartsWidths — сколько ячеек достаётся подписи фильтра и КАЖДОМУ из
// названий открытых переписок, по порядку их колонок.
//
// Правило то же, что у topBarWidths, названий может быть только больше: пока все
// подписи вместе влезают в терминал, каждая берёт свою естественную ширину, а
// промежутки добиваются фоном; не влезают — терминал делится между ними
// ПРОПОРЦИОНАЛЬНО естественным ширинам, и каждая всё равно ужимается до своей
// полосы.
//
// Полоса названия — от якоря его панели до колонки СЛЕДУЮЩЕГО названия (у
// последнего — до края терминала), а не до края терминала у каждого: иначе
// длинное название первого чата заехало бы на панель второго и читалось бы как
// его заголовок. Подпись фильтра живёт в полосе от нуля до якоря первого
// названия — то есть ровно там, где живут строки меню фильтра.
func topBarPartsWidths(width, sourceWidth int, parts []topBarPart) (int, []int) {
	if width <= 0 {
		// Полосы названий — всегда по одной на каждое название, даже когда рисовать
		// нечего: вызывающий идёт по названиям в порядке частей, и пустой список
		// сдвинул бы его на соседнее название.
		return 0, make([]int, len(parts))
	}
	if len(parts) == 0 {
		// Названий нет: вся строка отдана подписи фильтра.
		return width, nil
	}
	if len(parts) == 1 {
		source, title := topBarWidths(width, parts[0].anchor, sourceWidth, cellWidth(parts[0].text))
		return source, []int{title}
	}

	// bounds[i] — правый край полосы i-й подписи, где i = 0 — подпись фильтра,
	// дальше названия по порядку их колонок. Левый край полосы i — это bounds[i-1],
	// а у первой подписи ноль.
	bounds := make([]int, len(parts)+1)
	bounds[0] = parts[0].anchor
	for index := 1; index < len(parts); index++ {
		bounds[index] = parts[index].anchor
	}
	bounds[len(parts)] = width

	natural := make([]int, len(bounds))
	natural[0] = sourceWidth
	need := sourceWidth
	for index, part := range parts {
		natural[index+1] = cellWidth(part.text)
		need += natural[index+1]
	}

	cells := make([]int, len(bounds))
	for index := range cells {
		band := bandWidth(bounds, index)
		if need <= width {
			cells[index] = min(natural[index], band)
			continue
		}
		// need > width, значит need > 0 и деление на него безопасно.
		cells[index] = min(width*natural[index]/need, band)
	}
	return cells[0], cells[1:]
}

// bandWidth — ширина полосы i-й подписи строки по границам bounds (см.
// topBarPartsWidths). Ноль для перепутанных границ: подпись без полосы не
// рисуется вовсе, а не рисуется с начала строки.
func bandWidth(bounds []int, index int) int {
	left := 0
	if index > 0 {
		left = bounds[index-1]
	}
	return max(0, bounds[index]-left)
}

// renderTopBar — верхняя строка экрана: подпись фильтра слева, названия открытых
// переписок — каждое над своей панелью, от края до края терминала.
//
// Без боковых полей и без левой границы: строка занимает всю ширину и отделяется
// от стены только текстом. Цвет текста приглушённый у подписи фильтра (она
// сообщает состояние, а не содержание) и обычный у названия чата — в широком
// режиме это единственное место, где открытая переписка называет себя, и терять
// его читаемость нельзя.
func (m Model) renderTopBar(width int) string {
	if width <= 0 {
		return ""
	}
	source := m.wallSourceLabel()
	parts := m.topBarParts()
	sourceCells, titleCells := topBarPartsWidths(width, cellWidth(source), parts)

	line := foregroundBackgroundStyle(PaletteTextMuted, PaletteBackgroundMain).
		Render(truncateVisible(source, sourceCells))
	for index, part := range parts {
		if titleCells[index] == 0 {
			// Название не влезает ни в свою долю терминала, ни в свою полосу — на его
			// месте остаётся пустота: подпись, залезающая на соседнюю панель, врёт
			// больше, чем её отсутствие.
			continue
		}
		// Промежуток между подписями и хвост строки добивает fitLine фоном, а не
		// пробелами: иначе лишние ячейки уехали бы в родной фон терминала.
		line += renderedFill(part.anchor-cellWidth(line), PaletteBackgroundMain)
		line += foregroundBackgroundStyle(PaletteText, PaletteBackgroundMain).
			Render(truncateVisible(part.text, titleCells[index]))
	}
	return fitLine(line, width, PaletteBackgroundMain)
}
