package tgwall

import (
	"image/color"
	"strings"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/lipgloss/v2"

	"telecli/internal/keyinput"
)

// Поле ввода стены. Виджет тот же, что у tgcli, и растёт вверх точно так же:
// чем больше строк текста, тем выше поднимается верхний край поля, а нижний
// (разделитель над нижней строкой) остаётся на месте.
//
// Механика роста скопирована из internal/tgclitui (inputrows.go, caret.go) целиком,
// вместе с её причинами: подсчёт строк ЭКРАНА через одноразовый пробник, а не
// строк значения, и возврат окна к каретке после роста. Обе вещи там уже чинились
// сегодняшними фиксами регрессий, и переписывать их здесь заново значило бы
// вернуть те же баги.

const (
	// inputPaddingH — два пробела от левой границы зоны до текста поля.
	inputPaddingH = 2
	// inputMinHeight — минимум одна строка поля, даже когда значение пустое.
	inputMinHeight = 1
	// inputCaretBlinkSpeed — период мигания каретки. В v2 миганием занимается сам
	// виджет на своих часах, программа только пересылает его сообщения обратно в
	// Update (см. Model.Update).
	inputCaretBlinkSpeed = 1060 * time.Millisecond
	// inputRowsWalkLimit — сколько строк экрана перебирается при подсчёте: поле
	// всё равно ужимается, а обход очень длинного значения дороже, чем он даёт.
	inputRowsWalkLimit = 200
)

// Разделитель строки подсказок. Такой же, как у ленты tgcli: «клавиша слово»
// через пробел, пары через « · ».
const wallHintSeparator = " · "

// hintLine — строка подсказок над полем, собирается из клавиш, на которые стена
// правда реагирует: подписи берутся из keymap (то есть из keybindings.toml), иначе
// правка конфигурации меняла бы поведение, а подсказка продолжала бы показывать
// старое.
//
// Перенос строки (ctrl+j) в этом списке — исключение с обоснованием: это клавиша
// САМОГО виджета textarea (input.KeyMap.InsertNewline), а не действие стены, и
// настраивается она в keymap виджета, не в нашем (см. newInput). Поэтому подпись
// берётся оттуда же, откуда берётся поведение, — чтобы и здесь нельзя было
// разойтись с тем, что реально нажимается.
//
// Подписи слов — по состоянию, а не одним набором на все случаи: одна и та же
// клавиша в разных состояниях делает разное (см. moveUpHintWord и
// selectHintWord), и каждое слово берётся из ТОГО ЖЕ условия, что и сама
// обработка клавиши.
//
// Про «/ поиск» из прежнего текста подсказки: действия поиска у стены нет (в
// отличие от ленты tgcli, где поиск есть и в конфигурации, и в коде), поэтому в
// собираемой строке его нет — обещать клавишу, которая ничего не делает, хуже,
// чем молча не упомянуть несуществующую возможность.
func (m Model) hintLine() string {
	return hintText(m.hintPairs())
}

// hintText — подсказки одной строкой, для проверок и для мест, где нужен ТЕКСТ
// (тесты, дампы). Сама отрисовка идёт от hintPairs напрямую (см. renderHint):
// здесь каждая пара склеена со своим словом БЕЗ разделителя, потому что
// wallHintSeparator — это уже разделитель между парами, а не внутри пары.
//
// Одна склейка на двоих: собирать подсказку вручную в каждом месте значило бы
// через раз получить разные строки на экране и в проверке — и ровно это
// произошло бы, стоит поменять формат в одном из них.
func hintText(pairs []hintPair) string {
	plain := make([]string, 0, len(pairs))
	for _, pair := range pairs {
		plain = append(plain, pair.key+" "+pair.word)
	}
	return strings.Join(plain, wallHintSeparator)
}

