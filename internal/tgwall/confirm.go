package tgwall

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/auth"
)

// Модалка подтверждения удаления. Механика целиком перенесена из ленты tgcli
// (internal/tgclitui/confirm.go): вопрос с коротким превью сообщения, блок по
// центру экрана, наложение на готовые строки, Enter — подтвердить, Esc —
// отменить. Перенесено и поведение: пока модалка открыта, весь остальной ввод
// заблокирован, а подтверждённое удаление убирает карточку сразу, не дожидаясь
// следующего снимка стены.
//
// Два отличия стены, оба вынужденные и оба по причине, а не по вкусу: свои
// цвета (палитра стены, см. palette.go — своих PaletteAccent у неё нет) и
// выбор цели удаления по тому, что сейчас в фокусе — переписка или стена (см.
// openConfirmDeleteMessage).

const (
	confirmModalPaddingH = 2
	confirmModalPaddingV = 1
	// Ширина блока не растёт бесконечно: на очень широком терминале модалка
	// должна остаться компактным блоком, а не растянутой во всю ширину полосой.
	confirmModalMaxWidth = 60
	// Превью текста сообщения в вопросе — короткое, чтобы строка вопроса
	// помещалась в блок даже на узком терминале.
	confirmPreviewRunes = 48
	// confirmDeleteQuestion — вопрос модалки. Отдельной константой, потому что
	// на неё ссылаются и примитив, и его тесты.
	confirmDeleteQuestion = "Удалить сообщение?"

	// Куски подсказки модалки и строки выбора. Все три строки («Да / Нет»,
	// «<клавиша> — да», «<клавиша> — нет») печатаются дважды: один раз
	// считается их ширина для блока, второй — рисуется. Собранные из одних
	// констант, они совпадают по построению, а не по совпадению строк в коде:
	// разъехавшаяся ширина уехала бы вместе с центрированием.
	confirmChoiceYes     = "Да"
	confirmChoiceNo      = " / Нет"
	confirmHintYesSuffix = " — да"
	confirmHintNoSuffix  = " — нет"
)

// confirmModalKeys — подписи клавиш в подсказке модалки. Модалка реагирует на
// keySelect и keyBack, поэтому и показывать должна их, а не зашитые «enter»/«esc»:
// правка keybindings.toml иначе меняла бы поведение, а подсказка продолжала бы
// врать (ровно как у ленты, см. internal/tgclitui/confirm.go).
type confirmModalKeys struct {
	yes string
	no  string
}

func (m Model) confirmModalKeys() confirmModalKeys {
	return confirmModalKeys{yes: m.keymap.label(keySelect), no: m.keymap.label(keyBack)}
}

// hint — подсказка по клавишам целиком, какой её видит человек. Разделитель
// пар — тот же wallHintSeparator, что у строки подсказок под полем: правило
// одно, и зашивать его второй раз значило бы получить два разных оформления
// списка на одном экране.
func (k confirmModalKeys) hint() string {
	return k.yes + confirmHintYesSuffix + wallHintSeparator + k.no + confirmHintNoSuffix
}

// deleteMessageMsg — результат отправки deleteMessages в TDLib.
type deleteMessageMsg struct {
	chatID    int64
	messageID int64
	err       error
}

func (m Model) deleteMessageCmd(chatID, messageID int64) tea.Cmd {
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client := m.client
	return func() tea.Msg {
		if client == nil {
			return deleteMessageMsg{chatID: chatID, messageID: messageID, err: errNoTDLibClient}
		}
		err := auth.DeleteMessage(ctx, client, chatID, messageID)
		return deleteMessageMsg{chatID: chatID, messageID: messageID, err: err}
	}
}

