package tgwall

import (
	"context"
	"slices"

	tea "charm.land/bubbletea/v2"

	"telecli/internal/auth"
)

// Живые удаления на стене. Механизм перенесён из ленты tgcli
// (internal/tgclitui/live.go: waitForDeleteMessagesUpdate), а отличается от него
// только применением.
//
// Инвариант стены: на источник (чат, канал, личный диалог) показывается РОВНО
// одна карточка — его последнее сообщение (см. wallSnapshotMessagesPerChat).
// Поэтому удаление последнего сообщения источника не убирает источник, а
// обнажает предыдущее: карточка остаётся на своём месте и показывает то, что
// стало последним. Снимать её можно только когда у источника не осталось
// сообщений вовсе.

// wallDeleteUpdateMsg — один апдейт из client.DeleteMessagesUpdates(): из чата
// исчезли сообщения. Контракт полей тот же, что у wallMessageUpdateMsg, но с
// двумя отличиями: список ids здесь сразу множественный (один апдейт приносит
// все удалённые сообщения сразу), а вместо поля valid — ok.
//
// ok == false — нераспознанный апдейт (молча пропускается, но переподписка
// обязательна), closed == true — единственный случай, когда переподписываться
// не нужно.
type wallDeleteUpdateMsg struct {
	closed     bool
	ok         bool
	chatID     int64
	messageIDs []int64
}

// waitForWallDeleteUpdate блокируется на одном updateDeleteMessages. Скелет один
// в один с waitForWallMessageUpdate и waitForWallUnreadUpdate: подписка
// одноразовая, каждый вызов tea.Cmd ждёт ровно один апдейт, поэтому обработчик
// в Update обязан вернуть новый вызов после разбора.
//
// Разбор — готовый auth.ParseDeleteMessagesUpdate: он уже написан, документирован
// и покрыт тестами (в internal/auth), а своя копия разбора разошлась бы с ним на
// первом же неучтённом поле TDLib.
func (m Model) waitForWallDeleteUpdate() tea.Cmd {
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client := m.client
	if client == nil {
		return func() tea.Msg { return wallDeleteUpdateMsg{closed: true} }
	}
	ch := client.DeleteMessagesUpdates()
	if ch == nil {
		return func() tea.Msg { return wallDeleteUpdateMsg{closed: true} }
	}
	return func() tea.Msg {
		select {
		case update, ok := <-ch:
			if !ok {
				return wallDeleteUpdateMsg{closed: true}
			}
			chatID, messageIDs, valid := auth.ParseDeleteMessagesUpdate(update)
			return wallDeleteUpdateMsg{chatID: chatID, messageIDs: messageIDs, ok: valid}
		case <-ctx.Done():
			return wallDeleteUpdateMsg{closed: true}
		}
	}
}

// applyWallDeleteUpdate — удалённые сообщения исчезают из открытой переписки, а
// карточка источника на стене не снимается: вместо удалённого сообщения на ней
// должно появиться новое последнее.
//
// Перезапрос точечный, только по этому чату (те же два запроса и тот же предел
// в одно сообщение, что у снимка стены — см. loadWallMessages). Снимок стены
// целиком не перезагружается: это сотни запросов, а задача касается одного
// источника.
//
// Апдейт, удаливший не то сообщение, что показано на карточке, ни к чему не
// приводится: удалённого на стене и так нет, перезапрашивать нечего. Сюда же
// попадают и повторное применение того же апдейта, и его приход после уже
// применённого своего удаления — карточки с таким id на стене уже не осталось, и
// оба случая обязаны молча пройти (идемпотентность обоих путей удаления).
//
// Переподписка возвращается всегда, кроме закрытого канала: апдейт, который не
// вызвал перезапроса, тоже должен продолжить приходить.
func (m Model) applyWallDeleteUpdate(msg wallDeleteUpdateMsg) (Model, tea.Cmd) {
	if msg.closed {
		return m, nil
	}
	if !msg.ok {
		return m, m.waitForWallDeleteUpdate()
	}
	m.removeWallZoomMessages(msg.chatID, msg.messageIDs)
	// Ищем в m.source (полный поток), не в m.cards: источник может быть скрыт
	// текущим фильтром, но удаление его последнего сообщения всё равно обязано
	// запустить перезапрос — иначе после снятия галки источник вернулся бы на
	// стену с уже удалённым сообщением, ждущим следующего случайного апдейта.
	if indexOfDeletedCard(m.source, msg.chatID, msg.messageIDs) < 0 {
		return m, m.waitForWallDeleteUpdate()
	}
	return m, tea.Batch(m.waitForWallDeleteUpdate(), m.wallCardLatestCmd(msg.chatID, msg.messageIDs))
}