// hintPair — одна подсказка строки: КЛАВИША и СЛОВО к ней. Две части, а не одна
// строка, ровно потому, что рисуются они разными цветами: клавиша — тем, что
// PaletteTextMuted, слово — новым PaletteHintWord. Смешанная строка «ctrl+p
// фильтр» одним стилем была бы вязкой, а разбирать её обратно на части при
// отрисовке — значит повторно угадывать, где кончается клавиша.
type hintPair struct {
	key  string
	word string
}

// hintPairs — подсказки в порядке показа, по одной на действие. Слова берутся из
// ТЕХ ЖЕ условий, что и сама обработка клавиши (см. selectHintWord и
// moveUpHintWord), поэтому подсказка не может с поведением разойтись.
func (m Model) hintPairs() []hintPair {
	pairs := make([]hintPair, 0, 5)
	if label := m.keymap.label(keyOpenFilter); label != "" {
		pairs = append(pairs, hintPair{key: label, word: wallHintFilter})
	}
	if label := m.newLineLabel(); label != "" {
		pairs = append(pairs, hintPair{key: label, word: "строка"})
	}
	if label := m.keymap.label(keySelect); label != "" {
		pairs = append(pairs, hintPair{key: label, word: m.selectHintWord()})
	}
	if label := m.keymap.label(keyMoveUp); label != "" {
		pairs = append(pairs, hintPair{key: label, word: m.moveUpHintWord()})
	}
	// Круг фокуса — только когда фокусировать есть чем: в узком режиме панели
	// переписки нет вовсе, и обещанные в подсказке Tab были бы клавишами,
	// которые ничего не делают.
	if m.focusable() {
		if label := m.keymap.label(keyFocusNext); label != "" {
			pairs = append(pairs, hintPair{key: label, word: wallHintPanels})
		}
	}
	return pairs
}

// Подсказки меняются по месту, а не одним набором для всех состояний: «↑ лента»
// на самом деле листает переписку, когда фокус на ней, и обещать «лента» там —
// обещать не то, что сделает клавиша.
const (
	// wallHintFilter — что открывает keyOpenFilter: меню выбора источников, то
	// есть фильтр ленты.
	wallHintFilter = "фильтр"
	// wallHintWall — что листает keyMoveUp, когда в фокусе стена.
	wallHintWall = "лента"
	// wallHintChat — когда в фокусе переписка.
	wallHintChat = "чат"
	// wallHintSend — Enter с непустым полем.
	wallHintSend = "отправить"
	// wallHintOpenChat — Enter с пустым полем в узком режиме, где он открывает
	// источник под курсором.
	wallHintOpenChat = "чат"
	// wallHintAnswer — Enter при выбранной цели ответа (Ctrl+R). Отдельно от
	// wallHintSend, а не замена им: слово «отправить» ничего не говорит о том,
	// ЧТО отправится, а человек именно это и должен проверить перед нажатием.
	wallHintAnswer = "ответить"
	// wallHintPanels — куда ведёт Tab: по кругу панелей вперёд и назад. Знаки, а
	// не слова: строка подсказок и так в одну линию, а «туда и обратно» двумя
	// словами на узком терминале в неё не влезает. Сам Shift+Tab в слове не
	// назван — он читается по соседнему знаку, а подробность живёт в справке.
	wallHintPanels = "→ ←"
)

// moveUpHintWord — что на самом деле делает стрелка вверх в текущем состоянии.
// Тот же признак, что и у самой навигации (moveFocused), иначе подсказка и
// поведение разошлись бы ровно в тот момент, когда человек им поверил.
func (m Model) moveUpHintWord() string {
	if m.zoomHasFocus() {
		return wallHintChat
	}
	return wallHintWall
}

