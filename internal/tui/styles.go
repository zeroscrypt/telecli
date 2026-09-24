package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// pillTextColor — тёмный текст на цветных пилюлях (не зависит от темы).
var pillTextColor = lipgloss.Color("#12131A")

// chromeBackground — сплошной чёрный фон "хрома" интерфейса: заголовочные
// строки панелей и нижняя строка (логотип, режим, подсказки, версия,
// командная строка). По прямому запросу человека (0039) эти области должны
// быть на сплошном чёрном, а не на пустом фоне терминала. Применяется на
// каждый листовой фрагмент строки (см. bgFill/chromeLine) — фон внешнего
// стиля не переживает вложенных \x1b[0m-сбросов (тот же механизм, что у
// фона панелей по номеру).
var chromeBackground = lipgloss.Color("#000000")

// panePaddingH/panePaddingV — внутренний отступ панелей от рамки.
const (
	panePaddingH = 1
	panePaddingV = panePaddingH
)

// paneBorderSize — толщина рамки панели с каждой стороны (по одной колонке/
// строке у RoundedBorder и DoubleBorder). paneFrameH/paneFrameV — полный
// ГОРИЗОНТАЛЬНЫЙ/ВЕРТИКАЛЬНЫЙ «кад» панели paneBox/paneBorderStyle: рамка
// вместе с паддингом (рамка добавляется поверх, паддинг расходует ширину
// изнутри — см. paneBox). Контент панели = размер панели минус кад; в
// Insert-режиме это ровно то, что реально доступно ленте и чёрной области
// поля ввода внутри единой рамки (см. msgPane/applyLayout, 0047). Держи в
// синхроне с paneBorderStyle.
const (
	paneBorderSize = 2
	paneFrameH     = 2*panePaddingH + paneBorderSize
	paneFrameV     = 2*panePaddingV + paneBorderSize
)

// timeStyle — стиль времени (приглушённый).
var timeStyle = lipgloss.NewStyle().Faint(true)

// messagePanelBg — цвет фона панели сообщений (всегда номер 3): единственная
// "открытая лента", чей контент (карточки сообщений) рендерится стилями со
// собственными цветами. Помимо панельного стиля применяется на КАЖДЫЙ листовой
// фрагмент контента этой ленты: фон внешнего стиля (paneBorderStyle/viewport)
// ставится один раз в начале строки и после первого же вложенного \x1b[0m
// (от имени/времени/текста карточки) перестаёт действовать до конца строки —
// поэтому каждый фрагмент обязан нести фоновый цвет сам (0039).
func messagePanelBg() lipgloss.Color {
	return GetPanelBackground(3)
}

// bgFill — фрагмент строки из ПУСТЫХ пробелов заданной ширины (в колонках),
// целиком окрашенный фоновым цветом color. Закрывает "пустые" колонки строки,
// которые иначе остались бы без фона: обычный пробел после вложенного
// \x1b[0m-сброса листового стиля фона не несёт, а окрашенный фрагмент
// переносит его дальше по строке без разрывов.
func bgFill(color lipgloss.Color, cols int) string {
	if cols <= 0 {
		return ""
	}
	return lipgloss.NewStyle().Background(color).Render(strings.Repeat(" ", cols))
}

// chromeLine — строка нижней области на сплошном чёрном фоне
// (chromeBackground) заданной ширины. Верхний слой ПОВЕРХ фрагментов: сам по
// себе фон одного Render() не закрывает дыры после вложенных сбросов, поэтому
// каждый листовой фрагмент строки тоже несёт chromeBackground (см. bgFill) —
// этот слой лишь гарантирует фон на стыках и полную ширину строки. Если
// контент уже шире width — не переносим и не обрезаем его (длинная строка не
// должна превращаться в две: прежнего поведения это бы сломало высоту View).
func chromeLine(width int, content string) string {
	if lipgloss.Width(content) >= width {
		return lipgloss.NewStyle().Background(chromeBackground).Render(content)
	}
	return lipgloss.NewStyle().Background(chromeBackground).Width(max(0, width)).Render(content)
}

// chromeText — фрагмент чистого текста на сплошном чёрном фоне хрома
// (chromeBackground): добавляется ПОСЛЕ отрендеренного фрагмента нижней
// строки, чей последний \x1b[0m уже сбросил внешний фон — так что фрагмент
// несёт фон сам. Пустой текст возвращает пустую строку (никаких кодов).
func chromeText(s string) string {
	if s == "" {
		return ""
	}
	return lipgloss.NewStyle().Background(chromeBackground).Render(s)
}

