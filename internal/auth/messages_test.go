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
		{
			// SenderUserID заполнен даже там, где getUser упал и имя осталось
			// деградацией: id известен, и карточка найдётся по updateUser, когда
			// настоящее имя придёт позже.
			ID: 9, SenderName: "user#2", SenderUserID: 2, IsOutgoing: false, Date: 100, Text: "[фото]",
			Content: Content{Kind: ContentPhoto, PreviewFormat: "jpeg"},
		},
		{ID: 10, SenderName: "Иван Петров", SenderUserID: 1, IsOutgoing: false, Date: 150, Text: "привет"},
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
		{
			// Отправитель-чат: SenderUserID остаётся нулём — переименование чата
			// приходит отдельным апдейтом про чат, а не про пользователя.
			ID: 19, SenderName: "chat#999", IsOutgoing: false, Date: 5, Text: "🎲 5",
			Content: Content{Kind: ContentDice, Value: 5},
		},
		{ID: 20, SenderName: "user#5", SenderUserID: 5, IsOutgoing: false, Date: 10, Text: "текст"},
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

// TestParseMessageSenderUserID — карточка помнит id отправителя-пользователя.
//
// Без этого поля переименование человека в Telegram не к чему было бы применить:
// имя в карточке зафиксировано строкой в момент разбора, и найти её по одному
// имени нельзя (имена совпадают у разных людей, а «Вы» и вовсе не имя). Ноль при
// этом — не «id неизвестен», а «отправитель не пользователь»: исходящее своё
// сообщение и сообщение от имени чата обновлять нечем.
func TestParseMessageSenderUserID(t *testing.T) {
	cases := []struct {
		name       string
		responses  []map[string]interface{}
		message    map[string]interface{}
		wantName   string
		wantUserID int64
	}{
		{
			name:      "входящее от пользователя",
			responses: []map[string]interface{}{{"@type": "user", "id": float64(42), "first_name": "Иван", "last_name": "Петров"}},
			message: map[string]interface{}{
				"@type":       "message",
				"id":          float64(1),
				"is_outgoing": false,
				"sender_id":   map[string]interface{}{"@type": "messageSenderUser", "user_id": float64(42)},
			},
			wantName: "Иван Петров", wantUserID: 42,
		},
		{
			// getUser упал, имя деградировало до user#<id> — но id известен, и
			// карточка обновится, когда придёт updateUser с настоящим именем.
			name: "getUser упал, id сохранён",
			message: map[string]interface{}{
				"@type":       "message",
				"id":          float64(1),
				"is_outgoing": false,
				"sender_id":   map[string]interface{}{"@type": "messageSenderUser", "user_id": float64(7)},
			},
			wantName: "user#7", wantUserID: 7,
		},
		{
			name: "исходящее",
			message: map[string]interface{}{
				"@type":       "message",
				"id":          float64(1),
				"is_outgoing": true,
			},
			wantName: "Вы", wantUserID: 0,
		},
		{
			name:      "от имени чата",
			responses: []map[string]interface{}{{"@type": "chat", "id": float64(99), "title": "Канал"}},
			message: map[string]interface{}{
				"@type":       "message",
				"id":          float64(1),
				"is_outgoing": false,
				"sender_id":   map[string]interface{}{"@type": "messageSenderChat", "chat_id": float64(99)},
			},
			wantName: "Канал", wantUserID: 0,
		},
		{
			name: "от имени чата, getChat упал",
			message: map[string]interface{}{
				"@type":       "message",
				"id":          float64(1),
				"is_outgoing": false,
				"sender_id":   map[string]interface{}{"@type": "messageSenderChat", "chat_id": float64(99)},
			},
			wantName: "chat#99", wantUserID: 0,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mock := newMockTDClient()
			mock.responses = testCase.responses

			got := parseMessage(context.Background(), mock, testCase.message)

			if got.SenderName != testCase.wantName {
				t.Errorf("SenderName = %q, want %q", got.SenderName, testCase.wantName)
			}
			if got.SenderUserID != testCase.wantUserID {
				t.Errorf("SenderUserID = %d, want %d", got.SenderUserID, testCase.wantUserID)
			}
		})
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

func TestSendMessageReplyAddsReplyParameters(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{
			"@type":       "message",
			"id":          float64(43),
			"is_outgoing": true,
			"date":        float64(301),
			"content": map[string]interface{}{
				"@type": "messageText",
				"text": map[string]interface{}{
					"@type": "formattedText",
					"text":  "ответ",
				},
			},
		},
	}

	got, err := SendMessageReply(context.Background(), mock, 777, "ответ", 42)
	if err != nil {
		t.Fatalf("SendMessageReply failed: %v", err)
	}
	want := Message{ID: 43, SenderName: "Вы", IsOutgoing: true, Date: 301, Text: "ответ"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unexpected result:\n got: %+v\nwant: %+v", got, want)
	}
	if len(mock.requests) != 1 {
		t.Fatalf("expected one request, got %d", len(mock.requests))
	}
	request := mock.requests[0]
	if request["@type"] != "sendMessage" || request["chat_id"] != int64(777) {
		t.Fatalf("unexpected sendMessage envelope: %#v", request)
	}
	reply, ok := request["reply_to"].(map[string]interface{})
	if !ok {
		t.Fatalf("reply_to = %#v, want inputMessageReplyToMessage", request["reply_to"])
	}
	wantReply := map[string]interface{}{
		"@type":      "inputMessageReplyToMessage",
		"message_id": int64(42),
	}
	if !reflect.DeepEqual(reply, wantReply) {
		t.Errorf("reply_to = %#v, want %#v", reply, wantReply)
	}
}

