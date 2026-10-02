package tdclient

import (
	"testing"
	"time"
)

// routingTestClient — клиент только для проверки маршрутизации: реальный
// NewClient() поднимает cgo-цикл с живым TDLib, а здесь нужен лишь разбор
// апдейтов по каналам. Буферы взяты настоящие, иначе проверка «дошло/не дошло»
// ничего не значила бы.
func routingTestClient() *Client {
	return &Client{
		messageUpdates:                  make(chan map[string]interface{}, 20),
		authUpdates:                     make(chan map[string]interface{}, 10),
		sendStatusUpdates:               make(chan map[string]interface{}, 20),
		chatFolderUpdates:               make(chan map[string]interface{}, 5),
		chatReadInboxUpdates:            make(chan map[string]interface{}, 20),
		chatReadOutboxUpdates:           make(chan map[string]interface{}, 20),
		chatTitleUpdates:                make(chan map[string]interface{}, 20),
		chatNotificationSettingsUpdates: make(chan map[string]interface{}, 5),
		unreadCountUpdates:              make(chan map[string]interface{}, 5),
		unreadChatCountUpdates:          make(chan map[string]interface{}, 5),
		messageInteractionUpdates:       make(chan map[string]interface{}, 20),
		unreadReactionUpdates:           make(chan map[string]interface{}, 5),
		deleteMessagesUpdates:           make(chan map[string]interface{}, 20),
		messageContentUpdates:           make(chan map[string]interface{}, 20),
		newChatUpdates:                  make(chan map[string]interface{}, 20),
		chatAddedToListUpdates:          make(chan map[string]interface{}, 20),
		chatRemovedFromListUpdates:      make(chan map[string]interface{}, 20),
		chatPositionUpdates:             make(chan map[string]interface{}, 20),
		connectionStateUpdates:          make(chan map[string]interface{}, 5),
		// Тот же размер, что у NewClient: тест маршрутизации не должен быть
		// единственным местом, где буфер равен 5 (см. userUpdatesBuffer).
		userUpdates:             make(chan map[string]interface{}, userUpdatesBuffer),
		groupMemberCountUpdates: make(chan map[string]interface{}, 20),
		userStatusUpdates:       make(chan map[string]interface{}, 5),
		pending:                 make(map[string]chan map[string]interface{}),
	}
}

// waitUpdate — дождаться одного апдейта из канала или сдаться по таймауту.
func waitUpdate(t *testing.T, ch <-chan map[string]interface{}) map[string]interface{} {
	t.Helper()
	select {
	case update, ok := <-ch:
		if !ok {
			t.Fatal("канал закрыт")
		}
		return update
	case <-time.After(200 * time.Millisecond):
		t.Fatal("апдейт не дошёл до канала за 200мс")
		return nil
	}
}

// interactionUpdate — updateMessageInteractionInfo в том виде, в каком его
// присылает TDLib.
func interactionUpdate() map[string]interface{} {
	return map[string]interface{}{
		"@type":      "updateMessageInteractionInfo",
		"chat_id":    float64(5),
		"message_id": float64(9),
		"interaction_info": map[string]interface{}{
			"reactions": map[string]interface{}{"reactions": []interface{}{}},
		},
	}
}

// TestMessageInteractionUpdatesReachesItsChannel — апдейт о счётчиках реакций
// доходит до своего канала.
//
// Проверяется именно МАРШРУТИЗИРОВАНИЕ в receiveLoop, потому что без него апдейт
// молча терялся: цикл отбрасывал всё, на что нет ветки, и счётчики реакций
// обновлялись только перезапросом сообщения по нажатию →. На поведение TUI это не
// ловится вовсе — там канал просто пуст, и выглядит как «счётчики не меняются».
//
// Схема (td_api.tl): updateMessageInteractionInfo chat_id:int53 message_id:int53
// interaction_info:messageInteractionInfo = Update. Именно этот апдейт схема
// getMessageAvailableReactions называет причиной смены списка доступных реакций,
// поэтому слушать его обязаны.
func TestMessageInteractionUpdatesReachesItsChannel(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(interactionUpdate())

	update := waitUpdate(t, client.MessageInteractionUpdates())
	if update["@type"] != "updateMessageInteractionInfo" {
		t.Fatalf("в канал пришёл @type %q", update["@type"])
	}
	if update["chat_id"] != float64(5) || update["message_id"] != float64(9) {
		t.Fatalf("апдейт потерял идентификаторы: %+v", update)
	}
}

