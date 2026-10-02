package auth

import (
	"context"
	"fmt"
)

// OpenChat помечает чат "открытым" в TDLib — влияет на приоритет синхронизации
// истории (без этого при первом обращении к чату может быть доступно не всё
// содержимое локального кэша) и включает доставку updateNewMessage для него.
func OpenChat(ctx context.Context, client TDClientInterface, chatID int64) error {
	_, err := client.Send(ctx, map[string]interface{}{
		"@type":   "openChat",
		"chat_id": chatID,
	})
	if err != nil {
		return fmt.Errorf("openChat failed: %w", err)
	}
	return nil
}

func CloseChat(ctx context.Context, client TDClientInterface, chatID int64) error {
	_, err := client.Send(ctx, map[string]interface{}{
		"@type":   "closeChat",
		"chat_id": chatID,
	})
	if err != nil {
		return fmt.Errorf("closeChat failed: %w", err)
	}
	return nil
}

// ParseNewMessageUpdate разбирает "сырой" апдейт из MessageUpdates(): если это
// updateNewMessage — возвращает id чата и распарсенное сообщение (через тот же
// parseMessage, что и GetMessages/SendMessage), ok == true. Любая другая форма
// (неожиданный @type, отсутствует/битое поле message) — ok == false, без
// паники: вызывающая сторона (internal/tui) не должна разбирать сырой JSON
// TDLib сама, это единственное место, где это делается.
func ParseNewMessageUpdate(ctx context.Context, client TDClientInterface, update map[string]interface{}) (chatID int64, msg Message, ok bool) {
	if update["@type"] != "updateNewMessage" {
		return 0, Message{}, false
	}
	msgMap, mapOk := update["message"].(map[string]interface{})
	if !mapOk {
		return 0, Message{}, false
	}
	cid, cidOk := msgMap["chat_id"].(float64)
	if !cidOk {
		return 0, Message{}, false
	}
	return int64(cid), parseMessage(ctx, client, msgMap), true
}

// ParseChatReadInboxUpdate разбирает updateChatReadInbox — новое количество
// непрочитанных В КОНКРЕТНОМ чате (растёт на новом сообщении, падает при
// прочтении — оба случая один и тот же апдейт).
func ParseChatReadInboxUpdate(update map[string]interface{}) (chatID int64, unreadCount int32, ok bool) {
	if update["@type"] != "updateChatReadInbox" {
		return 0, 0, false
	}
	id, idOk := update["chat_id"].(float64)
	count, countOk := update["unread_count"].(float64)
	if !idOk || !countOk {
		return 0, 0, false
	}
	return int64(id), int32(count), true
}

// ParseChatReadOutboxUpdate разбирает updateChatReadOutbox — последний
// прочитанный ID исходящего сообщения в чате. message ID (int53), не count.
func ParseChatReadOutboxUpdate(update map[string]interface{}) (chatID int64, lastReadOutboxMessageID int64, ok bool) {
	if update["@type"] != "updateChatReadOutbox" {
		return 0, 0, false
	}
	id, idOk := update["chat_id"].(float64)
	msgID, msgIDOk := update["last_read_outbox_message_id"].(float64)
	if !idOk || !msgIDOk {
		return 0, 0, false
	}
	return int64(id), int64(msgID), true
}

// ParseChatTitleUpdate разбирает updateChatTitle — новое название чата. Для
// приватных чатов это имя собеседника, которое приходит ПОСЛЕ асинхронного
// резолва пользователя: в момент getChat title может быть ещё пустым (см.
// задачу 0045), реальное имя доставляет именно этот апдейт.
func ParseChatTitleUpdate(update map[string]interface{}) (chatID int64, title string, ok bool) {
	if update["@type"] != "updateChatTitle" {
		return 0, "", false
	}
	id, idOk := update["chat_id"].(float64)
	title, titleOk := update["title"].(string)
	if !idOk || !titleOk {
		return 0, "", false
	}
	return int64(id), title, true
}

