package auth

import (
	"strings"
	"testing"
)

// contentJSON — сообщение TDLib с указанным content.
func contentJSON(content map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"@type":       "message",
		"id":          float64(1),
		"date":        float64(100),
		"is_outgoing": false,
		"content":     content,
	}
}

func fileObject(id float64) map[string]interface{} {
	return map[string]interface{}{"id": id, "size": float64(1024)}
}

func thumbnailObject(format string) map[string]interface{} {
	return map[string]interface{}{
		"@type":  "thumbnail",
		"width":  float64(100),
		"height": float64(100),
		"file":   fileObject(7),
		"format": map[string]interface{}{"@type": "thumbnailFormat" + format},
	}
}

// TestParseContentCoversEveryMediaFamily — по одному случаю на каждое семейство
// содержимого: разобранный вид, файл, превью и длительность. Поля сверены с
// реальной схемой td_api.h, а не выдуманы.
func TestParseContentCoversEveryMediaFamily(t *testing.T) {
	for _, test := range []struct {
		name    string
		content map[string]interface{}
		want    Content
		wantAll func(Content) bool
	}{
		{
			name: "стикер с превью",
			content: map[string]interface{}{
				"@type": "messageSticker",
				"sticker": map[string]interface{}{
					"id": float64(5), "emoji": "🎉", "sticker": fileObject(11),
					"thumbnail": thumbnailObject("Jpeg"),
				},
			},
			want: Content{
				Kind: ContentSticker, FileID: 11, PreviewFileID: 7, PreviewFormat: "jpeg",
				Emoji: "🎉", Size: 1024,
			},
			wantAll: func(c Content) bool { return c.HasPreview() && c.PreviewSupported() && c.IsVisual() },
		},
		{
			// Регрессия на задачу 0133: превью анимированного эмодзи лежит в
			// animated_emoji.sticker.thumbnail, потому что у самого animatedEmoji
			// в td_api.h полей thumbnail/duration нет вовсе. Тест раньше подставлял
			// несуществующие поля и тем самым закреплял ошибку разбора.
			name: "анимированный эмодзи",
			content: map[string]interface{}{
				"@type": "messageAnimatedEmoji",
				"emoji": "👍",
				"animated_emoji": map[string]interface{}{
					"sticker": map[string]interface{}{"thumbnail": thumbnailObject("Jpeg")},
				},
			},
			want:    Content{Kind: ContentSticker, PreviewFileID: 7, PreviewFormat: "jpeg", Emoji: "👍"},
			wantAll: func(c Content) bool { return c.Emoji == "👍" && c.HasPreview() && c.PreviewSupported() },
		},
		{
			name: "анимация",
			content: map[string]interface{}{
				"@type":     "messageAnimation",
				"animation": map[string]interface{}{"animation": fileObject(12), "duration": float64(12), "file_name": "clips/funny.mp4", "thumbnail": thumbnailObject("Jpeg")},
			},
			want:    Content{Kind: ContentAnimation, FileID: 12, PreviewFileID: 7, PreviewFormat: "jpeg", Duration: 12, FileName: "clips/funny.mp4", Size: 1024},
			wantAll: func(c Content) bool { return c.IsVisual() },
		},
		{
			name: "видео",
			content: map[string]interface{}{
				"@type": "messageVideo",
				"video": map[string]interface{}{"video": fileObject(13), "duration": float64(65), "mime_type": "video/mp4", "thumbnail": thumbnailObject("Jpeg")},
			},
			want:    Content{Kind: ContentVideo, FileID: 13, PreviewFileID: 7, PreviewFormat: "jpeg", Duration: 65, MimeType: "video/mp4", Size: 1024},
			wantAll: func(c Content) bool { return c.IsVisual() && !c.IsAudio() },
		},
		{
			name: "видеозаметка",
			content: map[string]interface{}{
				"@type":      "messageVideoNote",
				"video_note": map[string]interface{}{"video": fileObject(14), "duration": float64(18), "thumbnail": thumbnailObject("Jpeg")},
			},
			want:    Content{Kind: ContentVideoNote, FileID: 14, PreviewFileID: 7, PreviewFormat: "jpeg", Duration: 18, Size: 1024},
			wantAll: func(c Content) bool { return c.IsVisual() },
		},
		{
			// Регрессия на задачу 0133: обложка аудио приходит в
			// album_cover_thumbnail — у class audio поля thumbnail не существует,
			// так что прежний разбор молча терял картинку.
			name: "аудио с названием",
			content: map[string]interface{}{
				"@type": "messageAudio",
				"audio": map[string]interface{}{
					"audio": fileObject(15), "duration": float64(201), "title": "Песня",
					"performer": "Группа", "album_cover_thumbnail": thumbnailObject("Jpeg"),
				},
			},
			want: Content{
				Kind: ContentAudio, FileID: 15, PreviewFileID: 7, PreviewFormat: "jpeg",
				Duration: 201, Title: "Песня", Performer: "Группа", Size: 1024,
			},
			wantAll: func(c Content) bool { return c.IsAudio() && c.HasPreview() && c.PreviewSupported() },
		},
		{
			name: "голосовое",
			content: map[string]interface{}{
				"@type": "messageVoiceNote",
				"voice_note": map[string]interface{}{
					"duration": float64(65),
					"voice":    map[string]interface{}{"id": float64(16), "size": float64(2048)},
				},
			},
			want:    Content{Kind: ContentVoiceNote, FileID: 16, Duration: 65, Size: 2048},
			wantAll: func(c Content) bool { return c.IsAudio() && !c.HasPreview() },
		},
		{
			name: "документ",
			content: map[string]interface{}{
				"@type":    "messageDocument",
				"document": map[string]interface{}{"document": fileObject(17), "file_name": "счёт.pdf", "mime_type": "application/pdf"},
			},
			want:    Content{Kind: ContentDocument, FileID: 17, FileName: "счёт.pdf", MimeType: "application/pdf", Size: 1024},
			wantAll: func(c Content) bool { return !c.HasPreview() },
		},
		{
			name:    "кубик",
			content: map[string]interface{}{"@type": "messageDice", "emoji": "🎯", "value": float64(4)},
			want:    Content{Kind: ContentDice, Emoji: "🎯", Value: 4},
		},
		{
			name:    "геолокация",
			content: map[string]interface{}{"@type": "messageLocation", "latitude": float64(55.7512), "longitude": float64(37.6184)},
			want:    Content{Kind: ContentLocation, Latitude: 55.7512, Longitude: 37.6184},
		},
		{
			name: "место",
			content: map[string]interface{}{
				"@type": "messageVenue", "latitude": float64(1), "longitude": float64(2),
				"venue": map[string]interface{}{"address": "Красная площадь, 1"},
			},
			want: Content{Kind: ContentVenue, Address: "Красная площадь, 1", Latitude: 1, Longitude: 2},
		},
		{
			name: "контакт",
			content: map[string]interface{}{
				"@type":   "messageContact",
				"contact": map[string]interface{}{"phone_number": "+79991234567", "first_name": "Аня", "last_name": "Петрова"},
			},
			want: Content{Kind: ContentContact, PhoneNumber: "+79991234567", FirstName: "Аня", LastName: "Петрова"},
		},
		{
			name: "опрос",
			content: map[string]interface{}{
				"@type": "messagePoll",
				"poll": map[string]interface{}{
					"question": "Что делаем?",
					"options": []interface{}{
						map[string]interface{}{"text": "A"}, map[string]interface{}{"text": "B"},
					},
				},
			},
			want: Content{Kind: ContentPoll, PollQuestion: "Что делаем?", Options: 2},
		},
		{
			name:    "служебное",
			content: map[string]interface{}{"@type": "messageChatJoinByLink", "inviter_user_id": float64(3)},
			want:    Content{Kind: ContentService, ServiceType: "ChatJoinByLink"},
		},
		{
			name:    "неизвестный тип",
			content: map[string]interface{}{"@type": "messageSomethingFromTheFuture"},
			want:    Content{Kind: ContentUnsupported, ServiceType: "messageSomethingFromTheFuture"},
		},
		{
			name:    "текст",
			content: map[string]interface{}{"@type": "messageText", "text": map[string]interface{}{"text": "привет"}},
			want:    Content{Kind: ContentText},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := parseContent(contentJSON(test.content))
			if got.Kind != test.want.Kind {
				t.Fatalf("kind = %v, want %v", got.Kind, test.want.Kind)
			}
			if got != test.want {
				t.Errorf("content = %+v, want %+v", got, test.want)
			}
			if test.wantAll != nil && !test.wantAll(got) {
				t.Errorf("content helpers disagree with %+v", got)
			}
		})
	}
}

