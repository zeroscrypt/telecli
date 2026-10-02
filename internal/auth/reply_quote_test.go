package auth

import (
	"context"
	"testing"
)

// replyQuoteMap — сообщение TDLib с ответом: цитата из textQuote либо, если её
// нет, содержимое оригинала. Ключи и вложенность — по td_api.h собранной версии
// (messageReplyToMessage -> quote -> text -> text, либо content -> @type).
func replyQuoteMap(reply map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id":       float64(2),
		"date":     float64(1700000000),
		"content":  map[string]interface{}{"@type": "messageText", "text": map[string]interface{}{"text": "мой ответ"}},
		"reply_to": reply,
	}
}

func quoteReply(text string) map[string]interface{} {
	return map[string]interface{}{
		"@type":      "messageReplyToMessage",
		"chat_id":    float64(1),
		"message_id": float64(1),
		"quote": map[string]interface{}{
			"@type": "textQuote",
			"text":  map[string]interface{}{"@type": "formattedText", "text": text},
		},
	}
}

func TestParseReplyQuoteTakesTheQuotedText(t *testing.T) {
	got := parseReplyQuote(replyQuoteMap(quoteReply("Привет - как дела?")))
	if got != "Привет - как дела?" {
		t.Fatalf("цитата = %q, want %q", got, "Привет - как дела?")
	}
}

// TDLib присылает quote_ только при ручном выделении фрагмента. У обычного
// ответа на текстовое сообщение quote_ пуст, и цитата обязана прийти из content_
// — иначе на живом скриншоте ответа на текст цитаты не видно (реальная находка).
func TestParseReplyQuoteFallsBackToTheTextOfATextMessage(t *testing.T) {
	reply := map[string]interface{}{
		"@type":      "messageReplyToMessage",
		"chat_id":    float64(1),
		"message_id": float64(1),
		"content": map[string]interface{}{
			"@type": "messageText",
			"text":  map[string]interface{}{"@type": "formattedText", "text": "вот на что я отвечаю"},
		},
	}
	got := parseReplyQuote(replyQuoteMap(reply))
	if got != "вот на что я отвечаю" {
		t.Fatalf("цитата текстового оригинала = %q, want %q", got, "вот на что я отвечаю")
	}
}

// Явная цитата фрагмента важнее полного текста оригинала: человек ответил на
// выделенную часть, и именно её надо показать.
func TestParseReplyQuotePrefersTheSelectedFragmentOverTheFullText(t *testing.T) {
	reply := map[string]interface{}{
		"@type":      "messageReplyToMessage",
		"chat_id":    float64(1),
		"message_id": float64(1),
		"quote": map[string]interface{}{
			"@type": "textQuote",
			"text":  map[string]interface{}{"@type": "formattedText", "text": "вот только это"},
		},
		"content": map[string]interface{}{
			"@type": "messageText",
			"text":  map[string]interface{}{"@type": "formattedText", "text": "а вот это всё сообщение целиком"},
		},
	}
	if got := parseReplyQuote(replyQuoteMap(reply)); got != "вот только это" {
		t.Fatalf("цитата = %q, want выделенный фрагмент %q", got, "вот только это")
	}
}

func TestParseReplyQuoteFallsBackToTheOriginalContentLabel(t *testing.T) {
	// Ответ на фото: textQuote нет, подпись обязана прийти из content_ тем же
	// разбором, что и основное тело.
	reply := map[string]interface{}{
		"@type":      "messageReplyToMessage",
		"chat_id":    float64(1),
		"message_id": float64(1),
		"content":    map[string]interface{}{"@type": "messagePhoto"},
	}
	if got := parseReplyQuote(replyQuoteMap(reply)); got != "[фото]" {
		t.Fatalf("подпись фото = %q, want %q", got, "[фото]")
	}

	// Ответ на голосовое: у него есть file id, но для подписи важны только
	// длительность и тип.
	voice := map[string]interface{}{
		"@type":      "messageReplyToMessage",
		"chat_id":    float64(1),
		"message_id": float64(1),
		"content": map[string]interface{}{
			"@type": "messageVoiceNote",
			"voice_note": map[string]interface{}{
				"duration": float64(12),
				"voice":    map[string]interface{}{"id": float64(5), "size": float64(1000)},
			},
		},
	}
	if got := parseReplyQuote(replyQuoteMap(voice)); got != "▶ голосовое [0:12]" {
		t.Fatalf("подпись голосового = %q, want %q", got, "▶ голосовое [0:12]")
	}
}

