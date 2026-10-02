package tgwall

import (
	"context"
	"strings"

	tea "charm.land/bubbletea/v2"

	"telecli/internal/auth"
)

// Отправка сообщений стеной. Механика целиком перенесена из ленты tgcli
// (internal/tgclitui/filter.go: sendMessageCmd/applySendMessage): запросы
// sendMessage, разбор ответа и обе защиты от гонок «человек уже начал печатать
// следующее» и «человек уже выбрал другую цель ответа» переписаны там давно и
// годами работают. Здесь их проще, чем у ленты: у стены нет «прилипшего» чата и
// черновика в поле, есть только курсор и цель ответа.

// wallReplyPreviewWidth — сколько символов текста сообщения влезает в строку
// контекста ответа. Обрезка здесь, а не по ширине терминала: подпись цели должна
// оставаться одинаковой на любом экране и не пересчитываться на каждый кадр.
const wallReplyPreviewWidth = 40

// isComposing — человек сейчас взаимодействует с конкретной карточкой: либо
// набирает текст в поле, либо уже зафиксировал цель ответа по Ctrl+R (даже если
// сам текст ещё не начал печатать — нажатие Ctrl+R уже выбор). В обоих случаях
// живой апдейт стены (applyWallMessageUpdate) не имеет права молча увести
// курсор на новое сообщение: это ровно то сообщение/чат, кому сейчас адресован
// набираемый текст, и Enter, нажатый по инерции, должен уйти туда же, куда
// человек смотрел, когда начал печатать, а не туда, куда курсор случайно
// перескочил.
func (m Model) isComposing() bool {
	if m.replyTarget != nil {
		return true
	}
	return m.input != nil && strings.TrimSpace(m.input.Value()) != ""
}

// replyTarget — ответ на карточку, выбранную по Ctrl+R. nil — ответа нет.
//
// Снимок адреса, а не указатель на карточку: карточка из потока уходит при
// повторном снимке или перестановке вставок, и ответ на сообщение, которого на
// стене уже нет, должен уйти туда же, куда человек его назначил. Готовая строка
// Label считается один раз, здесь: пересчитывать её на каждом кадре незачем, а
// «цель едет за курсором» — ровно тот баг, ради которого снимок и заводится.
type replyTarget struct {
	ChatID    int64
	MessageID int64
	Label     string
}

// wallSendMessageMsg — ответ на команду отправки. Контракт полей тот же, что у
// sendMessageMsg в internal/tgclitui: без него обработчик не отличил бы «отправка
// на A» от «отправка на B», а именно это различение защищает от двух гонок.
type wallSendMessageMsg struct {
	chatID           int64
	replyToMessageID int64
	inputValue       string
	message          auth.Message
	err              error
}

// sendWallMessageCmd — отправка текста в чат, обычная или ответом. Нулевой
// replyToMessageID означает обычную отправку (ровно так же, как у auth.SendMessage
// и tgclitui).
func (m Model) sendWallMessageCmd(chatID, replyToMessageID int64, text string) tea.Cmd {
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client := m.client
	return func() tea.Msg {
		if client == nil {
			return wallSendMessageMsg{chatID: chatID, replyToMessageID: replyToMessageID, inputValue: text, err: errNoTDLibClient}
		}
		var message auth.Message
		var err error
		if replyToMessageID != 0 {
			message, err = auth.SendMessageReply(ctx, client, chatID, text, replyToMessageID)
		} else {
			message, err = auth.SendMessage(ctx, client, chatID, text)
		}
		return wallSendMessageMsg{chatID: chatID, replyToMessageID: replyToMessageID, inputValue: text, message: message, err: err}
	}
}

// applyWallSendMessage — ответ на отправку.
//
// Поле уже очищено оптимистично в submitInput (см. там) — здесь его не трогаем
// на успехе. На сбое возвращаем исходный текст, но только если поле всё ещё
// пустое: если человек уже начал печатать заново, пока ждали ответ сети,
// вписывать туда чужой (уже неактуальный) текст поверх нельзя — это стёрло бы
// то, что он печатает СЕЙЧАС, тем же способом, каким раньше грозила
// немедленная очистка не по адресу.
//
// Отдельная гонка защищена так же, как и раньше: пока ждали ответ на ОДИН
// ответ, человек уже нажал Ctrl+R на ДРУГУЮ карточку — нельзя погасить эту
// новую, ещё не отправленную цель.
func (m Model) applyWallSendMessage(msg wallSendMessageMsg) Model {
	if msg.err != nil {
		// Ошибка отправки: отдельного сообщения об ошибке у стены нет (в отличие
		// от tgclitui) — решение оркестратора, лишний ради одного этого случая
		// индикатор сейчас не нужен.
		if m.input != nil && m.input.Value() == "" {
			m.input.SetValue(msg.inputValue)
			m.applyLayout()
		}
		return m
	}
	if m.replyTarget != nil && m.replyTarget.ChatID == msg.chatID && m.replyTarget.MessageID == msg.replyToMessageID {
		m.replyTarget = nil
	}
	// Вставка через уже существующий путь живых сообщений (задачи 0150/0152): своей
	// отправленной карточки у чата на стене, как правило, ещё нет, поэтому она
	// добавится; если карточка уже есть (тот же чат успел прислать что-то своё),
	// она заменится — на стене всё равно одна карточка на источник. Повторную
	// доставку того же сообщения через updateNewMessage гасит messageIsNewer.
	return m.applyWallMessageUpdate(msg.chatID, msg.message)
}