// applyDeleteMessage убирает удалённое сообщение со стены сразу, не дожидаясь
// перезагрузки: TDLib подтвердил удаление, ждать следующего снимка ради уже
// известного результата незачем.
//
// Карточка ищется по паре (чат, сообщение), а не по одному чату: пока ждали
// ответ сети, тот же чат мог прислать своё новое сообщение, и его карточка
// встала бы на место удаляемой. Удалённое тогда на стене уже не лежит, и снимать
// с неё нужно нечего. По той же причине путь идемпотентен: повторное применение
// того же ответа (или его приход после уже применённого удаления) не находит
// своей карточки и молча ничего не делает.
//
// Ошибка оставляет стену как была: сообщение не исчезает само из-за неудачной
// попытки. Показывать саму ошибку негде — индикатора ошибок у стены нет, и
// заводить его ради одного этого случая не заводили даже для отправки (см.
// applyWallSendMessage).
func (m Model) applyDeleteMessage(msg deleteMessageMsg) Model {
	if msg.err != nil {
		return m
	}
	index := -1
	for position, item := range m.source {
		if item.ChatID == msg.chatID && item.MessageID == msg.messageID {
			index = position
			break
		}
	}
	if index < 0 {
		return m
	}
	m = m.dropWallCard(index)
	// Цель ответа на удалённое сообшение снимается: отвечать на сообщение,
	// которого больше нет, бессмысленно — TDLib такой запрос отвергнет, а стена
	// покажет ровно ничего. Черновик в поле при этом остаётся: его человек
	// напечатал сам (то же, что у tgclitui, см. removeFeedMessage).
	if m.replyTarget != nil && m.replyTarget.ChatID == msg.chatID && m.replyTarget.MessageID == msg.messageID {
		m.replyTarget = nil
		m.applyLayout()
	}
	return m
}

// openConfirmDeleteMessage готовит модалку по сообщению, которое человек сейчас
// видит выделенным.
//
// Откуда берётся сообщение — переписка или карточка стены — решает ровно то же
// условие zoomHasFocus, что и у Ctrl+R (см. startReply). На стене отдельного
// «выделенного сообщения» нет: карточка — это ровно одно, последнее сообщение
// источника (см. задачу 0152), поэтому там «что выделено» и есть карточка под
// курсором. В открытой переписке выбранное сообщение своё, и это не то же
// самое, что последнее сообщение источника: Delete обязан спрашивать про то,
// что человек видит под курсором переписки, а не про карточку стены на другом
// чате (в широком режиме переписка открыта по тому источнику, на котором стоял
// курсор, когда её открыли, и с тех пор курсор по стене мог уйти).
//
// Вопрос включает короткое превью текста сообщения: подтверждение необратимого
// действия должно называть, что именно удаляется, иначе «Да» вслепую.
func (m *Model) openConfirmDeleteMessage() {
	target, ok := m.deleteTarget()
	if !ok {
		return // удалять нечего
	}
	m.showConfirm = true
	m.confirmChatID = target.ChatID
	m.confirmMessageID = target.MessageID
	m.confirmPrompt = confirmDeletePrompt(target)
}

// deleteTarget — карточка того сообщения, которое удалит Delete, и признак, что
// оно вообще есть.
//
// Сообщение открытой переписки берётся тем же путём, каким оно рисуется:
// zoomCard. Своего разбора текста здесь не заводится по той же причине, по
// которой его нет у zoomReplyLabel: превью обязано называть тот самый текст,
// который человек видит под курсором, а не второй раз разобранный по-своему.
// Адрес проставляется здесь же — zoomCard работает только с сообщением, а
// вопросу модалки нужен чат, иначе удалилось бы не то.
func (m Model) deleteTarget() (card, bool) {
	if m.zoomHasFocus() {
		message, ok := m.selectedWallZoomMessage()
		if !ok {
			return card{}, false // переписка есть, а сообщений в ней нет
		}
		// Удаляемое — из ПАНЕЛИ В ФОКУСЕ: при двух панелях иначе вопрос модалки
		// показал бы сообщение из первого чата, а удалилось бы выбранное во
		// втором (или наоборот).
		zoom := m.focusedZoom()
		target := zoomCard(message, m.zoomLastReadOutbox(zoom))
		target.ChatID = zoom.chatID
		target.MessageID = message.ID
		return target, true
	}
	if len(m.cards) == 0 {
		return card{}, false // удалять нечего
	}
	return m.cards[clampWallCursor(m.cursor, len(m.cards))], true
}

