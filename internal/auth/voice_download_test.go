package auth

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func fileResponse(fileID float64, path string, completed bool) map[string]interface{} {
	return map[string]interface{}{
		"@type": "file",
		"id":    fileID,
		"local": map[string]interface{}{
			"@type":                    "localFile",
			"path":                     path,
			"is_downloading_completed": completed,
			"is_downloading_active":    !completed,
		},
	}
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
}

func TestWaitForFileDownloadPollsUntilReady(t *testing.T) {
	originalPollInterval := fileDownloadPollInterval
	defer func() { fileDownloadPollInterval = originalPollInterval }()
	fileDownloadPollInterval = time.Millisecond

	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		fileResponse(100, "", false),
		fileResponse(100, "", false),
		fileResponse(100, "/tmp/voice.ogg", true),
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	path, err := WaitForFileDownload(ctx, mock, 100)
	if err != nil {
		t.Fatalf("WaitForFileDownload failed: %v", err)
	}
	if path != "/tmp/voice.ogg" {
		t.Errorf("expected path /tmp/voice.ogg, got %q", path)
	}
	if mock.sendCount != 3 {
		t.Errorf("expected downloadFile + two getFile calls, got %d", mock.sendCount)
	}
	if mock.requests[0]["@type"] != "downloadFile" || mock.requests[1]["@type"] != "getFile" || mock.requests[2]["@type"] != "getFile" {
		t.Errorf("unexpected request sequence: %#v", mock.requests)
	}
	downloadRequest := mock.requests[0]
	if downloadRequest["file_id"] != int32(100) || downloadRequest["priority"] != 1 || downloadRequest["offset"] != 0 || downloadRequest["limit"] != 0 || downloadRequest["synchronous"] != false {
		t.Errorf("unexpected downloadFile request: %#v", downloadRequest)
	}
}

func TestWaitForFileDownloadContextTimeout(t *testing.T) {
	originalPollInterval := fileDownloadPollInterval
	defer func() { fileDownloadPollInterval = originalPollInterval }()
	fileDownloadPollInterval = time.Second

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

type concurrentFileClient struct {
	*mockTDClient
	mu          sync.Mutex
	polls       map[int32]int
	fileUpdates chan map[string]interface{}
	updateOnce  sync.Once
}

var _ TDClientInterface = (*concurrentFileClient)(nil)

func (m *concurrentFileClient) Send(ctx context.Context, request map[string]interface{}) (map[string]interface{}, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendCount++
	m.requests = append(m.requests, request)
	fileID, _ := request["file_id"].(int32)
	switch request["@type"] {
	case "downloadFile":
		m.updateOnce.Do(func() {
			m.fileUpdates <- map[string]interface{}{
				"@type": "updateFile",
				"file":  fileResponse(100, "/tmp/voice-100.ogg", true),
			}
		})
		return fileResponse(float64(fileID), "", false), nil
	case "getFile":
		m.polls[fileID]++
		if m.polls[fileID] >= 2 {
			return fileResponse(float64(fileID), fmt.Sprintf("/tmp/voice-%d.ogg", fileID), true), nil
		}
		return fileResponse(float64(fileID), "", false), nil
	default:
		return nil, fmt.Errorf("unexpected request: %v", request["@type"])
	}
}

func (m *concurrentFileClient) FileUpdates() <-chan map[string]interface{} {
	return m.fileUpdates
}

func TestWaitForFileDownloadConcurrentWaitsDoNotConsumeEachOtherState(t *testing.T) {
	originalPollInterval := fileDownloadPollInterval
	defer func() { fileDownloadPollInterval = originalPollInterval }()
	fileDownloadPollInterval = time.Millisecond

	mock := &concurrentFileClient{
		mockTDClient: newMockTDClient(),
		polls:        make(map[int32]int),
		fileUpdates:  make(chan map[string]interface{}, 1),
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	type result struct {
		fileID int32
		path   string
		err    error
	}
	results := make(chan result, 2)
	for _, fileID := range []int32{100, 200} {
		go func(fileID int32) {
			path, err := WaitForFileDownload(ctx, mock, fileID)
			results <- result{fileID: fileID, path: path, err: err}
		}(fileID)
	}

	for range 2 {
		got := <-results
		if got.err != nil {
			t.Errorf("file %d: WaitForFileDownload failed: %v", got.fileID, got.err)
			continue
		}
		want := fmt.Sprintf("/tmp/voice-%d.ogg", got.fileID)
		if got.path != want {
			t.Errorf("file %d: expected path %q, got %q", got.fileID, want, got.path)
		}
	}
}

func TestWaitForFileDownloadGetFileError(t *testing.T) {
	originalPollInterval := fileDownloadPollInterval
	defer func() { fileDownloadPollInterval = originalPollInterval }()
	fileDownloadPollInterval = time.Millisecond

	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		fileResponse(100, "", false),
		{"@type": "error", "message": "File not found"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := WaitForFileDownload(ctx, mock, 100)
	if err == nil {
		t.Fatal("expected getFile error, got nil")
	}
	if !strings.Contains(err.Error(), "getFile failed: File not found") {
		t.Errorf("expected wrapped getFile error, got: %v", err)
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