// wallCardLatestMsg — ответ на точечный перезапрос последнего сообщения
// источника.
//
// deletedIDs — те ids, удаление которых вызвало перезапрос. Они нужны в ответе
// для проверки, что он не опоздал: пока запрос был в полёте, карточку источника
// могли заменить новым сообщением или снять своим удалением (см.
// applyWallCardLatest).
//
// Пустой ответ — это message с нулевым ID: у настоящего сообщения id ненулевой
// всегда, и отдельный флаг «ответ пуст» отличался бы от него формой, а не
// смыслом (ровно так же проверяется и сообщение под курсором переписки, см.
// selectedWallZoomMessage).
type wallCardLatestMsg struct {
	chatID     int64
	deletedIDs []int64
	message    auth.Message
	err        error
}

// wallCardLatestCmd — последнее сообщение одного источника. Ровно те два
// запроса, что делает снимок стены (loadWallMessages), и тем же пределом
// wallSnapshotMessagesPerChat: на источник полагается одна карточка, и брать
// больше нечего.
//
// openChat идёт первым по той же причине, что и в снимке: без него чат мог быть
// ещё не досинхронизирован, и getChatHistory вернул бы не то, что на сервере.
// Его ошибка — не повод отказываться от истории: после него getChatHistory всё
// равно может ответить, и тогда перезапрос отработает вхолостую.
func (m Model) wallCardLatestCmd(chatID int64, deletedIDs []int64) tea.Cmd {
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client := m.client
	return func() tea.Msg {
		if client == nil {
			return wallCardLatestMsg{chatID: chatID, deletedIDs: deletedIDs, err: errNoTDLibClient}
		}
		_ = auth.OpenChat(ctx, client, chatID)
		messages, err := auth.GetMessages(ctx, client, chatID, wallSnapshotMessagesPerChat)
		if err != nil {
			return wallCardLatestMsg{chatID: chatID, deletedIDs: deletedIDs, err: err}
		}
		// auth.GetMessages отдаёт историю хронологически (старые сверху), то есть
		// последнее сообщение источника — последнее в срезе.
		var latest auth.Message
		if len(messages) > 0 {
			latest = messages[len(messages)-1]
		}
		return wallCardLatestMsg{chatID: chatID, deletedIDs: deletedIDs, message: latest}
	}
}