// startReply — вход в режим ответа на сообщение под курсором.
//
// Откуда берётся сообщение — переписка или карточка стены — решает одно условие
// zoomHasFocus: открыта ли переписка и в фокусе ли она. Это то же условие, что
// решает, куда уйдёт текст по Enter (см. submitInput), и то же, что двигают
// стрелки: «что человек сейчас видит и о чём пишет» должно быть одним и тем же
// решением во всех трёх местах, а не тремя независимыми проверками.
//
// Курсор дальше свободно ходит стрелками, а replyTarget не пересчитывается:
// человек ответил на конкретное сообщение, и «цель едет за курсором» увело бы
// ответ туда, куда он не указывал. Зафиксировано решением человека.
func (m Model) startReply() Model {
	if m.zoomHasFocus() {
		message, ok := m.selectedWallZoomMessage()
		if !ok {
			return m // отвечать не на что
		}
		// Адресат — из ПАНЕЛИ В ФОКУСЕ: при двух панелях иначе ответ ушёл бы в
		// первый чат, а подпись показала бы чужое название.
		zoom := m.focusedZoom()
		m.replyTarget = &replyTarget{
			ChatID:    zoom.chatID,
			MessageID: message.ID,
			Label:     zoomReplyLabel(zoom, message),
		}
		return m
	}
	if len(m.cards) == 0 {
		return m // отвечать не на что
	}
	target := m.cards[clampWallCursor(m.cursor, len(m.cards))]
	m.replyTarget = &replyTarget{
		ChatID:    target.ChatID,
		MessageID: target.MessageID,
		Label:     replyLabel(target),
	}
	return m
}

// replyLabel — строка «на что отвечаем» для строки над полем: заголовок карточки
// (он же — то, что человек видит на стене) и усечённый кусок текста сообщения.
// Усечение здесь общей truncateVisible, второй способ обрезки в проекте не
// заводится.
func replyLabel(target card) string {
	return replyPreviewLabel(target.title(), target.Text)
}

// replyPreviewLabel — общая сборка строки ответа по заголовку источника и тексту
// сообщения. Одна на оба случая (карточка стены и сообщение открытой переписки):
// формат «заголовок — «превью»» и его предел в wallReplyPreviewWidth были зашиты
// в двух файлах по разу, и правка одного из них тихо разъехала бы подписи.
//
// Без текста (у нетекстового сообщения превью пустое) остаётся один заголовок —
// добавлять после него разделитель и пустые кавычки нельзя, строка прочтется
// как оборванная.
func replyPreviewLabel(title, text string) string {
	preview := truncateVisible(flattenToSingleLine(text), wallReplyPreviewWidth)
	if preview == "" {
		return title
	}
	return title + " — \"" + preview + "\""
}