// selectHintWord — что на самом деле делает Enter.
//
// Выбранная цель ответа проверяется ПЕРВОЙ и не зависит ни от режима, ни от
// пустоты поля: цель выбрал человек (Ctrl+R), и Enter в этом состоянии уходит
// именно на неё (см. submitInput — replyTarget стоит первым случаем там). Поэтому
// «ответить» показывается всегда, пока ответ выбран, даже при пустом поле.
//
// Без ответа: в широком режиме и при непустом поле это отправка, а в узком с
// пустым полем Enter открывает источник под курсором. Обещать там «отправить» —
// обещать отправку пустого сообщения, которой не будет.
func (m Model) selectHintWord() string {
	if m.replyTarget != nil {
		return wallHintAnswer
	}
	if m.wallPanelMode() || m.zoom != nil || m.inputHasText() {
		return wallHintSend
	}
	return wallHintOpenChat
}

// inputHasText — есть ли в поле что отправлять. Пробелы не в счёт: ровно так же
// от них избавляется submitInput, и подсказка про «чат» не должна появляться
// из-за пробела, который отправлять некуда.
func (m Model) inputHasText() bool {
	if m.input == nil {
		return false
	}
	return strings.TrimSpace(m.input.Value()) != ""
}

// newLineLabel — клавиша переноса строки по keymap'у самого виджета. Пустая строка,
// если у виджета такая клавиша не задана: тогда подсказка просто не упоминает её,
// а не показывает несуществующую.
func (m Model) newLineLabel() string {
	if m.input == nil {
		return ""
	}
	keys := m.input.KeyMap.InsertNewline.Keys()
	if len(keys) == 0 {
		return ""
	}
	return keyinput.DisplayName(keys[0])
}

// newInput — поле ввода стены.
//
// Enter переносом строки НЕ вставляет: enter — это отправка (см. submitInput), и
// втихую разбивать им набранный текст нельзя. Перенос строки живёт на ctrl+j,
// как и обещает строка подсказок.
func newInput() textarea.Model {
	input := textarea.New()
	input.Prompt = ""
	input.ShowLineNumbers = false
	input.MaxWidth = 0
	input.MaxHeight = 0
	input.KeyMap.InsertNewline.SetKeys("ctrl+j")

	background := PaletteBackgroundPanel
	styles := input.Styles()
	// Фон ставится сразу на все части стиля (база, строка каретки, промпт, текст,
	// подсказка, конец буфера): иначе часть строк уехала бы на фон терминала по
	// умолчанию, и при одинаковом фоне строк поле выглядело бы полосатым.
	styles.Focused = withInputBackground(styles.Focused, background)
	styles.Blurred = withInputBackground(styles.Blurred, background)
	styles.Cursor.Blink = true
	styles.Cursor.BlinkSpeed = inputCaretBlinkSpeed
	input.SetStyles(styles)
	input.Blur()
	return input
}

func withInputBackground(state textarea.StyleState, background color.Color) textarea.StyleState {
	state.Base = state.Base.Background(background)
	state.CursorLine = state.CursorLine.Background(background).Foreground(PaletteText)
	state.Prompt = state.Prompt.Background(background)
	state.Text = state.Text.Background(background)
	state.Placeholder = state.Placeholder.Background(background)
	state.EndOfBuffer = state.EndOfBuffer.Background(background)
	return state
}

// inputVisualRows — сколько строк экрана занимает значение поля, вместе с
// переносами длинных строк.
//
// Считает сам виджет: его CursorUp и CursorDown идут по строкам *экрана*, а не по
// строкам значения, поэтому обход пересчитывает ровно то, что нарисуется. Считать
// приблизительно по длине текста нельзя: перенос у виджета по словам, и «сколько
// символов влезет» ошиблось бы на длинном слове без пробелов.
//
// Обход идёт в обе стороны от каретки: строки выше неё и строки ниже. Обход идёт
// на одноразовом пробнике, а не на копии настоящего поля: в v2 копия делит с
// оригиналом указатель на окно (textarea.Model.viewport — это *viewport.Model), и
// CursorUp/CursorDown заканчиваются repositionView(), который окно и прокручивает.
func inputVisualRows(source textarea.Model) int {
	probe := newInputProbe(source)
	// Вверх: от самой каретки. Шаг виджета идёт по строке *экрана*, поэтому перенос
	// длинной строки пересчитывается сам.
	up := probe
	above := walkVisualRows(&up, (*textarea.Model).CursorUp)
	// Вниз: от самой каретки, шаг — одна строка экрана.
	down := probe
	below := walkVisualRows(&down, (*textarea.Model).CursorDown)
	// Плюс строка самой каретки: и выше, и ниже её посчитано, а её саму — нет.
	return above + below + 1
}