// ParseChatNotificationSettingsUpdate разбирает updateChatNotificationSettings —
// изменились настройки уведомлений чата. Единственное, что из них нужно меню
// «приглушённые», — факт явного заглушения, поэтому наружу отдаётся именно он.
//
// Апдейт приходит, когда человек заглушил или разглушил чат прямо в Telegram, и
// без него включённый чекбокс врал бы до перезапуска. Схема сверена с
// ~/.local/tdlib/include/td/telegram/td_api.h (сборка из master):
//
//	class updateChatNotificationSettings final : public Update {
//	  int53 chat_id_;
//	  object_ptr<chatNotificationSettings> notification_settings_;
//
// Контракт тот же, что у остальных парсеров файла: на чужой @type или битом
// объекте notification_settings — ok == false, без паники. Отсутствующий
// notification_settings читаем как «не заглушено» (nil передаёт chatMutedOf
// именно так), иначе чат молча пропал бы из ленты при включённом фильтре.
func ParseChatNotificationSettingsUpdate(update map[string]interface{}) (chatID int64, muted bool, ok bool) {
	if update["@type"] != "updateChatNotificationSettings" {
		return 0, false, false
	}
	id, idOk := update["chat_id"].(float64)
	if !idOk {
		return 0, false, false
	}
	settings, _ := update["notification_settings"].(map[string]interface{})
	return int64(id), chatMutedOf(settings), true
}

// ParseNewChatUpdate разбирает updateNewChat — «чат загружен или создан».
//
// Схема сверена с ~/.local/tdlib/include/td/telegram/td_api.h (сборка TDLib из
// master, та же, что и в остальном проекте), ID -77484353:
//
//	class updateNewChat final : public Update {
//	  object_ptr<chat> chat_;
//
// Апдейт несёт объект chat ЦЕЛИКОМ, поэтому наружу отдаётся готовый Chat, а не
// набор полей: разбирать его здесь, в auth, — единственный способ не завести
// второй, разъезжающийся с GetChats разбор объекта chat. Схема chat (id_, type_,
// title_, unread_count_, last_read_outbox_message_id_, notification_settings_)
// разбирает chatFromTDLib, общий с GetChats.
//
// ok == false на чужом @type или битом объекте chat (нет обязательного id) —
// тот же контракт, что у остальных парсеров файла.
func ParseNewChatUpdate(update map[string]interface{}) (chat Chat, ok bool) {
	if update["@type"] != "updateNewChat" {
		return Chat{}, false
	}
	chatMap, mapOk := update["chat"].(map[string]interface{})
	if !mapOk {
		return Chat{}, false
	}
	return chatFromTDLib(chatMap)
}

// ParseChatListMembershipUpdate разбирает updateChatAddedToList и
// updateChatRemovedFromList — чат добавлен в список или убран из него.
//
// Схема сверена с тем же td_api.h, ID -1418722068 и 1294647836:
//
//	class updateChatAddedToList final : public Update {
//	  int53 chat_id_;
//	  object_ptr<ChatList> chat_list_;
//	class updateChatRemovedFromList final : public Update {
//	  int53 chat_id_;
//	  object_ptr<ChatList> chat_list_;
//
// onlyMainList отвечает на вопрос «это про главный список?», а не отдаёт
// объект списка наружу: список в TDLib ровно трёх видов (chatListMain,
// chatListArchive, chatListFolder — сверено с td_api.h, ID -400991316 /
// 362770115 / 385760856), и клиенту, который показывает только главный список,
// нужно знать одно — этот апдейт его касается или нет. Всё, что не
// chatListMain (архив, папка), вызывающая сторона обязана игнорировать, не
// запрашивая по такому апдейту getChat впустую.
//
// В отличие от соседних парсеров здесь НЕ ok == false на «чужой» список: апдейт
// разобран верно, он просто не про наш список, и молчаливый ok == false смешал
// бы его с битым апдейтом, который надо переждать.
func ParseChatListMembershipUpdate(update map[string]interface{}) (chatID int64, added bool, onlyMainList bool, ok bool) {
	updType, typeOk := update["@type"].(string)
	if !typeOk {
		return 0, false, false, false
	}
	switch updType {
	case "updateChatAddedToList":
		added = true
	case "updateChatRemovedFromList":
		added = false
	default:
		return 0, false, false, false
	}
	id, idOk := update["chat_id"].(float64)
	if !idOk {
		return 0, false, false, false
	}
	chatList, listOk := update["chat_list"].(map[string]interface{})
	if !listOk {
		return 0, false, false, false
	}
	return int64(id), added, chatList["@type"] == "chatListMain", true
}