// TestMessageInteractionUpdateDoesNotLeakIntoNewMessages — апдейт о реакциях не
// должен попадать в канал новых сообщений. Иначе лента трактовала бы его как
// приход сообщения и вставляла бы в переписку пустышку.
func TestMessageInteractionUpdateDoesNotLeakIntoNewMessages(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(interactionUpdate())
	waitUpdate(t, client.MessageInteractionUpdates())

	select {
	case leaked := <-client.MessageUpdates():
		t.Fatalf("апдейт о реакциях попал в канал новых сообщений: %+v", leaked)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestInteractionUpdateIsNotTreatedAsRequestResponse — апдейт не привязан к
// запросу, поэтому он не должен попасть в pending-канал по @extra. Если бы
// попал, чужой вызов ждал бы его как ответ.
func TestInteractionUpdateIsNotTreatedAsRequestResponse(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	update := interactionUpdate()
	update["@extra"] = "чужой-запрос"
	client.routeUpdate(update)
	waitUpdate(t, client.MessageInteractionUpdates())

	// routeUpdate возвращает true — апдейт разобран по своему @type и до разбора
	// @extra не дошёл, то есть в pending он не попал. Проверяется возвратом, а не
	// содержимым pending: трогать pending из теста маршрутизации незачем.
	if !client.routeUpdate(update) {
		t.Error("апдейт о реакциях не разобран по @type — он ушёл в разбор @extra")
	}
}

// TestReactionUpdatesKeepOtherRoutesIntact — остальные апдейты продолжают идти
// по своим каналам после добавления новой ветки. Ветка стоит в общей
// простыне if'ов, и её легко вставить так, что что-то перестанет маршрутизироваться.
func TestReactionUpdatesKeepOtherRoutesIntact(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(map[string]interface{}{
		"@type":      "updateNewMessage",
		"message":    map[string]interface{}{"@type": "message", "id": float64(1)},
		"chat_id":    float64(1),
		"message_id": float64(1),
	})
	if update := waitUpdate(t, client.MessageUpdates()); update["@type"] != "updateNewMessage" {
		t.Fatalf("в канал сообщений пришёл @type %q", update["@type"])
	}

	client.routeUpdate(map[string]interface{}{
		"@type":                 "updateChatNotificationSettings",
		"chat_id":               float64(1),
		"notification_settings": map[string]interface{}{"@type": "chatNotificationSettings", "mute_for_duration": float64(0), "is_silent": false},
	})
	if update := waitUpdate(t, client.ChatNotificationSettingsUpdates()); update["@type"] != "updateChatNotificationSettings" {
		t.Fatalf("в канал настроек пришёл @type %q", update["@type"])
	}
}

// unreadReactionsUpdateJSON — updateMessageUnreadReactions в том виде, в каком его
// присылает TDLib. Схема (td_api.h собранной версии, updateMessageUnreadReactions):
// chat_id:int53 message_id:int53 unread_reactions:vector<unreadReaction>
// unread_reaction_count:int32.
func unreadReactionsUpdateJSON(count int) map[string]interface{} {
	return map[string]interface{}{
		"@type":                 "updateMessageUnreadReactions",
		"chat_id":               float64(5),
		"message_id":            float64(9),
		"unread_reactions":      []interface{}{},
		"unread_reaction_count": float64(count),
	}
}

// TestUnreadReactionUpdatesReachItsChannel — апдейт о непрочитанных реакциях
// доходит до своего канала.
//
// Проверяется именно МАРШРУТИЗИРОВАНИЕ в receiveLoop. Без ветки апдейт молча
// терялся: цикл отбрасывал всё, на что нет ветки, и маркер непрочитанных реакций
// появлялся бы только после перезапуска — то есть никогда сам по себе. На
// поведение TUI это не ловится: там канал просто пуст.
func TestUnreadReactionUpdatesReachItsChannel(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(unreadReactionsUpdateJSON(3))

	update := waitUpdate(t, client.UnreadReactionMessageUpdates())
	if update["@type"] != "updateMessageUnreadReactions" {
		t.Fatalf("в канал пришёл @type %q", update["@type"])
	}
	if update["chat_id"] != float64(5) || update["message_id"] != float64(9) {
		t.Fatalf("апдейт потерял идентификаторы: %+v", update)
	}
	if update["unread_reaction_count"] != float64(3) {
		t.Fatalf("апдейт потерял счётчик: %+v", update)
	}
}

// TestUnreadReactionUpdateReachesZeroToo — сброс счётчика в ноль обязан доезжать
// тем же каналом: TDLib шлёт updateMessageUnreadReactions и когда реакции
// становятся увиденными, и без этого апдейта маркер в карточке горел бы вечно.
func TestUnreadReactionUpdateReachesZeroToo(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(unreadReactionsUpdateJSON(0))

	update := waitUpdate(t, client.UnreadReactionMessageUpdates())
	if update["unread_reaction_count"] != float64(0) {
		t.Fatalf("обнуление не дошло: %+v", update)
	}
}

// TestUnreadReactionUpdateDoesNotLeakIntoOtherChannels — апдейт о непрочитанных
// реакциях не должен попадать в каналы новых сообщений и счётчиков реакций. Обе
// ветки стоят в общей простыне if'ов рядом, и соседние @type легко перепутать
// местами: тогда либо лента вставила бы пустышку вместо апдейта, либо маркер
// ездил бы на счётчиках реакций.
func TestUnreadReactionUpdateDoesNotLeakIntoOtherChannels(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(unreadReactionsUpdateJSON(2))
	waitUpdate(t, client.UnreadReactionMessageUpdates())

	select {
	case leaked := <-client.MessageUpdates():
		t.Fatalf("апдейт о непрочитанных реакциях попал в канал новых сообщений: %+v", leaked)
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case leaked := <-client.MessageInteractionUpdates():
		t.Fatalf("апдейт о непрочитанных реакциях попал в канал счётчиков реакций: %+v", leaked)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestInteractionUpdateDoesNotLeakIntoUnreadReactions — и обратная сторона того же
// риска: счётчики реакций не должны попадать в канал непрочитанных. Это разные
// данные (у всех сообщений против своих непрочитанных), и смешались бы они в
// одном маркере.
func TestInteractionUpdateDoesNotLeakIntoUnreadReactions(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(interactionUpdate())
	waitUpdate(t, client.MessageInteractionUpdates())

	select {
	case leaked := <-client.UnreadReactionMessageUpdates():
		t.Fatalf("апдейт о счётчиках реакций попал в канал непрочитанных: %+v", leaked)
	case <-time.After(50 * time.Millisecond):
	}
}

// deleteMessagesUpdateJSON — updateDeleteMessages в том виде, в каком его
// присылает TDLib. Схема (td_api.h собранной версии, ID 1669252686):
// chat_id:int53 message_ids_:array<int53> is_permanent_:Bool from_cache_:Bool.
func deleteMessagesUpdateJSON(ids ...float64) map[string]interface{} {
	list := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		list = append(list, id)
	}
	return map[string]interface{}{
		"@type":        "updateDeleteMessages",
		"chat_id":      float64(5),
		"message_ids":  list,
		"is_permanent": true,
		"from_cache":   false,
	}
}

// TestDeleteMessagesUpdateReachesItsChannel — апдейт об удалённых сообщениях
// доходит до своего канала.
//
// Проверяется именно МАРШРУТИЗИРОВАНИЕ в receiveLoop. Без ветки апдейт молча
// терялся: цикл отбрасывал всё, на что нет ветки, и удалённое из другого клиента
// сообщение висело бы в ленте до перезапуска. На поведение TUI это не ловится:
// там канал просто пуст.
func TestDeleteMessagesUpdateReachesItsChannel(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(deleteMessagesUpdateJSON(42, 43))

	update := waitUpdate(t, client.DeleteMessagesUpdates())
	if update["@type"] != "updateDeleteMessages" {
		t.Fatalf("в канал пришёл @type %q", update["@type"])
	}
	if update["chat_id"] != float64(5) {
		t.Fatalf("апдейт потерял чат: %+v", update)
	}
	list, ok := update["message_ids"].([]interface{})
	if !ok || len(list) != 2 {
		t.Fatalf("апдейт потерял message_ids: %#v", update["message_ids"])
	}
}

// TestDeleteMessagesUpdateWithEmptyListReachesChannelToo — удалять нечего, но
// апдейт всё равно валидный и обязан доехать: иначе лента осталась бы с
// предыдущим кадром, хотя на самом деле переписка изменилась.
func TestDeleteMessagesUpdateWithEmptyListReachesChannelToo(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(deleteMessagesUpdateJSON())

	update := waitUpdate(t, client.DeleteMessagesUpdates())
	if list, ok := update["message_ids"].([]interface{}); !ok || len(list) != 0 {
		t.Fatalf("пустое удаление дошло битым: %#v", update["message_ids"])
	}
}

// TestDeleteMessagesUpdateDoesNotLeakIntoOtherChannels — апдейт об удалениях не
// должен попадать в каналы новых сообщений и счётчиков реакций. Все ветки стоят
// в общей простыне if'ов рядом, и соседние @type легко переставить местами:
// тогда либо лента вставила бы вместо удаления пустое сообщение, либо счётчики
// реакций ездили бы на удалённых сообщениях.
func TestDeleteMessagesUpdateDoesNotLeakIntoOtherChannels(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(deleteMessagesUpdateJSON(42))
	waitUpdate(t, client.DeleteMessagesUpdates())

	select {
	case leaked := <-client.MessageUpdates():
		t.Fatalf("апдейт об удалениях попал в канал новых сообщений: %+v", leaked)
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case leaked := <-client.MessageInteractionUpdates():
		t.Fatalf("апдейт об удалениях попал в канал счётчиков реакций: %+v", leaked)
	case <-time.After(50 * time.Millisecond):
	}
}

// messageContentUpdateJSON — updateMessageContent в том виде, в каком его
// присылает TDLib. Схема (td_api.h собранной версии, ID 506903332):
// chat_id:int53 message_id:int53 new_content_:messageContent. Именно new_content:
// у апдейта нет ни отправителя, ни даты, ни готового текста на верхнем уровне.
func messageContentUpdateJSON(chatID, messageID float64, text string) map[string]interface{} {
	return map[string]interface{}{
		"@type":      "updateMessageContent",
		"chat_id":    chatID,
		"message_id": messageID,
		"new_content": map[string]interface{}{
			"@type": "messageText",
			"text":  map[string]interface{}{"@type": "formattedText", "text": text},
		},
	}
}

// TestMessageContentUpdateReachesItsChannel — апдейт правки доходит до своего
// канала.
//
// Проверяется именно МАРШРУТИЗИРОВАНИЕ в receiveLoop. Без ветки апдейт молча
// терялся: цикл отбрасывал всё, на что нет ветки, и отредактированное в другом
// клиенте сообщение показывало бы старый текст до перезапуска. На поведение TUI
// это не ловится: там канал просто пуст.
func TestMessageContentUpdateReachesItsChannel(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(messageContentUpdateJSON(5, 42, "новый текст"))

	update := waitUpdate(t, client.MessageContentUpdates())
	if update["@type"] != "updateMessageContent" {
		t.Fatalf("в канал пришёл @type %q", update["@type"])
	}
	if update["chat_id"] != float64(5) || update["message_id"] != float64(42) {
		t.Fatalf("апдейт потерял адрес сообщения: %+v", update)
	}
	content, ok := update["new_content"].(map[string]interface{})
	if !ok {
		t.Fatalf("апдейт потерял new_content: %#v", update["new_content"])
	}
	if content["@type"] != "messageText" {
		t.Fatalf("new_content = %#v, want messageText", content)
	}
}

// TestMessageContentUpdateDoesNotLeakIntoOtherChannels — апдейт правки не должен
// попадать в каналы новых сообщений и удалений. Все ветки стоят в общей простыне
// if'ов рядом, и соседние @type легко переставить местами: тогда правка вставила
// бы в ленту новое сообщение (карточек стало бы на одну больше) или убрала бы
// отредактированное сообщение совсем.
func TestMessageContentUpdateDoesNotLeakIntoOtherChannels(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(messageContentUpdateJSON(5, 42, "новый текст"))
	waitUpdate(t, client.MessageContentUpdates())

	select {
	case leaked := <-client.MessageUpdates():
		t.Fatalf("апдейт правки попал в канал новых сообщений: %+v", leaked)
	case <-time.After(50 * time.Millisecond):
	}
	select {
	case leaked := <-client.DeleteMessagesUpdates():
		t.Fatalf("апдейт правки попал в канал удалений: %+v", leaked)
	case <-time.After(50 * time.Millisecond):
	}
}

// Живость списка чатов: маршрутизация четырёх каналов. Пять @type (updateNewChat,
// updateChatAddedToList, updateChatRemovedFromList и три апдейта позиции) идут в
// четыре канала, причём все три «позиционных» — в ОДИН.

// newChatUpdateJSON — updateNewChat в том виде, в каком его присылает TDLib.
// Схема (td_api.h): updateNewChat chat_:chat, а объект chat несёт id (не chat_id).
func newChatUpdateJSON(chatID float64, title string) map[string]interface{} {
	return map[string]interface{}{
		"@type": "updateNewChat",
		"chat": map[string]interface{}{
			"@type": "chat",
			"id":    chatID,
			"type":  map[string]interface{}{"@type": "chatTypePrivate"},
			"title": title,
		},
	}
}

// chatListUpdateJSON — updateChatAddedToList/updateChatRemovedFromList: только
// chat_id и список, объекта чата апдейт не несёт.
func chatListUpdateJSON(chatID float64, updType, listType string) map[string]interface{} {
	return map[string]interface{}{
		"@type":     updType,
		"chat_id":   chatID,
		"chat_list": map[string]interface{}{"@type": listType},
	}
}

// chatPositionUpdateJSON — три апдейта позиции в их РАЗНЫХ формах: у
// updateChatPosition единственная position_, у двух остальных — массив positions_.
// Смешивание этих форм — ровно та ошибка, которую ловит тест на маршрутизацию:
// updateChatPosition обязан идти в общий канал позиций, а не в канал новых чатов.
func chatPositionUpdateJSON(chatID float64, updType string) map[string]interface{} {
	main := map[string]interface{}{
		"@type": "chatPosition",
		"list":  map[string]interface{}{"@type": "chatListMain"},
		"order": float64(0),
	}
	switch updType {
	case "updateChatPosition":
		return map[string]interface{}{
			"@type":    "updateChatPosition",
			"chat_id":  chatID,
			"position": main,
		}
	case "updateChatLastMessage":
		return map[string]interface{}{
			"@type":        "updateChatLastMessage",
			"chat_id":      chatID,
			"last_message": nil,
			"positions":    []interface{}{main},
		}
	case "updateChatDraftMessage":
		return map[string]interface{}{
			"@type":         "updateChatDraftMessage",
			"chat_id":       chatID,
			"draft_message": nil,
			"positions":     []interface{}{main},
		}
	}
	panic("неизвестный апдейт позиции: " + updType)
}

// drainAndFail — в канале не должно ничего быть. Используется на «не протек в
// соседний канал»: пустой канал через небольшую паузу означает, что апдейт туда
// не ушёл.
func drainAndFail(t *testing.T, ch <-chan map[string]interface{}, what string) {
	t.Helper()
	select {
	case leaked := <-ch:
		t.Fatalf("апдейт протёк в канал %s: %+v", what, leaked)
	case <-time.After(50 * time.Millisecond):
	}
}

// TestNewChatUpdateReachesItsChannel — апдейт о новом чате доходит до своего канала.
//
// Проверяется именно МАРШРУТИЗИРОВЦИЯ: без ветки апдейт молча терялся в
// receiveLoop, и чат, появившийся уже после старта, не знали бы ни названия, ни
// категории — и, главное, по нему никогда не звали бы openChat, а без openChat
// TDLib не присылает по чату живые обновления вовсе («all updates are received
// only for opened chats»). На поведение TUI это не ловится: там канал пуст.
func TestNewChatUpdateReachesItsChannel(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(newChatUpdateJSON(77, "Новый чат"))

	update := waitUpdate(t, client.NewChatUpdates())
	if update["@type"] != "updateNewChat" {
		t.Fatalf("в канал пришёл @type %q", update["@type"])
	}
	chat, ok := update["chat"].(map[string]interface{})
	if !ok {
		t.Fatalf("апдейт потерял объект chat: %#v", update["chat"])
	}
	if chat["id"] != float64(77) || chat["title"] != "Новый чат" {
		t.Fatalf("объект chat искажён: %+v", chat)
	}
}

// TestChatListMembershipUpdatesReachTheirChannels — добавление и удаление из
// списка идут в РАЗНЫЕ каналы. Смешать их — значит обработать удаление как
// добавление (дёрнуть getChat по убранному чату) и наоборот.
func TestChatListMembershipUpdatesReachTheirChannels(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(chatListUpdateJSON(5, "updateChatAddedToList", "chatListMain"))
	added := waitUpdate(t, client.ChatAddedToListUpdates())
	if added["@type"] != "updateChatAddedToList" || added["chat_id"] != float64(5) {
		t.Fatalf("в канал добавления пришёл %+v", added)
	}

	client.routeUpdate(chatListUpdateJSON(5, "updateChatRemovedFromList", "chatListMain"))
	removed := waitUpdate(t, client.ChatRemovedFromListUpdates())
	if removed["@type"] != "updateChatRemovedFromList" || removed["chat_id"] != float64(5) {
		t.Fatalf("в канал удаления пришёл %+v", removed)
	}

	drainAndFail(t, client.ChatAddedToListUpdates(), "добавления")
	drainAndFail(t, client.ChatRemovedFromListUpdates(), "удаления")
}

// TestAllPositionUpdatesShareOneChannel — все три апдейта позиции идут в ОДИН
// канал. Это не симметрия ради симметрии: по документации updateChatPosition
// «An updateChatLastMessage or updateChatDraftMessage update might be sent instead
// of the update», то есть это взаимозаменяемые способы сообщить об изменении
// позиции, а не три апдейта на одно событие. Сузить обработку до одного типа
// нельзя — тогда часть событий потеряла бы позицию молча.
func TestAllPositionUpdatesShareOneChannel(t *testing.T) {
	for _, updType := range []string{"updateChatPosition", "updateChatLastMessage", "updateChatDraftMessage"} {
		t.Run(updType, func(t *testing.T) {
			client := routingTestClient()
			t.Cleanup(client.Close)

			client.routeUpdate(chatPositionUpdateJSON(5, updType))

			update := waitUpdate(t, client.ChatPositionUpdates())
			if update["@type"] != updType {
				t.Fatalf("в общий канал позиций пришёл @type %q, want %q", update["@type"], updType)
			}
			if update["chat_id"] != float64(5) {
				t.Fatalf("апдейт потерял chat_id: %+v", update)
			}
		})
	}
}

// TestChatListUpdatesDoNotLeakIntoOtherChannels — четыре новых канала стоят в
// общей простыне if'ов рядом с уже существующими, и соседние @type легко
// переставить местами. Особенно опасен updateChatPosition: он несёт одну
// позицию, а updateChatLastMessage — массив, и перестановка веток означала бы
// разбор чужой формы (и, наоборот, потерю апдейта целиком).
func TestChatListUpdatesDoNotLeakIntoOtherChannels(t *testing.T) {
	cases := []struct {
		name   string
		update map[string]interface{}
	}{
		{name: "новый чат", update: newChatUpdateJSON(5, "Аня")},
		{name: "добавлен в список", update: chatListUpdateJSON(5, "updateChatAddedToList", "chatListMain")},
		{name: "убран из списка", update: chatListUpdateJSON(5, "updateChatRemovedFromList", "chatListMain")},
		{name: "позиция", update: chatPositionUpdateJSON(5, "updateChatPosition")},
		{name: "последнее сообщение", update: chatPositionUpdateJSON(5, "updateChatLastMessage")},
		{name: "черновик", update: chatPositionUpdateJSON(5, "updateChatDraftMessage")},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client := routingTestClient()
			t.Cleanup(client.Close)

			client.routeUpdate(testCase.update)
			switch testCase.name {
			case "новый чат":
				waitUpdate(t, client.NewChatUpdates())
			case "добавлен в список":
				waitUpdate(t, client.ChatAddedToListUpdates())
			case "убран из списка":
				waitUpdate(t, client.ChatRemovedFromListUpdates())
			default:
				waitUpdate(t, client.ChatPositionUpdates())
			}

			// Ни один из четырёх новых каналов, кроме своего, не должен получить
			// этот апдейт: иначе updateChatPosition был бы обработан как «чат
			// убран», а updateNewChat — как «добавлен в список» с чужим форматом.
			for _, other := range []struct {
				what string
				ch   <-chan map[string]interface{}
			}{
				{"новых чатов", client.NewChatUpdates()},
				{"добавления в список", client.ChatAddedToListUpdates()},
				{"удаления из списка", client.ChatRemovedFromListUpdates()},
				{"позиций", client.ChatPositionUpdates()},
				{"новых сообщений", client.MessageUpdates()},
				{"правок сообщений", client.MessageContentUpdates()},
			} {
				select {
				case leaked := <-other.ch:
					t.Fatalf("апдейт протёк в канал %s: %+v", other.what, leaked)
				case <-time.After(20 * time.Millisecond):
				}
			}
		})
	}
}

// TestChatListUpdatesAreNotTreatedAsRequestResponse — апдейты не привязаны к
// запросу, поэтому routeUpdate обязан вернуть true и не отдавать их в
// pending-канал по @extra. Иначе чужой Send ждал бы апдейт как ответ на свой
// запрос и завис бы на все 10 секунд таймаута.
func TestChatListUpdatesAreNotTreatedAsRequestResponse(t *testing.T) {
	cases := []struct {
		name   string
		update map[string]interface{}
	}{
		{name: "новый чат", update: newChatUpdateJSON(5, "Аня")},
		{name: "добавлен в список", update: chatListUpdateJSON(5, "updateChatAddedToList", "chatListMain")},
		{name: "убран из списка", update: chatListUpdateJSON(5, "updateChatRemovedFromList", "chatListMain")},
		{name: "позиция", update: chatPositionUpdateJSON(5, "updateChatPosition")},
		{name: "последнее сообщение", update: chatPositionUpdateJSON(5, "updateChatLastMessage")},
		{name: "черновик", update: chatPositionUpdateJSON(5, "updateChatDraftMessage")},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client := routingTestClient()
			t.Cleanup(client.Close)

			update := testCase.update
			update["@extra"] = "чужой-запрос"
			if !client.routeUpdate(update) {
				t.Fatalf("апдейт %q не разобран по @type — он ушёл в разбор @extra", update["@type"])
			}
		})
	}
}

