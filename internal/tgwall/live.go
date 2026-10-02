package tgwall

import (
	"context"
	"sort"

	tea "charm.land/bubbletea/v2"

	"telecli/internal/auth"
)

// Живые сообщения стены. Механизм целиком перенесён из ленты tgcli
// (internal/tgclitui/live.go и insertFeedMessage в filter.go): TDLib и без
// повторных запросов шлёт updateNewMessage на каждое новое сообщение, разбор
// апдейта и канал в проекте уже есть, а опрашивать GetAllChats+GetMessages по
// таймеру значило бы изобретать заново то, что уже годами работает.

// wallMessageUpdateMsg — один апдейт из client.MessageUpdates(): новое сообщение.
// Контракт полей тот же, что у остальных живых сообщений в проекте.
type wallMessageUpdateMsg struct {
	closed  bool
	valid   bool
	chatID  int64
	message auth.Message
}

// waitForWallMessageUpdate блокируется на одном updateNewMessage. Подписка
// одноразовая, как и все waitFor* в проекте: каждый вызов tea.Cmd ждёт ровно
// один апдейт, поэтому обработчик в Update обязан вернуть новый вызов после
// разбора.
//
// Закрытый канал (или клиент, которого нет) — единственный случай, когда
// переподписка не нужна: канала, из которого пришёл бы следующий апдейт, уже не
// существует.
func (m Model) waitForWallMessageUpdate() tea.Cmd {
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client := m.client
	if client == nil {
		return func() tea.Msg { return wallMessageUpdateMsg{closed: true} }
	}
	ch := client.MessageUpdates()
	if ch == nil {
		return func() tea.Msg { return wallMessageUpdateMsg{closed: true} }
	}
	return func() tea.Msg {
		select {
		case update, ok := <-ch:
			if !ok {
				return wallMessageUpdateMsg{closed: true}
			}
			chatID, message, valid := auth.ParseNewMessageUpdate(ctx, client, update)
			return wallMessageUpdateMsg{chatID: chatID, message: message, valid: valid}
		case <-ctx.Done():
			return wallMessageUpdateMsg{closed: true}
		}
	}
}

// applyWallMessageUpdate — живое сообщение на стене, ничего не трогая в случае
// чата вне снимка. Порт insertFeedMessage (internal/tgclitui/filter.go) на модель
// стены с окном прокрутки, но с главным отличием: на стене РОВНО одна карточка
// на источник, поэтому апдейт по источнику, у которого карточка уже есть, не
// добавляет вторую, а ЗАМЕНЯЕТ содержимое существующей и переставляет её на
// новое место в потоке (по дате, то есть уезжает вниз).
//
// Правка идёт в m.source (полный поток), а видимый список пересобирается в
// rebuildVisible: карточка, скрытая фильтром, всё равно обязана попасть в полный
// поток, иначе снятие галки уже не воскресило бы её. Решение «следит ли человек
// за самым свежим» принимается ДО пересборки, по видимому списку, в котором он
// сейчас смотрит, — ровно как до появления фильтра.
func (m Model) applyWallMessageUpdate(chatID int64, message auth.Message) Model {
	chat, known := m.chatsByID[chatID]
	if !known {
		// Чат, которого не было в снимке, — это отдельная работа (живость списка
		// чатов, целый блок в tgclitui), а не «заодно к живым сообщениям».
		// Молча отбросить здесь ничего не теряет: если сообщение пришло раньше
		// снимка, то снимок сам запросит его как последнее сообщение чата.
		return m
	}

	newCard := wallCard(chat, message)

	// «Человек смотрит на самое свежее» — по ИНДЕКСУ курсора, а не по видимости
	// в окне. То же определение, что в tgclitui (m.feedCursor >= len(m.feed)-1):
	// оно уже проверено в бою, а перестановка карточки только что сдвинула
	// индексы, и «видно ли что-то у нижнего края» после неё означало бы совсем
	// другое, чем до неё.
	wasAtLatest := len(m.cards) == 0 || m.cursor >= len(m.cards)-1
	// Но не тогда, когда человек прямо сейчас с этой карточкой взаимодействует
	// (печатает ответ ей или уже зафиксировал её Ctrl+R как цель) — иначе
	// пришедшее сообщение молча уводит курсор на другой чат, и Enter,
	// нажатый по инерции, уходит не туда, куда человек смотрел секунду назад.
	// Найдено человеком вживую: Андрей пишет "привет" (карточка внизу, курсор на
	// ней), человек начинает набирать ответ — и тут учитель ребёнка присылает
	// своё сообщение, курсор перескакивает на него, а "здорова" улетает
	// учителю. Реальная, а не теоретическая цена ошибки.
	followLatest := wasAtLatest && !m.isComposing()

	source, changed := insertWallSourceCard(m.source, newCard)
	if !changed {
		// Повторная доставка того же сообщения или апдейт, пришедший не по
		// порядку: карточка источника уже показывает не меньшее. Видимый список
		// не трогаем — в нём ничего и не менялось.
		return m
	}
	m.source = source
	// Курсор после пересборки удерживается на ТОМ ЖЕ источнике, а не на прежнем
	// номере (см. rebuildVisible): карточка представляет источник, а не сообщение,
	// и её MessageID меняется при каждом апдейте, так что привязка к старой паре
	// (чат, сообщение) потеряла бы карточку ровно тогда, когда она обновилась.
	m = m.rebuildVisible(followLatest)
	return m
}

