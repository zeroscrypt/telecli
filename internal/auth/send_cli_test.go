package auth

import (
	"context"
	"testing"
)

func TestResolveTargetByUsername(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "chat", "id": float64(123), "title": "telegram"},
	}

	got, err := ResolveTarget(context.Background(), mock, "telegram")
	if err != nil {
		t.Fatalf("ResolveTarget failed: %v", err)
	}
	if got != 123 {
		t.Errorf("expected chat_id 123, got %d", got)
	}
	if mock.sendCount != 1 {
		t.Errorf("expected 1 send, got %d", mock.sendCount)
	}
	req := mock.requests[0]
	if req["@type"] != "searchPublicChat" || req["username"] != "telegram" {
		t.Errorf("unexpected request: %v", req)
	}
}

func TestResolveTargetStripsAtPrefix(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "chat", "id": float64(123), "title": "telegram"},
	}

	got, err := ResolveTarget(context.Background(), mock, "@telegram")
	if err != nil {
		t.Fatalf("ResolveTarget failed: %v", err)
	}
	if got != 123 {
		t.Errorf("expected chat_id 123, got %d", got)
	}
	req := mock.requests[0]
	if req["username"] != "telegram" {
		t.Errorf("expected username without @, got %v", req["username"])
	}
}

func TestResolveTargetByChatID(t *testing.T) {
	mock := newMockTDClient()

	got, err := ResolveTarget(context.Background(), mock, "12345")
	if err != nil {
		t.Fatalf("ResolveTarget failed: %v", err)
	}
	if got != 12345 {
		t.Errorf("expected chat_id 12345, got %d", got)
	}
	// Числовой адресат резолвится локально — сетевой вызов не нужен вовсе.
	if mock.sendCount != 0 {
		t.Errorf("expected 0 sends for numeric chat_id, got %d", mock.sendCount)
	}
}

func TestResolveTargetMe(t *testing.T) {
	for _, target := range []string{"me", "Me", "ME", "mE"} {
		mock := newMockTDClient()
		mock.responses = []map[string]interface{}{
			{"@type": "user", "id": float64(999)},
		}

		got, err := ResolveTarget(context.Background(), mock, target)
		if err != nil {
			t.Fatalf("ResolveTarget(%q) failed: %v", target, err)
		}
		if got != 999 {
			t.Errorf("ResolveTarget(%q): expected 999, got %d", target, got)
		}
		if mock.sendCount != 1 {
			t.Errorf("ResolveTarget(%q): expected 1 send, got %d", target, mock.sendCount)
		}
		if req := mock.requests[0]; req["@type"] != "getMe" {
			t.Errorf("ResolveTarget(%q): expected getMe request, got %v", target, req)
		}
	}
}

func TestResolveTargetSearchError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "error", "code": 400, "message": "USERNAME_NOT_OCCUPIED"},
	}

	_, err := ResolveTarget(context.Background(), mock, "nobody_here")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "searchPublicChat failed") || !contains(err.Error(), "USERNAME_NOT_OCCUPIED") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestResolveTargetGetMeError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "error", "code": 400, "message": "UNAUTHORIZED"},
	}

	_, err := ResolveTarget(context.Background(), mock, "me")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "getMe failed") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSendFileSuccessWithoutCaption(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{
			"@type":       "message",
			"id":          float64(7),
			"is_outgoing": true,
			"date":        float64(500),
			"content":     map[string]interface{}{"@type": "messageDocument"},
		},
	}

	got, err := SendFile(context.Background(), mock, 42, "/tmp/file.pdf", "")
	if err != nil {
		t.Fatalf("SendFile failed: %v", err)
	}
	if got.ID != 7 || got.IsOutgoing != true {
		t.Errorf("unexpected result: %+v", got)
	}
	if mock.sendCount != 1 {
		t.Errorf("expected 1 send, got %d", mock.sendCount)
	}

	req := mock.requests[0]
	if req["@type"] != "sendMessage" || req["chat_id"] != int64(42) {
		t.Errorf("unexpected request: %v", req)
	}
	content, ok := req["input_message_content"].(map[string]interface{})
	if !ok || content["@type"] != "inputMessageDocument" {
		t.Fatalf("unexpected input_message_content: %v", req["input_message_content"])
	}
	// Пустая подпись — поле caption не должно попадать в запрос вовсе.
	if _, has := content["caption"]; has {
		t.Errorf("expected no caption in request, got: %v", content)
	}
	doc, ok := content["document"].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected document: %v", content["document"])
	}
	if doc["@type"] != "inputDocument" {
		t.Errorf("unexpected document type: %v", doc["@type"])
	}
	docDoc, ok := doc["document"].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected nested document: %v", doc["document"])
	}
	if docDoc["@type"] != "inputFileLocal" || docDoc["path"] != "/tmp/file.pdf" {
		t.Errorf("unexpected local file: %v", docDoc)
	}
}

func TestSendFileSuccessWithCaption(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{
			"@type":       "message",
			"id":          float64(8),
			"is_outgoing": true,
			"date":        float64(501),
			"content":     map[string]interface{}{"@type": "messageDocument"},
		},
	}

	got, err := SendFile(context.Background(), mock, 42, "/tmp/file.pdf", "подпись")
	if err != nil {
		t.Fatalf("SendFile failed: %v", err)
	}
	if got.ID != 8 {
		t.Errorf("unexpected result: %+v", got)
	}

	req := mock.requests[0]
	content, ok := req["input_message_content"].(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected input_message_content: %v", req["input_message_content"])
	}
	caption, ok := content["caption"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected caption in request, got: %v", content)
	}
	if caption["@type"] != "formattedText" || caption["text"] != "подпись" {
		t.Errorf("unexpected caption: %v", caption)
	}
}

func TestSendFileError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "error", "code": 400, "message": "CHAT_NOT_FOUND"},
	}

	_, err := SendFile(context.Background(), mock, 42, "/tmp/file.pdf", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "sendMessage (document) failed") || !contains(err.Error(), "CHAT_NOT_FOUND") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSendFileUnexpectedResponseType(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "ok"},
	}

	_, err := SendFile(context.Background(), mock, 42, "/tmp/file.pdf", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "unexpected response type") {
		t.Errorf("unexpected error: %v", err)
	}
}