// connectionStateUpdateJSON — updateConnectionState в том виде, в каком его
// присылает TDLib. Схема (td_api.h собранной версии, ID 1469292078):
// state:ConnectionState = Update, и все пять классов ConnectionState — пустые
// структуры, то есть внутри state лежит только @type.
func connectionStateUpdateJSON(state string) map[string]interface{} {
	return map[string]interface{}{
		"@type": "updateConnectionState",
		"state": map[string]interface{}{"@type": state},
	}
}

// TestConnectionStateUpdateReachesItsChannel — апдейт о состоянии соединения
// доходит до своего канала.
//
// Проверяется именно МАРШРУТИЗАЦИЯ в receiveLoop. Без ветки апдейт молча
// терялся: цикл отбрасывал всё, на что нет ветки, и пропадание сети выглядело
// бы как обычная тишина в чате — сообщения просто перестают приходить, и
// отличить одно от другого нечем. На поведение TUI это не ловится: там канал
// пуст.
func TestConnectionStateUpdateReachesItsChannel(t *testing.T) {
	states := []string{
		"connectionStateWaitingForNetwork",
		"connectionStateConnectingToProxy",
		"connectionStateConnecting",
		"connectionStateUpdating",
		"connectionStateReady",
	}
	for _, state := range states {
		t.Run(state, func(t *testing.T) {
			client := routingTestClient()
			t.Cleanup(client.Close)

			client.routeUpdate(connectionStateUpdateJSON(state))

			update := waitUpdate(t, client.ConnectionStateUpdates())
			if update["@type"] != "updateConnectionState" {
				t.Fatalf("в канал пришёл @type %q", update["@type"])
			}
			inner, ok := update["state"].(map[string]interface{})
			if !ok {
				t.Fatalf("апдейт потерял объект state: %#v", update["state"])
			}
			if inner["@type"] != state {
				t.Fatalf("состояние искажено: %+v", inner)
			}
		})
	}
}

