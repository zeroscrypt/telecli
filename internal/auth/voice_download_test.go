package auth

import (
	"context"
	"strings"
	"testing"
	"time"
)

// fileUpdate строит "сырой" апдейт updateFile для файла fileID с указанным
// локальным путём и флагом завершения скачивания.
func fileUpdate(fileID float64, path string, completed bool) map[string]interface{} {
	return map[string]interface{}{
		"@type": "updateFile",
		"file": map[string]interface{}{
			"@type": "file",
			"id":    fileID,
			"local": map[string]interface{}{
				"@type":                    "localFile",
				"path":                     path,
				"is_downloading_completed": completed,
			},
		},
	}
}

// fileResponse строит ответ downloadFile типа "file" (не апдейт).
func fileResponse(fileID float64, path string, completed bool) map[string]interface{} {
	upd := fileUpdate(fileID, path, completed)
	return upd["file"].(map[string]interface{})
}

func TestWaitForFileDownloadAlreadyReady(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		fileResponse(100, "/tmp/voice.ogg", true),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	path, err := WaitForFileDownload(ctx, mock, 100)
	if err != nil {
		t.Fatalf("WaitForFileDownload failed: %v", err)
	}
	if path != "/tmp/voice.ogg" {
		t.Errorf("expected path /tmp/voice.ogg, got %q", path)
	}
	if mock.sendCount != 1 {
		t.Errorf("expected exactly 1 Send, got %d", mock.sendCount)
	}
	// Канал свободен: функция должна была вернуться ДО чтения fileUpdates
	// (иначе бы зависла до таймаута — тест это тоже поймал бы).
	if len(mock.fileCh) != 0 {
		t.Errorf("expected no channel reads for already-ready file")
	}
}

func TestWaitForFileDownloadWaitsForMatchingUpdate(t *testing.T) {
	mock := newMockTDClient()
	// Ответ downloadFile — файл ещё скачивается (пустой путь).
	mock.responses = []map[string]interface{}{
		fileResponse(100, "", false),
	}

	// Сначала апдейт про ДРУГОЙ файл (должен игнорироваться), затем — наш.
	mock.fileCh <- fileUpdate(999, "/other.ogg", true)
	mock.fileCh <- fileUpdate(100, "/tmp/voice.ogg", true)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	path, err := WaitForFileDownload(ctx, mock, 100)
	if err != nil {
		t.Fatalf("WaitForFileDownload failed: %v", err)
	}
	if path != "/tmp/voice.ogg" {
		t.Errorf("expected path /tmp/voice.ogg, got %q", path)
	}
	if mock.sendCount != 1 {
		t.Errorf("expected exactly 1 Send, got %d", mock.sendCount)
	}
}

func TestWaitForFileDownloadContextTimeout(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		fileResponse(100, "", false),
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	_, err := WaitForFileDownload(ctx, mock, 100)
	if err == nil {
		t.Fatal("expected error on expired context, got nil")
	}
	if !strings.Contains(err.Error(), ctx.Err().Error()) {
		t.Errorf("expected error to mention ctx.Err, got: %v", err)
	}
}

func TestWaitForFileDownloadClosedChannel(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		fileResponse(100, "", false),
	}
	close(mock.fileCh)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := WaitForFileDownload(ctx, mock, 100)
	if err == nil {
		t.Fatal("expected error on closed channel, got nil")
	}
	if !strings.Contains(err.Error(), "channel closed") {
		t.Errorf("expected 'channel closed' error, got: %v", err)
	}
}

func TestWaitForFileDownloadUnexpectedResponseType(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "ok"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := WaitForFileDownload(ctx, mock, 100)
	if err == nil {
		t.Fatal("expected error on non-file response, got nil")
	}
	if !strings.Contains(err.Error(), "unexpected response type: ok") {
		t.Errorf("expected 'unexpected response type' error, got: %v", err)
	}
}