func (m *Model) closeConfirm() {
	m.showConfirm = false
	m.confirmPrompt = ""
	m.confirmChatID = 0
	m.confirmMessageID = 0
}

// confirmDeletePrompt — вопрос модалки плюс превью текста сообщения. У
// нетекстовых сообщений card.Text уже несёт подпись («[фото]»), поэтому превью
// берётся из того же поля.
func confirmDeletePrompt(target card) string {
	preview := truncateRunes(target.Text, confirmPreviewRunes)
	if preview == "" {
		return confirmDeleteQuestion
	}
	return confirmDeleteQuestion + "\n" + preview
}

// updateConfirm — ввод модалки. Пока она открыта, весь остальной ввод
// заблокирован: подтверждение необратимого действия не должно срабатывать от
// нажатия, задуманного для стены или поля ввода.
//
// Клавиши те же, что и у стены в целом (keySelect/keyBack), а не отдельные для
// модалки: отдельный набор означал бы, что одинаковое действие («подтвердить» и
// «отправить» на Enter) настраивается двумя разными полями конфигурации.
func (m Model) updateConfirm(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.keymap.pressed(keyBack, msg) {
		m.closeConfirm()
		return m, nil
	}
	if m.keymap.pressed(keySelect, msg) {
		return m.confirmSubmit()
	}
	return m, nil
}

// confirmSubmit — единственное место, где видно, ЧТО человек подтвердил.
// Модалка закрывается до отправки запроса: ответ TDLib может задержаться, и всё
// это время вопрос на экране висел бы уже без причины.
func (m Model) confirmSubmit() (tea.Model, tea.Cmd) {
	chatID, messageID := m.confirmChatID, m.confirmMessageID
	m.closeConfirm()
	return m, m.deleteMessageCmd(chatID, messageID)
}

// confirmModalBlock — блок подтверждения границами внутри области: top — номер
// строки, с которой блок начинается, left — колонка. Нужны наложению, которое
// вклеивает блок в готовые строки экрана, не перерисовывая его целиком.
func confirmModalBlock(width, height int, prompt string, keys confirmModalKeys) (lines []string, top, left int) {
	if width <= 0 || height <= 0 {
		return nil, 0, 0
	}
	inner := confirmModalContentWidth(prompt, keys)
	if limit := width - 2*confirmModalPaddingH; inner > limit {
		inner = max(0, limit)
	}
	if inner <= 0 {
		return nil, 0, 0
	}

	padding := renderedFill(confirmModalPaddingH, PaletteBackgroundPanel)
	blank := renderedFill(inner+2*confirmModalPaddingH, PaletteBackgroundPanel)
	body := confirmModalContent(inner, prompt, keys)
	block := make([]string, 0, len(body)+2*confirmModalPaddingV)
	for range confirmModalPaddingV {
		block = append(block, blank)
	}
	for _, line := range body {
		block = append(block, padding+fitLine(line, inner, PaletteBackgroundPanel)+padding)
	}
	for range confirmModalPaddingV {
		block = append(block, blank)
	}

	boxWidth := cellWidth(block[0])
	top = max(0, (height-len(block))/2)
	left = max(0, (width-boxWidth)/2)
	return block, top, left
}

