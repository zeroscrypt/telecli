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