func TestSendMessageReplyZeroIDOmitsReplyParameters(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{
			"@type":       "message",
			"id":          float64(44),
			"is_outgoing": true,
			"date":        float64(302),
			"content": map[string]interface{}{
				"@type": "messageText",
				"text": map[string]interface{}{
					"@type": "formattedText",
					"text":  "обычный текст",
				},
			},
		},
	}

	if _, err := SendMessageReply(context.Background(), mock, 777, "обычный текст", 0); err != nil {
		t.Fatalf("SendMessageReply failed: %v", err)
	}
	if _, ok := mock.requests[0]["reply_to"]; ok {
		t.Fatalf("zero reply ID unexpectedly added reply_to: %#v", mock.requests[0])
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

func TestDeleteMessageSuccess(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "ok"},
	}

	if err := DeleteMessage(context.Background(), mock, 777, 42); err != nil {
		t.Fatalf("DeleteMessage failed: %v", err)
	}

	if mock.sendCount != 1 {
		t.Fatalf("expected 1 send, got %d", mock.sendCount)
	}
	want := map[string]interface{}{
		"@type":       "deleteMessages",
		"chat_id":     int64(777),
		"message_ids": []int64{42},
		"revoke":      false,
	}
	if !reflect.DeepEqual(mock.requests[0], want) {
		t.Errorf("unexpected deleteMessages envelope:\n got: %#v\nwant: %#v", mock.requests[0], want)
	}
}

// TestDeleteMessageSendsOneRequestPerMessage — удаление одного сообщения за раз
// (массового удаления в приложении нет): два вызова подряд дают два запроса, в
// каждом ровно один message_id, revoke выключен. Так ловятся общий вектор
// запросов и случайно включённый revoke — оба ломают историю собеседника.
func TestDeleteMessageSendsOneRequestPerMessage(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{{"@type": "ok"}, {"@type": "ok"}}

	if err := DeleteMessage(context.Background(), mock, 777, 42); err != nil {
		t.Fatalf("DeleteMessage failed: %v", err)
	}
	if err := DeleteMessage(context.Background(), mock, 777, 43); err != nil {
		t.Fatalf("DeleteMessage failed: %v", err)
	}

	if mock.sendCount != 2 || len(mock.requests) != 2 {
		t.Fatalf("expected 2 sends, got sendCount=%d requests=%d", mock.sendCount, len(mock.requests))
	}
	for index, request := range mock.requests {
		ids, ok := request["message_ids"].([]int64)
		if !ok || len(ids) != 1 {
			t.Fatalf("request %d message_ids = %#v, want exactly one id", index, request["message_ids"])
		}
		if want := int64(42 + index); ids[0] != want {
			t.Errorf("request %d message id = %d, want %d", index, ids[0], want)
		}
		if revoke, ok := request["revoke"].(bool); !ok || revoke {
			t.Fatalf("request %d revoke = %#v, want false", index, request["revoke"])
		}
	}
}