// TestContentTextNeverLeaksTheDebugPlaceholder — исходная жалоба: вместо
// содержимого в ленте появлялась строка «[тип сообщения: messageAnimatedEmoji]».
// Ни один тип не должен давать такую подпись.
func TestContentTextNeverLeaksTheDebugPlaceholder(t *testing.T) {
	contents := []map[string]interface{}{
		{"@type": "messageText", "text": map[string]interface{}{"text": "ок"}},
		{"@type": "messagePhoto", "photo": map[string]interface{}{"sizes": []interface{}{
			map[string]interface{}{"width": float64(10), "height": float64(10), "photo": fileObject(1)},
		}}},
		{"@type": "messageSticker", "sticker": map[string]interface{}{"emoji": "🎉"}},
		{"@type": "messageAnimatedEmoji", "emoji": "👍", "animated_emoji": map[string]interface{}{}},
		{"@type": "messageAnimation", "animation": map[string]interface{}{"animation": fileObject(1), "duration": float64(3)}},
		{"@type": "messageVideo", "video": map[string]interface{}{"video": fileObject(1), "duration": float64(9)}},
		{"@type": "messageVideoNote", "video_note": map[string]interface{}{"video": fileObject(1)}},
		{"@type": "messageVoiceNote", "voice_note": map[string]interface{}{"voice": map[string]interface{}{"id": float64(1)}, "duration": float64(4)}},
		{"@type": "messageAudio", "audio": map[string]interface{}{"audio": fileObject(1), "duration": float64(5), "title": "T", "performer": "P"}},
		{"@type": "messageDocument", "document": map[string]interface{}{"document": fileObject(1), "file_name": "f.pdf"}},
		{"@type": "messageDice", "emoji": "🎲", "value": float64(6)},
		{"@type": "messageLocation", "latitude": float64(1), "longitude": float64(2)},
		{"@type": "messageVenue", "venue": map[string]interface{}{"address": "A"}},
		{"@type": "messageContact", "contact": map[string]interface{}{"first_name": "A"}},
		{"@type": "messagePoll", "poll": map[string]interface{}{"question": "Q", "options": []interface{}{}}},
		{"@type": "messageChatJoinByLink"},
		{"@type": "messageSomeBrandNewType"},
		{"@type": "messagePaidMedia"},
		{"@type": "messageStory"},
		{"@type": "messageUnsupported"},
	}
	for _, content := range contents {
		contentType, _ := content["@type"].(string)
		// Проверяем итоговый Text сообщения, а не только contentText: заглушка
		// могла бы вернуться в messageText, и тогда тест на contentText остался бы
		// зелёным, а в ленте снова поехала бы отладочная строка.
		text := parseMessage(t.Context(), nil, contentJSON(content)).Text
		if strings.Contains(text, "тип сообщения") {
			t.Errorf("%s: text = %q, still the debug placeholder", contentType, text)
		}
		if strings.Contains(text, "message") {
			t.Errorf("%s: text = %q, leaks the raw type name", contentType, text)
		}
	}
}

