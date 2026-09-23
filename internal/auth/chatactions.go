package auth

import (
	"context"
	"fmt"
)

// LeaveChat покидает группу/канал (сам чат остаётся у остальных участников).
func LeaveChat(ctx context.Context, client TDClientInterface, chatID int64) error {
	_, err := client.Send(ctx, map[string]interface{}{
		"@type":   "leaveChat",
		"chat_id": chatID,
	})
	if err != nil {
		return fmt.Errorf("leaveChat failed: %w", err)
	}
	return nil
}

// DeleteChatHistory удаляет личный/секретный чат: всегда убирает его из
// списка (remove_from_chat_list=true), revoke=true — также у собеседника,
// revoke=false — только у себя.
func DeleteChatHistory(ctx context.Context, client TDClientInterface, chatID int64, revoke bool) error {
	_, err := client.Send(ctx, map[string]interface{}{
		"@type":                 "deleteChatHistory",
		"chat_id":               chatID,
		"remove_from_chat_list": true,
		"revoke":                revoke,
	})
	if err != nil {
		return fmt.Errorf("deleteChatHistory failed: %w", err)
	}
	return nil
}