// newInputProbe — одноразовый пробник для обхода: своё окно, своё значение, те же
// настройки переноса, что у настоящего поля.
//
// Настройки копируются не для красоты: у textarea перенос считается по ширине, а
// SetWidth вычисляет её как SetWidth минус ширина промпта, рамки и номеров строк,
// поэтому у пробника после настройки ширина обязана совпасть с шириной настоящего
// поля, а не с той, что в него передали. Фокус копируется по той же причине:
// SetWidth смотрит на стили АКТИВНОГО состояния, а у пробника по умолчанию оно
// всегда blurred.
func newInputProbe(source textarea.Model) textarea.Model {
	probe := textarea.New()
	probe.Prompt = source.Prompt
	probe.ShowLineNumbers = source.ShowLineNumbers
	probe.MaxWidth = source.MaxWidth
	probe.MaxHeight = source.MaxHeight
	probe.SetStyles(source.Styles())
	if source.Focused() {
		// Focus возвращает команду запуска мигания, а пробник одноразовый и команду
		// никто не исполнит: нужна только сама отметка о фокусе, её ставит Focus.
		_ = probe.Focus()
	}
	probe.SetWidth(source.Width())
	probe.SetHeight(source.Height())
	probe.SetValue(source.Value())
	return probe
}

// walkVisualRows — сколько строк экрана обход пересчитал, прежде чем каретка
// перестала двигаться. Шаг задан отдельно, потому что вверх и вниз у виджета это
// разные методы.
func walkVisualRows(probe *textarea.Model, step func(*textarea.Model)) int {
	rows := 0
	for rows < inputRowsWalkLimit {
		line, wrapped := probe.Line(), probe.LineInfo().RowOffset
		step(probe)
		if probe.Line() == line && probe.LineInfo().RowOffset == wrapped {
			// Каретка не сдвинулась: дошли до края значения.
			break
		}
		rows++
	}
	return rows
}

// keepInputVisibleAfterGrowth — вернуть видимую часть поля к началу содержимого.
//
// Виджет textarea прокручивает своё окно по строке каретки, но делает это с той
// высотой, какая была до нажатия: repositionView зовётся внутри Update, а высоту мы
// ставим позже. Из-за этого перенос строки в конце набранного текста оставлял поле
// пролистанным вниз — текст уходил выше окна, и человек видел пустое поле.
//
// Лечение: SetValue внутри сбрасывает виджет, а Reset уводит окно в начало, поэтому
// значение переставляется тем же текстом, а каретка возвращается на своё место.
// Колонка берётся сложением StartColumn и ColumnOffset, а не CharOffset: при
// переносе длинной строки CharOffset — это колонка внутри перенесённого куска, и
// такая каретка встала бы в середине слова.
//
// Вызывается только когда число строк *на экране* выросло: в остальных случаях окно
// уезжать не может, а лишний SetValue на каждом нажатии стоил бы каретке позиции.
func keepInputVisibleAfterGrowth(input *textarea.Model, rowsBefore int) {
	if inputVisualRows(*input) <= rowsBefore {
		return
	}
	row := input.Line()
	info := input.LineInfo()
	input.SetValue(input.Value())
	for input.Line() > row {
		input.CursorUp()
	}
	input.SetCursorColumn(info.StartColumn + info.ColumnOffset)
}

// wallReplyMarker — знак ответа в начале контекстной строки. Короткий и
// узнаваемый: по нему видно, что это ответ, ещё не прочитав ни слова подписи.
const wallReplyMarker = "↩"