// insertWallSourceCard — новое сообщение источника в полный поток: карточка
// этого источника заменяется, а не добавляется второй.
//
// Место в потоке находит insertSortedCard — по дате, то есть новая карточка
// (или переставленная) уезжает вниз, и отдельная проверка «была ли она уже
// внизу» не нужна. Возврат -1 (дубль по паре чат+сообщение) здесь невозможен:
// карточка этого источника только что убрана, а совпасть ей с новой парой неоткуда,
// поэтому позиция и игнорируется.
//
// Возвращает поток и признак, что он изменился: повторная доставка того же
// сообщения и приход не по порядку оставляют поток как был, и пересобирать из
// него видимый список незачем.
func insertWallSourceCard(source []card, newCard card) ([]card, bool) {
	if existingIndex := indexOfChatCard(source, newCard.ChatID); existingIndex >= 0 {
		existing := source[existingIndex]
		if !messageIsNewer(newCard, existing) {
			return source, false
		}
		// Счётчик непрочитанных переносится со старой карточки: wallCard
		// строит новую из снимка чата (chatsByID), где UnreadCount — то, что
		// было на момент снимка, а не то, что уже показывает живой счётчик
		// (задача 0153, applyWallUnreadUpdate). Без переноса счётчик на кадр
		// мигнул бы к значению снимка при каждом новом сообщении источника,
		// пока не придёт следующее updateChatReadInbox.
		newCard.UnreadCount = existing.UnreadCount
		// Сведения об источнике переносятся по той же причине и тем же
		// способом, что и UnreadCount: они живут в кэше модели, синхронизируются
		// на existing отдельным путём (syncWallSourceInfo), а wallCard строит
		// карточку из снимка чата, где их нет вовсе. insertWallSourceCard — не
		// метод Model и кэшей не видит, поэтому берёт уже синхронизированные
		// значения с existing напрямую, а не считает их заново. Без переноса
		// счётчик участников или статус мигнули бы к пустому значению при
		// каждом новом сообщении источника.
		newCard.MemberCount = existing.MemberCount
		newCard.Status = existing.Status
		// Карточка источника на стене одна, поэтому новое сообщение не
		// ДОБАВЛЯЕТ вторую, а ЗАМЕНЯЕТ содержимое существующей.
		source = removeCard(source, existingIndex)
	}

	_, source = insertSortedCard(source, newCard)
	return source, true
}

