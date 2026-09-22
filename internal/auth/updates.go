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