// inputTargetAccent — цвет левой границы поля ввода: тип ТОЙ цели, в которую
// сейчас уйдёт набранный текст.
//
// Цель определяется ровно тем же условием, что и отправка (см. submitInput) и
// ответ (см. startReply): переписка в фокусе — значит пишем в неё, иначе в чат
// карточки под курсором стены. Граница обязана показывать именно её, а не то,
// что видно под курсором: в широком режиме переписка открыта по тому источнику,
// на котором курсор стоял, когда её открыли, и с тех пор курсор мог уйти на
// другой источник — граница, покрашенная по курсору, обещала бы отправку не туда,
// куда сообщение уйдёт на самом деле.
//
// У переписки своего поля с типом нет, поэтому акцент берётся у карточки её
// источника в полном потоке (m.source), а не у карточки под курсором. Источник
// не найден (переписка открыта, а карточка отфильтрована или удалена) —
// безопасный откат на цвет личного диалога, а не паника: граница не обязана быть
// точной в состоянии, которого на живой стене не бывает.
func (m Model) inputTargetAccent() color.Color {
	if m.zoomHasFocus() {
		// Акцент по ЧАТУ В ФОКУСЕ, а не по первой панели: при двух панелях
		// граница красилась бы цветом чата, в который текст не уйдёт.
		if index := indexOfChatCard(m.source, m.focusedZoom().chatID); index >= 0 {
			return m.source[index].accent()
		}
		return PalettePersonal
	}
	if len(m.cards) == 0 {
		return PalettePersonal // пустая стена: отправлять некуда
	}
	return m.cards[clampWallCursor(m.cursor, len(m.cards))].accent()
}

// renderInputZone — строки блока ввода: подсказка, строка контекста ответа (только
// когда ответ выбран) и само поле, все с левой границей ┃ и боковыми полями
// экрана. Блок растёт вверх, но всегда прижат к разделителю над нижней строкой.
// fieldHeight — сколько строк отведено самому полю; строка подсказок и строка
// ответа идут над ним и в это число не входят.
// wallNoticeTypeToSend — сообщение в строке над полем, когда человек нажал Enter
// на ленте с ПУСТЫМ полем: фокус перешёл в открытый чат, но отправлять нечего.
// Без подсказки это выглядело бы как «Enter сломался» — текст остаётся в поле,
// ничего не уходит.
//
// Формулировка отвечает на вопрос «что делать дальше», а не сообщает о
// состоянии: человек уже в чате, ему нужен следующий шаг.
const wallNoticeTypeToSend = "Введите текст и нажмите enter для отправки"

// hint — подсказки (собранные из конфигурации, см. Model.hintPairs): сюда они
// приходят ПАРАМИ, а не готовой строкой, потому что блок их раскрашивает —
// сочетание одним цветом, слово другим. Текстом сюда пришлось бы передавать уже
// раскрашенную строку, и блок ввода знал бы про цвета подсказок стены.
// accent — цвет левой границы ┃, приходит от вызывающего: он зависит от цели
// ввода (см. inputTargetAccent), а блок ввода о цели ничего не знает. Красится
// только граница — фон панели остаётся своим.
// truncateHint — обрезка строки подсказок по ширине, с многоточием и ПО ЦЕЛЫМ
// парам.
//
// Общая truncateVisible режет по символам, и на строке вида «ctrl+j строка ·
// ctrl+p фильтр» это даёт обрывок вроде «ctrl+p фильтр · ctrl+j строк…»: человек
// читает несуществующее действие «строк» вместо того, чтобы понять, что дальше
// подсказка не поместилась. Подсказка состоит из пар через wallHintSeparator, и
// разрез между ними не оставляет обрывка: вместо половины пары остаётся целое
// число пар и многоточие.
//
// Многоточие и здесь не съедает всю строку (см. truncateVisible): если даже одна
// пара не влезает, обрезок бесполезен и показывается одно многоточие.
func truncateHint(text string, width int) string {
	if width <= 0 {
		return ""
	}
	if cellWidth(text) <= width {
		return text
	}
	pairs := strings.Split(text, wallHintSeparator)
	kept := 0
	used := 0
	for _, pair := range pairs {
		// Разделитель нужен только МЕЖДУ парами, поэтому цена первой пары в этой
		// строке — ноль, а каждой следующей — длина разделителя.
		price := cellWidth(pair)
		if kept > 0 {
			price += cellWidth(wallHintSeparator)
		}
		// Многоточие тоже занимает ячейку, и оно важнее, чем ещё одна пара.
		if used+price+cellWidth(ellipsis) > width {
			break
		}
		kept++
		used += price
	}
	if kept == 0 {
		return ellipsis
	}
	return strings.Join(pairs[:kept], wallHintSeparator) + ellipsis
}

