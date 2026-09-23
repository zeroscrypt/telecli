package auth

import (
	"context"
	"fmt"
)

type Chat struct {
	ID                      int64
	Title                   string
	UnreadCount             int32
	LastReadOutboxMessageID int64
	IsGroup                 bool // true — группа/канал (leaveChat), false — личный/секретный чат (deleteChatHistory)
}

// GetChats загружает до limit чатов из указанного списка (chatList — сырой
// JSON-объект TDLib ChatList: {"@type": "chatListMain"} для "Все чаты" или
// {"@type": "chatListFolder", "chat_folder_id": <id>} для конкретной папки).
func GetChats(ctx context.Context, client TDClientInterface, chatList map[string]interface{}, limit int) ([]Chat, error) {
	// Актуальная схема (сверено с td_api.tl текущего master TDLib): offset_order/offset_chat_id
	// в getChats больше нет, вместо них — chat_list. Официальная документация прямо говорит, что
	// getChats "for informational purposes only" и ничего не вернёт, пока список не загружен через
	// loadChats — поэтому сначала грузим (updates обрабатываются в receiveLoop и здесь не нужны,
	// нам достаточно самого факта, что после loadChats у TDLib есть что вернуть через getChats).
	// 404 от loadChats означает "все чаты уже загружены" — не ошибка для нас в этом контексте.
	// Ошибку loadChats сознательно игнорируем (в т.ч. документированный 404 "все чаты уже
	// загружены" — не ошибка для нас): не действуем по-разному в зависимости от причины,
	// просто читаем то, что успело оказаться в памяти TDLib, через getChats ниже.
	_, _ = client.Send(ctx, map[string]interface{}{
		"@type":     "loadChats",
		"chat_list": chatList,
		"limit":     limit,
	})

	request := map[string]interface{}{
		"@type":     "getChats",
		"chat_list": chatList,
		"limit":     limit,
	}

	resp, err := client.Send(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("getChats failed: %w", err)
	}

	if resp["@type"] != "chats" {
		return nil, fmt.Errorf("unexpected response type: %v", resp["@type"])
	}

	chatIDsRaw, ok := resp["chat_ids"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid chat_ids format")
	}

	var chats []Chat
	for _, idRaw := range chatIDsRaw {
		chatID, ok := idRaw.(float64)
		if !ok {
			continue
		}
		chatResp, err := client.Send(ctx, map[string]interface{}{
			"@type":   "getChat",
			"chat_id": int64(chatID),
		})
		if err != nil {
			continue
		}
		title, _ := chatResp["title"].(string)
		unreadCount, _ := chatResp["unread_count"].(float64)
		lastReadOutboxMessageID, _ := chatResp["last_read_outbox_message_id"].(float64)
		isGroup := false
		if typeRaw, ok := chatResp["type"].(map[string]interface{}); ok {
			switch typeRaw["@type"] {
			case "chatTypeBasicGroup", "chatTypeSupergroup":
				isGroup = true
			}
		}
		chats = append(chats, Chat{
			ID:                      int64(chatID),
			Title:                   title,
			UnreadCount:             int32(unreadCount),
			LastReadOutboxMessageID: int64(lastReadOutboxMessageID),
			IsGroup:                 isGroup,
		})
	}

	return chats, nil
}
