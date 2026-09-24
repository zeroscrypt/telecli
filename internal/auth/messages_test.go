package auth

import (
	"context"
	"os"
	"reflect"
	"testing"
)

// TestMain отключает retry-задержки истории по умолчанию: тесты ниже проверяют
// логику одного запроса (fetchChatHistoryOnce), и без этого каждый из них при
// limit больше фактического числа сообщений делал бы лишние повторы с реальными
// задержками. Тесты самого retry (messages_retry_test.go) включают его сами.
func TestMain(m *testing.M) {
	historyRetryDelays = nil
	os.Exit(m.Run())
}

func TestGetMessagesParsesAndReverses(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{
			"@type":       "messages",
			"total_count": 4,
			"messages": []interface{}{
				// TDLib отдаёт новые первыми — здесь порядок id 11, 10, 9, 8.
				map[string]interface{}{
					"@type":       "message",
					"id":          float64(11),
					"is_outgoing": false,
					"date":        float64(200),
					"sender_id": map[string]interface{}{
						"@type":   "messageSenderChat",
						"chat_id": float64(777),
					},
					"content": map[string]interface{}{
						"@type": "messageText",
						"text": map[string]interface{}{
							"@type": "formattedText",
							"text":  "объявление",
						},
					},
				},
				map[string]interface{}{
					"@type":       "message",
					"id":          float64(10),
					"is_outgoing": false,
					"date":        float64(150),
					"sender_id": map[string]interface{}{
						"@type":   "messageSenderUser",
						"user_id": float64(1),
					},
					"content": map[string]interface{}{
						"@type": "messageText",
						"text": map[string]interface{}{
							"@type": "formattedText",
							"text":  "привет",
						},
					},
				},
				map[string]interface{}{
					"@type":       "message",
					"id":          float64(9),
					"is_outgoing": false,
					"date":        float64(100),
					"sender_id": map[string]interface{}{
						"@type":   "messageSenderUser",
						"user_id": float64(2),
					},
					"content": map[string]interface{}{
						"@type": "messagePhoto",
						"photo": map[string]interface{}{},
					},
				},
				map[string]interface{}{
					"@type":       "message",
					"id":          float64(8),
					"is_outgoing": true,
					"date":        float64(50),
					"content": map[string]interface{}{
						"@type": "messageText",
						"text": map[string]interface{}{
							"@type": "formattedText",
							"text":  "исходящее",
						},
					},
				},
			},
		},
		// Разрешение отправителей идёт в том же порядке, в каком TDLib отдал
		// сообщения (новые первыми): id=11 (getChat), затем id=10 (getUser).
		{"@type": "chat", "id": float64(777), "title": "Тестовый канал"},
		{"@type": "user", "id": float64(1), "first_name": "Иван", "last_name": "Петров"},
		// getUser для user_id=2 (сообщение id=9) отвечает ошибкой — резолв
		// имени должен деградировать до user#2, а не ронять весь вызов.
	}

	got, err := GetMessages(context.Background(), mock, 42, 50)
	if err != nil {
		t.Fatalf("GetMessages failed: %v", err)
	}

	want := []Message{
		{ID: 8, SenderName: "Вы", IsOutgoing: true, Date: 50, Text: "исходящее"},
		{ID: 9, SenderName: "user#2", IsOutgoing: false, Date: 100, Text: "[тип сообщения: messagePhoto]"},
		{ID: 10, SenderName: "Иван Петров", IsOutgoing: false, Date: 150, Text: "привет"},
		{ID: 11, SenderName: "Тестовый канал", IsOutgoing: false, Date: 200, Text: "объявление"},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("unexpected result:\n got: %+v\nwant: %+v", got, want)
	}

	// getChatHistory + getUser + getChat + getUser (с ошибкой) = 4 вызова.
	if mock.sendCount != 4 {
		t.Errorf("expected 4 sends, got %d", mock.sendCount)
	}
}

func TestGetMessagesSenderResolveErrorDoesNotFailCall(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{
			"@type":       "messages",
			"total_count": 2,
			"messages": []interface{}{
				map[string]interface{}{
					"@type":       "message",
					"id":          float64(20),
					"is_outgoing": false,
					"date":        float64(10),
					"sender_id": map[string]interface{}{
						"@type":   "messageSenderUser",
						"user_id": float64(5),
					},
					"content": map[string]interface{}{
						"@type": "messageText",
						"text": map[string]interface{}{
							"@type": "formattedText",
							"text":  "текст",
						},
					},
				},
				map[string]interface{}{
					"@type":       "message",
					"id":          float64(19),
					"is_outgoing": false,
					"date":        float64(5),
					"sender_id": map[string]interface{}{
						"@type":   "messageSenderChat",
						"chat_id": float64(999),
					},
					"content": map[string]interface{}{
						"@type": "messageDice",
						"value": float64(5),
					},
				},
			},
		},
	}
	// Ответов на getUser/getChat нет — резолв вернёт ошибку для обоих сообщений.

	got, err := GetMessages(context.Background(), mock, 42, 50)
	if err != nil {
		t.Fatalf("GetMessages failed: %v", err)
	}

	want := []Message{
		{ID: 19, SenderName: "chat#999", IsOutgoing: false, Date: 5, Text: "[тип сообщения: messageDice]"},
		{ID: 20, SenderName: "user#5", IsOutgoing: false, Date: 10, Text: "текст"},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("unexpected result:\n got: %+v\nwant: %+v", got, want)
	}
}

