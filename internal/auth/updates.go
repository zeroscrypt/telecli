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
