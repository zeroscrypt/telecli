package auth

import (
	"context"
	"fmt"
	"time"
)

// Message — одно сообщение, готовое к отображению в TUI. Резолв имени
// отправителя выполняется во время загрузки (см. GetMessages), а не в момент
// отрисовки: лента строится разом как снимок, без live-обновлений.
type Message struct {
	ID         int64
	SenderName string
	IsOutgoing bool
	Date       int64  // unix-время, как пришло от TDLib
	Text       string // готовый к отображению текст (либо плейсхолдер для не-текстовых типов)

	IsVoiceNote   bool  // true — content имеет тип messageVoiceNote с валидным voice_note.voice.id
	VoiceFileID   int32 // file.id голосового (для downloadFile) — см. parseMessage
	VoiceDuration int   // duration в секундах (JSON-ключ без _, см. файл задачи)
	VoiceSize     int64 // ожидаемый размер файла в байтах (voice_note.voice.size) — для проверки готовности файла на диске перед запуском плеера, см. задачу 0042
}

// historyRetryDelays — задержки между повторными попытками getChatHistory,
// если первый ответ короче limit (может означать, что чат ещё не полностью
// синхронизирован фоном после openChat — задача 0008 показала, что сама по
// себе openChat этого не гарантирует). Переменная, не константа — тесты
// подменяют на мгновенные задержки, чтобы не ждать реальные секунды.
var historyRetryDelays = []time.Duration{300 * time.Millisecond, 600 * time.Millisecond, 1200 * time.Millisecond}

// GetMessages загружает до limit последних сообщений чата chatID и возвращает
// их в хронологическом порядке (старые сверху, новые снизу).
//
// Если ответ getChatHistory короче limit, чат мог быть ещё не полностью
// синхронизирован фоном после openChat — в этом случае запрос повторяется с
// нарастающей задержкой (см. historyRetryDelays), берётся последний результат.
// Отличить «мало сообщений» от «ещё не досинхронизировано» надёжно нельзя,
// поэтому retry применяется одинаково в обоих случаях (осознанное упрощение).
func GetMessages(ctx context.Context, client TDClientInterface, chatID int64, limit int) ([]Message, error) {
	messages, err := fetchChatHistoryOnce(ctx, client, chatID, limit)
	if err != nil {
		return nil, err
	}
	for _, delay := range historyRetryDelays {
		if len(messages) >= limit {
			break
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return messages, nil
		}
		retried, err := fetchChatHistoryOnce(ctx, client, chatID, limit)
		if err != nil {
			return messages, nil
		}
		messages = retried
	}
	return messages, nil
}

// fetchChatHistoryOnce выполняет один запрос getChatHistory и парсит ответ в
// хронологическом порядке. Схема (сверено с td_api.h текущей собранной версии
// TDLib): getChatHistory с from_message_id=0 и offset=0 отдаёт самые новые
// сообщения; каждый message содержит id, sender_id (messageSenderUser.user_id
// или messageSenderChat.chat_id), is_outgoing, date, content; текст содержится
// только в content.@type "messageText" → content.text.text. TDLib отдаёт
// сообщения от новых к старым — порядок разворачивается перед возвратом.
func fetchChatHistoryOnce(ctx context.Context, client TDClientInterface, chatID int64, limit int) ([]Message, error) {
	resp, err := client.Send(ctx, map[string]interface{}{
		"@type":           "getChatHistory",
		"chat_id":         chatID,
		"from_message_id": 0,
		"offset":          0,
		"limit":           limit,
		"only_local":      false,
	})
	if err != nil {
		return nil, fmt.Errorf("getChatHistory failed: %w", err)
	}

	if resp["@type"] != "messages" {
		return nil, fmt.Errorf("unexpected response type: %v", resp["@type"])
	}

	messagesRaw, ok := resp["messages"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid messages format")
	}

	messages := make([]Message, 0, len(messagesRaw))
	for _, mRaw := range messagesRaw {
		msgMap, ok := mRaw.(map[string]interface{})
		if !ok {
			continue
		}
		messages = append(messages, parseMessage(ctx, client, msgMap))
	}

	// TDLib отдаёт сообщения от новых к старым — разворачиваем в привычный
	// хронологический порядок (старые сверху, новые снизу).
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}

	return messages, nil
}

// parseMessage строит Message из одного JSON-объекта TDLib "message" —
// используется и для истории (GetMessages), и для ответа отправки (SendMessage).
func parseMessage(ctx context.Context, client TDClientInterface, msgMap map[string]interface{}) Message {
	msg := Message{
		SenderName: resolveSender(ctx, client, msgMap),
		Text:       messageText(msgMap),
	}
	if fileID, dur, size, ok := voiceNoteInfo(msgMap); ok {
		msg.IsVoiceNote = true
		msg.VoiceFileID = fileID
		msg.VoiceDuration = dur
		msg.VoiceSize = size
		msg.Text = fmt.Sprintf("▶ голосовое [%s]", formatVoiceDuration(dur))
	}
	if id, ok := msgMap["id"].(float64); ok {
		msg.ID = int64(id)
	}
	if out, ok := msgMap["is_outgoing"].(bool); ok {
		msg.IsOutgoing = out
	}
	if date, ok := msgMap["date"].(float64); ok {
		msg.Date = int64(date)
	}
	return msg
}