// submitInput — разбор Enter. Отправляет набранное в открытую переписку (когда
// фокус на ней) или в чат карточки под курсором, либо в зафиксированную цель
// ответа.
//
// Поле очищается СРАЗУ, оптимистично, а не по ответу сети: иначе, пока ждём
// ответ, дальнейшая печать ложится поверх ещё не стёртого отправленного текста
// и склеивается с ним в одну бессмысленную строку («первое»+«второе» =
// «первоевторое») — человек увидел бы это раньше, чем что-либо, вместе с тем,
// как его новый текст пропал бы при сбое отправки. Цель определяется ДО
// очистки: если отправлять некуда, поле остаётся как было — обнулять его
// раньше времени незачем, ответа сети всё равно не будет, чтобы его потом
// восстановить.
func (m Model) submitInput() (Model, tea.Cmd) {
	text := ""
	if m.input != nil {
		text = m.input.Value()
	}

	// Фокус на ЛЕНТЕ. Поведение Enter разное в простом и двойном режимах — это
	// решение человека (2026-10-01), и разница не в коде, а в том, что означает
	// «чат под курсором» при двух панелях.
	//
	// ПРОСТОЙ РЕЖИМ (одна панель):
	//   - пустое поле — выбирается чат, фокус остаётся на ленте, над полем на 5
	//     секунд появляется подсказка «Введите текст и нажмите enter…»;
	//   - есть текст — отправляется в чат под курсором, фокус остаётся на ленте.
	//
	// ДВОЙНОЙ РЕЖИМ (две панели): Enter не отправляет ничего — он переключает
	// свободную панель. Под курсором один чат, а на экране две переписки, и
	// «отправить в чат под курсором» значило бы отправить не туда, куда человек
	// смотрит. Отправка в двойном режиме — только с фокусом на панели (Tab).
	//
	// КОМПАКТНЫЙ РЕЖИМ (панелей нет вовсе) сюда не попадает: там стена и переписка
	// занимают весь экран по очереди, и отправка идёт туда, что человек видит.
	if m.wallPanelMode() && !m.focusPanel.isChatPanel() {
		if m.wallTwoPanelMode() {
			if len(m.cards) == 0 {
				return m, nil
			}
			target := m.cards[clampWallCursor(m.cursor, len(m.cards))]
			if target.ChatID == 0 {
				return m, nil
			}
			// Enter БЕЗУСЛОВНО меняет свободную и зафиксированную панели
			// местами (docs/tgwall-help.md). Проверки «чат под курсором уже
			// открыт» здесь быть не может: свободная панель следует за курсором
			// через syncWallZoomToCursor, поэтому к моменту нажатия чат под
			// курсором в ней уже есть, и такая проверка срабатывала бы всегда —
			// Enter стал бы мёртвой клавишей.
			m.previewPanel = otherChatPanel(m.previewPanel)
			return m.openWallCursorZoomIn(m.previewPanel)
		}
		if strings.TrimSpace(text) == "" {
			next, cmd := m.openWallCursorZoomIn(panelChat1)
			if next.panelZoom(panelChat1) == nil {
				return next, cmd // открывать нечего — подсказывать бессмысленно
			}
			notified, noticeCmd := next.ShowSystemNotice(wallNoticeTypeToSend)
			if cmd == nil {
				return notified, noticeCmd
			}
			return notified, tea.Batch(cmd, noticeCmd)
		}
		// Есть что отправлять — отправляем в чат под курсором и остаёмся на ленте;
		// цель разбирается в общей части ниже.
	}

	if strings.TrimSpace(text) == "" {
		// Пустое поле. Узкий терминал, переписка не открыта: Enter открывает
		// источник под курсором стены. Уже открытая переписка на Enter не
		// закрывается — закрывает её Esc, — иначе нажатие Enter ради отправки
		// мимо цели выкидывало бы человека из чата. В широком режиме переписка и
		// так всегда открыта по курсору, и повторное открытие было бы лишним
		// запросом в сеть, поэтому там это остаётся no-op. В режиме ответа пустой
		// Enter тоже ничего не делает: выйти из ответа — только Esc, иначе ответ
		// потерял бы цель, молча сбросив её.
		if m.zoom == nil && !m.wallPanelMode() {
			return m.openWallCursorZoom()
		}
		return m, nil
	}

	var chatID, replyToMessageID int64
	switch {
	case m.replyTarget != nil:
		chatID, replyToMessageID = m.replyTarget.ChatID, m.replyTarget.MessageID
	case m.zoomHasFocus():
		// Фокус на переписке — текст уходит в открытый чат, а НЕ в источник
		// карточки под курсором стены: в широком режиме они могут быть разными
		// (переписка открыта по тому источнику, на котором курсор стоял, когда
		// её открыли, а курсор с тех пор двигался по стене).
		chatID = m.focusedZoom().chatID
	case len(m.cards) > 0 && !m.wallTwoPanelMode():
		// Компактный режим с открытой лентой (панелей нет) ИЛИ простой режим с
		// фокусом на ленте: на экране одна переписка, и выделенная карточка — единственный
		// выбор чата. Enter с набранным текстом отправляет в неё (решения человека,
		// 2026-10-01, для обоих режимов).
		//
		// Двойной режим сюда НЕ входит: там на экране две переписки, и отправка по
		// Enter на ленте была бы отправкой не туда (см. выше).
		chatID = m.cards[clampWallCursor(m.cursor, len(m.cards))].ChatID
	default:
		return m, nil // отправлять некуда — поле не трогаем
	}

	if m.input != nil {
		m.input.SetValue("")
		m.applyLayout()
	}
	return m, m.sendWallMessageCmd(chatID, replyToMessageID, text)
}

// replyRows — сколько строк экрана занимает строка контекста ответа: одна, пока
// ответ выбран, ноль иначе.
//
// Единый хелпер ровно потому, что высоту стены считают ДВА места (wallHeight и
// ceiling в applyLayout), и две разные формулы разъехались бы на одну строку:
// стена либо налезла бы на разделитель, либо оставила бы дыру под полем.
func (m Model) replyRows() int {
	if m.replyTarget != nil {
		return 1
	}
	return 0
}