// TestConnectionStateUpdateDoesNotLeakIntoOtherChannels — апдейт о соединении не
// должен проходить в каналы сообщений и авторизации. Обе ветки стоят в общей
// простыне if'ов рядом, и соседние @type легко перепутать: тогда лента вставила
// бы в переписку пустышку вместо апдейта о сети.
func TestConnectionStateUpdateDoesNotLeakIntoOtherChannels(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(connectionStateUpdateJSON("connectionStateWaitingForNetwork"))
	waitUpdate(t, client.ConnectionStateUpdates())

	drainAndFail(t, client.MessageUpdates(), "новых сообщений")
	drainAndFail(t, client.AuthUpdates(), "авторизации")
}

// TestConnectionStateUpdateIsNotTreatedAsRequestResponse — апдейт не привязан к
// запросу, поэтому routeUpdate обязан вернуть true и не отдать его в
// pending-канал по @extra. Иначе чужой Send ждал бы его как ответ на свой
// запрос и завис бы на все 10 секунд таймаута.
func TestConnectionStateUpdateIsNotTreatedAsRequestResponse(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	update := connectionStateUpdateJSON("connectionStateReady")
	update["@extra"] = "чужой-запрос"
	if !client.routeUpdate(update) {
		t.Fatal("апдейт не разобран по @type — он ушёл в разбор @extra")
	}
	waitUpdate(t, client.ConnectionStateUpdates())

	client.pendingMu.Lock()
	registered, ok := client.pending["чужой-запрос"]
	client.pendingMu.Unlock()
	if ok {
		close(registered)
		t.Fatal("апдейт протёк в pending-канал чужого запроса")
	}
}