// ParseChatPositionRemoval разбирает updateChatPosition, updateChatLastMessage и
// updateChatDraftMessage и отвечает ровно на один вопрос: значит ли этот апдейт
// «убрать чат из chatListMain».
//
// Формы у трёх апдейтов разные, и это не домысел, а схема (td_api.h,
// ID -8979849 / -923244537 / 1455190380):
//
//	updateChatPosition:     int53 chat_id_; object_ptr<chatPosition> position_;
//	updateChatLastMessage:  int53 chat_id_; object_ptr<message> last_message_;
//	                        array<object_ptr<chatPosition>> positions_;
//	updateChatDraftMessage: int53 chat_id_; object_ptr<draftMessage> draft_message_;
//	                        array<object_ptr<chatPosition>> positions_;
//
// Правило одно на всех троих и оно документировано к chatPosition (td_api.h:
// «class chatPosition { object_ptr<ChatList> list_; int64 order_; ... }»): НОВЫЙ
// order, равный нулю, означает, что чат надо убрать из списка. Ноль — это не
// «порядок ноль», а сигнал удаления, и перепутать его с обычным значением
// нельзя. Порядок при этом сам по себе нигде не используется: порядок m.chats в
// tgclitui не отображается, лента сортируется по дате сообщения.
//
// removed == false при ok == true — апдейт валиден, но про удаление не говорит:
// либо позиция не про chatListMain (TDLib вполне может прислать апдейт только
// ради папки или архива), либо order != 0. Вызывающая сторона в этом случае не
// делает ничего. Отдельно: отсутствие позиции для chatListMain в МАССИВЕ — тоже
// removed == false, а не «пустой массив ⇒ убрать»: пустой массив означает «этот
// апдейт не про главный список», а не «чат из него удалён».
func ParseChatPositionRemoval(update map[string]interface{}) (chatID int64, removed bool, ok bool) {
	updType, typeOk := update["@type"].(string)
	if !typeOk {
		return 0, false, false
	}
	id, idOk := update["chat_id"].(float64)
	if !idOk {
		return 0, false, false
	}
	switch updType {
	case "updateChatPosition":
		position, positionOk := update["position"].(map[string]interface{})
		if !positionOk {
			return 0, false, false
		}
		return int64(id), chatPositionRemovesFromMain(position), true
	case "updateChatLastMessage", "updateChatDraftMessage":
		positions, positionsOk := update["positions"].([]interface{})
		if !positionsOk {
			return 0, false, false
		}
		for _, raw := range positions {
			position, positionOk := raw.(map[string]interface{})
			if !positionOk {
				continue
			}
			if chatPositionRemovesFromMain(position) {
				return int64(id), true, true
			}
		}
		return int64(id), false, true
	default:
		return 0, false, false
	}
}

// chatPositionRemovesFromMain — «эта позиция означает убрать чат из главного
// списка». Единственное место, где читается order: правило «order == 0 ⇒ убрать».
func chatPositionRemovesFromMain(position map[string]interface{}) bool {
	list, listOk := position["list"].(map[string]interface{})
	if !listOk || list["@type"] != "chatListMain" {
		return false
	}
	order, orderOk := position["order"].(float64)
	return orderOk && order == 0
}