// applyWallCardLatest — ответ на перезапрос: карточка источника заменяется его
// новым последним сообщением, а если сообщений не осталось — снимается.
//
// Ошибка оставляет стену как была: индикатора ошибок у стены нет (то же
// решение, что у отправки, см. applyWallSendMessage), и выключать источник из-за
// неудачной попытки узнать, что за сообщением было, хуже, чем показать
// устаревшую карточку.
//
// Ответ на УЖЕ неактуальный запрос не применяется: пока он был в полёте, карточку
// источника могли заменить новым сообщением, снять своим удалением или убрать
// вовсе, и тогда ответ опоздал бы. Та же сверка делает идемпотентным повторное
// применение удаления: после первой замены на стене лежит другое сообщение, и
// пара (чат, сообщение) больше не совпадает.
func (m Model) applyWallCardLatest(msg wallCardLatestMsg) Model {
	if msg.err != nil {
		return m
	}
	// Ищем и правим m.source (полный поток), а не m.cards — тем же принципом,
	// что и остальные пути, меняющие карточки источника (applyWallMessageUpdate,
	// dropWallCard): источник мог быть скрыт фильтром на момент ответа, и правка
	// одного лишь видимого списка потерялась бы при следующей же пересборке.
	index := indexOfDeletedCard(m.source, msg.chatID, msg.deletedIDs)
	if index < 0 {
		// Ответ опоздал: карточки с удалённым сообщением в потоке уже нет.
		return m
	}
	if msg.message.ID == 0 {
		// У источника не осталось сообщений — снимать карточку правильно, это
		// легитимный случай «источник опустел» (на источник полагается одна
		// карточка, и показывать больше нечего).
		return m.dropWallCard(index)
	}
	chat, known := m.chatsByID[msg.chatID]
	if !known {
		// Чата нет в снимке, а карточка собирается из чата (заголовок, тип, тег) —
		// собирать нечего. Молча, ровно как у живых сообщений (живость списка
		// чатов — отдельная работа).
		return m
	}

	newCard := wallCard(chat, msg.message)
	// Счётчик непрочитанных и сведения об источнике переносятся со старой
	// карточки — тем же приёмом и по той же причине, что в insertWallSourceCard:
	// wallCard строит карточку из снимка чата, где их нет вовсе, а без переноса
	// они мигнули бы к пустому/снимочному значению при каждой замене карточки.
	existing := m.source[index]
	newCard.UnreadCount = existing.UnreadCount
	newCard.MemberCount = existing.MemberCount
	newCard.Status = existing.Status

	source := removeCard(m.source, index)
	// Место в потоке находит insertSortedCard — по дате, тем же вставщиком, что и
	// у живых сообщений: порядок карточек по дате инвариант стены, и дописывание
	// в конец увезло бы заменённую карточку в конец потока, мимо остальных.
	// Возврат -1 (дубль по паре чат+сообщение) здесь невозможен: карточка этого
	// источника только что убрана, а совпасть ей с новой парой неоткуда, поэтому
	// позиция и игнорируется.
	_, source = insertSortedCard(source, newCard)
	m.source = source
	// Курсор/окно — забота rebuildVisible(false), она же держит курсор на том же
	// источнике после перестройки (см. dropWallCard).
	return m.rebuildVisible(false)
}

// indexOfDeletedCard — индекс карточки, которую удалил апдейт, -1 если такой на
// стене нет.
//
// Именно пара (чат, сообщение), а не один чат: удалённым могло быть не то
// сообщение, что показано на карточке, а что-то из истории источника (тогда
// перезапрашивать нечего), либо наоборот — удалили ровно то, что на ней
// показано (тогда надо).
func indexOfDeletedCard(cards []card, chatID int64, deletedIDs []int64) int {
	for index, item := range cards {
		if item.ChatID != chatID {
			continue
		}
		if slices.Contains(deletedIDs, item.MessageID) {
			return index
		}
	}
	return -1
}

// removeWallZoomMessages — выбросить из открытой переписки сообщения этого чата с
// удалёнными id.
//
// Переписка рисуется из собственного списка, а не из карточек стены, и без этого
// удалённое сообщение висело бы в ней до переоткрытия. Список приходит разом по
// всем удалённым id, а не по одному: один апдейт удаляет их пачкой, и на каждый
// перезапрашивать историю было бы незачем (в переписке нужен только факт
// исчезновения, новое сообщение придёт своим живым апдейтом).
//
// Курсор и окно зажимаются по новой длине списка — тем же приёмом, что при любом
// другом изменении списка переписки (applyWallZoomLoaded, moveWallZoomCursor).
func (m *Model) removeWallZoomMessages(chatID int64, deletedIDs []int64) {
	if m.zoom == nil || m.zoom.chatID != chatID || len(m.zoom.messages) == 0 {
		return
	}
	kept := make([]auth.Message, 0, len(m.zoom.messages))
	for _, message := range m.zoom.messages {
		if slices.Contains(deletedIDs, message.ID) {
			continue
		}
		kept = append(kept, message)
	}
	if len(kept) == len(m.zoom.messages) {
		// Ни одного удалённого в переписке не было (например, апдейт по чужому
		// источнику) — список и не трогаем.
		return
	}
	m.zoom.messages = kept
	m.zoom.cursor = clampWallCursor(m.zoom.cursor, len(kept))
	m.zoom.scrollTop = clampScrollToCursor(zoomCardHeight, m.zoomCardsOf(m.zoom, kept), m.zoom.scrollTop, m.zoom.cursor, m.zoomListWidth(), m.wallZoomListHeight())
}