// voiceNoteInfo извлекает голосовое из content (schema сверена с td_api.h,
// см. файл задачи). ok=false — content не messageVoiceNote или битый:
// в этом случае используется обычный messageText (плейсхолдер).
// Возвращаемые значения: file.id, voice_note.duration, file.size (ожидаемый
// размер в байтах; 0 — если TDLib его не сообщил — допустимый случай).
func voiceNoteInfo(msg map[string]interface{}) (fileID int32, duration int, size int64, ok bool) {
	content, ok := msg["content"].(map[string]interface{})
	if !ok {
		return 0, 0, 0, false
	}
	if contentType, _ := content["@type"].(string); contentType != "messageVoiceNote" {
		return 0, 0, 0, false
	}
	vn, ok := content["voice_note"].(map[string]interface{})
	if !ok {
		return 0, 0, 0, false
	}
	voice, ok := vn["voice"].(map[string]interface{})
	if !ok {
		return 0, 0, 0, false
	}
	id, ok := voice["id"].(float64)
	if !ok || id <= 0 {
		return 0, 0, 0, false
	}
	dur, _ := vn["duration"].(float64)
	sizeF, _ := voice["size"].(float64)
	return int32(id), int(dur), int64(sizeF), true
}

// formatVoiceDuration — продолжительность голосового в "M:SS": минуты без
// ведущего нуля, секунды всегда двузначными. Часы не обрабатываются
// (голосовые длиннее часа не встречаются практически; если вдруг — минуты
// просто больше 60, "87:05" — читаемо).
func formatVoiceDuration(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

// SendMessage отправляет текстовое сообщение в чат chatID и возвращает его
// как Message (та же схема ответа, что у истории — см. parseMessage).
// Остальные поля sendMessage/inputMessageText (reply_to, topic_id, превью
// ссылок, entities и т.д.) — вне скоупа: отправляем голый текст, как и при
// чтении истории.
func SendMessage(ctx context.Context, client TDClientInterface, chatID int64, text string) (Message, error) {
	resp, err := client.Send(ctx, map[string]interface{}{
		"@type":   "sendMessage",
		"chat_id": chatID,
		"input_message_content": map[string]interface{}{
			"@type": "inputMessageText",
			"text": map[string]interface{}{
				"@type": "formattedText",
				"text":  text,
			},
		},
	})
	if err != nil {
		return Message{}, fmt.Errorf("sendMessage failed: %w", err)
	}
	if resp["@type"] != "message" {
		return Message{}, fmt.Errorf("unexpected response type: %v", resp["@type"])
	}
	return parseMessage(ctx, client, resp), nil
}

// messageText извлекает текст сообщения из content. Для не-текстовых типов
// содержимого (фото, стикер, голосовое и т.д.) возвращает плейсхолдер —
// полный рендеринг медиа в этот Roadmap-пункт не входит.
func messageText(msg map[string]interface{}) string {
	content, ok := msg["content"].(map[string]interface{})
	if !ok {
		return ""
	}
	contentType, _ := content["@type"].(string)
	if contentType != "messageText" {
		if contentType == "" {
			return ""
		}
		return fmt.Sprintf("[тип сообщения: %s]", contentType)
	}
	formatted, ok := content["text"].(map[string]interface{})
	if !ok {
		return ""
	}
	text, _ := formatted["text"].(string)
	return text
}

// resolveSender возвращает отображаемое имя отправителя сообщения: "Вы" для
// исходящих, имя пользователя (getUser) или название чата (getChat) для
// входящих. При ошибке резолва не блокирует отображение сообщения — вместо
// имени подставляется сырой id (user#<id> / chat#<id>), та же логика
// деградации, что в GetChats для названия чата.
func resolveSender(ctx context.Context, client TDClientInterface, msg map[string]interface{}) string {
	if out, ok := msg["is_outgoing"].(bool); ok && out {
		return "Вы"
	}

	sender, ok := msg["sender_id"].(map[string]interface{})
	if !ok {
		return ""
	}

	switch sender["@type"] {
	case "messageSenderUser":
		userID, ok := sender["user_id"].(float64)
		if !ok || userID == 0 {
			return ""
		}
		resp, err := client.Send(ctx, map[string]interface{}{
			"@type":   "getUser",
			"user_id": int64(userID),
		})
		if err != nil {
			return fmt.Sprintf("user#%d", int64(userID))
		}
		firstName, _ := resp["first_name"].(string)
		lastName, _ := resp["last_name"].(string)
		name := firstName
		if lastName != "" {
			if name != "" {
				name += " "
			}
			name += lastName
		}
		if name == "" {
			return fmt.Sprintf("user#%d", int64(userID))
		}
		return name

	case "messageSenderChat":
		chatID, ok := sender["chat_id"].(float64)
		if !ok || chatID == 0 {
			return ""
		}
		resp, err := client.Send(ctx, map[string]interface{}{
			"@type":   "getChat",
			"chat_id": int64(chatID),
		})
		if err != nil {
			return fmt.Sprintf("chat#%d", int64(chatID))
		}
		title, _ := resp["title"].(string)
		if title == "" {
			return fmt.Sprintf("chat#%d", int64(chatID))
		}
		return title
	}

	return ""
}
