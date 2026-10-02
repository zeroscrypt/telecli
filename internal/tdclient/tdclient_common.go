package tdclient

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Файл с общей логикой клиента, без cgo-блока. Функции C API TDLib
// (tdCreateClientID/tdReceive/tdSend/tdExecute) определены в tdclient.go
// (!tdjson_static, динамическая линковка) и tdclient_static.go
// (tdjson_static, статическая) — этот файл работает с любым из двух вариантов.

type Client struct {
	id                    int32
	pending               map[string]chan map[string]interface{}
	pendingMu             sync.Mutex
	authUpdates           chan map[string]interface{}
	messageUpdates        chan map[string]interface{}
	sendStatusUpdates     chan map[string]interface{}
	chatFolderUpdates     chan map[string]interface{}
	chatReadInboxUpdates  chan map[string]interface{}
	chatReadOutboxUpdates chan map[string]interface{}
	chatTitleUpdates      chan map[string]interface{}
	// chatNotificationSettingsUpdates приходит редко (человек заглушил чат
	// руками), поэтому буфер как у chatFolderUpdates, а не у chatTitleUpdates.
	chatNotificationSettingsUpdates chan map[string]interface{}
	unreadCountUpdates              chan map[string]interface{}
	unreadChatCountUpdates          chan map[string]interface{}
	messageInteractionUpdates       chan map[string]interface{}
	unreadReactionUpdates           chan map[string]interface{}
	deleteMessagesUpdates           chan map[string]interface{}
	messageContentUpdates           chan map[string]interface{}
	// newChatUpdates — updateNewChat: TDLib поднял новый чат. На старте
	// прилетает пачкой на все чаты, которые он поднял из локальной базы, но
	// обработка сводится к «уже есть в списке ⇒ ничего не делать», поэтому
	// буфер как у messageUpdates, а шторм гасится на стороне TUI.
	newChatUpdates chan map[string]interface{}
	// chatAddedToListUpdates/chatRemovedFromListUpdates — членство чата в
	// списке. События редкие (перенос в архив, возврат из архива), но на старте
	// TDLib проигрывает их вместе с остальным списком, поэтому буфер как у
	// chatReadInboxUpdates, а не как у редких агрегатов.
	chatAddedToListUpdates     chan map[string]interface{}
	chatRemovedFromListUpdates chan map[string]interface{}
	// chatPositionUpdates — ОДИН канал на три апдейта позиции
	// (updateChatPosition/updateChatLastMessage/updateChatDraftMessage): все
	// трое несут один и тот же смысл «может значить, что чат убрали из
	// chatListMain», и различать их в TUI нечего. Буфер 20: на старте
	// позиции приходят пачками по всему списку.
	chatPositionUpdates chan map[string]interface{}
	// connectionStateUpdates — updateConnectionState: состояние соединения с
	// Telegram. Переключений немного (обычно несколько за сессию: подъём,
	// пропала сеть, вернулась), и важен только ПОСЛЕДНИЙ статус, а не история
	// переключений, поэтому маленький буфер — как у chatNotificationSettingsUpdates.
	connectionStateUpdates chan map[string]interface{}
	// userUpdates — updateUser: изменились данные пользователя, из них клиенту
	// нужно новое имя (лента) и статус (стена, задача 0163).
	//
	// Буфер большой, а не «как у редких событий», потому что updateUser — это не
	// только переименование: на старте TDLib присылает его пачками, по одному на
	// каждого пользователя из списка чатов, и по живому прогону на настоящем
	// аккаунте это 230 апдейтов на 200 чатов. При прежнем буфере 5 и
	// неблокирующей отправке с дропом из них доходили бы первые пять, и у
	// подавляющего большинства карточек личных диалогов статус просто не
	// появился бы. Отправка остаётся неблокирующей: receiveLoop зависнуть не
	// должен ни при каком размере буфера.
	userUpdates chan map[string]interface{}
	// groupMemberCountUpdates — ОДИН канал на два апдейта
	// (updateBasicGroup/updateSupergroup): оба несут одно и то же нужное клиенту
	// «число участников или подписчиков» и различать их в интерфейсе нечего, а
	// разбирать их порознь значило бы завести две копии одной арифметики. Буфер
	// 20: на старте TDLib шлёт их пачками по всему списку чатов (проверено
	// вживью на настоящем аккаунте, задача 0163 — 7 и 99 апдейтов на 200 чатов).
	groupMemberCountUpdates chan map[string]interface{}
	// userStatusUpdates — updateUserStatus: смена статуса собеседника отдельным
	// апдейтом, без остальных полей пользователя. На старте не приходит вовсе
	// (проверено вживью: при загрузке списка чатов TDLib шлёт updateUser, но не
	// updateUserStatus), то есть снимок статуса едет через userUpdates, а этот
	// канал живёт изменениями. Буфер 5 — как у userUpdates, по той же причине.
	userStatusUpdates chan map[string]interface{}
	nextExtra         int64
	nextExtraMu       sync.Mutex
	closed            bool
	closedMu          sync.Mutex
}

