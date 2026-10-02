package auth

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Message — одно сообщение, готовое к отображению в TUI. Резолв имени
// отправителя выполняется во время загрузки (см. GetMessages) или разбора
// live-обновления, а не в момент отрисовки.
type Message struct {
	ID         int64
	SenderName string
	// SenderUserID — user_id отправителя, если это пользователь
	// (messageSenderUser). 0 для исходящих ("Вы"), сообщений от имени
	// чата/канала (messageSenderChat) и при ошибке резолва. Нужен, чтобы найти
	// уже загруженные сообщения этого пользователя при updateUser, не для
	// отображения.
	SenderUserID int64
	IsOutgoing   bool
	Date         int64  // unix-время, как пришло от TDLib
	Text         string // готовый к отображению текст (либо плейсхолдер для не-текстовых типов)

	// AuthorSignature — подпись автора поста, если она включена у канала.
	// Поле TDLib message.author_signature_ (сверено с ~/.local/tdlib/include/td/telegram/td_api.h
	// собранной версии: "string const &author_signature_" в классе message). Пустая
	// строка — норма, а не сбой разбора: подпись есть далеко не у каждого поста.
	// Назначение — заголовок карточки стены tgwall («канал — подпись»), у
	// tgclitui поле просто не используется.
	AuthorSignature string

	IsVoiceNote   bool  // true — content имеет тип messageVoiceNote с валидным voice_note.voice.id
	VoiceFileID   int32 // file.id голосового (для downloadFile) — см. parseMessage
	VoiceDuration int   // duration в секундах (JSON-ключ без _, см. файл задачи)
	VoiceSize     int64 // размер файла в байтах (voice_note.voice.size) — дополнительная проверка локальной копии перед воспроизведением
	IsPhoto       bool
	PhotoFileID   int32
	PhotoBase64   string

	// ReplyToMessageID — сообщение, на которое это отвечает; ноль, если ответа нет.
	// TDLib отдаёт объект reply_to с вариантами (сверено с td_api.h собранной
	// версии 1.8.67: messageReplyToMessage несёт chat_id и message_id, а
	// messageReplyToStory — нет). Разбирается только messageReplyToMessage.
	ReplyToMessageID int64
	// ReplyToChatID — чат, в котором лежит цитируемое сообщение. В TDLib он лежит
	// рядом с message_id в том же reply_to, но нужен не сам по себе: message_id
	// уникален в пределах чата, а лента смешивает сообщения разных чатов, и поиск
	// оригинала только по id подставил бы чужой текст.
	ReplyToChatID int64

	// ReplyQuote — что показать в шапке карточки после имени и времени, одной
	// строкой и приглушённо: либо текст, на который ответили (reply_to.quote.
	// text.text — у textQuote поле text это formattedText с готовой строкой
	// text_, сущности для однострочного превью не нужны), либо короткая подпись
	// по типу, если оригинал нетекстовый и цитаты в нём нет. Пусто, если ответа
	// нет либо это messageReplyToStory: у ответа на сторис цитаты нет по
	// построению.
	ReplyQuote string

	// Content — разобранное содержимое для типов, которых раньше не умели
	// показывать: стикер, анимация, видео, аудио, документ, кубик, геолокация,
	// контакт, опрос, служебные сообщения. Для фото и голосового поля выше
	// остаются заполненными — ими пользуются оба TUI, — а этот тип даёт то же
	// самое единым образом плюс всё остальное.
	Content Content

	// Reactions — реакции под сообщением с эмодзи (см. reactions.go). Пусто у
	// большинства сообщений, и это норма, а не отсутствие данных: счётчики лежат
	// в message.interaction_info.reactions, а не на верхнем уровне message.
	Reactions []MessageReaction

	// UnreadReactionCount — сколько реакций на этом сообщении ещё не увидены (TDLib:
	// updateMessageUnreadReactions.unread_reaction_count). Ноль — увиденные или их нет.
	UnreadReactionCount int32
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
	return fetchChatHistory(ctx, client, chatID, 0, limit)
}

