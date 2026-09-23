package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// pillTextColor — тёмный текст на цветных пилюлях (не зависит от темы).
var pillTextColor = lipgloss.Color("#12131A")

// panePaddingH/panePaddingV — внутренний отступ панелей от рамки.
const (
	panePaddingH = 1
	panePaddingV = panePaddingH
)

// timeStyle — стиль времени (приглушённый).
var timeStyle = lipgloss.NewStyle().Faint(true)

// composeCardBorderColor — белая рамка карточки черновика (не зависит от темы).
const composeCardBorderColor = "#FFFFFF"

// composeCardStyle — стиль рамки карточки черновика.
func composeCardStyle() lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(lipgloss.Color(composeCardBorderColor))
}

// paneBorderStyle возвращает стиль рамки панели для заданной темы. paneNum —
// номер панели (1-9) для фона по номеру (см. PanelBackgroundColors,
// независимый от Theme слой, PLAN.md п.16) — 0 означает «без фона по номеру»
// (используется для оверлеев вроде helpScreen, которые не одна из
// пронумерованных панелей). Background() на внешнем стиле панели
// корректно прокрашивает весь вложенный отрендеренный контент, включая
// карточки сообщений со своей рамкой — проверено эмпирически оркестратором
// в задаче 0016/0020, доп. Background() на вложенных стилях не нужен.
func paneBorderStyle(focused bool, t Theme, paneNum int) lipgloss.Style {
	var style lipgloss.Style
	if focused {
		style = lipgloss.NewStyle().
			Border(lipgloss.DoubleBorder()).
			BorderForeground(t.ActiveBorderColor).
			Padding(panePaddingV, panePaddingH)
	} else {
		style = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(t.InactiveBorderColor).
			Padding(panePaddingV, panePaddingH)
	}
	if paneNum >= 1 {
		style = style.Background(GetPanelBackground(paneNum))
	}
	return style
}

// hintKeyStyle — стиль названия клавиши в подсказках (использует цвет темы).
func hintKeyStyle(t Theme) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(t.HintKeyColor)
}

// renderHint склеивает пары "клавиша"/"описание" через " — " (клавиша цветом темы,
// описание тусклым), записи между собой — через sep (тоже тусклым).
func renderHint(sep string, t Theme, pairs ...[2]string) string {
	descStyle := lipgloss.NewStyle().Faint(true)
	hintStyle := hintKeyStyle(t)
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