// userUpdateJSON — updateUser в том виде, в каком его присылает TDLib. Схема
// (td_api.h собранной версии, ID 1183394041): у апдейта РОВНО одно поле user_,
// внутри которого лежит объект user целиком. Отдельного user_id на верхнем
// уровне нет — а именно на такой разбор и рассчитан парсер.
func userUpdateJSON(userID float64, first, last string) map[string]interface{} {
	return map[string]interface{}{
		"@type": "updateUser",
		"user": map[string]interface{}{
			"@type":      "user",
			"id":         userID,
			"first_name": first,
			"last_name":  last,
		},
	}
}

// TestUserUpdateReachesItsChannel — апдейт о переименовании доходит до своего
// канала.
//
// Проверяется именно МАРШРУТИЗАЦИЯ в receiveLoop. Без ветки апдейт молча
// терялся: цикл отбрасывал всё, на что нет ветки, и имя отправителя, зафиксированное
// при разборе карточки, оставалось бы прежним до перезапуска. На поведение TUI
// это не ловится: там канал просто пуст.
func TestUserUpdateReachesItsChannel(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(userUpdateJSON(42, "Иван", "Петров"))

	update := waitUpdate(t, client.UserUpdates())
	if update["@type"] != "updateUser" {
		t.Fatalf("в канал пришёл @type %q", update["@type"])
	}
	user, ok := update["user"].(map[string]interface{})
	if !ok {
		t.Fatalf("апдейт потерял объект user: %#v", update["user"])
	}
	if user["id"] != float64(42) {
		t.Fatalf("апдейт потерял id пользователя: %+v", user)
	}
}

