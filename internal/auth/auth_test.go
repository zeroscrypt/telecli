package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"telecli/internal/config"
)

type mockTDClient struct {
	responses    []map[string]interface{}
	authCh       chan map[string]interface{}
	sendStatusCh chan map[string]interface{}
	sendCount    int
	requests     []map[string]interface{}
}

func newMockTDClient() *mockTDClient {
	return &mockTDClient{
		authCh:       make(chan map[string]interface{}, 10),
		sendStatusCh: make(chan map[string]interface{}, 10),
	}
}

func (m *mockTDClient) Send(ctx context.Context, request map[string]interface{}) (map[string]interface{}, error) {
	m.sendCount++
	m.requests = append(m.requests, request)
	if len(m.responses) > 0 {
		resp := m.responses[0]
		m.responses = m.responses[1:]
		if resp["@type"] == "error" {
			return nil, errors.New(resp["message"].(string))
		}
		return resp, nil
	}
	return nil, errors.New("no more responses")
}

func (m *mockTDClient) Execute(request map[string]interface{}) (map[string]interface{}, error) {
	return nil, errors.New("not implemented")
}

func (m *mockTDClient) AuthUpdates() <-chan map[string]interface{} {
	return m.authCh
}

func (m *mockTDClient) MessageUpdates() <-chan map[string]interface{} {
	return nil
}

func (m *mockTDClient) SendStatusUpdates() <-chan map[string]interface{} {
	return m.sendStatusCh
}

func (m *mockTDClient) ChatFolderUpdates() <-chan map[string]interface{} {
	return nil
}

func (m *mockTDClient) ChatReadInboxUpdates() <-chan map[string]interface{} {
	return nil
}

func (m *mockTDClient) ChatReadOutboxUpdates() <-chan map[string]interface{} {
	return nil
}

func (m *mockTDClient) ChatTitleUpdates() <-chan map[string]interface{} {
	return nil
}

func (m *mockTDClient) ChatNotificationSettingsUpdates() <-chan map[string]interface{} {
	return nil
}

func (m *mockTDClient) UnreadCountUpdates() <-chan map[string]interface{} {
	return nil
}

func (m *mockTDClient) UnreadChatCountUpdates() <-chan map[string]interface{} {
	return nil
}

func (m *mockTDClient) MessageInteractionUpdates() <-chan map[string]interface{} {
	return nil
}

func (m *mockTDClient) UnreadReactionMessageUpdates() <-chan map[string]interface{} {
	return nil
}

func (m *mockTDClient) DeleteMessagesUpdates() <-chan map[string]interface{} {
	return nil
}
func (m *mockTDClient) MessageContentUpdates() <-chan map[string]interface{} {
	return nil
}

func (m *mockTDClient) NewChatUpdates() <-chan map[string]interface{} {
	return nil
}
func (m *mockTDClient) ChatAddedToListUpdates() <-chan map[string]interface{} {
	return nil
}
func (m *mockTDClient) ChatRemovedFromListUpdates() <-chan map[string]interface{} {
	return nil
}
func (m *mockTDClient) ChatPositionUpdates() <-chan map[string]interface{} {
	return nil
}
func (m *mockTDClient) ConnectionStateUpdates() <-chan map[string]interface{} {
	return nil
}
func (m *mockTDClient) UserUpdates() <-chan map[string]interface{} {
	return nil
}
func (m *mockTDClient) GroupMemberCountUpdates() <-chan map[string]interface{} { return nil }
func (m *mockTDClient) UserStatusUpdates() <-chan map[string]interface{}       { return nil }

func (m *mockTDClient) Close() {
	close(m.authCh)
}

type mockPrompter struct {
	phone string
	code  string
	pass  string
	err   error
}

func newMockPrompter(phone, code, pass string) *mockPrompter {
	return &mockPrompter{phone: phone, code: code, pass: pass}
}

func (m *mockPrompter) PhoneNumber() (string, error) {
	return m.phone, m.err
}

func (m *mockPrompter) Code() (string, error) {
	return m.code, m.err
}

func (m *mockPrompter) Password() (string, error) {
	return m.pass, m.err
}

func sendAuthUpdate(m *mockTDClient, stateType string) {
	m.authCh <- map[string]interface{}{
		"@type": "updateAuthorizationState",
		"authorization_state": map[string]interface{}{
			"@type": stateType,
		},
	}
}

func TestAuthenticateSuccessNo2FA(t *testing.T) {
	mockClient := newMockTDClient()
	mockClient.responses = []map[string]interface{}{
		{"@type": "ok"},
		{"@type": "ok"},
		{"@type": "ok"},
	}
	prompter := newMockPrompter("+1234567890", "12345", "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		sendAuthUpdate(mockClient, "authorizationStateWaitTdlibParameters")
		sendAuthUpdate(mockClient, "authorizationStateWaitPhoneNumber")
		sendAuthUpdate(mockClient, "authorizationStateWaitCode")
		sendAuthUpdate(mockClient, "authorizationStateReady")
	}()

	err := Authenticate(ctx, mockClient, config.Credentials{APIID: 12345, APIHash: "test_hash"}, prompter)
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if mockClient.sendCount != 3 {
		t.Errorf("expected 3 sends, got %d", mockClient.sendCount)
	}
}