// ParseConnectionStateUpdate разбирает updateConnectionState. state — одно из пяти
// значений TDLib (waiting_for_network/connecting_to_proxy/connecting/updating/ready)
// как строка @type вложенного объекта, без сокращений — решение о тексте для
// человека принимает вызывающая сторона, не парсер.
//
// Схема сверена с ~/.local/tdlib/include/td/telegram/td_api.h (сборка из master,
// ID 1469292078):
//
//	class updateConnectionState final : public Update {
//	  object_ptr<ConnectionState> state_;
//
// Состояний ровно пять, и все они — пустые структуры (собственных полей нет ни у
// одной), то есть весь апдейт это его @type:
//
//	connectionStateWaitingForNetwork   (ID 1695405912)
//	connectionStateConnectingToProxy   (ID -93187239)
//	connectionStateConnecting          (ID -1298400670)
//	connectionStateUpdating            (ID -188104009)
//	connectionStateReady               (ID 48608492)
//
// Наружу отдаётся строка @type как есть, БЕЗ проверки на принадлежность к
// перечисленным пяти: новое состояние в будущей версии TDLib должно дойти до
// вызывающей стороны и быть там тихо проигнорировано, а не превратиться в «битый
// апдейт», который ничем не отличается от чужого @type. Решение о тексте для
// человека принимает вызывающая сторона — парсер не знает ни про журнал, ни про
// статус-бар.
func ParseConnectionStateUpdate(update map[string]interface{}) (state string, ok bool) {
	if update["@type"] != "updateConnectionState" {
		return "", false
	}
	stateMap, mapOk := update["state"].(map[string]interface{})
	if !mapOk {
		return "", false
	}
	state, ok = stateMap["@type"].(string)
	if !ok {
		return "", false
	}
	return state, true
}

// ParseUserUpdate разбирает updateUser — изменились данные пользователя: имя
// и статус.
//
// Схема сверена с ~/.local/tdlib/include/td/telegram/td_api.h (сборка из master,
// ID 1183394041):
//
//	class updateUser final : public Update {
//	  object_ptr<user> user_;
//
// Ключевое здесь то, чего у апдейта НЕТ: user_id отдельного поля у него не
// несёт вовсе, весь смысл лежит в объекте user, а id лежит внутри него (у user
// поле называется id, а не user_id — сверено с td_api.h, class user, ID
// 1874921182). Апдейт приносит пользователя ЦЕЛИКОМ, а не только изменившиеся
// поля, поэтому имя берётся тем же userDisplayName, что и при первичном разборе
// сообщения: переименованный в Telegram человек должен получить в ленте ровно то
// же написание, что и у его же старых карточек.
//
// Возвращает ещё и status — тот же объект user несёт его в поле status, и на
// живом аккаунте именно сюда приезжает снимок статуса собеседника: TDLib при
// загрузке списка чатов шлёт updateUser пачками, а отдельный updateUserStatus
// приходит только на СМЕНУ статуса (проверено вживью, задача 0163). Без status
// в возврате снимок пришлось бы брать ещё одним запросом на каждый диалог.
//
// ok == false на чужой @type или битом объекте user — тот же контракт, что у
// остальных парсеров файла. Значение userID == 0 при ok == true означает
// странный объект без внятного id и проверяется уже вызывающей стороной.
func ParseUserUpdate(update map[string]interface{}) (userID int64, name string, status UserStatus, ok bool) {
	if update["@type"] != "updateUser" {
		return 0, "", UserStatus{}, false
	}
	user, mapOk := update["user"].(map[string]interface{})
	if !mapOk {
		return 0, "", UserStatus{}, false
	}
	id, idOk := user["id"].(float64)
	if !idOk {
		return 0, "", UserStatus{}, false
	}
	// Статус берётся оттуда же, откуда и имя: апдейт приносит пользователя
	// ЦЕЛИКОМ, а user.status лежит в том же объекте (сверено с td_api.h, class
	// user, поле status_). Отдельный запрос getUser ради одного статуса не
	// нужен, ровно как и ради имени — это тот же объект.
	statusMap, _ := user["status"].(map[string]interface{})
	return int64(id), userDisplayName(user), ParseUserStatus(statusMap), true
}

