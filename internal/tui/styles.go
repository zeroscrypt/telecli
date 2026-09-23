package tui

import "github.com/charmbracelet/lipgloss"

// activeBorderColor/inactiveBorderColor — один и тот же акцентный цвет рамки
// (фирменный синий Telegram, #2AABEE), разной яркости: активная панель —
// полный цвет, неактивная — тот же оттенок при ~55% яркости (RGB
// равномерно уменьшен, так оттенок не съезжает, ярче прежних 45% — по
// правке "рамки надо сделать поярче"). Цвета захардкожены как временный
// дефолт; конфигурируемая тема (lipgloss.AdaptiveColor, TOML-настройка) —
// будущий отдельный блок Theme в PLAN.md, не часть этой задачи.
var (
	activeBorderColor   = lipgloss.Color("#2AABEE")
	inactiveBorderColor = lipgloss.Color("#175E83")
)

// chatSelectionColor/pillTextColor — подсветка курсора в списке чатов:
// цветная «пилюля» (тёмный текст на ярком фоне) вместо инверсии, и текст на
// пилюле в статус-строке Normal-режима (там фон — activeBorderColor, тот же
// pillTextColor). По правке человека — тот же оранжево-жёлтый оттенок, что и
// у "Игорь" в nickPalette (осознанно отдельная константа, не прямая ссылка на
// nickPalette[2]: смысл разный, менять один не должно менять другой).
// Курсор в списке ПАПОК больше не красится этим цветом — см. foldersPane
// (треугольник-маркер + жирный текст вместо пилюли, по правке человека).
var (
	chatSelectionColor = lipgloss.Color("#FFC857")
	pillTextColor      = lipgloss.Color("#12131A") // тёмный текст на цветных пилюлях
)

// insertModeColor — оранжево-жёлтый цвет метки "INP" (Insert-режим) в нижней
// строке, по правке человека. Тот же оттенок, что chatSelectionColor —
// намеренно отдельная константа (смысл разный: там подсветка курсора в
// списке чатов, здесь — метка режима; менять один не должно менять другой).
var insertModeColor = lipgloss.Color("#FFC857")

// panePaddingH/panePaddingV — внутренний отступ панелей от рамки: по 1
// колонке слева/справа, по panePaddingV строк СВЕРХУ И СНИЗУ (по правке
// человека — пробовали 2 строки сверху+снизу, показалось примерно в 5 раз
// заметнее ожидаемого; затем 1 строка сверху+снизу; затем только сверху;
// финал — снова симметрично сверху и снизу, по 1 строке). КАЖДАЯ строка
// паддинга напрямую отнимает строку видимого контента (список папок/чатов
// короче, лента сообщений теснее) — учтено в internal/tui/model.go,
// listContentRows() вычитает panePaddingV ДВАЖДЫ (верх+низ), не забудь
// поправить там же, если это изменится снова.
const (
	panePaddingH = 1
	panePaddingV = panePaddingH
)

// paneBorderStyle — общий стиль рамки панели: у активной панели — двойная
// рамка (DoubleBorder) в полном акцентном цвете, у неактивной — одинарная
// (RoundedBorder) в затемнённом варианте того же оттенка, плюс горизонтальный
// паддинг (panePaddingH) с каждой стороны. Оба стиля задают стороны рамок
// строками из ОДНОЙ руны (GetHorizontalBorderSize()/GetVerticalBorderSize()
// равны 2 и 2 для обоих — ТОЛЬКО рамка, без паддинга), поэтому заменяющий
// DoubleBorder геометрию панелей не меняет — сверено по исходникам lipgloss
// v1.1.0 и зафиксировано regression-тестом
// TestActiveAndInactiveBorderFrameSizesEqual. ВАЖНО: с паддингом
// GetHorizontalFrameSize() (рамка+паддинг вместе) для расчёта Width() у
// paneBox использовать НЕЛЬЗЯ — см. комментарий paneBox, там отдельная
// эмпирически проверенная формула через GetHorizontalBorderSize().
func paneBorderStyle(focused bool) lipgloss.Style {
	if focused {
		return lipgloss.NewStyle().Border(lipgloss.DoubleBorder()).BorderForeground(activeBorderColor).Padding(panePaddingV, panePaddingH)
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(inactiveBorderColor).Padding(panePaddingV, panePaddingH)
}

// ownColor/otherColor — мягкий неоновый бирюзовый для своих сообщений
// (is_outgoing == true) и почти белый для сообщений собеседника, по правке
// человека.
var (
	ownColor   = lipgloss.Color("#2DD4BF")
	otherColor = lipgloss.Color("#F0F0F5")
)

var timeStyle = lipgloss.NewStyle().Faint(true)

// nickPalette — 4 мягких неоновых акцента для цвета ника собеседника; выбор
// детерминированный по хешу имени (nickIndex) — поэтому в личном чате (один
// собеседник) цвет всегда один и тот же без отдельной проверки "это группа
// или нет", а в группе у разных участников он естественно различается.
var nickPalette = []lipgloss.Color{
	lipgloss.Color("#FF6FCF"), // магента
	lipgloss.Color("#B18CFF"), // сиреневый
	lipgloss.Color("#FFC857"), // янтарный
	lipgloss.Color("#5AC8FA"), // голубой
}

// nickBorderPalette — приглушённые (55% яркости, тот же приём, что у
// activeBorderColor/inactiveBorderColor) варианты nickPalette для рамки
// карточки сообщения; индексы позиционно совпадают с nickPalette.
var nickBorderPalette = []lipgloss.Color{
	lipgloss.Color("#8C3D72"),
	lipgloss.Color("#614D8C"),
	lipgloss.Color("#8C6E30"),
	lipgloss.Color("#326E8A"),
}

// ownBorderColor — приглушённый (55%) вариант ownColor для рамки МОИХ
// сообщений. Без ротации — идентичность одна, в отличие от собеседников.
var ownBorderColor = lipgloss.Color("#197569")

// nickIndex — простой стабильный хеш имени отправителя (сумма байтов по
// модулю размера палитры) — не крипто, нужна только детерминированность и
// разброс по корзинам.
func nickIndex(name string) int {
	sum := 0
	for i := 0; i < len(name); i++ {
		sum += int(name[i])
	}
	return sum % len(nickPalette)
}

func nickColor(name string) lipgloss.Color {
	return nickPalette[nickIndex(name)]
}

func nickBorderColor(name string) lipgloss.Color {
	return nickBorderPalette[nickIndex(name)]
}

// messageColor — цвет для конкретного сообщения по признаку "моё/чужое".
// Вынесена отдельной функцией, чтобы её можно было протестировать напрямую,
// не парся ANSI-код из отрендеренной строки.
func messageColor(isOutgoing bool) lipgloss.Color {
	if isOutgoing {
		return ownColor
	}
	return otherColor
}

// composeCardBorderColor — белая рамка карточки черновика (Insert-режим),
// по прямому запросу человека: черновик встроен в низ панели сообщений как
// отдельная карточка, "по стилю как рамка сообщения" (тот же язык рамки,
// что у renderMessageCard — RoundedBorder, без паддинга), но белым цветом —
// чтобы визуально отличаться и от акцентной рамки панели, и от цветных
// рамок карточек сообщений (те красятся по отправителю).
var composeCardBorderColor = lipgloss.Color("#FFFFFF")

// composeCardStyle — стиль рамки карточки черновика. Без паддинга — тот же
// приём, что у renderMessageCard (только рамка + сам текст).
func composeCardStyle() lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(composeCardBorderColor)
}
