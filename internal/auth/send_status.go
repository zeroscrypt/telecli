package auth

import (
	"context"
	"errors"
	"fmt"
)

// WaitForSendConfirmation блокируется, пока не придёт updateMessageSendSucceeded
// или updateMessageSendFailed с old_message_id == messageID, либо пока не
// истечёт ctx. Нужна для неинтерактивных точек входа (CLI send): процесс
// завершается сразу после main(), убивая ещё не закончившуюся фоновую
// отправку TDLib, если явно не дождаться подтверждения (sendMessage
// возвращается быстро, реальная передача — в фоне уже после ответа).
func WaitForSendConfirmation(ctx context.Context, client TDClientInterface, messageID int64) error {
	ch := client.SendStatusUpdates()
	for {
		select {
		case upd, ok := <-ch:
			if !ok {
				return errors.New("tdlib: send-status channel closed before confirmation")
			}
			oldID, _ := upd["old_message_id"].(float64)
			if int64(oldID) != messageID {
				continue // апдейт про другое сообщение — не наше, ждём дальше
			}
			switch upd["@type"] {
			case "updateMessageSendSucceeded":
				return nil
			case "updateMessageSendFailed":
				errMsg := ""
				if errObj, ok := upd["error"].(map[string]interface{}); ok {
					if m, ok := errObj["message"].(string); ok {
						errMsg = m
					}
				}
				return fmt.Errorf("tdlib: отправка не удалась: %s", errMsg)
			default:
				continue
			}
		case <-ctx.Done():
			return fmt.Errorf("не дождались подтверждения отправки: %w", ctx.Err())
		}
	}
}