// userUpdatesBuffer — размер буфера userUpdates. Обоснован живым прогоном на
// настоящем аккаунте (задача 0163): 230 апдейтов updateUser на список из 200
// чатов, то есть примерно по одному на чат. Взят с запасом от этой величины:
// буфер ничего не стоит (канал указателей), а нехватка означала бы молча
// потерянные статусы на части карточек, потому что отправка идёт с дропом.
const userUpdatesBuffer = 512

func NewClient() *Client {
	// Приглушаем встроенное логирование TDLib ДО создания клиента — иначе оно захлёстывает
	// stdout построчными трассами ("Begin/End to wait for updates") каждую секунду, из-за чего
	// интерактивные приглашения (номер телефона/код) физически не видно за потоком логов.
	// Уровень 1 — только ошибки, этого достаточно для диагностики реальных проблем.
	silenceLogging()

	clientID := tdCreateClientID()
	c := &Client{
		id:                clientID,
		pending:           make(map[string]chan map[string]interface{}),
		authUpdates:       make(chan map[string]interface{}, 10),
		messageUpdates:    make(chan map[string]interface{}, 20),
		sendStatusUpdates: make(chan map[string]interface{}, 20),
		// Папки меняются редко (при AuthorizationStateReady и при изменении папок
		// пользователем) — достаточно небольшого буфера, в отличие от
		// messageUpdates/sendStatusUpdates, которым нужен запас под более частые апдейты.
		chatFolderUpdates: make(chan map[string]interface{}, 5),
		// Апдейты о прочтении внутри чата могут приходить часто (как у
		// messageUpdates) — буфер по аналогии.
		chatReadInboxUpdates: make(chan map[string]interface{}, 20),
		// Апдейты о прочтении своих исходящих сообщений (updateChatReadOutbox)
		// по частоте аналогичны chatReadInboxUpdates — тот же буфер.
		chatReadOutboxUpdates: make(chan map[string]interface{}, 20),
		// updateChatTitle приносит настоящее имя приватного чата ПОСЛЕ
		// асинхронного резолва собеседника (в момент getChat title может быть
		// пустым — см. задачу 0045) — по частоте аналогичен
		// chatReadInboxUpdates, тот же буфер.
		chatTitleUpdates: make(chan map[string]interface{}, 20),
		// updateChatNotificationSettings — заглушили/разглушили чат в Telegram.
		// Событие редкое, буфера 5 хватает с запасом (как у chatFolderUpdates).
		chatNotificationSettingsUpdates: make(chan map[string]interface{}, 5),
		// Агрегаты по спискам чатов (папкам) меняются редко — буфер как у
		// chatFolderUpdates.
		unreadCountUpdates: make(chan map[string]interface{}, 5),
		// Тот же паттерн, что у unreadCountUpdates — агрегат по чатам с
		// непрочитанным (не по сообщениям), нужен для бейджей папок.
		unreadChatCountUpdates: make(chan map[string]interface{}, 5),
		// updateMessageInteractionInfo приходит на каждое изменение счётчиков
		// реакций под сообщением, а реакции в оживлённой переписке ставят часто —
		// буфер как у messageUpdates, а не как у редких агрегатов.
		messageInteractionUpdates: make(chan map[string]interface{}, 20),
		// updateMessageUnreadReactions — свои непрочитанные реакции на сообщении.
		// Событие редкое (поставили реакцию на СВОЁ сообщение), поэтому буфер как у
		// chatNotificationSettingsUpdates, а не как у messageInteractionUpdates.
		unreadReactionUpdates: make(chan map[string]interface{}, 5),
		// updateDeleteMessages приходит при удалении переписки, в том числе
		// массовом: один апдейт несёт сразу все message_ids. Событие при активной
		// чистке чата может быть частым (и TDLib шлёт его пачками), поэтому буфер
		// как у messageUpdates, а не как у редких агрегатов.
		deleteMessagesUpdates: make(chan map[string]interface{}, 20),
		// updateMessageContent приходит при правке сообщения — своё или чужое, из
		// другого клиента. Правка во время активной переписки не редкость, поэтому
		// буфер как у messageUpdates, а не как у агрегатов.
		messageContentUpdates: make(chan map[string]interface{}, 20),
		// updateNewChat приходит пачками на старте, но буфер берём как у
		// messageUpdates: дропается апдейт, а не тормозится receiveLoop.
		newChatUpdates: make(chan map[string]interface{}, 20),
		// События членства в списке на старте идут вместе с остальным списком,
		// поэтому буфер как у chatReadInboxUpdates.
		chatAddedToListUpdates:     make(chan map[string]interface{}, 20),
		chatRemovedFromListUpdates: make(chan map[string]interface{}, 20),
		// Позиции на старте приходят пачками по всему списку — буфер 20.
		chatPositionUpdates: make(chan map[string]interface{}, 20),
		// Состояние соединения переключается редко, и важен только последний
		// статус — буфер как у chatNotificationSettingsUpdates.
		connectionStateUpdates: make(chan map[string]interface{}, 5),
		// updateUser приходит на любое изменение данных пользователя (имя,
		// фото, статус), из которых клиенту нужно только имя, и переименования
		// редки — буфер как у chatNotificationSettingsUpdates.
		// Размер обоснование выше в объявлении поля: пачка updateUser на
		// старте — по одному на пользователя из списка чатов.
		userUpdates: make(chan map[string]interface{}, userUpdatesBuffer),
		// updateBasicGroup/updateSupergroup на старте идут пачками по всему
		// списку чатов — буфер как у chatPositionUpdates.
		groupMemberCountUpdates: make(chan map[string]interface{}, 20),
		// Смена статуса приходит на каждое переключение онлайн/офлайн у людей,
		// с которыми идёт живая переписка; на старте апдейтов нет вовсе — буфер
		// как у userUpdates.
		userStatusUpdates: make(chan map[string]interface{}, 5),
	}
	go c.receiveLoop()

	// Без этого запроса TDLib не запускает внутренний actor system для нового client_id и
	// никогда не эмитит первый updateAuthorizationState — проверено эмпирически (без этого
	// вызова receiveLoop вечно получает пустой ответ от td_receive). Результат не нужен —
	// важен сам факт первого td_send, а не содержимое ответа.
	if _, err := c.Send(context.Background(), map[string]interface{}{
		"@type": "getAuthorizationState",
	}); err != nil {
		// Не фатально: даже если этот конкретный запрос не получил ответа за 10с,
		// initial-запрос всё равно достиг TDLib и запустил actor system.
		_ = err
	}

	return c
}

