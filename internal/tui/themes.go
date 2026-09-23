package tui

import (
	"sort"

	"github.com/charmbracelet/lipgloss"
)

// Theme определяет цветовую тему приложения.
type Theme struct {
	Name string

	// Основной цвет — рамка панелей.
	ActiveBorderColor   lipgloss.Color
	InactiveBorderColor lipgloss.Color

	// Акцентный цвет — палитра ников (nickPalette) и цвет выделения (chatSelectionColor).
	NickPalette        []lipgloss.Color
	NickBorderPalette  []lipgloss.Color
	ChatSelectionColor lipgloss.Color

	// Дополнительный цвет — названия клавиш в подсказках (hintKeyStyle).
	HintKeyColor lipgloss.Color

	// Цвета своих сообщений (исходящих).
	OwnColor       lipgloss.Color
	OwnBorderColor lipgloss.Color
}

// Themes — зарегистрированные темы.
var Themes = map[string]Theme{
	"neon":       neonTheme(),
	"yellow":     yellowTheme(),
	"blue":       blueTheme(),
	"terracotta": terracottaTheme(),
}

// DefaultThemeName — имя темы по умолчанию.
const DefaultThemeName = "neon"

// themeNames возвращает имена зарегистрированных тем в стабильном
// (алфавитном) порядке — map-итерация недетерминирована, а список
// показывается человеку (helpScreen, ошибка неизвестной темы в :theme) и не
// должен скакать между запусками/рендерами.
func themeNames() []string {
	names := make([]string, 0, len(Themes))
	for n := range Themes {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// neonTheme — текущая палитра приложения (как в styles.go).
func neonTheme() Theme {
	return Theme{
		Name:                "neon",
		ActiveBorderColor:   lipgloss.Color("#2AABEE"),
		InactiveBorderColor: lipgloss.Color("#175E83"),
		NickPalette: []lipgloss.Color{
			lipgloss.Color("#FF6FCF"), // магента
			lipgloss.Color("#B18CFF"), // сиреневый
			lipgloss.Color("#FFC857"), // янтарный
			lipgloss.Color("#5AC8FA"), // голубой
		},
		NickBorderPalette: []lipgloss.Color{
			lipgloss.Color("#8C3D72"),
			lipgloss.Color("#614D8C"),
			lipgloss.Color("#8C6E30"),
			lipgloss.Color("#326E8A"),
		},
		ChatSelectionColor: lipgloss.Color("#FFC857"),
		HintKeyColor:       lipgloss.Color("#2AABEE"),
		OwnColor:           lipgloss.Color("#2DD4BF"),
		OwnBorderColor:     lipgloss.Color("#197569"),
	}
}

// yellowTheme — неоновый жёлтый как акцентный цвет.
func yellowTheme() Theme {
	return Theme{
		Name:                "yellow",
		ActiveBorderColor:   lipgloss.Color("#FFD600"),
		InactiveBorderColor: lipgloss.Color("#8C6E00"),
		NickPalette: []lipgloss.Color{
			lipgloss.Color("#FFD600"), // неоновый жёлтый
			lipgloss.Color("#FFEA00"), // яркий жёлтый
			lipgloss.Color("#FFF176"), // мягкий жёлтый
			lipgloss.Color("#FFEE58"), // тёплый жёлтый
		},
		NickBorderPalette: []lipgloss.Color{
			lipgloss.Color("#8C6E00"),
			lipgloss.Color("#8C7C00"),
			lipgloss.Color("#8C803E"),
			lipgloss.Color("#8C7A2E"),
		},
		ChatSelectionColor: lipgloss.Color("#FFD600"),
		HintKeyColor:       lipgloss.Color("#FFD600"),
		OwnColor:           lipgloss.Color("#FFF176"),
		OwnBorderColor:     lipgloss.Color("#8C7A2E"),
	}
}

// blueTheme — синяя тема (акцентный цвет синий, отличная от neon).
func blueTheme() Theme {
	return Theme{
		Name:                "blue",
		ActiveBorderColor:   lipgloss.Color("#2196F3"),
		InactiveBorderColor: lipgloss.Color("#124D7A"),
		NickPalette: []lipgloss.Color{
			lipgloss.Color("#2196F3"), // синий
			lipgloss.Color("#64B5F6"), // светло-синий
			lipgloss.Color("#BBDEFB"), // очень светло-синий
			lipgloss.Color("#1976D2"), // насыщенный синий
		},
		NickBorderPalette: []lipgloss.Color{
			lipgloss.Color("#124D7A"),
			lipgloss.Color("#1E5E7A"),
			lipgloss.Color("#3A6E8A"),
			lipgloss.Color("#0D3D5E"),
		},
		ChatSelectionColor: lipgloss.Color("#2196F3"),
		HintKeyColor:       lipgloss.Color("#2196F3"),
		OwnColor:           lipgloss.Color("#64B5F6"),
		OwnBorderColor:     lipgloss.Color("#1E5E7A"),
	}
}

// terracottaTheme — терракотовая тема (тёплый терракота).
func terracottaTheme() Theme {
	return Theme{
		Name:                "terracotta",
		ActiveBorderColor:   lipgloss.Color("#E2725B"),
		InactiveBorderColor: lipgloss.Color("#7A3A2D"),
		NickPalette: []lipgloss.Color{
			lipgloss.Color("#E2725B"), // терракота
			lipgloss.Color("#D69E8E"), // мягкий терракота
			lipgloss.Color("#C97A5E"), // тёплый терракота
			lipgloss.Color("#B8866B"), // пыльный терракота
		},
		NickBorderPalette: []lipgloss.Color{
			lipgloss.Color("#7A3A2D"),
			lipgloss.Color("#6B4F46"),
			lipgloss.Color("#6B3E30"),
			lipgloss.Color("#5E463D"),
		},
		ChatSelectionColor: lipgloss.Color("#E2725B"),
		HintKeyColor:       lipgloss.Color("#E2725B"),
		OwnColor:           lipgloss.Color("#D69E8E"),
		OwnBorderColor:     lipgloss.Color("#6B4F46"),
	}
}

// PanelBackgroundColors — цвета фона панелей по номеру (1-9), точные значения
// человека (PLAN.md, п.16) — шаг +0x08 по каждому каналу для 1-8, последний
// шаг 8→9 нерегулярный (+0x0C, не +0x08) — так задано явно, не округлено до
// регулярного шага.
var PanelBackgroundColors = []lipgloss.Color{
	lipgloss.Color("#111111"), // 1
	lipgloss.Color("#191919"), // 2
	lipgloss.Color("#212121"), // 3
	lipgloss.Color("#292929"), // 4
	lipgloss.Color("#313131"), // 5
	lipgloss.Color("#393939"), // 6
	lipgloss.Color("#414141"), // 7
	lipgloss.Color("#494949"), // 8
	lipgloss.Color("#555555"), // 9
}

// GetPanelBackground возвращает цвет фона для панели с заданным номером (1-9).
// Если номер вне диапазона — возвращает первый цвет.
func GetPanelBackground(paneNum int) lipgloss.Color {
	if paneNum < 1 || paneNum > len(PanelBackgroundColors) {
		return PanelBackgroundColors[0]
	}
	return PanelBackgroundColors[paneNum-1]
}