// renderHint — строка подсказок, готовая к отрисовке: сочетания клавиш своим
// цветом (PaletteTextMuted), слова-описания своим (PaletteHintWord), разделитель
// — цветом слова. Обрезка по ширине идёт по границе пар (см. truncateHint) и
// делается ДО раскраски: цвета не занимают ячеек, а обрезать раскрашенную строку
// значило бы резать по SGR-последовательностям.
//
// Многоточие обрезки красится цветом слова: это не часть подсказки, а знак
// «дальше не влезло», и он должен читаться вместе с описаниями, а не висеть
// цветом соседней клавиши.
//
// background — фон панели: он передаётся, потому что строка подсказок рисуется
// ровно на нём, и отдельный стиль без него оставил бы дыру в фоне экрана.
func renderHint(pairs []hintPair, width int, background color.Color) string {
	if width <= 0 {
		return ""
	}
	// Обрезка идёт по ТЕКСТУ (hintText), а не по раскраске: SGR не занимают
	// ячеек, и резать раскрашенную строку значило бы резать по
	// escape-последовательностям. Дальше текст делится обратно на пары, и
	// раскраска идёт по порядку.
	truncated := truncateHint(hintText(pairs), width)
	// Многоточие обрезки отделяется ДО деления на пары: иначе оно стало бы
	// пятой «парой» и раскрасилось бы как подсказка, которой не является.
	tail := ""
	if cut, trimmed := strings.CutSuffix(truncated, ellipsis); trimmed {
		truncated, tail = cut, ellipsis
	}
	// Деление на пары обязано дать ровно столько же частей, сколько их было до
	// обрезки: обрезка не переставляет и не теряет разделителей, поэтому здесь
	// проверять нечего, а вот несовпадение длин означало бы, что подпись в
	// отрисовке разъехалась с реально нарисованным числом пар.
	visible := strings.Split(truncated, wallHintSeparator)
	if len(visible) > len(pairs) {
		visible = visible[:len(pairs)]
	}

	rendered := make([]string, 0, len(visible)*3+1)
	for index := range visible {
		if index > 0 {
			rendered = append(rendered, hintSeparator(background))
		}
		pair := pairs[index]
		rendered = append(rendered,
			foregroundBackgroundStyle(PaletteHintKey, background).Render(pair.key),
			hintSpace(background),
			foregroundBackgroundStyle(PaletteHintWord, background).Render(pair.word),
		)
	}
	if tail != "" {
		rendered = append(rendered, foregroundBackgroundStyle(PaletteHintWord, background).Render(tail))
	}
	return strings.Join(rendered, "")
}

// hintPrefixStyled — отступ в одну ячейку слева от подсказок, на фоне панели и
// цветом описаний. Отдельным стилем, а не сложением с renderHint, потому что
// фон панели обязателен: без него ячейка отступа уехала бы на фон терминала,
// и строка подсказок начиналась бы с тёмного пикселя на тёмном.
func hintPrefixStyled(background color.Color) string {
	return foregroundBackgroundStyle(PaletteHintWord, background).Render(" ")
}