// fetchChatHistory — один запрос getChatHistory от точки fromMessageID. Ноль в
// fromMessageID означает «от последнего сообщения», как требует TDLib.
//
// Схема сверена с официальной документацией td_api (getChatHistory):
// from_message_id — «идентификатор сообщения, с которого брать историю», offset_ —
// «0, чтобы получить результаты начиная ровно с from_message_id», limit — «не больше
// 100». Сообщения TDLib отдаёт в обратном хронологическом порядке, порядок
// разворачивается ниже.
func fetchChatHistory(ctx context.Context, client TDClientInterface, chatID, fromMessageID int64, limit int) ([]Message, error) {
	resp, err := client.Send(ctx, map[string]interface{}{
		"@type":           "getChatHistory",
		"chat_id":         chatID,
		"from_message_id": fromMessageID,
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

// parseReplyToMessageID — идентификатор сообщения, на которое отвечают.
// TDLib кладёт в reply_to объект с @type; у messageReplyToMessage есть
// message_id, у других вариантов (например, ответа на сторис) — нет, и тогда
// возвращается ноль: карточка покажет обычный заголовок, как у простого
// сообщения.
func parseReplyToMessageID(msgMap map[string]interface{}) int64 {
	replyRaw, ok := msgMap["reply_to"].(map[string]interface{})
	if !ok {
		return 0
	}
	if name, _ := replyRaw["@type"].(string); name != "messageReplyToMessage" {
		return 0
	}
	messageID, _ := replyRaw["message_id"].(float64)
	return int64(messageID)
}

// parseReplyQuote — однострочная цитата для шапки карточки. Сверно с
// td_api.h собранной версии: messageReplyToMessage несёт quote_ (textQuote с
// text_ → formattedText, где text_ — готовая строка) и content_ (полное
// содержимое оригинала). Сначала берётся цитата: у ручного ответа на фрагмент
// это ровно выделенный кусок, а у ответа на сообщение — его начало, и в обоих
// случаях это ровно то, что человек процитировал. Если цитаты нет (ответ на
// фото, голосовое, файл), подпись берётся из content_ ТЕМ ЖЕ разбором, что и
// основное тело сообщения, — иначе задача 0105, расширяющая медиа, улучшила бы
// карточки, но оставила бы цитаты позади.
// parseReplyToChatID — чат цитируемого сообщения, см. ReplyToChatID. Лежит в том
// же объекте reply_to, что и message_id.
func parseReplyToChatID(msgMap map[string]interface{}) int64 {
	replyRaw, ok := msgMap["reply_to"].(map[string]interface{})
	if !ok {
		return 0
	}
	if name, _ := replyRaw["@type"].(string); name != "messageReplyToMessage" {
		return 0
	}
	chatID, _ := replyRaw["chat_id"].(float64)
	return int64(chatID)
}

func parseReplyQuote(msgMap map[string]interface{}) string {
	replyRaw, ok := msgMap["reply_to"].(map[string]interface{})
	if !ok {
		return ""
	}
	if name, _ := replyRaw["@type"].(string); name != "messageReplyToMessage" {
		return ""
	}
	if quote, ok := replyRaw["quote"].(map[string]interface{}); ok {
		if formatted, ok := quote["text"].(map[string]interface{}); ok {
			if text, _ := formatted["text"].(string); text != "" {
				return text
			}
		}
	}
	content, ok := replyRaw["content"].(map[string]interface{})
	if !ok {
		return ""
	}
	// Для текстового оригинала берётся сам текст, а не contentText: тот для
	// ContentText возвращает пустую строку нарочно (у основного тела текст приходит
	// из messageText, и contentText его не дублирует). TDLib присылает quote_
	// только когда человек выделил фрагмент вручную, поэтому у обычного ответа на
	// текст quote_ пуст и цитата приходит именно отсюда — иначе на вашем же
	// скриншоте ответа на текстовое сообщение цитаты не было бы видно.
	if text, _ := content["text"].(map[string]interface{}); text != nil {
		if value, _ := text["text"].(string); value != "" {
			return value
		}
	}
	return contentText(parseContentObject(content))
}

// parseMessage строит Message из одного JSON-объекта TDLib "message" —
// используется и для истории (GetMessages), и для ответа отправки (SendMessage).
func parseMessage(ctx context.Context, client TDClientInterface, msgMap map[string]interface{}) Message {
	content := parseContent(msgMap)
	senderName, senderUserID := resolveSender(ctx, client, msgMap)
	msg := Message{
		SenderName:       senderName,
		SenderUserID:     senderUserID,
		ReplyToMessageID: parseReplyToMessageID(msgMap),
		ReplyToChatID:    parseReplyToChatID(msgMap),
		ReplyQuote:       parseReplyQuote(msgMap),
		Reactions:        parseReactions(msgMap),
	}
	// Подпись автора поста — необязательное поле, поэтому читается «как есть»:
	// отсутствие ключа и пустая строка означают одно и то же (подписи нет).
	// Приводится к строке здесь, в parseMessage, а не в отрисовке: Message —
	// это уже разобранный TDLib, и держать в нём interface{} незачем.
	if signature, ok := msgMap["author_signature"].(string); ok {
		msg.AuthorSignature = signature
	}
	applyContentToMessage(&msg, content, messageText(msgMap))
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

// applyContentToMessage переносит в msg РОВНО те поля Message, что выводятся из
// содержимого сообщения: сам Content, текст карточки и признаки голосового и
// фото с их файлами. text — сырой текст сообщения (messageText); он же остаётся
// на месте, если содержимое нетекстовое и подпись собирает contentText.
//
// Общий хелпер у parseMessage и ParseMessageContentUpdate, и совпадение списка
// полей у них обязано быть дословным: правка сообщения меняет только
// содержимое, значит пересчитывать надо ровно его. Поэтому поля не
// «дописываются», а ВЫСТРАИВАЮТСЯ заново, вместе с обнулением. Сообщение, к
// которому применили апдейт, уже есть, и без сброса правка «с фото на текст»
// оставила бы у карточки хвосты от прежнего типа (IsPhoto при новом тексте) —
// то есть лента показывала бы то, чего в чате уже нет.
func applyContentToMessage(msg *Message, content Content, text string) {
	// Непустой разбор заменяет текст: раньше здесь была заглушка
	// «[тип сообщения: %s]», из-за которой в ленте появлялась отладочная строка
	// вместо содержимого.
	if placeholder := contentText(content); placeholder != "" {
		text = placeholder
	}
	msg.Content = content
	msg.Text = text
	// Голосовое и фото помечаются только при наличии файла: без file.id
	// скачивать нечего, и карточка показывала бы плеер, которому нечего играть.
	msg.IsVoiceNote = content.Kind == ContentVoiceNote && content.FileID > 0
	if msg.IsVoiceNote {
		msg.VoiceFileID = content.FileID
		msg.VoiceDuration = content.Duration
		msg.VoiceSize = content.Size
	} else {
		msg.VoiceFileID = 0
		msg.VoiceDuration = 0
		msg.VoiceSize = 0
	}
	msg.IsPhoto = content.Kind == ContentPhoto && content.FileID > 0
	if msg.IsPhoto {
		msg.PhotoFileID = content.FileID
	} else {
		msg.PhotoFileID = 0
	}
}

// voiceNoteInfo извлекает голосовое из content (schema сверена с td_api.h,
// см. файл задачи). ok=false — content не messageVoiceNote или битый:
// в этом случае используется обычный messageText (плейсхолдер).
// Возвращаемые значения: file.id, voice_note.duration, file.size (ожидаемый
// размер в байтах; 0 — если TDLib его не сообщил — допустимый случай).
// voiceNoteInfo и photoInfo принимают объект messageContent, а не всё
// сообщение: тот же объект лежит в messageReplyToMessage.content_, и разбор
// подписи цитаты обязан идти по тем же правилам (см. parseReplyQuote).
func voiceNoteInfo(content map[string]interface{}) (fileID int32, duration int, size int64, ok bool) {
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

func photoInfo(content map[string]interface{}) (int32, bool) {
	if contentType, _ := content["@type"].(string); contentType != "messagePhoto" {
		return 0, false
	}
	photo, ok := content["photo"].(map[string]interface{})
	if !ok {
		return 0, false
	}
	sizes, ok := photo["sizes"].([]interface{})
	if !ok {
		return 0, false
	}

	var bestID int32
	var bestArea float64
	for _, rawSize := range sizes {
		size, ok := rawSize.(map[string]interface{})
		if !ok {
			continue
		}
		width, widthOK := size["width"].(float64)
		height, heightOK := size["height"].(float64)
		if !widthOK || !heightOK || width <= 0 || height <= 0 {
			continue
		}
		file, ok := size["photo"].(map[string]interface{})
		if !ok {
			continue
		}
		id, ok := file["id"].(float64)
		if !ok || id <= 0 {
			continue
		}
		area := width * height
		if bestID == 0 || area > bestArea {
			bestID = int32(id)
			bestArea = area
		}
	}
	return bestID, bestID > 0
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

// FormatMessageTime — время отправки сообщения в виде часов и минут. Даты здесь
// намеренно нет: в единой ленте (tgclitui) и на стене (tgwall) смешаны сообщения
// разных чатов и разных дней, и колонка дат в каждой карточке только шумела бы.
// Сколько времени прошло, видно и так — по положению сообщения в потоке.
//
// Функция живёт здесь, а не рядом с каждым из двух интерфейсов, потому что это
// чистое форматирование: одинаково посчитанные у tgcli и tgwall заголовки должны
// совпадать до символа, а две копии одного и того же time.Format разъедутся при
// первой же правке. Нулевое или отрицательное время (сообщение без даты —
// битый ответ TDLib) даёт "--:--", а не пустую строку: колонка времени есть
// всегда, и пустая сдвинула бы остальные колонки строки.
func FormatMessageTime(unixTime int64) string {
	if unixTime <= 0 {
		return "--:--"
	}
	return time.Unix(unixTime, 0).Local().Format("15:04")
}

// SendMessage отправляет текстовое сообщение в чат chatID и возвращает его
// как Message (та же схема ответа, что у истории — см. parseMessage).
func SendMessage(ctx context.Context, client TDClientInterface, chatID int64, text string) (Message, error) {
	return sendMessage(ctx, client, chatID, text, 0)
}

func SendMessageReply(ctx context.Context, client TDClientInterface, chatID int64, text string, replyToMessageID int64) (Message, error) {
	return sendMessage(ctx, client, chatID, text, replyToMessageID)
}

func sendMessage(ctx context.Context, client TDClientInterface, chatID int64, text string, replyToMessageID int64) (Message, error) {
	request := map[string]interface{}{
		"@type":   "sendMessage",
		"chat_id": chatID,
		"input_message_content": map[string]interface{}{
			"@type": "inputMessageText",
			"text": map[string]interface{}{
				"@type": "formattedText",
				"text":  text,
			},
		},
	}
	if replyToMessageID != 0 {
		request["reply_to"] = map[string]interface{}{
			"@type":      "inputMessageReplyToMessage",
			"message_id": replyToMessageID,
		}
	}

	resp, err := client.Send(ctx, request)
	if err != nil {
		return Message{}, fmt.Errorf("sendMessage failed: %w", err)
	}
	if resp["@type"] != "message" {
		return Message{}, fmt.Errorf("unexpected response type: %v", resp["@type"])
	}
	return parseMessage(ctx, client, resp), nil
}

// DeleteMessage удаляет сообщение messageID из чата chatID только у текущего
// пользователя (revoke=false) — история собеседника при этом не затрагивается,
// поэтому функция годится для любого сообщения, а не только для собственных.
// Схема сверена с td_api.h собранной версии TDLib: deleteMessages
// chat_id:int53 message_ids:array<int53> revoke:Bool = Ok, то есть ответ — пустой
// объект типа "ok".
func DeleteMessage(ctx context.Context, client TDClientInterface, chatID int64, messageID int64) error {
	request := map[string]interface{}{
		"@type":       "deleteMessages",
		"chat_id":     chatID,
		"message_ids": []int64{messageID},
		"revoke":      false,
	}

	resp, err := client.Send(ctx, request)
	if err != nil {
		return fmt.Errorf("deleteMessages failed: %w", err)
	}
	if resp["@type"] != "ok" {
		return fmt.Errorf("unexpected response type: %v", resp["@type"])
	}
	return nil
}

// ParseDeleteMessagesUpdate разбирает updateDeleteMessages — сообщения исчезли из
// чата.
//
// Это НЕ обратный ход собственному DeleteMessage: апдейт приходит, когда удалили
// из другого клиента или удалил кто-то ещё из участников чата. Своё удаление
// клиент и так применяет сразу по ответу deleteMessages, а вот чужое без
// подписки на этот апдейт оставалось в ленте до перезапуска приложения.
//
// Схема сверена с td_api.h собранной версии TDLib (updateDeleteMessages, ID
// 1669252686): chat_id:int53 message_ids_:array<int53> is_permanent_:Bool
// from_cache_:Bool. is_permanent и from_cache намеренно НЕ разбираются: если
// TDLib прислал апдейт, то с точки зрения клиента этих сообщений в чате больше
// нет независимо от причины — сервер это подтвердил или это пришло из локального
// кэша. Проверять признаки было бы значило оставлять удалённые сообщения на
// экране по признаку, который ничего не меняет для отображения.
//
// Контракт тот же, что у остальных парсеров файла: чужой @type или отсутствие
// chat_id — ok == false, без паники. Отсутствующий или пустой message_ids — не
// ошибка (ok == true, пустой список): удалять всё равно нечего.
func ParseDeleteMessagesUpdate(update map[string]interface{}) (chatID int64, messageIDs []int64, ok bool) {
	if name, _ := update["@type"].(string); name != "updateDeleteMessages" {
		return 0, nil, false
	}
	id, idOK := update["chat_id"].(float64)
	if !idOK {
		return 0, nil, false
	}
	return int64(id), messageIDsOf(update["message_ids"]), true
}

// ParseMessageContentUpdate разбирает updateMessageContent — содержимое
// сообщения изменилось, то есть его отредактировали.
//
// Схема сверена с td_api.h собранной версии TDLib (updateMessageContent, ID
// 506903332): chat_id:int53 message_id:int53 new_content_:messageContent. Именно
// new_content, а не сообщение целиком: у апдейта нет ни отправителя, ни даты, ни
// reply_to, поэтому ни parseMessage, ни ParseNewMessageUpdate его не разбирают.
// Разбор содержимого идёт ТЕМ ЖЕ приёмом, что у цитаты ответа (parseReplyQuote):
// объект messageContent раскрывается через parseContentObject/contentText —
// там лежит ровно такой же объект, просто без обёртки message.
//
// Из updateMessageEdited апдейт этот НЕ дублирует: тот несёт edit_date_ и
// reply_markup_, а нового содержимого в нём нет — правленный текст приходит
// только здесь.
//
// Возвращаются только те поля Message, что выводятся из содержимого (те же, что
// пересчитывает parseMessage, — см. applyContentToMessage). Отправитель, дата,
// ответ и реакции апдейт не меняет: пересчитывать их здесь было бы значило
// стереть их у уже показанной карточки, поэтому наружу они не возвращаются вовсе.
//
// Контракт тот же, что у остальных парсеров файла: чужой @type, отсутствие
// chat_id/message_id или битый new_content — ok == false, без паники. Подставлять
// нули молча нельзя: по chat_id == 0 правка «пройдёт» и не найдёт сообщение.
func ParseMessageContentUpdate(update map[string]interface{}) (chatID, messageID int64, updated Message, ok bool) {
	if name, _ := update["@type"].(string); name != "updateMessageContent" {
		return 0, 0, Message{}, false
	}
	chat, chatOK := update["chat_id"].(float64)
	message, messageOK := update["message_id"].(float64)
	if !chatOK || !messageOK {
		return 0, 0, Message{}, false
	}
	content, contentOK := update["new_content"].(map[string]interface{})
	if !contentOK {
		return 0, 0, Message{}, false
	}
	applyContentToMessage(&updated, parseContentObject(content), contentObjectText(content))
	return int64(chat), int64(message), updated, true
}

// messageIDsOf — список int53 из массива, который TDLib присылает как
// []interface{} с элементами-float64. Не-числовые элементы пропускаются, а не
// роняют разбор: единственное, что важно, — это id удалённых сообщений.
func messageIDsOf(raw interface{}) []int64 {
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	ids := make([]int64, 0, len(list))
	for _, item := range list {
		id, ok := item.(float64)
		if !ok {
			continue
		}
		ids = append(ids, int64(id))
	}
	return ids
}

// messageText извлекает текст сообщения из content.
func messageText(msg map[string]interface{}) string {
	content, ok := msg["content"].(map[string]interface{})
	if !ok {
		return ""
	}
	return contentObjectText(content)
}

// contentObjectText — тот же разбор текста, но от объекта messageContent, а не от
// сообщения целиком: у updateMessageContent новое содержимое лежит под
// new_content, без обёртки message, и parseMessage с его ключом "content" здесь
// бесполезен. Тот же приём уже применён у цитаты ответа (parseReplyQuote), где
// объект messageContent лежит в messageReplyToMessage.content_.
//
// Проверки типа здесь нет намеренно: у не-текстовых типов поля content.text в
// схеме не существует, поэтому функция вернёт пустую строку, а подпись для них
// собирает contentText по разобранному Content. Раньше здесь стояла заглушка
// «[тип сообщения: %s]» на всё, кроме messageText — из-за неё в ленте
// появлялась отладочная строка вместо содержимого.
func contentObjectText(content map[string]interface{}) string {
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
//
// Второе значение — user_id отправителя-пользователя, 0 во всех остальных
// случаях (исходящие, чат/канал, нераспознанный отправитель). Оно нужно не для
// отображения, а чтобы по updateUser найти уже загруженные карточки этого
// человека: имя в карточке зафиксировано строкой в момент разбора, и без id
// обновлять их было бы не по чему.
func resolveSender(ctx context.Context, client TDClientInterface, msg map[string]interface{}) (string, int64) {
	if out, ok := msg["is_outgoing"].(bool); ok && out {
		return "Вы", 0
	}

	sender, ok := msg["sender_id"].(map[string]interface{})
	if !ok {
		return "", 0
	}

	switch sender["@type"] {
	case "messageSenderUser":
		userID, ok := sender["user_id"].(float64)
		if !ok || userID == 0 {
			return "", 0
		}
		resp, err := client.Send(ctx, map[string]interface{}{
			"@type":   "getUser",
			"user_id": int64(userID),
		})
		// user_id известен даже когда getUser упал: имя подставится позже, по
		// updateUser, который придёт уже с настоящим.
		if err == nil {
			if name := userDisplayName(resp); name != "" {
				return name, int64(userID)
			}
		}
		// Тот же «user#<id>», что и у userDisplayName для пользователя без
		// имени: карточка должна оставаться читаемой, когда имени нет.
		return fmt.Sprintf("user#%d", int64(userID)), int64(userID)

	case "messageSenderChat":
		chatID, ok := sender["chat_id"].(float64)
		if !ok || chatID == 0 {
			return "", 0
		}
		resp, err := client.Send(ctx, map[string]interface{}{
			"@type":   "getChat",
			"chat_id": int64(chatID),
		})
		if err != nil {
			return fmt.Sprintf("chat#%d", int64(chatID)), 0
		}
		title, _ := resp["title"].(string)
		if title == "" {
			return fmt.Sprintf("chat#%d", int64(chatID)), 0
		}
		return title, 0
	}

	return "", 0
}

// userDisplayName — отображаемое имя пользователя из объекта user TDLib:
// first_name и last_name через пробел, а если оба пустые — «user#<id>».
//
// Единственное место, где имя собирается из полей user: этим же помощником
// пользуется ParseUserUpdate, иначе переименованный в Telegram человек получил
// бы в ленте имя, отличное от того, что стоит у его же старых карточек. И
// фолбэк «user#<id>» тоже здесь, поэтому вызывающей стороне не обязано быть
// известно про него.
func userDisplayName(user map[string]interface{}) string {
	firstName, _ := user["first_name"].(string)
	lastName, _ := user["last_name"].(string)
	name := firstName
	if lastName != "" {
		if name != "" {
			name += " "
		}
		name += lastName
	}
	if name != "" {
		return name
	}
	id, ok := user["id"].(float64)
	if !ok || id == 0 {
		return ""
	}
	return fmt.Sprintf("user#%d", int64(id))
}

// GetMessagesBefore загружает до limit сообщений старше fromMessageID и возвращает их
// в хронологическом порядке (старые сверху, новые снизу). Само fromMessageID в
// результат не попадает: оно уже есть у ленты, и повторно показать его — значит
// показать дубль.
//
// Возвращённый пустой срез означает, что старше этого сообщения ничего нет: так
// лента запоминает, что чат дочитан, и больше не спрашивает. Отличать «дочитан» от
// «TDLib подкинул меньше, чем просили», нельзя — документация прямо говорит, что
// число возвращённых сообщений TDLib выбирает сам ради скорости, — поэтому
// исчерпанным считается только пустой ответ.
// GetMessage — одно сообщение по паре (chat_id, message_id).
//
// Зачем он нужен для цитат: по официальной документации TDLib у
// messageReplyToMessage поле content_ заполняется только «если сообщение было из
// другого чата или топика» и для ответа в том же чате всегда null, а quote_
// приходит только при явно выбранном фрагменте. То есть у обычного ответа
// сервер не присылает текст оригинала, и официальные клиенты берут его локально:
// из своего кэша сообщений, а при промахе — отдельным запросом. Это тот же
// getMessage, что и в схеме: chat_id_ + message_id_ на входе, message на выходе.
func GetMessage(ctx context.Context, client TDClientInterface, chatID, messageID int64) (Message, error) {
	if client == nil {
		return Message{}, errors.New("TDLib client is nil")
	}
	resp, err := client.Send(ctx, map[string]interface{}{
		"@type":      "getMessage",
		"chat_id":    chatID,
		"message_id": messageID,
	})
	if err != nil {
		return Message{}, fmt.Errorf("getMessage failed: %w", err)
	}
	// Сообщение могло быть удалено: TDLib отвечает объектом error, а не message.
	// Отдельный тип ошибки, чтобы вызывающий отличал «оригинала нет» от «сеть
	// отвалилась» и не спрашивал повторно то, чего уже не существует.
	if resp["@type"] != "message" {
		return Message{}, &RemoteError{
			Type:    responseType(resp),
			Code:    responseCode(resp),
			Message: responseMessage(resp),
		}
	}
	return parseMessage(ctx, client, resp), nil
}

// RemoteError — ошибка, пришедшая от TDLib объектом error. Отдельный тип нужен
// для вызывающих, которые по коду решают, стоит ли повторять запрос: 404 на
// удалённое сообщение повторов не заслуживает, а сетевая сбой — заслуживает.
type RemoteError struct {
	Type    string
	Code    int
	Message string
}

func (e *RemoteError) Error() string {
	if e.Code != 0 {
		return fmt.Sprintf("TDLib error %d (%s): %s", e.Code, e.Type, e.Message)
	}
	return fmt.Sprintf("TDLib error %s: %s", e.Type, e.Message)
}

// IsNotFound — TDLib отвечает 404 на сообщение, которого нет: удалено или
// никогда не было.
func (e *RemoteError) IsNotFound() bool { return e.Code == 404 }

func responseType(resp map[string]interface{}) string {
	name, _ := resp["@type"].(string)
	return name
}

func responseMessage(resp map[string]interface{}) string {
	text, _ := resp["message"].(string)
	return text
}

// responseCode — числовой код ошибки TDLib. Приходит как JSON-число, поэтому
// разбирается через float64, как и остальные поля td_api.
func responseCode(resp map[string]interface{}) int {
	code, _ := resp["code"].(float64)
	return int(code)
}

func GetMessagesBefore(ctx context.Context, client TDClientInterface, chatID, fromMessageID int64, limit int) ([]Message, error) {
	messages, err := fetchChatHistory(ctx, client, chatID, fromMessageID, limit)
	if err != nil {
		return nil, err
	}
	older := make([]Message, 0, len(messages))
	for _, message := range messages {
		if message.ID == fromMessageID {
			continue
		}
		older = append(older, message)
	}
	return older, nil
}