func (c *Client) receiveLoop() {
	for {
		c.closedMu.Lock()
		closed := c.closed
		c.closedMu.Unlock()
		if closed {
			return
		}

		result := tdReceive(1.0)
		if result == "" {
			continue
		}

		var resp map[string]interface{}
		if err := json.Unmarshal([]byte(result), &resp); err != nil {
			continue
		}

		if c.routeUpdate(resp) {
			continue
		}

		if extraVal, ok := resp["@extra"]; ok {
			if extraStr, ok := extraVal.(string); ok {
				c.pendingMu.Lock()
				if ch, ok := c.pending[extraStr]; ok {
					select {
					case ch <- resp:
					default:
					}
					delete(c.pending, extraStr)
				}
				c.pendingMu.Unlock()
			}
		}
	}
}

// routeUpdate разносит апдейт по каналам и говорит, был ли он кем-то заинтересован.
//
// Вынесено из receiveLoop отдельной функцией по существу, а не ради удобства: весь
// разбор по @type стал единым местом, где видно, какие апдейты TDLib присылает и
// на какой канал они идут, и это проверяется тестом напрямую. Внутри receiveLoop
// ветки читались как одна простыня, и пропущенный тип нельзя было заметить, не
// перечитывая её целиком.
//
// ВАЖНО: маршрутизация по типу стоит ДО разбора @extra. Апдейты не привязаны к
// конкретному запросу, и ушедшие в канал не должны попадать в pending-каналы.
func (c *Client) routeUpdate(resp map[string]interface{}) bool {
	if authState, ok := resp["@type"].(string); ok && authState == "updateAuthorizationState" {
		// Недоблокирующая отправка с дропом при переполнении — сознательный выбор:
		// после authorizationStateReady никто не читает этот канал, а блокирующая
		// отправка застопорила бы receiveLoop навсегда на первом же лишнем апдейте,
		// заодно остановив обработку всех остальных запросов (getChats и т.п.).
		// Буфер (10) с запасом покрывает реальную последовательность переходов логина.
		select {
		case c.authUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateNewMessage" {
		// Тот же паттерн, что у authUpdates: неблокирующая отправка с дропом при
		// переполнении — receiveLoop не должен зависать, если получатель временно
		// не читает канал (или его вообще нет — до задачи 0008 никто не читал).
		select {
		case c.messageUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && (updType == "updateMessageSendSucceeded" || updType == "updateMessageSendFailed") {
		// Тот же паттерн, что у authUpdates/messageUpdates: неблокирующая отправка
		// с дропом при переполнении. Канал отдельный от messageUpdates — другой
		// смысл (статус именно отправки, не новое сообщение) и другой потребитель
		// (CLI-режим перед выходом из процесса, а не TUI/live-лента). Этот `continue`
		// стоит ДО проверки @extra: апдейты не привязаны к конкретному запросу и не
		// должны попадать в pending-каналы.
		select {
		case c.sendStatusUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateChatFolders" {
		// Тот же паттерн, что у authUpdates/messageUpdates: неблокирующая отправка
		// с дропом при переполнении. Панель «Папки» (задача 0018) подпишется на
		// этот канал; до подписки избыточные апдейты просто отбрасываются.
		select {
		case c.chatFolderUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateChatReadInbox" {
		// Тот же паттерн, что у chatFolderUpdates: неблокирующая отправка
		// с дропом при переполнении. Счётчики непрочитанных в чатах (задача
		// 0028) читаются этим каналом; до подписки избыточные апдейты
		// просто отбрасываются.
		select {
		case c.chatReadInboxUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateChatReadOutbox" {
		// Тот же паттерн, что у chatReadInboxUpdates: неблокирующая отправка
		// с дропом при переполнении. Счётчики прочтения исходящих сообщений
		// (updateChatReadOutbox) читаются этим каналом; до подписки
		// избыточные апдейты просто отбрасываются.
		select {
		case c.chatReadOutboxUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateChatNotificationSettings" {
		// Тот же паттерн, что у chatTitleUpdates: неблокирующая отправка
		// с дропом при переполнении. Приходит, когда человек заглушил или
		// разглушил чат в самом Telegram; из него живёт фильтр
		// «приглушённые» (см. ParseChatNotificationSettingsUpdate). До
		// подписки избыточные апдейты просто отбрасываются.
		select {
		case c.chatNotificationSettingsUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateMessageInteractionInfo" {
		// Тот же паттерн, что у messageUpdates: неблокирующая отправка с дропом
		// при переполнении, receiveLoop не должен зависать.
		//
		// Апдейт приходит на каждое изменение счётчиков реакций под сообщением.
		// Схема getMessageAvailableReactions прямо обещает, что список доступных
		// реакций меняется вместе с updateMessageInteractionInfo, то есть без
		// него список в пикере устаревал. Разбор счётчиков переиспользует
		// parseReactions: по схеме это те же поля
		// message.interaction_info.reactions, что приходят в message.
		select {
		case c.messageInteractionUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateMessageUnreadReactions" {
		// Тот же паттерн, что у chatNotificationSettingsUpdates: неблокирующая
		// отправка с дропом при переполнении, receiveLoop не должен зависать.
		//
		// Апдейт приходит, когда на СВОЁ сообщение поставили реакцию, которую
		// человек ещё не видел. Счётчики реакций обновляет updateMessageInteractionInfo,
		// а этот несёт другое — сколько таких реакций ещё не просмотрено; в ленте
		// это маркер у карточки. Разбор — ParseMessageUnreadReactionsUpdate.
		select {
		case c.unreadReactionUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateDeleteMessages" {
		// Тот же паттерн, что у messageUpdates: неблокирующая отправка с дропом
		// при переполнении, receiveLoop не должен зависать.
		//
		// Апдейт приходит, когда сообщения удалили не мы — из другого клиента
		// либо удалил кто-то ещё из участников чата. Один апдейт несёт сразу все
		// message_ids удалённых сообщений, поэтому под массовую чистку переписки
		// он может приходить пачками. Разбор — ParseDeleteMessagesUpdate.
		select {
		case c.deleteMessagesUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateMessageContent" {
		// Тот же паттерн, что у messageUpdates: неблокирующая отправка с дропом
		// при переполнении, receiveLoop не должен зависать.
		//
		// Апдейт приходит, когда содержимое сообщения изменилось, то есть его
		// отредактировали — в том числе из другого клиента. Своего нового текста
		// клиент не показывает по ответу на правку (правки в TUI нет), так что
		// этот апдейт — единственный сигнал о новом содержимом. Разбор —
		// ParseMessageContentUpdate.
		select {
		case c.messageContentUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateNewChat" {
		// Тот же паттерн, что у messageUpdates: неблокирующая отправка с дропом
		// при переполнении, receiveLoop не должен зависать.
		//
		// updateNewChat — единственный источник полного объекта чата для чата,
		// который появился в списке уже после старта. Без него такой чат не
		// знали бы ни названия, ни категории, и — главное — по нему никогда не
		// позвали бы openChat, а без openChat TDLib не присылает живые
		// обновления по чату вовсе. Разбор — ParseNewChatUpdate.
		select {
		case c.newChatUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateChatAddedToList" {
		// Тот же паттерн, что у messageUpdates: неблокирующая отправка с дропом
		// при переполнении, receiveLoop не должен зависать.
		//
		// Апдейт приходит, когда чат добавили в список (в том числе вернули
		// из архива). Самого объекта чата он не несёт — только chat_id и
		// список, — поэтому разбор отдаёт только список (ParseChatListMembershipUpdate),
		// а полный объект дозагружается отдельным getChat.
		select {
		case c.chatAddedToListUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateChatRemovedFromList" {
		// Тот же паттерн, что у chatAddedToListUpdates: неблокирующая отправка
		// с дропом при переполнении. Разбор тот же
		// (ParseChatListMembershipUpdate), но по факту удаления из списка.
		select {
		case c.chatRemovedFromListUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok &&
		(updType == "updateChatPosition" || updType == "updateChatLastMessage" || updType == "updateChatDraftMessage") {
		// Тот же паттерн, что у messageUpdates: неблокирующая отправка с дропом
		// при переполнении, receiveLoop не должен зависать.
		//
		// Три апдейта позиции идут в ОДИН канал сознательно: по документации
		// updateChatPosition «An updateChatLastMessage or updateChatDraftMessage
		// update might be sent instead of the update», то есть это
		// взаимозаменяемые способы сообщить об изменении позиции, а не три
		// апдейта на одно событие. Различать их в TUI нечего — разбор
		// (ParseChatPositionRemoval) отвечает на один вопрос «убирать ли чат».
		select {
		case c.chatPositionUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateConnectionState" {
		// Тот же паттерн, что у chatNotificationSettingsUpdates: неблокирующая
		// отправка с дропом при переполнении.
		//
		// Апдейт приходит при каждой смене состояния соединения (подключение,
		// пропала сеть, восстановление), и без него в интерфейсе не было ни
		// одного признака того, что сообщения перестали приходить по причине
		// сети, а не по причине тишины в чате. Разбор —
		// ParseConnectionStateUpdate.
		select {
		case c.connectionStateUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateUser" {
		// Тот же паттерн, что у chatNotificationSettingsUpdates: неблокирующая
		// отправка с дропом при переполнении.
		//
		// Апдейт приходит, когда человек переименовал себя в Telegram. Лента
		// держит имя отправителя строкой, зафиксированной при разборе
		// сообщения, и без этого апдейта старая карточка показывала бы прежнее
		// имя до перезапуска. Собственная база пользователей TDLib обновляет
		// независимо от подписки клиента — то есть дело не в том, что TDLib
		// чего-то не знает, а в том, что у нас нет сигнала. Разбор —
		// ParseUserUpdate.
		select {
		case c.userUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok &&
		(updType == "updateBasicGroup" || updType == "updateSupergroup") {
		// Тот же паттерн, что у chatPositionUpdates (одним каналом на два
		// взаимозаменяемых по смыслу апдейта): неблокирующая отправка с дропом
		// при переполнении, receiveLoop не должен зависать.
		//
		// Апдейты приходят при загрузке списка чатов (проверено вживью на
		// настоящем аккаунте, задача 0163) и при живых изменениях числа
		// участников. Разбор — ParseGroupMemberCountUpdate.
		select {
		case c.groupMemberCountUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateUserStatus" {
		// Тот же паттерн, что у userUpdates: неблокирующая отправка с дропом
		// при переполнении.
		//
		// Отдельный апдейт от updateUser: тот приносит пользователя целиком и
		// едет пачками на старте, а этот — только на СМЕНУ статуса, и без него
		// карточка личного диалога показывала бы снимок статуса до перезапуска.
		// Разбор — ParseUserStatusUpdate.
		select {
		case c.userStatusUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateChatTitle" {
		// Тот же паттерн, что у chatReadOutboxUpdates: неблокирующая
		// отправка с дропом при переполнении. Реальное имя чата (для
		// приватных — имя собеседника) приходит этим апдейтом после
		// асинхронного резолва пользователя; обработчик в TUI заменяет
		// m.chats[i].Title (задача 0045). До подписки избыточные апдейты
		// просто отбрасываются.
		select {
		case c.chatTitleUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateUnreadMessageCount" {
		// Тот же паттерн, что у chatFolderUpdates: неблокирующая отправка
		// с дропом при переполнении. Сумма непрочитанных СООБЩЕНИЙ по
		// списку чатов (для "Все чаты", задача 0028) читается этим
		// каналом; до подписки избыточные апдейты просто отбрасываются.
		select {
		case c.unreadCountUpdates <- resp:
		default:
		}
		return true
	}

	if updType, ok := resp["@type"].(string); ok && updType == "updateUnreadChatCount" {
		// Тот же паттерн — число ЧАТОВ (не сообщений) с непрочитанным по
		// списку, нужно для бейджей ПАПОК (не "Все чаты", см.
		// ParseUnreadChatCountUpdate — другая метрика, чем у
		// updateUnreadMessageCount).
		select {
		case c.unreadChatCountUpdates <- resp:
		default:
		}
		return true
	}
	return false
}

func (c *Client) generateExtra() string {
	c.nextExtraMu.Lock()
	defer c.nextExtraMu.Unlock()
	c.nextExtra++
	return strconv.FormatInt(c.nextExtra, 10)
}

func (c *Client) Send(ctx context.Context, request map[string]interface{}) (map[string]interface{}, error) {
	extra := c.generateExtra()
	request["@extra"] = extra

	ch := make(chan map[string]interface{}, 1)
	c.pendingMu.Lock()
	c.pending[extra] = ch
	c.pendingMu.Unlock()

	requestJSON, err := json.Marshal(request)
	if err != nil {
		c.pendingMu.Lock()
		delete(c.pending, extra)
		c.pendingMu.Unlock()
		return nil, err
	}

	tdSend(c.id, string(requestJSON))

	select {
	case resp := <-ch:
		if resp["@type"] == "error" {
			message := ""
			if m, ok := resp["message"].(string); ok {
				message = m
			}
			return nil, errors.New(message)
		}
		return resp, nil
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, extra)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	case <-time.After(10 * time.Second):
		c.pendingMu.Lock()
		delete(c.pending, extra)
		c.pendingMu.Unlock()
		return nil, errors.New("tdlib: request timed out")
	}
}

// silenceLogging разводит потоки логов TDLib и интерактивного ввода-вывода приложения: без
// этого TDLib построчно пишет трассировку ("Begin/End to wait for updates") прямо в stdout/
// stderr каждую секунду, из-за чего интерактивные приглашения (номер телефона/код) физически не
// видно за потоком логов — а позже, в TUI на bubbletea, такой левый вывод в stdout сломает экран.
// Логи TDLib уходят в отдельный файл вне терминала, stdout/stderr — только под наш собственный
// ввод-вывод.
func silenceLogging() {
	logPath := filepath.Join(tdlibConfigDir(), "tdlib.log")
	execJSON(map[string]interface{}{
		"@type": "setLogStream",
		"log_stream": map[string]interface{}{
			"@type":         "logStreamFile",
			"path":          logPath,
			"max_file_size": 10 * 1024 * 1024,
			// redirect_stderr=false: этот флаг перехватывает stderr ВСЕГО процесса на уровне ОС,
			// включая наши собственные сообщения об ошибках (fmt.Fprintf(os.Stderr, ...)) — они
			// молча утекали бы в файл лога вместо терминала пользователя. Обнаружено на практике.
			"redirect_stderr": false,
		},
	})
	execJSON(map[string]interface{}{
		"@type":               "setLogVerbosityLevel",
		"new_verbosity_level": 2,
	})
}

// execJSON — низкоуровневый td_execute без привязки к client_id и без обработки ответа,
// используется только для команд настройки (логирование), где результат нам не нужен.
func execJSON(request map[string]interface{}) {
	req, err := json.Marshal(request)
	if err != nil {
		return
	}
	tdExecute(string(req))
}

// tdlibConfigDir возвращает <UserConfigDir>/telecli, создавая каталог (0700), если его нет.
func tdlibConfigDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = "."
	}
	dir := filepath.Join(configDir, "telecli")
	_ = os.MkdirAll(dir, 0700)
	return dir
}

func (c *Client) Execute(request map[string]interface{}) (map[string]interface{}, error) {
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	result := tdExecute(string(requestJSON))
	if result == "" {
		return nil, errors.New("td_execute returned nil")
	}

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		return nil, err
	}

	if resp["@type"] == "error" {
		message := ""
		if m, ok := resp["message"].(string); ok {
			message = m
		}
		return nil, errors.New(message)
	}

	return resp, nil
}

func (c *Client) AuthUpdates() <-chan map[string]interface{} {
	return c.authUpdates
}

func (c *Client) MessageUpdates() <-chan map[string]interface{} {
	return c.messageUpdates
}

func (c *Client) SendStatusUpdates() <-chan map[string]interface{} {
	return c.sendStatusUpdates
}

func (c *Client) ChatFolderUpdates() <-chan map[string]interface{} {
	return c.chatFolderUpdates
}

func (c *Client) ChatReadInboxUpdates() <-chan map[string]interface{} {
	return c.chatReadInboxUpdates
}

func (c *Client) ChatReadOutboxUpdates() <-chan map[string]interface{} {
	return c.chatReadOutboxUpdates
}

func (c *Client) ChatTitleUpdates() <-chan map[string]interface{} {
	return c.chatTitleUpdates
}

// ChatNotificationSettingsUpdates — updateChatNotificationSettings: человек
// заглушил или разглушил чат в Telegram, и фильтр «приглушённые» должен узнать
// об этом, не дожидаясь перезапуска.
func (c *Client) ChatNotificationSettingsUpdates() <-chan map[string]interface{} {
	return c.chatNotificationSettingsUpdates
}

func (c *Client) MessageInteractionUpdates() <-chan map[string]interface{} {
	return c.messageInteractionUpdates
}

// UnreadReactionMessageUpdates — updateMessageUnreadReactions: на СВОЁ сообщение
// поставили реакцию, которую человек ещё не видел. Отдельный канал от
// MessageInteractionUpdates потому, что счётчики реакций и «непрочитанные» —
// разные данные: счётчики меняются у всех сообщений, а этот апдейт приходит про
// собственные сообщения.
func (c *Client) UnreadReactionMessageUpdates() <-chan map[string]interface{} {
	return c.unreadReactionUpdates
}

// DeleteMessagesUpdates — updateDeleteMessages: сообщения удалили не мы (другой
// клиент или другой участник чата). Отдельный канал от MessageUpdates потому,
// что апдейт не приносит сообщений, а убирает их: в ленте по нему надо не
// добавлять карточку, а молча убирать указанные message_ids.
func (c *Client) DeleteMessagesUpdates() <-chan map[string]interface{} {
	return c.deleteMessagesUpdates
}

// MessageContentUpdates — updateMessageContent: содержимое сообщения изменилось
// (его отредактировали). Отдельный канал от MessageUpdates потому, что апдейт не
// приносит нового сообщения, а переписывает существующее: по нему нечего
// добавлять в ленту, нужно заменить поля у сообщения, которое в ней уже есть.
func (c *Client) MessageContentUpdates() <-chan map[string]interface{} {
	return c.messageContentUpdates
}

// NewChatUpdates — updateNewChat: TDLib поднял новый чат. Без подписки чат,
// появившийся уже после старта, остался бы неизвестным: без названия и категории
// в ленте, и — главное — без openChat, а без него TDLib не присылает по нему
// живых обновлений («in supergroups and channels all updates are received only
// for opened chats»), то есть чат молчал бы до перезапуска приложения.
func (c *Client) NewChatUpdates() <-chan map[string]interface{} {
	return c.newChatUpdates
}

// ChatAddedToListUpdates — updateChatAddedToList: чат добавили в список. Отдельный
// канал от NewChatUpdates потому, что апдейт не несёт объекта чата — только
// chat_id и список, — и по нему объект надо дозагружать отдельным getChat.
func (c *Client) ChatAddedToListUpdates() <-chan map[string]interface{} {
	return c.chatAddedToListUpdates
}

// ChatRemovedFromListUpdates — updateChatRemovedFromList: чат убрали из списка
// (перенесли в архив или папку). В ленте по нему надо не добавлять карточку, а
// убрать чат из локального списка.
func (c *Client) ChatRemovedFromListUpdates() <-chan map[string]interface{} {
	return c.chatRemovedFromListUpdates
}

// ChatPositionUpdates — позиция чата в списке: updateChatPosition,
// updateChatLastMessage и updateChatDraftMessage в ОДНОМ канале. Все три по
// документации взаимозаменяемы («An updateChatLastMessage or
// updateChatDraftMessage update might be sent instead of the update»), и для
// клиента значат одно: чат, возможно, убрали из главного списка.
func (c *Client) ChatPositionUpdates() <-chan map[string]interface{} {
	return c.chatPositionUpdates
}

// ConnectionStateUpdates — updateConnectionState: состояние соединения с Telegram
// (подключение, нет сети, синхронизация, готово). Отдельный канал от прочих
// обновлений потому, что апдейт не приносит данных о чатах и сообщениях, а
// сообщает о соединении: в интерфейсе по нему показывается, что лента молчит из-за
// сети, а не потому что в чате просто тихо.
func (c *Client) ConnectionStateUpdates() <-chan map[string]interface{} {
	return c.connectionStateUpdates
}

// UserUpdates — updateUser: изменились данные пользователя. Отдельный канал от
// прочих обновлений потому, что апдейт приносит пользователя целиком, а нужен
// из него только id и новое имя: по нему лента переименовывает уже
// отрисованные карточки этого человека, у которых имя было зафиксировано
// строкой при разборе сообщения.
func (c *Client) UserUpdates() <-chan map[string]interface{} {
	return c.userUpdates
}

// GroupMemberCountUpdates — updateBasicGroup и updateSupergroup в ОДНОМ канале:
// оба несут число участников группы или подписчиков канала, а различать их
// клиенту нечего. Из него стена берёт и снимок числа (апдейты приходят уже при
// загрузке списка чатов), и живые изменения.
func (c *Client) GroupMemberCountUpdates() <-chan map[string]interface{} {
	return c.groupMemberCountUpdates
}

// UserStatusUpdates — updateUserStatus: смена статуса собеседника отдельным
// апдейтом. Отдельный канал от UserUpdates потому, что этот приносит только
// статус (и приходит на КАЖДУЮ смену онлайн/офлайн), а updateUser —
// пользователя целиком и пачками на старте.
func (c *Client) UserStatusUpdates() <-chan map[string]interface{} {
	return c.userStatusUpdates
}

func (c *Client) UnreadCountUpdates() <-chan map[string]interface{} {
	return c.unreadCountUpdates
}

func (c *Client) UnreadChatCountUpdates() <-chan map[string]interface{} {
	return c.unreadChatCountUpdates
}

func (c *Client) Close() {
	c.closedMu.Lock()
	c.closed = true
	c.closedMu.Unlock()
}