// hintSpace — пробел ВНУТРИ пары, между сочетанием и его словом. Отдельный
// хелпер, а не hintSeparator, потому что это разные знаки: внутри пары это
// пробел, а между парами — « · ». Одним и тем же знаком строка читалась бы как
// «ctrl+p · фильтр · ctrl+j · строка», то есть как восемь отдельных подсказок
// вместо четырёх.
func hintSpace(background color.Color) string {
	return foregroundBackgroundStyle(PaletteHintWord, background).Render(" ")
}

// hintSeparator — разделитель « · » между парами подсказок, цветом описаний.
// Отдельный хелпер, а не константа из трёх символов: красить его надо тем же
// цветом, что и слова, иначе строка распадается на пары, разделённые чем-то
// третьим, и перестаёт читаться как один список.
func hintSeparator(background color.Color) string {
	return foregroundBackgroundStyle(PaletteHintWord, background).Render(wallHintSeparator)
}

func renderInputZone(input textarea.Model, width, fieldHeight int, replyLabel string, hint []hintPair, accent color.Color) []string {
	if width <= 0 || fieldHeight <= 0 {
		return nil
	}
	background := PaletteBackgroundPanel
	margin := renderedFill(cardMarginH, PaletteBackgroundMain)
	border := foregroundBackgroundStyle(accent, background).Render("┃")

	// Один пробел слева — строка подсказок иначе начиналась бы вплотную к левой
	// границе ┃, без единого отступа (в отличие от самого поля ниже, у которого
	// есть inputPaddingH). Ширина префикса считается из него самого, а не
	// зашивается числом — тем же приёмом, что и у префикса ответа ниже, где
	// число «3» означало бы «знак ответа одноклеточный» и разъехалось бы с ним
	// бесшумно.
	// Один пробел слева — строка подсказок иначе начиналась бы вплотную к левой
	// границе ┃, без единого отступа (в отличие от самого поля ниже, у которого
	// есть inputPaddingH). Пробел рисуется цветом описаний, а не отдельным
	// стилем: он часть той же строки, и «голый» он выглядел бы другим фоном
	// на экране там, где человек читает одну непрерывную полосу.
	hintPrefix := " "
	hints := hintPrefixStyled(background) + renderHint(hint, max(0, width-cellWidth(hintPrefix)), background)
	field := lipgloss.NewStyle().Width(width).MaxWidth(width).Padding(0, inputPaddingH).
		Background(background).Render(strings.TrimSuffix(input.View(), "\n"))

	// Строка ответа рисуется тем же приёмом, что и подсказка: тот же фон панели,
	// та же левая граница, и она тоже обрезается по ширине поля. Она не часть
	// значения поля и в отправку не попадает.
	//
	// Цвет — PaletteTextMuted, а не приглушённый (#555555), по прямому слову
	// человека (2026-10-01): строка ответа сообщает, ЧТО человек сейчас отвечает,
	// и она обязана читаться так же ярко, как сочетания клавиш в подсказке под
	// ней. На #555555 она тонула в общей серости панели, и ответ можно было
	// отправить, не заметив, на что.
	replyRows := 0
	if replyLabel != "" {
		replyRows = 1
	}
	replyPrefix := " " + wallReplyMarker + " "
	reply := foregroundBackgroundStyle(PaletteTextMuted, background).
		Render(replyPrefix + truncateVisible(replyLabel, max(0, width-cellWidth(replyPrefix))))

	total := fieldHeight + 1 + replyRows
	lines := make([]string, 0, total)
	appendLine := func(content string) {
		lines = append(lines, margin+border+fitLine(content, width, background)+margin)
	}
	appendLine(hints)
	if replyRows == 1 {
		appendLine(reply)
	}
	for _, line := range splitLines(field) {
		if len(lines) >= total {
			break
		}
		appendLine(line)
	}
	// Поле ужимается до отведённой высоты: если виджет вышел длиннее (например,
	// после ресайза терминала в узкий), лишние строки просто не рисуются.
	for len(lines) < total {
		lines = append(lines, margin+border+renderedFill(width, background)+margin)
	}
	return lines
}