// TestUserUpdateWithoutNamesStillReachesChannel — апдейт про пользователя без имён
// (удалённый аккаунт) обязан доехать тем же каналом: лента тогда покажет у его
// карточек «user#<id>», а не рассинхронизированное с TDLib имя.
func TestUserUpdateWithoutNamesStillReachesChannel(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(userUpdateJSON(42, "", ""))

	update := waitUpdate(t, client.UserUpdates())
	user, ok := update["user"].(map[string]interface{})
	if !ok || user["id"] != float64(42) {
		t.Fatalf("битое переименование дошло не так: %+v", update)
	}
}

// TestUserUpdateDoesNotLeakIntoOtherChannels — апдейт о переименовании не должен
// попадать в каналы сообщений, авторизации и настроек чатов. Ветка стоит в общей
// простыне if'ов рядом с уже существующими, и соседние @type легко перепутать:
// тогда лента вставила бы вместо переименования пустое сообщение.
func TestUserUpdateDoesNotLeakIntoOtherChannels(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(userUpdateJSON(42, "Иван", "Петров"))
	waitUpdate(t, client.UserUpdates())

	drainAndFail(t, client.MessageUpdates(), "новых сообщений")
	drainAndFail(t, client.AuthUpdates(), "авторизации")
	drainAndFail(t, client.ChatTitleUpdates(), "названий чатов")
	drainAndFail(t, client.NewChatUpdates(), "новых чатов")
}