// confirmModalContent — строки содержимого: вопрос (может быть многострочным из-за
// превью), варианты «Да/Нет» и подсказка по клавишам. Выбор Да/Нет переключается
// только клавишами подтверждения и отмены: у действия, удаляющего данные, «отмена»
// не должна быть результатом одного нажатия, а дефолтный пункт иначе пришлось бы
// угадывать.
//
// Акцентного цвета у стены нет, поэтому роль акцента играет обычный текст, а
// приглушённым помечено всё, что не «Да» (тот же приём, что у выделения
// карточки стены, где выбранная строка — обычным цветом по PaletteText).
func confirmModalContent(width int, prompt string, keys confirmModalKeys) []string {
	accent := foregroundBackgroundStyle(PaletteText, PaletteBackgroundPanel)
	muted := foregroundBackgroundStyle(PaletteTextMuted, PaletteBackgroundPanel)
	fill := func(content string) string {
		return renderedFill(max(0, (width-cellWidth(content))/2), PaletteBackgroundPanel) + content
	}

	lines := make([]string, 0, 6)
	for _, line := range splitLines(prompt) {
		lines = append(lines, fill(accent.Render(truncateVisible(line, width))))
	}
	lines = append(lines, renderedFill(width, PaletteBackgroundPanel))
	lines = append(lines, fill(accent.Render(confirmChoiceYes)+muted.Render(confirmChoiceNo)))
	lines = append(lines, renderedFill(width, PaletteBackgroundPanel))
	// Подсказка по клавишам — теми же кусками, из которых собрана keys.hint(), по
	// которой считается ширина блока.
	lines = append(lines, fill(accent.Render(keys.yes)+
		muted.Render(confirmHintYesSuffix+wallHintSeparator)+
		accent.Render(keys.no)+
		muted.Render(confirmHintNoSuffix)))
	return lines
}

// confirmModalContentWidth — ширина СОДЕРЖИМОГО, которое блок просит у области:
// самая широкая строка вопроса, но не уже «Да / Нет» и подсказки по клавишам.
// Потолок confirmModalMaxWidth задаётся тут же обрезкой каждой строки вопроса:
// и ширина, и её реальный рендер тогда считаются по одному и тому же значению.
// Горизонтальные отступы добавляет confirmModalBlock — иначе они учитывались бы
// дважды.
func confirmModalContentWidth(prompt string, keys confirmModalKeys) int {
	inner := 0
	for _, line := range splitLines(prompt) {
		inner = max(inner, cellWidth(truncateVisible(line, confirmModalMaxWidth)))
	}
	inner = max(inner, cellWidth(confirmChoiceYes+confirmChoiceNo))
	inner = max(inner, cellWidth(keys.hint()))
	return inner
}

// overlayPasteLine вклеивает строку блока block в готовую строку экрана line
// начиная с колонки left. Префикс и суффикс берутся из исходной строки по
// видимым ячейкам, поэтому геометрия строки (а значит и всего экрана) не
// меняется.
//
// Общий помощник всех оверлеев стены (этот блок и меню фильтра источников): сам
// способ наложения — один, а копии разъехались бы при первой же правке.
func overlayPasteLine(line string, left int, block string) string {
	width := cellWidth(block)
	if width <= 0 || left < 0 {
		return line
	}
	lineWidth := cellWidth(line)
	prefix := ""
	if left > 0 {
		prefix = ansi.Cut(line, 0, min(left, lineWidth))
	}
	suffix := ""
	if end := left + width; end < lineWidth {
		suffix = ansi.Cut(line, end, lineWidth)
	}
	return prefix + block + suffix
}

// overlayConfirm вклеивает блок подтверждения по центру готового экрана. Экран
// приходит готовой строкой (renderScreen собирает его одним выражением из кусков
// разной высоты), поэтому наложение идёт по видимым ячейкам каждой строки и не
// трогает ни высоту, ни ширину экрана.
func (m Model) overlayConfirm(screen string) string {
	if !m.showConfirm || screen == "" || m.width <= 0 || m.height <= 0 {
		return screen
	}
	lines := splitLines(screen)
	block, top, left := confirmModalBlock(m.width, m.height, m.confirmPrompt, m.confirmModalKeys())
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

// truncateRunes обрезает текст по числу рун, ставя многоточие вместо хвоста.
// Именно рун, а не ячеек: кириллица в тексте сообщений — норма, и обрезанное
// превью не должно «съедать» ширину блока, оставляя справа пустоту. Перенос
// строки внутри текста схлопывается в пробел: превью — одна строка вопроса.
func truncateRunes(text string, limit int) string {
	runes := []rune(strings.ReplaceAll(text, "\n", " "))
	if limit <= 0 || len(runes) <= limit {
		return string(runes)
	}
	return strings.TrimSpace(string(runes[:limit])) + ellipsis
}