func TestGetMessagesHistoryError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "error", "code": 400, "message": "CHAT_NOT_FOUND"},
	}

	_, err := GetMessages(context.Background(), mock, 42, 50)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "CHAT_NOT_FOUND") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestGetMessagesUnexpectedResponseType(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "ok"},
	}
	_, err := GetMessages(context.Background(), mock, 42, 50)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSendMessageSuccess(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{
			"@type":       "message",
			"id":          float64(42),
			"is_outgoing": true,
			"date":        float64(300),
			"content": map[string]interface{}{
				"@type": "messageText",
				"text": map[string]interface{}{
					"@type": "formattedText",
					"text":  "привет",
				},
			},
		},
	}

	got, err := SendMessage(context.Background(), mock, 777, "привет")
	if err != nil {
		t.Fatalf("SendMessage failed: %v", err)
	}

	want := Message{ID: 42, SenderName: "Вы", IsOutgoing: true, Date: 300, Text: "привет"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unexpected result:\n got: %+v\nwant: %+v", got, want)
	}

	// Исходящее сообщение резолвится как "Вы" без сетевых вызовов — ровно одни
	// Send (сам sendMessage), никаких getUser/getChat сверх него.
	if mock.sendCount != 1 {
		t.Errorf("expected 1 send, got %d", mock.sendCount)
	}
}

func TestSendMessageError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "error", "code": 400, "message": "CHAT_NOT_FOUND"},
	}

	_, err := SendMessage(context.Background(), mock, 777, "привет")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "sendMessage failed") || !contains(err.Error(), "CHAT_NOT_FOUND") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSendMessageUnexpectedResponseType(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "ok"},
	}

	_, err := SendMessage(context.Background(), mock, 777, "привет")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// voiceNoteMsgMap строит голосовое сообщение TDLib (content messageVoiceNote)
// с указанными id файла и длительностью — для тестов парсинга голосового.
func voiceNoteMsgMap(fileID float64, duration float64) map[string]interface{} {
	return map[string]interface{}{
		"@type":       "message",
		"id":          float64(1),
		"is_outgoing": true,
		"date":        float64(100),
		"content": map[string]interface{}{
			"@type":       "messageVoiceNote",
			"voice_note":  map[string]interface{}{"@type": "voiceNote", "duration": duration, "voice": map[string]interface{}{"@type": "file", "id": fileID, "size": float64(4096)}},
			"is_listened": false,
		},
	}
}

func TestParseMessageVoiceNoteSetsVoiceFields(t *testing.T) {
	got := parseMessage(context.Background(), nil, voiceNoteMsgMap(12345, 65))
	if !got.IsVoiceNote {
		t.Error("expected IsVoiceNote=true")
	}
	if got.VoiceFileID != 12345 {
		t.Errorf("expected VoiceFileID=12345, got %d", got.VoiceFileID)
	}
	if got.VoiceDuration != 65 {
		t.Errorf("expected VoiceDuration=65, got %d", got.VoiceDuration)
	}
	if got.VoiceSize != 4096 {
		t.Errorf("expected VoiceSize=4096, got %d", got.VoiceSize)
	}
	if got.Text != "▶ голосовое [1:05]" {
		t.Errorf("expected Text %q, got %q", "▶ голосовое [1:05]", got.Text)
	}
}

// TestParseMessageVoiceNoteMissingSizeKeepsVoiceNote — file.size может
// отсутствовать (TDLib не всегда сообщает ожидаемый размер): голосовое всё
// равно валидно, VoiceSize деградирует в 0 без потери остальных полей.
func TestParseMessageVoiceNoteMissingSizeKeepsVoiceNote(t *testing.T) {
	raw := voiceNoteMsgMap(12345, 65)
	voice := raw["content"].(map[string]interface{})["voice_note"].(map[string]interface{})["voice"].(map[string]interface{})
	delete(voice, "size")

	got := parseMessage(context.Background(), nil, raw)
	if !got.IsVoiceNote {
		t.Fatal("expected IsVoiceNote=true even without voice.size")
	}
	if got.VoiceFileID != 12345 || got.VoiceDuration != 65 {
		t.Errorf("expected id/duration intact, got id=%d dur=%d", got.VoiceFileID, got.VoiceDuration)
	}
	if got.VoiceSize != 0 {
		t.Errorf("expected VoiceSize=0 when size missing, got %d", got.VoiceSize)
	}
}

func photoSize(fileID, width, height float64) map[string]interface{} {
	return map[string]interface{}{
		"@type":  "photoSize",
		"type":   "x",
		"width":  width,
		"height": height,
		"photo": map[string]interface{}{
			"@type": "file",
			"id":    fileID,
		},
	}
}