// ParseUnreadMessageCountUpdate разбирает updateUnreadMessageCount —
// агрегированное количество НЕПРОЧИТАННЫХ СООБЩЕНИЙ (не чатов!) по ЦЕЛОМУ
// списку чатов. Берётся unread_count (ВКЛЮЧАЯ замьюченные чаты) — по живой
// проверке человека: у "Все чаты" бейдж в самом Telegram именно такой,
// сумма всех непрочитанных сообщений без фильтра по mute. ВАЖНО: этот
// парсер используется ТОЛЬКО для folderID==0 (Main/"Все чаты") — для
// остальных папок Telegram показывает ДРУГУЮ метрику (число ЧАТОВ с
// непрочитанным, не сумму сообщений) из ДРУГОГО апдейта, см.
// ParseUnreadChatCountUpdate; результат этого парсера для chatListFolder
// парсится (для полноты и симметрии с остальным кодом), но вызывающая
// сторона (internal/tui) обязана ИГНОРИРОВАТЬ его для folderID != 0, не
// писать в folderUnread. folderID == 0 — синтетическая "Все чаты"/
// chatListMain, тот же sentinel, что уже использует
// internal/tui.Model.selectedFolderID для главного списка. Любой ChatList,
// кроме chatListMain/chatListFolder (например, chatListArchive) —
// ok == false, эта задача его не показывает.
func ParseUnreadMessageCountUpdate(update map[string]interface{}) (folderID int32, unreadCount int32, ok bool) {
	if update["@type"] != "updateUnreadMessageCount" {
		return 0, 0, false
	}
	count, countOk := update["unread_count"].(float64)
	if !countOk {
		return 0, 0, false
	}
	chatList, listOk := update["chat_list"].(map[string]interface{})
	if !listOk {
		return 0, 0, false
	}
	switch chatList["@type"] {
	case "chatListMain":
		return 0, int32(count), true
	case "chatListFolder":
		id, idOk := chatList["chat_folder_id"].(float64)
		if !idOk {
			return 0, 0, false
		}
		return int32(id), int32(count), true
	default:
		return 0, 0, false
	}
}

// ParseUnreadChatCountUpdate разбирает updateUnreadChatCount —
// агрегированное количество ЧАТОВ (не сообщений!) с непрочитанным по
// целому списку. Берётся unread_count (ВКЛЮЧАЯ замьюченные чаты) — по живой
// проверке человека: папка с 12 замьюченными и 1 незамьюченным чатом с
// непрочитанным показывает "13" в самом Telegram, mute не фильтруется.
// ИСПОЛЬЗУЕТСЯ ТОЛЬКО для папок (folderID != 0) — для "Все чаты"
// (folderID == 0) Telegram показывает другую метрику (сумму непрочитанных
// СООБЩЕНИЙ, не число чатов), см. ParseUnreadMessageCountUpdate; результат
// этого парсера для chatListMain парсится (для полноты), но вызывающая
// сторона (internal/tui) обязана его игнорировать для folderID == 0.
// Контракт тот же, что у остальных парсеров этого файла: не паникует на
// битом входе, ok == false при несовпадении @type или отсутствии полей.
func ParseUnreadChatCountUpdate(update map[string]interface{}) (folderID int32, chatCount int32, ok bool) {
	if update["@type"] != "updateUnreadChatCount" {
		return 0, 0, false
	}
	count, countOk := update["unread_count"].(float64)
	if !countOk {
		return 0, 0, false
	}
	chatList, listOk := update["chat_list"].(map[string]interface{})
	if !listOk {
		return 0, 0, false
	}
	switch chatList["@type"] {
	case "chatListMain":
		return 0, int32(count), true
	case "chatListFolder":
		id, idOk := chatList["chat_folder_id"].(float64)
		if !idOk {
			return 0, 0, false
		}
		return int32(id), int32(count), true
	default:
		return 0, 0, false
	}
}