// TestParseContentSurvivesBrokenPayloads — битый content не должен паниковать и не
// должен превращаться в отладочную строку.
func TestParseContentSurvivesBrokenPayloads(t *testing.T) {
	for name, raw := range map[string]map[string]interface{}{
		"нет content":         map[string]interface{}{"@type": "message"},
		"content не объект":   map[string]interface{}{"@type": "message", "content": "строка"},
		"content без типа":    map[string]interface{}{"@type": "message", "content": map[string]interface{}{}},
		"пустой sticker":      map[string]interface{}{"@type": "message", "content": map[string]interface{}{"@type": "messageSticker"}},
		"пустой audio":        map[string]interface{}{"@type": "message", "content": map[string]interface{}{"@type": "messageAudio"}},
		"file без id":         map[string]interface{}{"@type": "message", "content": map[string]interface{}{"@type": "messageDocument", "document": map[string]interface{}{"document": map[string]interface{}{}}}},
		"thumbnail без файла": map[string]interface{}{"@type": "message", "content": map[string]interface{}{"@type": "messageVideo", "video": map[string]interface{}{"thumbnail": map[string]interface{}{}}}},
	} {
		t.Run(name, func(t *testing.T) {
			got := parseContent(raw)
			if text := contentText(got); strings.Contains(text, "тип сообщения") {
				t.Fatalf("text = %q, debug placeholder leaked", text)
			}
		})
	}
}

