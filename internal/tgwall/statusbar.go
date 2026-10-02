package tgwall

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Нижняя строка: слева индикатор загрузки и название (плюс собственный ник
// аккаунта, если он есть), справа версия. Пока грузится хоть что-то видимое
// сейчас (стена целиком или история открытой переписки — см. Model.isLoading),
// индикатор — крутящийся спиннер; когда загружать нечего, на его месте пусто:
// крутящийся спиннер врал бы человеку, что что-то ещё едет, а статичный знак
// не сообщал бы ничего (по требованию человека — убран вовсе, а не заменён на
// другой символ).

const (
	// spinnerInterval — период одного кадра спиннера.
	spinnerInterval = 80 * time.Millisecond
	// spinnerColumnWidth — ширина колонки индикатора в ячейках, константа на
	// оба состояния. Когда грузиться нечего, колонка остаётся пустым фоном
	// ровно такой же ширины, какой был кадр: иначе название уезжало бы на
	// ячейку влево-вправо при каждом включении и выключении загрузки.
	spinnerColumnWidth = 1
)

// spinnerFrames — кадры спиннера, буквально из макета и в том же порядке, за
// циклом по кругу.
var spinnerFrames = []rune{'⣋', '⣙', '⢻', '⢼', '⣶', '⣴', '⣦', '⣧', '⣏', '⣏'}

// spinnerGlyph — значок индикатора для текущего состояния загрузки. Когда
// ничего не грузится, значка нет вовсе и возвращается пустая строка: колонку
// индикатора тогда держит пустой фон (spinnerColumnWidth), а не символ,
// который ничего не сообщает.
func spinnerGlyph(loading bool, frame int) string {
	if !loading {
		return ""
	}
	return string(spinnerFrames[frame%len(spinnerFrames)])
}

type spinnerTickMsg struct{}

// spinnerTickCmd — следующий кадр спиннера. Обычный tea.Tick, как и периодические
// перерисовки в tgcli: модель на каждом тике сдвигает кадр и заказывает следующий.
func spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg {
		return spinnerTickMsg{}
	})
}

// renderStatusBar — нижняя строка целиком: индикатор с названием слева, версия
// справа, между ними всё свободное место.
//
// loading — грузится ли сейчас что-то видимое, то есть Model.isLoading (стена
// целиком ИЛИ история открытой переписки), а не только первый источник.
//
// username — собственный ник аккаунта, выводится с «@» справа от логотипа (по
// указанию человека). Пустая строка (у аккаунта нет ника) не печатается вовсе,
// вместе с разделителем: выдумывать заглушку вместо данных нельзя, а пустой «@»
// на экране читался бы как сломанный интерфейс.
//
// appName и version — ТОЖЕ, что пришли снаружи (Model.appName/Model.version),
// а не константы пакета: и то и другое — про то, как приложение называет себя
// в этом конкретном запуске, и единственный их источник — точка входа
// cmd/telecli (версия — из -ldflags). Стена их только рисует.
func renderStatusBar(width, frame int, loading bool, username, appName, version string) string {
	if width <= 0 {
		return ""
	}
	background := PaletteBackgroundMain
	margin := renderedFill(cardMarginH, background)
	inner := width - 2*cardMarginH
	if inner <= 0 {
		return renderedFill(width, background)
	}

	// Зелёный (PaletteOK) — у крутящегося спиннера: цвет кадра и есть индикатор
	// загрузки, а не украшение (в задаче 0147 цвет спиннера был задан именно как
	// «пока состояние загрузки включено»). Приглушать больше нечего: когда
	// грузиться нечего, значка на этом месте нет вовсе, и колонку держит пустой
	// фон той же ширины.
	left := renderedFill(spinnerColumnWidth, background)
	if glyph := spinnerGlyph(loading, frame); glyph != "" {
		left = foregroundBackgroundStyle(PaletteOK, background).Render(glyph)
	}
	// Название приходит снаружи и в живом запуске непустое, но пустую строку
	// рисовать нечем: lipgloss на пустом тексте всё равно вернул бы управляющие
	// последовательности, и в колонке логотипа появился бы невидимый символ.
	if appName != "" {
		left += renderedFill(1, background) +
			lipgloss.NewStyle().Foreground(PaletteText).Bold(true).Background(background).Render(appName)
	}
	if username != "" {
		left += renderedFill(1, background) +
			foregroundBackgroundStyle(PaletteTextMuted, background).Render("@"+username)
	}
	// Версия — ровно та, что пришла: у сборки без -ldflags там "dev", и
	// выдумывать вместо неё настоящий номер релиза нельзя (см.
	// update.IsNewer — "dev" никогда не считается устаревшей версией).
	right := ""
	if version != "" {
		right = foregroundBackgroundStyle(PaletteTextFaint, background).Render(version)
	}
	// На очень узком терминале версия не влезает — она уходит целиком, а не
	// половиной строки: обрезанная версия читается как мусор в углу.
	if cellWidth(left)+1+cellWidth(right) > inner {
		return margin + fitLine(left, inner, background) + margin
	}
	fill := inner - cellWidth(left) - cellWidth(right)
	return margin + left + renderedFill(fill, background) + right + margin
}