func TestAuthenticateSuccessWith2FA(t *testing.T) {
	mockClient := newMockTDClient()
	mockClient.responses = []map[string]interface{}{
		{"@type": "ok"},
		{"@type": "ok"},
		{"@type": "ok"},
		{"@type": "ok"},
	}
	prompter := newMockPrompter("+1234567890", "12345", "password123")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		sendAuthUpdate(mockClient, "authorizationStateWaitTdlibParameters")
		sendAuthUpdate(mockClient, "authorizationStateWaitPhoneNumber")
		sendAuthUpdate(mockClient, "authorizationStateWaitCode")
		sendAuthUpdate(mockClient, "authorizationStateWaitPassword")
		sendAuthUpdate(mockClient, "authorizationStateReady")
	}()

	err := Authenticate(ctx, mockClient, config.Credentials{APIID: 12345, APIHash: "test_hash"}, prompter)
	if err != nil {
		t.Fatalf("Authenticate failed: %v", err)
	}
	if mockClient.sendCount != 4 {
		t.Errorf("expected 4 sends, got %d", mockClient.sendCount)
	}
}

func TestAuthenticateErrorOnCode(t *testing.T) {
	mockClient := newMockTDClient()
	mockClient.responses = []map[string]interface{}{
		{"@type": "ok"},
		{"@type": "ok"},
		{"@type": "error", "code": 400, "message": "CODE_INVALID"},
	}
	prompter := newMockPrompter("+1234567890", "wrong_code", "")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	go func() {
		sendAuthUpdate(mockClient, "authorizationStateWaitTdlibParameters")
		sendAuthUpdate(mockClient, "authorizationStateWaitPhoneNumber")
		sendAuthUpdate(mockClient, "authorizationStateWaitCode")
	}()

	err := Authenticate(ctx, mockClient, config.Credentials{APIID: 12345, APIHash: "test_hash"}, prompter)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "CODE_INVALID") && !contains(err.Error(), "checkAuthenticationCode") {
		t.Errorf("unexpected error: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || contains(s[1:], substr)))
}

// TestAuthenticateUnsupportedStateReturnsError — регрессия на пробел из аудита 0133:
// пустая ветка default заставляла цикл вечно читать AuthUpdates() без единого
// сообщения (номер без аккаунта в Telegram → authorizationStateWaitRegistration).
// Тест идёт по всем пяти непокрытым состояниям из td_api.h и по одному
// заведомо несуществующему — все должны дать ошибку с именем состояния.
//
// Ключевое: контекст с коротким таймаутом. Если ветка default снова станет пустой,
// цикл не завершится сам — тест упадёт по таймауту с понятным сообщением,
// а не зависнет намертво.
func TestAuthenticateUnsupportedStateReturnsError(t *testing.T) {
	cases := []string{
		"authorizationStateWaitRegistration",
		"authorizationStateWaitEmailAddress",
		"authorizationStateWaitEmailCode",
		"authorizationStateWaitOtherDeviceConfirmation",
		"authorizationStateWaitPremiumPurchase",
		"authorizationStateSomethingFromTheFuture",
	}

	for _, stateType := range cases {
		t.Run(stateType, func(t *testing.T) {
			mockClient := newMockTDClient()
			prompter := newMockPrompter("+1234567890", "12345", "password123")

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			go sendAuthUpdate(mockClient, stateType)

			err := Authenticate(ctx, mockClient, config.Credentials{APIID: 12345, APIHash: "test_hash"}, prompter)
			if err == nil {
				t.Fatalf("%s: ожидалась ошибка, получено nil (цикл завис на состоянии)", stateType)
			}
			if errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("%s: Authenticate уперся в таймаут вместо явной ошибки — ветка default снова пустая", stateType)
			}
			if !contains(err.Error(), stateType) {
				t.Errorf("%s: ошибка должна называть состояние по имени, получено: %v", stateType, err)
			}
			if mockClient.sendCount != 0 {
				t.Errorf("%s: на неподдерживаемом состоянии ничего отправлять нельзя, отправлено %d", stateType, mockClient.sendCount)
			}
		})
	}
}

// TestAuthenticateHandlesEveryExplicitState — все шесть состояний, перечисленных
// в switch явно (четыре Wait* + Ready + Closed), продолжают вести себя как раньше:
// задача меняет только ветку default.
func TestAuthenticateHandlesEveryExplicitState(t *testing.T) {
	t.Run("полный путь до Ready", func(t *testing.T) {
		mockClient := newMockTDClient()
		mockClient.responses = []map[string]interface{}{
			{"@type": "ok"},
			{"@type": "ok"},
			{"@type": "ok"},
			{"@type": "ok"},
		}
		prompter := newMockPrompter("+1234567890", "12345", "password123")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go func() {
			for _, stateType := range []string{
				"authorizationStateWaitTdlibParameters",
				"authorizationStateWaitPhoneNumber",
				"authorizationStateWaitCode",
				"authorizationStateWaitPassword",
				"authorizationStateReady",
			} {
				sendAuthUpdate(mockClient, stateType)
			}
		}()

		err := Authenticate(ctx, mockClient, config.Credentials{APIID: 12345, APIHash: "test_hash"}, prompter)
		if err != nil {
			t.Fatalf("Authenticate failed: %v", err)
		}
		if mockClient.sendCount != 4 {
			t.Errorf("ожидалось 4 отправки (setTdlibParameters, телефон, код, пароль), получено %d", mockClient.sendCount)
		}
	})

	t.Run("Closed", func(t *testing.T) {
		mockClient := newMockTDClient()
		prompter := newMockPrompter("", "", "")

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		go sendAuthUpdate(mockClient, "authorizationStateClosed")

		err := Authenticate(ctx, mockClient, config.Credentials{APIID: 12345, APIHash: "test_hash"}, prompter)
		if err == nil {
			t.Fatal("ожидалась ошибка на authorizationStateClosed")
		}
		if !contains(err.Error(), "authorization closed") {
			t.Errorf("неожиданная ошибка: %v", err)
		}
		if mockClient.sendCount != 0 {
			t.Errorf("на Closed ничего отправлять нельзя, отправлено %d", mockClient.sendCount)
		}
	})
}