// TestAnimatedEmojiPreviewComesFromNestedSticker — регрессия на задачу 0133.
//
// Разбор читал превью и длительность из animated_emoji.thumbnail/duration, но у
// class animatedEmoji в td_api.h (.h:562) таких полей нет вообще: sticker_,
// sticker_width_, sticker_height_, fitzpatrick_type_, sound_. Превью лежит
// внутри вложенного sticker. Ошибка была неочевидна, потому что JSON собирался
// в тестах вручную и повторял то же неверное поле.
//
// Тест строит объект РОВНО по схеме (никакого thumbnail на верхнем уровне) и
// требует, чтобы превью нашлось — и через sticker, и никогда из несуществующего
// поля.
func TestAnimatedEmojiPreviewComesFromNestedSticker(t *testing.T) {
	content := map[string]interface{}{
		"@type": "messageAnimatedEmoji",
		"emoji": "👍",
		"animated_emoji": map[string]interface{}{
			// Ровно те поля, что объявлены у animatedEmoji в схеме.
			"sticker_width_":    float64(100),
			"sticker_height_":   float64(100),
			"fitzpatrick_type_": float64(0),
			"sticker": map[string]interface{}{
				"sticker":   fileObject(31),
				"thumbnail": thumbnailObject("Jpeg"),
			},
		},
	}
	got := parseMessage(t.Context(), nil, contentJSON(content)).Content
	if got.Kind != ContentSticker {
		t.Fatalf("Kind = %v, want ContentSticker", got.Kind)
	}
	if got.Emoji != "👍" {
		t.Errorf("Emoji = %q, want 👍", got.Emoji)
	}
	if got.PreviewFileID != 7 {
		t.Errorf("PreviewFileID = %d, want 7 (thumbnail вложенного sticker)", got.PreviewFileID)
	}
	if got.PreviewFormat != "jpeg" {
		t.Errorf("PreviewFormat = %q, want jpeg", got.PreviewFormat)
	}
	if !got.HasPreview() || !got.PreviewSupported() {
		t.Errorf("HasPreview=%v PreviewSupported=%v, want оба true", got.HasPreview(), got.PreviewSupported())
	}
	// Длительности у animatedEmoji в схеме нет — значит и показывать её не от чего.
	if got.Duration != 0 {
		t.Errorf("Duration = %d, want 0: у animatedEmoji нет поля duration", got.Duration)
	}
}