// TestUserUpdateIsNotTreatedAsRequestResponse — апдейт не привязан к запросу,
// поэтому routeUpdate обязан вернуть true и не отдать его в pending-канал по
// @extra. Иначе чужой Send ждал бы его как ответ на свой запрос и завис бы на
// все 10 секунд таймаута.
func TestUserUpdateIsNotTreatedAsRequestResponse(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	update := userUpdateJSON(42, "Иван", "Петров")
	update["@extra"] = "чужой-запрос"
	if !client.routeUpdate(update) {
		t.Fatal("апдейт не разобран по @type — он ушёл в разбор @extra")
	}
	waitUpdate(t, client.UserUpdates())

	client.pendingMu.Lock()
	registered, ok := client.pending["чужой-запрос"]
	client.pendingMu.Unlock()
	if ok {
		close(registered)
		t.Fatal("апдейт протёк в pending-канал чужого запроса")
	}
}

// TestUserUpdatesKeepOtherRoutesIntact — остальные апдейты продолжают идти по своим
// каналам после добавления новой ветки.
func TestUserUpdatesKeepOtherRoutesIntact(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(map[string]interface{}{
		"@type":   "updateNewMessage",
		"message": map[string]interface{}{"@type": "message", "id": float64(1)},
		"chat_id": float64(1),
	})
	if update := waitUpdate(t, client.MessageUpdates()); update["@type"] != "updateNewMessage" {
		t.Fatalf("в канал сообщений пришёл @type %q", update["@type"])
	}

	client.routeUpdate(userUpdateJSON(42, "Иван", "Петров"))
	if update := waitUpdate(t, client.UserUpdates()); update["@type"] != "updateUser" {
		t.Fatalf("в канал переименований пришёл @type %q", update["@type"])
	}
}