// paneBorderStyle возвращает стиль рамки панели для заданной темы. paneNum —
// номер панели (1-9) для фона по номеру (см. PanelBackgroundColors,
// независимый от Theme слой, PLAN.md п.16) — 0 означает «хром»: оверлеи вроде
// helpScreen/aboutScreen (они не одна из пронумерованных панелей) получают
// сплошной чёрный фон, как заголовки и нижняя строка (0042). ВАЖНО: Background()
// на внешнем стиле панели ставит фоновый цвет в каждом line только до ПЕРВОГО
// вложенного \x1b[0m — строки из чистого текста (папки, чаты, пустые
// промежутки) окрашиваются целиком, а строки с вложенными цветными стилями
// (карточки сообщений, см. renderMessageCard) требуют, чтобы каждый листовой
// фрагмент нёс фон панели сам (0039). Сам по себе внешний фон остаётся нужен и
// для панелей без вложенных стилей. Рамка (глифы ╔═╗│ и т.п.) — отдельный
// механизм lipgloss (borderRenderer стилизует её по BorderBackground, а не по
// Background) и рисуется на сплошном чёрном фоне (chromeBackground): рамка
// панели — тот же «рамочный» элемент интерфейса, что заголовок и нижняя
// строка (0039).
func paneBorderStyle(focused bool, t Theme, paneNum int) lipgloss.Style {
	var style lipgloss.Style
	if focused {
		style = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(t.ActiveBorderColor).
			BorderBackground(chromeBackground).
			Padding(panePaddingV, panePaddingH)
	} else {
		style = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.InactiveBorderColor).
			BorderBackground(chromeBackground).
			Padding(panePaddingV, panePaddingH)
	}
	if paneNum >= 1 {
		style = style.Background(GetPanelBackground(paneNum))
	} else {
		// paneNum == 0 — оверлеи вроде helpScreen/aboutScreen: не одна из
		// пронумерованных панелей, а хром (0042). Без этого их внешний стиль
		// вообще не получал фона: помогать должны были листовые стили, но дыры
		// на стыках многофрагментных строк оставались (баг живой проверки).
		style = style.Background(chromeBackground)
	}
	return style
}

// panelContentStyle — стиль внутреннего контента панели в Insert-режиме (см.
// msgPane): та же логика фона, что у paneBorderStyle, но БЕЗ рамки
// (Border/BorderForeground/BorderBackground) и БЕЗ паддинга (Padding) — и то,
// и другое рисуется РОВНО ОДИН раз на весь блок ленты+поля внешним paneBox,
// иначе рамка/паддинг задвоятся (0047). Фон нужен для пустых строк-
// разделителей между карточками сообщений, которые собственного фона не несут
// (см. messagePanelBg) и иначе просвечивали бы сквозь единую рамку. focused
// для цвета рамки здесь не нужен (рамки нет) — параметр оставлен для
// единообразия сигнатуры с paneBorderStyle.
func panelContentStyle(focused bool, t Theme, paneNum int) lipgloss.Style {
	var style lipgloss.Style
	if paneNum >= 1 {
		style = style.Background(GetPanelBackground(paneNum))
	} else {
		style = style.Background(chromeBackground)
	}
	return style
}

// hintKeyStyle — стиль названия клавиши в подсказках (использует цвет темы).
func hintKeyStyle(t Theme) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.HintKeyColor)
}

// renderHint склеивает пары "клавиша"/"описание" через " — " (клавиша цветом темы,
// описание тусклым), записи между собой — через sep (тоже тусклым). Каждый
// фрагмент несёт chromeBackground: renderHint живёт в нижней строке на
// сплошном чёрном фоне (см. bottomLine, 0039), а иначе его собственные
// \x1b[0m-сбросы оставили бы хвост строки без фона.
func renderHint(sep string, t Theme, pairs ...[2]string) string {
	descStyle := lipgloss.NewStyle().Faint(true).Background(chromeBackground)
	hintStyle := hintKeyStyle(t).Background(chromeBackground)
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = hintStyle.Render(p[0]) + descStyle.Render(" — "+p[1])
	}
	return strings.Join(parts, descStyle.Render(sep))
}

// nickIndex — простой стабильный хеш имени отправителя.
func nickIndex(name string, paletteSize int) int {
	sum := 0
	for i := 0; i < len(name); i++ {
		sum += int(name[i])
	}
	return sum % paletteSize
}

// nickColor возвращает цвет ника для имени по теме.
func nickColor(name string, t Theme) lipgloss.Color {
	return t.NickPalette[nickIndex(name, len(t.NickPalette))]
}

// nickBorderColor возвращает цвет рамки для имени по теме.
func nickBorderColor(name string, t Theme) lipgloss.Color {
	return t.NickBorderPalette[nickIndex(name, len(t.NickBorderPalette))]
}

// messageColor — цвет для конкретного сообщения по признаку "моё/чужое" по теме.
func messageColor(isOutgoing bool, t Theme) lipgloss.Color {
	if isOutgoing {
		return t.OwnColor
	}
	return otherColor
}

// otherColor — цвет сообщений собеседника (не зависит от темы, остаётся мягким белым).
const otherColor = "#F0F0F5"