func TestDeleteMessageError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "error", "code": 400, "message": "Message not found"},
	}

	err := DeleteMessage(context.Background(), mock, 777, 42)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "deleteMessages failed") || !contains(err.Error(), "Message not found") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDeleteMessageUnexpectedResponseType(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "message", "id": float64(42)},
	}

	err := DeleteMessage(context.Background(), mock, 777, 42)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !contains(err.Error(), "unexpected response type") {
		t.Errorf("unexpected error: %v", err)
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

	fileID, ok := photoInfo(msg["content"].(map[string]interface{}))
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
		// Отладочной строки «[тип сообщения: …]» больше нет: даже битое фото
		// подписывается как фото. Сообщение без content — не фото, и подписи не
		// получает.
		want := "[фото]"
		if i == 0 {
			want = ""
		}
		if got.Text != want {
			t.Errorf("case %d: text = %q, want %q", i, got.Text, want)
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

// probeErr — только для временной проверки текста ошибки 404.
func getMessageErr() error {
	_, err := GetMessage(nil, nil, 5, 7)
	return err
}

// deleteMessagesUpdate — updateDeleteMessages в том виде, в каком его присылает
// TDLib: chat_id, message_ids и два признака (is_permanent, from_cache), которые
// парсер намеренно не читает. Их наличие в тесте нужно, чтобы зафиксировать,
// что решение «не смотреть на признаки» не сломается, когда TDLib их присылает.
func deleteMessagesUpdate(chatID int64, ids []interface{}) map[string]interface{} {
	if ids == nil {
		ids = []interface{}{}
	}
	return map[string]interface{}{
		"@type":        "updateDeleteMessages",
		"chat_id":      float64(chatID),
		"message_ids":  ids,
		"is_permanent": true,
		"from_cache":   false,
	}
}

// TestParseDeleteMessagesUpdateReadsAllIDs — типовой апдейт: возвращаются чат и
// ВСЕ удалённые id, а не только первый. Один апдейт несёт сразу пачку
// message_ids, поэтому парсер, берущий единственное значение, тихо терял бы
// остальные удаления.
func TestParseDeleteMessagesUpdateReadsAllIDs(t *testing.T) {
	chatID, ids, ok := ParseDeleteMessagesUpdate(
		deleteMessagesUpdate(777, []interface{}{float64(42), float64(43), float64(51)}))

	if !ok {
		t.Fatal("ParseDeleteMessagesUpdate reported failure on a well-formed update")
	}
	if chatID != 777 {
		t.Errorf("chat_id = %d, want 777", chatID)
	}
	want := []int64{42, 43, 51}
	if !reflect.DeepEqual(ids, want) {
		t.Errorf("message_ids = %v, want %v", ids, want)
	}
}

// TestParseDeleteMessagesUpdateEmptyListIsNotAnError — пустой список удалённых
// сообщений и вовсе отсутствующее поле message_ids — валидные апдейты с ok == true
// и пустым списком. Ошибкой они быть не могут: TDLib присылает апдейт и тогда,
// когда удалять нечего, а «пустой апдейт = поломка разбора» заставила бы
// вызывающую сторону вести себя с обычным событием как с аварийным.
func TestParseDeleteMessagesUpdateEmptyListIsNotAnError(t *testing.T) {
	cases := map[string]map[string]interface{}{
		"пустой массив": deleteMessagesUpdate(5, []interface{}{}),
		"нет поля": {
			"@type":   "updateDeleteMessages",
			"chat_id": float64(5),
		},
	}
	for name, update := range cases {
		chatID, ids, ok := ParseDeleteMessagesUpdate(update)
		if !ok {
			t.Errorf("случай %q: ok == false, want true — пустое удаление не ошибка", name)
		}
		if chatID != 5 {
			t.Errorf("случай %q: chat_id = %d, want 5", name, chatID)
		}
		if len(ids) != 0 {
			t.Errorf("случай %q: message_ids = %v, want пустой список", name, ids)
		}
	}
}

// TestParseDeleteMessagesUpdateRejectsForeignUpdate — чужой @type отсекается:
// парсер вызывается на каждом апдейте из общего канала, и без проверки типа он
// согласился бы разбирать что угодно. Отдельно проверяется апдейт без chat_id:
// подставлять 0 молча нельзя, по нулевому чату удаление «пройдёт» и уберёт не то.
func TestParseDeleteMessagesUpdateRejectsForeignUpdate(t *testing.T) {
	cases := map[string]map[string]interface{}{
		"чужой тип": {
			"@type":       "updateNewMessage",
			"chat_id":     float64(5),
			"message_ids": []interface{}{float64(1)},
		},
		"нет chat_id": {
			"@type":       "updateDeleteMessages",
			"message_ids": []interface{}{float64(1)},
		},
	}
	for name, update := range cases {
		if _, _, ok := ParseDeleteMessagesUpdate(update); ok {
			t.Errorf("случай %q: ok == true, want false", name)
		}
	}
}

// TestParseDeleteMessagesUpdateSkipsBrokenIDs — не-числовой элемент в message_ids
// пропускается, а не роняет весь разбор: TDLib шлёт только числа, но единственная
// защита от битого значения — не паниковать на нём.
func TestParseDeleteMessagesUpdateSkipsBrokenIDs(t *testing.T) {
	_, ids, ok := ParseDeleteMessagesUpdate(
		deleteMessagesUpdate(5, []interface{}{float64(1), "мусор", float64(2)}))

	if !ok {
		t.Fatal("битый элемент в message_ids не должен делать апдейт невалидным")
	}
	if want := []int64{1, 2}; !reflect.DeepEqual(ids, want) {
		t.Errorf("message_ids = %v, want %v", ids, want)
	}
}

// textContent — объект messageText в том виде, в каком его кладёт TDLib: и в
// message.content, и в updateMessageContent.new_content это один и тот же объект.
func textContent(text string) map[string]interface{} {
	return map[string]interface{}{
		"@type": "messageText",
		"text":  map[string]interface{}{"@type": "formattedText", "text": text},
	}
}

// photoContent — объект messagePhoto с единственным размером: по нему
// parseContentObject выбирает файл для показа.
func photoContent(fileID, width, height float64) map[string]interface{} {
	return map[string]interface{}{
		"@type": "messagePhoto",
		"photo": map[string]interface{}{
			"@type": "photo",
			"sizes": []interface{}{photoSize(fileID, width, height)},
		},
	}
}

// voiceContent — объект messageVoiceNote с файлом и длительностью: того же вида,
// что и в voiceNoteMsgMap, но без обёртки message.
func voiceContent(fileID, duration float64) map[string]interface{} {
	return map[string]interface{}{
		"@type": "messageVoiceNote",
		"voice_note": map[string]interface{}{
			"@type":    "voiceNote",
			"duration": duration,
			"voice": map[string]interface{}{
				"@type": "file",
				"id":    fileID,
				"size":  float64(4096),
			},
		},
	}
}

// messageContentUpdate — updateMessageContent в том виде, в каком его присылает
// TDLib (сверено с td_api.h собранной версии, ID 506903332): chat_id, message_id
// и new_content. updateMessageEdited сюда НЕ подставляется намеренно: он несёт
// edit_date и reply_markup, а нового содержимого не несёт вовсе.
func messageContentUpdate(chatID, messageID float64, content map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"@type":       "updateMessageContent",
		"chat_id":     chatID,
		"message_id":  messageID,
		"new_content": content,
	}
}

// TestParseMessageContentUpdateReadsNewText — главный случай задачи: сообщение
// отредактировали, и апдейт приносит НОВЫЙ текст. Без этого карточка держала бы
// прежний до перезапуска клиента.
func TestParseMessageContentUpdateReadsNewText(t *testing.T) {
	chatID, messageID, updated, ok := ParseMessageContentUpdate(
		messageContentUpdate(7, 42, textContent("новый текст")))

	if !ok {
		t.Fatal("ParseMessageContentUpdate reported failure on a well-formed update")
	}
	if chatID != 7 || messageID != 42 {
		t.Errorf("chat/message = %d/%d, want 7/42", chatID, messageID)
	}
	if updated.Text != "новый текст" {
		t.Errorf("Text = %q, want %q", updated.Text, "новый текст")
	}
	if updated.Content.Kind != ContentText {
		t.Errorf("Content.Kind = %v, want ContentText", updated.Content.Kind)
	}
	if updated.IsPhoto || updated.IsVoiceNote {
		t.Errorf("текстовое сообщение помечено как медиа: IsPhoto=%v IsVoiceNote=%v",
			updated.IsPhoto, updated.IsVoiceNote)
	}
}

// TestParseMessageContentUpdateTextToPhoto — правка с текста на фото: кроме
// IsPhoto/PhotoFileID должен смениться и текст карточки (на подпись
// contentText), иначе на месте фото осталась бы строка прежнего текста.
//
// Это ровно тот случай, где нужна честная пересборка полей, а не «дописать
// новое»: у разбора своя карточка, обнулять нечего, проверять надо результат.
func TestParseMessageContentUpdateTextToPhoto(t *testing.T) {
	_, _, updated, ok := ParseMessageContentUpdate(
		messageContentUpdate(7, 42, photoContent(999, 800, 600)))

	if !ok {
		t.Fatal("правка на фото должна быть валидным апдейтом")
	}
	if !updated.IsPhoto {
		t.Error("IsPhoto = false, want true после правки на фото")
	}
	if updated.PhotoFileID != 999 {
		t.Errorf("PhotoFileID = %d, want 999", updated.PhotoFileID)
	}
	if updated.Text != "[фото]" {
		t.Errorf("Text = %q, want %q — на карточке остался прежний текст", updated.Text, "[фото]")
	}
	if updated.IsVoiceNote {
		t.Error("IsVoiceNote = true на фотографии")
	}
	if updated.Content.Kind != ContentPhoto {
		t.Errorf("Content.Kind = %v, want ContentPhoto", updated.Content.Kind)
	}
}

// TestParseMessageContentUpdatePhotoToTextClearsPhotoFields — обратная правка,
// фото на текст. Проверяется сброс: парсер, который только дописывает новые поля,
// оставил бы IsPhoto с PhotoFileID от старого типа, и лента продолжала бы
// считать карточку фотографией (и показывать превью, которого в чате уже нет).
func TestParseMessageContentUpdatePhotoToTextClearsPhotoFields(t *testing.T) {
	_, _, updated, ok := ParseMessageContentUpdate(
		messageContentUpdate(7, 42, textContent("уже не фото")))

	if !ok {
		t.Fatal("правка с фото на текст должна быть валидным апдейтом")
	}
	if updated.IsPhoto || updated.PhotoFileID != 0 {
		t.Errorf("хвосты от фото остались: IsPhoto=%v PhotoFileID=%d", updated.IsPhoto, updated.PhotoFileID)
	}
	if updated.Text != "уже не фото" {
		t.Errorf("Text = %q, want %q", updated.Text, "уже не фото")
	}
}

// TestParseMessageContentUpdateVoiceToTextClearsVoiceFields — то же для голосового:
// правка голосового на текст обязана погасить и признак, и файл, и длительность с
// размером, иначе карточка показывала бы плеер несуществующей записи.
func TestParseMessageContentUpdateVoiceToTextClearsVoiceFields(t *testing.T) {
	_, _, updated, ok := ParseMessageContentUpdate(
		messageContentUpdate(7, 42, textContent("вместо голосового")))

	if !ok {
		t.Fatal("правка с голосового на текст должна быть валидным апдейтом")
	}
	if updated.IsVoiceNote || updated.VoiceFileID != 0 || updated.VoiceDuration != 0 || updated.VoiceSize != 0 {
		t.Errorf("хвосты от голосового остались: IsVoiceNote=%v VoiceFileID=%d VoiceDuration=%d VoiceSize=%d",
			updated.IsVoiceNote, updated.VoiceFileID, updated.VoiceDuration, updated.VoiceSize)
	}
}

// TestParseMessageContentUpdateReadsVoiceFields — правка на голосовое заполняет
// те же поля, что и обычное сообщение с голосовым: файл, длительность и подпись.
func TestParseMessageContentUpdateReadsVoiceFields(t *testing.T) {
	_, _, updated, ok := ParseMessageContentUpdate(
		messageContentUpdate(7, 42, voiceContent(12345, 65)))

	if !ok {
		t.Fatal("правка на голосовое должна быть валидным апдейтом")
	}
	if !updated.IsVoiceNote || updated.VoiceFileID != 12345 || updated.VoiceDuration != 65 {
		t.Errorf("поля голосового = %v/%d/%d, want true/12345/65",
			updated.IsVoiceNote, updated.VoiceFileID, updated.VoiceDuration)
	}
	if updated.Text != "▶ голосовое [1:05]" {
		t.Errorf("Text = %q, want %q", updated.Text, "▶ голосовое [1:05]")
	}
	if updated.IsPhoto {
		t.Error("голосовое помечено как фото")
	}
}

// TestParseMessageContentUpdateLeavesOtherFieldsEmpty — наружу отдаются только
// поля, зависящие от содержимого. Отправитель, дата и реакции апдейт не меняет:
// возврат нулей здесь — не потеря, а контракт, по которому вызывающая сторона
// присваивает ровно перечисленные поля и не стирает остальные у карточки.
func TestParseMessageContentUpdateLeavesOtherFieldsEmpty(t *testing.T) {
	_, _, updated, ok := ParseMessageContentUpdate(
		messageContentUpdate(7, 42, textContent("правка")))

	if !ok {
		t.Fatal("валидный апдейт разобран как ошибочный")
	}
	if updated.SenderName != "" || updated.Date != 0 || updated.ID != 0 || updated.IsOutgoing {
		t.Errorf("апдейт правки не должен нести поля сообщения: %+v", updated)
	}
	if updated.Reactions != nil || updated.ReplyQuote != "" || updated.ReplyToMessageID != 0 {
		t.Errorf("апдейт правки не должен нести реакции и ответ: %+v", updated)
	}
}

// TestParseMessageContentUpdateRejectsForeignAndBrokenUpdates — чужой @type
// отсекается: парсер зовётся на апдейте из общего канала, и без проверки типа он
// согласился бы разбирать что угодно. Отдельно — отсутствующие chat_id/message_id
// и битый new_content: подставлять нули молча нельзя, по chat_id == 0 правка
// «пройдёт» и не найдёт ни одного сообщения.
func TestParseMessageContentUpdateRejectsForeignAndBrokenUpdates(t *testing.T) {
	cases := map[string]map[string]interface{}{
		"чужой тип": {
			"@type":       "updateMessageEdited",
			"chat_id":     float64(5),
			"message_id":  float64(1),
			"new_content": textContent("у updateMessageEdited текста нет вовсе"),
		},
		"нет @type": {
			"chat_id":     float64(5),
			"message_id":  float64(1),
			"new_content": textContent("правка"),
		},
		"нет chat_id": {
			"@type":       "updateMessageContent",
			"message_id":  float64(1),
			"new_content": textContent("правка"),
		},
		"нет message_id": {
			"@type":       "updateMessageContent",
			"chat_id":     float64(5),
			"new_content": textContent("правка"),
		},
		"нет new_content": {
			"@type":      "updateMessageContent",
			"chat_id":    float64(5),
			"message_id": float64(1),
		},
		"new_content не объект": {
			"@type":       "updateMessageContent",
			"chat_id":     float64(5),
			"message_id":  float64(1),
			"new_content": "текст",
		},
	}
	for name, update := range cases {
		if _, _, _, ok := ParseMessageContentUpdate(update); ok {
			t.Errorf("случай %q: ok == true, want false", name)
		}
	}
}

// TestParseMessageContentUpdateKeepsUnknownContentHonest — содержимое типа, которого
// нет в разборе, не должно превращаться в отладочную строку «[тип сообщения: X]»
// (это был исходный баг разбора) и не должно оставлять у карточки признаки медиа.
func TestParseMessageContentUpdateKeepsUnknownContentHonest(t *testing.T) {
	_, _, updated, ok := ParseMessageContentUpdate(messageContentUpdate(5, 1, map[string]interface{}{
		"@type": "messageTypeNobodyParsedYet",
	}))

	if !ok {
		t.Fatal("неизвестный тип содержимого не должен делать апдейт невалидным")
	}
	if updated.Text != "[что-то не поддерживается]" {
		t.Errorf("Text = %q, want %q", updated.Text, "[что-то не поддерживается]")
	}
	if updated.IsPhoto || updated.IsVoiceNote {
		t.Errorf("неизвестное содержимое помечено как медиа: %+v", updated)
	}
}