// groupCountUpdateJSON — updateBasicGroup/updateSupergroup: оба несут одно и то
// же нужное клиенту поле member_count, поэтому проверяются оба сразу и на
// одном канале. member_count кладётся рядом с id, а не вместо него: потеря
// самого числа — самый вероятный дефект разбора, и пустой объект его бы не
// показал.
func groupCountUpdateJSON(updateType, objectType, objectField string, id, count float64) map[string]interface{} {
	return map[string]interface{}{
		"@type": updateType,
		objectField: map[string]interface{}{
			"@type":        objectType,
			"id":           id,
			"member_count": count,
		},
	}
}

// TestGroupMemberCountUpdatesReachTheirChannel — оба апдейта о числе участников
// доходят до своего канала.
//
// Проверяется именно МАРШРУТИЗАЦИЯ в receiveLoop. Без ветки апдейт молча
// терялся: цикл отбрасывал всё, на что нет ветки, и стена показывала бы рядом с
// названием канала пустое место до перезапуска. На поведение TUI это не ловится:
// там канал просто пуст.
func TestGroupMemberCountUpdatesReachTheirChannel(t *testing.T) {
	for _, testCase := range []struct {
		updateType  string
		objectType  string
		objectField string
	}{
		{updateType: "updateBasicGroup", objectType: "basicGroup", objectField: "basic_group"},
		{updateType: "updateSupergroup", objectType: "supergroup", objectField: "supergroup"},
	} {
		t.Run(testCase.updateType, func(t *testing.T) {
			client := routingTestClient()
			t.Cleanup(client.Close)

			client.routeUpdate(groupCountUpdateJSON(testCase.updateType, testCase.objectType, testCase.objectField, 100, 250))

			update := waitUpdate(t, client.GroupMemberCountUpdates())
			if update["@type"] != testCase.updateType {
				t.Fatalf("в канал пришёл @type %q", update["@type"])
			}
			object, ok := update[testCase.objectField].(map[string]interface{})
			if !ok {
				t.Fatalf("апдейт потерял объект %s: %#v", testCase.objectField, update[testCase.objectField])
			}
			if object["member_count"] != float64(250) || object["id"] != float64(100) {
				t.Fatalf("апдейт потерял id или member_count: %+v", object)
			}

			// Ни в один из соседних каналов апдейт протёчь не должен: ветки
			// стоят в общей простыне соседних if'ов, и перепутать @type легко —
			// тогда стена вставила бы в поток сообщений чужой апдейт.
			drainAndFail(t, client.MessageUpdates(), "новых сообщений")
			drainAndFail(t, client.UserUpdates(), "переименований")
			drainAndFail(t, client.UserStatusUpdates(), "смены статуса")
		})
	}
}

// TestUserStatusUpdateReachesItsChannel — смена статуса собеседника доходит до
// своего канала.
//
// Отдельный канал от UserUpdates не для красоты: updateUserStatus приходит на
// КАЖДУЮ смену «в сети ⇄ был(а) недавно» и несёт только статус, тогда как
// updateUser едет пачками на старте и нужен ради имени. Смешанные в одном
// канале они заставили бы ленту разбирать имя у апдейта, где его нет.
func TestUserStatusUpdateReachesItsChannel(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	client.routeUpdate(map[string]interface{}{
		"@type":   "updateUserStatus",
		"user_id": float64(42),
		"status":  map[string]interface{}{"@type": "userStatusOnline"},
	})

	update := waitUpdate(t, client.UserStatusUpdates())
	if update["@type"] != "updateUserStatus" {
		t.Fatalf("в канал пришёл @type %q", update["@type"])
	}
	status, ok := update["status"].(map[string]interface{})
	if !ok || status["@type"] != "userStatusOnline" {
		t.Fatalf("апдейт потерял объект status: %#v", update["status"])
	}

	drainAndFail(t, client.UserUpdates(), "переименований")
	drainAndFail(t, client.GroupMemberCountUpdates(), "числа участников")
	drainAndFail(t, client.MessageUpdates(), "новых сообщений")
}

// Буфер userUpdates вырос из-за пачки updateUser на старте: TDLib шлёт по
// апдейту на каждого пользователя из списка чатов (живой прогон задачи 0163 —
// 230 апдейтов на 200 чатов), а отправка неблокирующая и С ДРОПОМ. При прежнем
// буфере 5 до стены доходили бы первые пять, и у подавляющего большинства
// карточек личных диалогов статус просто не появился бы.
//
// Тест именно на размер: поведение при переполнении не проверяется (дропнуть
// апдейт — осознанное решение, а не баг), но вот тихое возвращение буфера к
// старым 5 симптом был бы неочевидный — карточки молчали бы без статуса.
func TestUserUpdatesBufferCoversStartupBurst(t *testing.T) {
	client := routingTestClient()
	t.Cleanup(client.Close)

	// Нижняя граница пачки, измеренной вживую: апдейт на каждого пользователя
	// списка чатов. Взята с запасом, ровно как сам буфер.
	const startupBurst = 230
	if got := cap(client.UserUpdates()); got < startupBurst {
		t.Fatalf("буфер userUpdates = %d, меньше измеренной пачки на старте %d — "+
			"часть статусов потеряется с дропом", got, startupBurst)
	}
}
