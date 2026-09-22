package auth

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestWaitForSendConfirmationSuccess(t *testing.T) {
	mock := newMockTDClient()
	mock.sendStatusCh <- map[string]interface{}{
		"@type":          "updateMessageSendSucceeded",
		"old_message_id": float64(42),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := WaitForSendConfirmation(ctx, mock, 42); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWaitForSendConfirmationFailed(t *testing.T) {
	mock := newMockTDClient()
	mock.sendStatusCh <- map[string]interface{}{
		"@type":          "updateMessageSendFailed",
		"old_message_id": float64(7),
		"error": map[string]interface{}{
			"@type":   "error",
			"code":    400,
			"message": "AUTH_KEY_UNREGISTERED",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := WaitForSendConfirmation(ctx, mock, 7)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "AUTH_KEY_UNREGISTERED") {
		t.Errorf("expected error to contain failure message, got %v", err)
	}
}

func TestWaitForSendConfirmationIgnoresOtherMessages(t *testing.T) {
	mock := newMockTDClient()
	mock.sendStatusCh <- map[string]interface{}{
		"@type":          "updateMessageSendSucceeded",
		"old_message_id": float64(100500),
	}
	mock.sendStatusCh <- map[string]interface{}{
		"@type":          "updateMessageSendSucceeded",
		"old_message_id": float64(42),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Апдейт про чужое сообщение приходит первым и должен быть проигнорирован,
	// а подтверждение засчитано именно по второму (с совпадающим old_message_id).
	if err := WaitForSendConfirmation(ctx, mock, 42); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestWaitForSendConfirmationTimeout(t *testing.T) {
	mock := newMockTDClient()

	// Уже истёкший контекст + пустой канал — ожидаем ошибку о таймауте,
	// а не зависание или панику.
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()

	err := WaitForSendConfirmation(ctx, mock, 42)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "не дождались подтверждения отправки") {
		t.Errorf("expected timeout error, got %v", err)
	}
}