// messageIsNewer — можно ли заменить существующую карточку источника на next.
// Нет — если next это тот же самый апдейт, что уже отражён (совпадает MessageID:
// повторная доставка того же сообщения, TDLib такое шлёт при переподключении), и
// нет — если next младше по дате существующей карточки (апдейт, пришедший не по
// порядку; на практике для одного чата TDLib шлёт updateNewMessage по порядку, но
// проверка дешёвая и не даёт состоянию поехать назад по времени).
func messageIsNewer(next, existing card) bool {
	if next.MessageID != 0 && next.MessageID == existing.MessageID {
		return false
	}
	return next.Date >= existing.Date
}

// dropWallCard — снять карточку с ПОЛНОГО потока (m.source) по индексу и
// пересобрать видимый список. Общий шаг обоих путей удаления (своего по ответу
// deleteMessages и чужого по updateDeleteMessages): удаляем из source, а не из
// cards — карточка, скрытая фильтром, всё равно обязана исчезнуть из полного
// потока, иначе снятие галки вернула бы её на стену уже удалённой.
//
// Курсор/окно после пересборки — забота rebuildVisible(false): он уже ищет
// карточку ТОГО ЖЕ источника, на котором стоял курсор (по ChatID, до
// пересборки), и держит его там же, если источник остался, — ровно то, что
// раньше отдельно решала keepCursorOnSource. Отдельной функции под это здесь
// больше нет: она дублировала бы то, что rebuildVisible уже делает сама для
// любой пересборки видимого списка, не только для удаления.
func (m Model) dropWallCard(index int) Model {
	m.source = removeCard(m.source, index)
	return m.rebuildVisible(false)
}

// indexOfChatCard — индекс карточки этого источника на стене, -1 если такого
// источника на стене нет (например, у него была пустая история при загрузке, и
// это первое сообщение в нём вообще).
func indexOfChatCard(cards []card, chatID int64) int {
	for index, item := range cards {
		if item.ChatID == chatID {
			return index
		}
	}
	return -1
}

// removeCard — карточка убрана по индексу. Тройной срез (cards[:index:index]), а не
// cards[:index]: иначе append внутри insertSortedCard дописал бы новую карточку в
// общий, невидимый снаружи запас ёмкости и переписал хвост чужого среза —
// классическая ловушка Go при работе с sub-slice.
func removeCard(cards []card, index int) []card {
	return append(cards[:index:index], cards[index+1:]...)
}

// insertSortedCard — вставка карточки в поток, отсортированный по дате, с
// дедупликацией по паре (чат, id). Порт insertSortedFeedMessage
// (internal/tgclitui/filter.go); предела длины здесь нет намеренно: снимок
// стены тоже ничем не ограничен, и вводить его только для живых сообщений
// было бы непоследовательно.
//
// Нулевой id дублем не считается: на живых данных он невозможен, а в тестовых
// карточках без него иначе половина потока сошлась бы в одно «сообщение».
//
// С момента «одна карточка на источник» живой путь сюда приходит уже без
// карточки этого чата (она убрана перед вставкой), так что своя дедупликация
// на живом апдейте больше не срабатывает — но проверять её не зря: она
// защищает любой другой вызов с готовым набором карточек.
func insertSortedCard(cards []card, next card) (int, []card) {
	if next.MessageID != 0 {
		for _, existing := range cards {
			if existing.ChatID == next.ChatID && existing.MessageID == next.MessageID {
				return -1, cards
			}
		}
	}
	// sort.Search находит первый элемент ПОЗЖЕ вставляемого: поток стены
	// отсортирован по возрастанию даты, старые сверху.
	position := sort.Search(len(cards), func(index int) bool {
		return cards[index].Date > next.Date
	})
	cards = append(cards, card{})
	copy(cards[position+1:], cards[position:])
	cards[position] = next
	return position, cards
}

// chatsByID — чаты снимка по id. Обратный индекс строится один раз на весь
// поток живых апдейтов, а не ищется перебором на каждом сообщении.
func chatsByID(chats []auth.Chat) map[int64]auth.Chat {
	byID := make(map[int64]auth.Chat, len(chats))
	for _, chat := range chats {
		byID[chat.ID] = chat
	}
	return byID
}