func TestParseReplyQuoteIsEmptyWithoutAReply(t *testing.T) {
	if got := parseReplyQuote(map[string]interface{}{
		"content": map[string]interface{}{"@type": "messageText"},
	}); got != "" {
		t.Fatalf("без ответа цитата = %q, want пусто", got)
	}
}

func TestParseReplyQuoteIsEmptyForAReplyToStory(t *testing.T) {
	// У messageReplyToStory нет ни message_id, ни цитаты: показывать нечего, и
	// карточка остаётся обычной.
	story := map[string]interface{}{"@type": "messageReplyToStory", "chat_id": float64(1)}
	if got := parseReplyQuote(replyQuoteMap(story)); got != "" {
		t.Fatalf("ответ на сторис: цитата = %q, want пусто", got)
	}
	if got := parseReplyToMessageID(replyQuoteMap(story)); got != 0 {
		t.Fatalf("ответ на сторис: ReplyToMessageID = %d, want 0", got)
	}
}

func TestParseReplyQuoteSurvivesBrokenShapes(t *testing.T) {
	// Мусор в полях не должен ни падать, ни подставлять мусор в шапку.
	cases := []struct {
		name  string
		reply map[string]interface{}
	}{
		{"reply без цитаты и без содержимого", map[string]interface{}{"@type": "messageReplyToMessage"}},
		{"quote без text", map[string]interface{}{"@type": "messageReplyToMessage", "quote": map[string]interface{}{"@type": "textQuote"}}},
		{"text не строка", map[string]interface{}{"@type": "messageReplyToMessage",
			"quote": map[string]interface{}{"text": map[string]interface{}{"text": float64(42)}}}},
		{"content не объект", map[string]interface{}{"@type": "messageReplyToMessage", "content": "мусор"}},
		{"content без типа", map[string]interface{}{"@type": "messageReplyToMessage", "content": map[string]interface{}{}}},
		{"reply не объект", map[string]interface{}{"@type": "messageReplyToMessage", "quote": "мусор"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := parseReplyQuote(replyQuoteMap(test.reply)); got != "" {
				t.Fatalf("цитата = %q, want пусто для битой формы", got)
			}
		})
	}
}

// Типа, которого нет в разборе, contentText честно называет «[что-то не
// поддерживается» — и в цитате должно быть то же самое, а не пустота: иначе
// ответ на сообщение нового типа выглядел бы как сообщение без ответа.
func TestParseReplyQuoteNamesAnUnknownContentKind(t *testing.T) {
	reply := map[string]interface{}{"@type": "messageReplyToMessage",
		"content": map[string]interface{}{"@type": "messageSomethingNew"}}
	if got := parseReplyQuote(replyQuoteMap(reply)); got != "[что-то не поддерживается]" {
		t.Fatalf("подпись неизвестного типа = %q, want %q", got, "[что-то не поддерживается]")
	}
}

func TestParseReplyQuoteReachesTheMessage(t *testing.T) {
	// Сквозная проверка: разбор не просто правильный на куске JSON, а реально
	// попадает в Message, из которого рисуется карточка.
	msg := parseMessage(context.Background(), nil, replyQuoteMap(quoteReply("Привет - как дела?")))
	if msg.ReplyQuote != "Привет - как дела?" {
		t.Fatalf("Message.ReplyQuote = %q, want %q", msg.ReplyQuote, "Привет - как дела?")
	}
	if msg.ReplyToMessageID != 1 {
		t.Fatalf("Message.ReplyToMessageID = %d, want 1", msg.ReplyToMessageID)
	}
}
