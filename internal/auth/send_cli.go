package auth

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// ResolveTarget превращает CLI-адресат (@username, username без @, chat_id
// числом, или "me"/"Me"/"ME") в chat_id.
func ResolveTarget(ctx context.Context, client TDClientInterface, target string) (int64, error) {
	target = strings.TrimPrefix(target, "@")
	if strings.EqualFold(target, "me") {
		resp, err := client.Send(ctx, map[string]interface{}{"@type": "getMe"})
		if err != nil {
			return 0, fmt.Errorf("getMe failed: %w", err)
		}
		id, ok := resp["id"].(float64)
		if !ok {
			return 0, fmt.Errorf("unexpected getMe response")
		}
		return int64(id), nil
	}
	if chatID, err := strconv.ParseInt(target, 10, 64); err == nil {
		return chatID, nil
	}
	resp, err := client.Send(ctx, map[string]interface{}{
		"@type":    "searchPublicChat",
		"username": target,
	})
	if err != nil {
		return 0, fmt.Errorf("searchPublicChat failed: %w", err)
	}
	id, ok := resp["id"].(float64)
	if !ok {
		return 0, fmt.Errorf("unexpected searchPublicChat response")
	}
	return int64(id), nil
}

// SendFile отправляет локальный файл документом (inputMessageDocument), с
// опциональной подписью caption, в чат chatID. Переиспользует parseMessage
// для разбора ответа (та же схема message, что у GetMessages/SendMessage).
func SendFile(ctx context.Context, client TDClientInterface, chatID int64, path string, caption string) (Message, error) {
	content := map[string]interface{}{
		"@type": "inputMessageDocument",
		"document": map[string]interface{}{
			"@type": "inputDocument",
			"document": map[string]interface{}{
				"@type": "inputFileLocal",
				"path":  path,
			},
		},
	}
	if caption != "" {
		content["caption"] = map[string]interface{}{
			"@type": "formattedText",
			"text":  caption,
		}
	}
	resp, err := client.Send(ctx, map[string]interface{}{
		"@type":                 "sendMessage",
		"chat_id":               chatID,
		"input_message_content": content,
	})
	if err != nil {
		return Message{}, fmt.Errorf("sendMessage (document) failed: %w", err)
	}
	if resp["@type"] != "message" {
		return Message{}, fmt.Errorf("unexpected response type: %v", resp["@type"])
	}
	return parseMessage(ctx, client, resp), nil
}