// ParseUserStatusUpdate разбирает updateUserStatus — смена статуса пользователя
// ОТДЕЛЬНЫМ апдейтом, без остальных его полей.
//
// Схема сверена с ~/.local/tdlib/include/td/telegram/td_api.h (сборка из master,
// ID 958468625):
//
//	class updateUserStatus final : public Update {
//	  int53 user_id_;
//	  object_ptr<UserStatus> status_;
//
// Отдельный апдейт нужен в дополнение к status внутри updateUser, а не вместо
// него. Проверено живьём на настоящем аккаунте (задача 0163): при обычной
// загрузке списка чатов TDLib шлёт updateUser пачками (230 апдейтов на 200 чатов
// в прогоне) и НЕ шлёт ни одного updateUserStatus — то есть снимок статуса
// приезжает именно через updateUser. А вот смена «в сети ⇄ был(а) недавно»
// приходит уже отдельным updateUserStatus, и без него карточка личного диалога
// показывала бы снимок статуса до перезапуска.
//
// Контракт тот же, что у остальных парсеров файла: чужой @type или битый объект
// status — ok == false, без паники.
func ParseUserStatusUpdate(update map[string]interface{}) (userID int64, status UserStatus, ok bool) {
	if update["@type"] != "updateUserStatus" {
		return 0, UserStatus{}, false
	}
	id, idOk := update["user_id"].(float64)
	if !idOk {
		return 0, UserStatus{}, false
	}
	statusMap, statusOk := update["status"].(map[string]interface{})
	if !statusOk {
		return 0, UserStatus{}, false
	}
	return int64(id), ParseUserStatus(statusMap), true
}

// ParseGroupMemberCountUpdate разбирает updateBasicGroup и updateSupergroup —
// изменилось число участников группы или подписчиков канала.
//
// Оба апдейта несут ровно одно и то же нужное нам поле — member_count в
// объекте группы/супергруппы, — поэтому разбираются ОДНИМ парсером, а не двумя:
// различать их вызывающей стороне нечего, а разбирать их порознь значило бы
// завести две копии одной арифметики, которые разъедутся при первой же правке.
// Идентификатор достаётся из вложенного объекта, потому что отдельного поля
// group_id у апдейтов нет (сверено с td_api.h, ID -1003239581 и -76782300):
//
//	updateBasicGroup   { object_ptr<basicGroup> basic_group_; }
//	updateSupergroup   { object_ptr<supergroup> supergroup_; }
//	basicGroup   { int53 id_; int32 member_count_; ... }         ID -194767217
//	supergroup   { int53 id_; int32 member_count_; ... }         (id_, member_count_)
//
// member_count лежит ПРЯМО в базовом объекте, поэтому getBasicGroupFullInfo /
// getSupergroupFullInfo ради одного числа не нужны: в них на порядок больше
// полей, которых здесь всё равно не показать.
//
// Проверено живьём на настоящем аккаунте (задача 0163): при загрузке списка
// чатов TDLib шлёт эти апдейты САМИ, без явных getBasicGroup/getSupergroup —
// updateBasicGroup и updateSupergroup в прогоне на 200 чатов. Значит снимок
// числа участников приезжает тем же каналом, что и живые его изменения, и
// отдельный первичный запрос на каждую карточку не нужен вовсе.
//
// Контракт тот же, что у остальных парсеров файла.
func ParseGroupMemberCountUpdate(update map[string]interface{}) (groupID int64, memberCount int32, ok bool) {
	var objectType, objectField string
	switch update["@type"] {
	case "updateBasicGroup":
		objectType, objectField = "basicGroup", "basic_group"
	case "updateSupergroup":
		objectType, objectField = "supergroup", "supergroup"
	default:
		return 0, 0, false
	}
	object, objectOk := update[objectField].(map[string]interface{})
	if !objectOk || object["@type"] != objectType {
		return 0, 0, false
	}
	id, idOk := object["id"].(float64)
	count, countOk := object["member_count"].(float64)
	if !idOk || !countOk {
		return 0, 0, false
	}
	return int64(id), int32(count), true
}