func photoMsgMap(sizes ...interface{}) map[string]interface{} {
	return map[string]interface{}{
		"@type":       "message",
		"id":          float64(1),
		"is_outgoing": true,
		"date":        float64(100),
		"content": map[string]interface{}{
			"@type": "messagePhoto",
			"photo": map[string]interface{}{
				"@type": "photo",
				"sizes": sizes,
			},
		},
	}
}

func TestParseMessagePhotoSelectsLargestArea(t *testing.T) {
	msg := photoMsgMap(
		photoSize(100, 10, 100),
		photoSize(200, 80, 80),
		photoSize(300, 100, 20),
	)

	got := parseMessage(context.Background(), nil, msg)
	if !got.IsPhoto {
		t.Fatal("expected IsPhoto=true")
	}
	if got.IsVoiceNote {
		t.Error("expected IsVoiceNote=false")
	}
	if got.PhotoFileID != 200 {
		t.Errorf("expected PhotoFileID=200, got %d", got.PhotoFileID)
	}
	if got.Text != "[фото]" {
		t.Errorf("expected photo placeholder, got %q", got.Text)
	}
}

func TestPhotoInfoSkipsMalformedSizes(t *testing.T) {
	msg := photoMsgMap(
		"broken",
		map[string]interface{}{"width": float64(100), "height": float64(100)},
		photoSize(0, 100, 100),
		photoSize(1, 0, 100),
		photoSize(2, 100, 0),
		photoSize(3, 4, 5),
	)

	fileID, ok := photoInfo(msg)
	if !ok || fileID != 3 {
		t.Fatalf("photoInfo=(%d, %v), want (3, true)", fileID, ok)
	}
}

func TestParseMessageDegradedPhotoKeepsPlaceholder(t *testing.T) {
	cases := []map[string]interface{}{
		{"@type": "message", "is_outgoing": true},
		{"@type": "message", "is_outgoing": true, "content": map[string]interface{}{"@type": "messagePhoto"}},
		{"@type": "message", "is_outgoing": true, "content": map[string]interface{}{"@type": "messagePhoto", "photo": map[string]interface{}{}}},
		photoMsgMap(),
	}
	for i, raw := range cases {
		got := parseMessage(context.Background(), nil, raw)
		if got.IsPhoto || got.PhotoFileID != 0 {
			t.Errorf("case %d: expected zero photo fields, got photo=%t id=%d", i, got.IsPhoto, got.PhotoFileID)
		}
		if got.Text != "[тип сообщения: messagePhoto]" && got.Text != "" {
			t.Errorf("case %d: expected generic photo placeholder, got %q", i, got.Text)
		}
	}
}

func TestParseMessageDegradedVoiceNoteNoPanic(t *testing.T) {
	cases := []map[string]interface{}{
		// вообще нет content
		{"@type": "message", "id": 1, "is_outgoing": true},
		// content есть, но voice_note отсутствует
		{"@type": "message", "is_outgoing": true, "content": map[string]interface{}{"@type": "messageVoiceNote"}},
		// voice_note есть, но voice отсутствует
		{"@type": "message", "is_outgoing": true, "content": map[string]interface{}{"@type": "messageVoiceNote", "voice_note": map[string]interface{}{"@type": "voiceNote"}}},
		// voice есть, но id нечисловой
		{"@type": "message", "is_outgoing": true, "content": map[string]interface{}{"@type": "messageVoiceNote", "voice_note": map[string]interface{}{"@type": "voiceNote", "voice": map[string]interface{}{"@type": "file", "id": "abc"}}}},
		// id <= 0 — валидация отсекает
		{"@type": "message", "is_outgoing": true, "content": map[string]interface{}{"@type": "messageVoiceNote", "voice_note": map[string]interface{}{"@type": "voiceNote", "voice": map[string]interface{}{"@type": "file", "id": 0}}}},
	}
	for i, raw := range cases {
		got := parseMessage(context.Background(), nil, raw)
		if got.IsVoiceNote {
			t.Errorf("case %d: expected IsVoiceNote=false, flags set despite broken input", i)
		}
		if got.VoiceFileID != 0 || got.VoiceDuration != 0 || got.VoiceSize != 0 {
			t.Errorf("case %d: expected zero voice fields, got id=%d dur=%d size=%d", i, got.VoiceFileID, got.VoiceDuration, got.VoiceSize)
		}
		if got.SenderName != "Вы" {
			t.Errorf("case %d: expected SenderName Вы, got %q", i, got.SenderName)
		}
	}
}

func TestFormatVoiceDuration(t *testing.T) {
	cases := []struct {
		in   int
		want string
	}{
		{0, "0:00"},
		{5, "0:05"},
		{65, "1:05"},
		{120, "2:00"},
		{3665, "61:05"},
		{-10, "0:00"},
	}
	for _, c := range cases {
		if got := formatVoiceDuration(c.in); got != c.want {
			t.Errorf("formatVoiceDuration(%d)=%q, want %q", c.in, got, c.want)
		}
	}
}