// TestAudioPreviewComesFromAlbumCoverThumbnail — регрессия на задачу 0133.
//
// Разбор читал обложку из audio.thumbnail, но у class audio в td_api.h (.h:837)
// поля thumbnail нет: обложек ровно три, и все названы album_cover_*
// (album_cover_minithumbnail_, album_cover_thumbnail_, external_album_covers_).
func TestAudioPreviewComesFromAlbumCoverThumbnail(t *testing.T) {
	content := map[string]interface{}{
		"@type": "messageAudio",
		"audio": map[string]interface{}{
			"audio":                 fileObject(41),
			"duration":              float64(201),
			"title":                 "Песня",
			"performer":             "Группа",
			"file_name":             "track.mp3",
			"mime_type":             "audio/mpeg",
			"album_cover_thumbnail": thumbnailObject("Jpeg"),
		},
	}
	got := parseMessage(t.Context(), nil, contentJSON(content)).Content
	if got.Kind != ContentAudio {
		t.Fatalf("Kind = %v, want ContentAudio", got.Kind)
	}
	if got.PreviewFileID != 7 {
		t.Errorf("PreviewFileID = %d, want 7 (album_cover_thumbnail)", got.PreviewFileID)
	}
	if got.PreviewFormat != "jpeg" {
		t.Errorf("PreviewFormat = %q, want jpeg", got.PreviewFormat)
	}
	if !got.HasPreview() {
		t.Errorf("HasPreview = false, want true: обложка аудио должна находиться")
	}

	// Контроль: если бы разбор всё ещё смотрел в audio.thumbnail, подмена одного
	// только настоящего имени на выдуманное дала бы другой результат.
	withFakeField := map[string]interface{}{
		"@type": "messageAudio",
		"audio": map[string]interface{}{
			"audio":     fileObject(41),
			"thumbnail": thumbnailObject("Jpeg"),
		},
	}
	if fake := parseMessage(t.Context(), nil, contentJSON(withFakeField)).Content; fake.HasPreview() {
		t.Errorf("превью нашлось по несуществующему полю audio.thumbnail: %+v", fake)
	}
}

// TestPreviewFormatGateKeepsUnsupportedFormatsOut — анимированные типы приходят в
// webp/tgs/webm, которые терминал не рисует. Показывать такое превью нельзя:
// вместо картинки останется пустое место, поэтому формат проверяется, и при
// неподдерживаемом типе остаётся только текст.
func TestPreviewFormatGateKeepsUnsupportedFormatsOut(t *testing.T) {
	for format, want := range map[string]bool{
		"jpeg": true, "jpg": true, "png": true, "Jpeg": true,
		"webp": false, "gif": false, "tgs": false, "webm": false, "mpeg4": false, "": false,
	} {
		if got := (Content{PreviewFormat: format}).PreviewSupported(); got != want {
			t.Errorf("format %q: PreviewSupported = %v, want %v", format, got, want)
		}
	}
}

// TestParseMessageFillsLegacyPhotoAndVoiceFields — старые поля IsPhoto/IsVoiceNote
// и FileID/Size заполняются из разбора: ими пользуются оба TUI, и они не должны
// разойтись с новым Content.
func TestParseMessageFillsLegacyPhotoAndVoiceFields(t *testing.T) {
	photo := parseMessage(t.Context(), nil, contentJSON(map[string]interface{}{
		"@type": "messagePhoto",
		"photo": map[string]interface{}{"sizes": []interface{}{
			map[string]interface{}{"width": float64(10), "height": float64(10), "photo": fileObject(21)},
			map[string]interface{}{"width": float64(100), "height": float64(100), "photo": fileObject(22)},
		}},
	}))
	if !photo.IsPhoto || photo.PhotoFileID != 22 {
		t.Fatalf("photo fields = %t/%d, want true/22 (самая большая)", photo.IsPhoto, photo.PhotoFileID)
	}
	if photo.Content.Kind != ContentPhoto || photo.Content.FileID != 22 {
		t.Fatalf("content = %+v, want a photo with file 22", photo.Content)
	}

	voice := parseMessage(t.Context(), nil, contentJSON(map[string]interface{}{
		"@type": "messageVoiceNote",
		"voice_note": map[string]interface{}{
			"duration": float64(65),
			"voice":    map[string]interface{}{"id": float64(31), "size": float64(4096)},
		},
	}))
	if !voice.IsVoiceNote || voice.VoiceFileID != 31 || voice.VoiceDuration != 65 || voice.VoiceSize != 4096 {
		t.Fatalf("voice fields = %+v, want file 31, 65s, 4096 bytes", voice)
	}
	if voice.Text != "▶ голосовое [1:05]" {
		t.Fatalf("voice text = %q", voice.Text)
	}
}
