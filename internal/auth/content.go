package auth

import (
	"fmt"
	"strings"
)

// ContentKind — вид непустого содержимого сообщения. Разобран по реальной схеме
// TDLib (td_api.h собранной версии): раньше всё, что не messageText, превращалось
// в «[тип сообщения: X]», и на экране появлялась отладочная строка вместо
// содержимого.
type ContentKind int

const (
	// ContentText — обычный текст, единственный тип без файла.
	ContentText ContentKind = iota
	ContentPhoto
	ContentSticker
	ContentAnimation
	ContentVideo
	ContentVideoNote
	ContentVoiceNote
	ContentAudio
	ContentDocument
	ContentDice
	ContentLocation
	ContentVenue
	ContentContact
	ContentPoll
	// ContentService — служебное сообщение чата (вход, смена названия, закреп и
	// подобные). Их около восьмидесяти, поэтому показывается нейтральной строкой
	// по общему принципу, а не таблицей на каждый тип.
	ContentService
	// ContentUnsupported — тип, которого нет в разборе. Показывается как
	// «что-то не поддерживается», но никогда как «[тип сообщения: X]».
	ContentUnsupported
)

// Content — содержимое сообщения, которое не текст: файл, который можно
// послушать или показать превью, либо данные, которые нужно выписать словами.
// Поля соответствуют схеме TDLib: имена совпадают с JSON-ключами (см. td_api.h).
type Content struct {
	Kind ContentKind

	// FileID — основной файл (фото, стикер, анимация, видео, голосовое, аудио,
	// документ): его качают, чтобы показать или воспроизвести.
	FileID int32
	// PreviewFileID — файл превью (thumbnail.file.id). У анимированных типов он
	// единственный, в котором картинка вообще пригодна для показа: стикеры и
	// анимации приходят в webp/tgs/webm, которые терминал не рисует, а thumbnail
	// обычно jpeg.
	PreviewFileID int32
	// PreviewFormat — формат превью из thumbnail.format: "jpeg", "png", "webp",
	// "gif" и так далее. Показывать имеет смысл только jpeg и png.
	PreviewFormat string
	// Duration — длительность в секундах (голосовое, аудио, видео).
	Duration int
	// Size — размер файла в байтах: дополнительная проверка локальной копии перед
	// воспроизведением.
	Size int64

	// MimeType, FileName — для документа: по ним понятно, что это и как назвать.
	MimeType string
	FileName string
	// Title, Performer — для аудио: «Исполнитель — Название».
	Title     string
	Performer string
	// Emoji — эмодзи, приходящие отдельной строкой: анимированные эмодзи, стикеры
	// и кубик. Для messageAnimatedEmoji это и есть всё содержимое.
	Emoji string
	// Value — значение кубика (messageDice.value).
	Value int
	// Latitude, Longitude, Address — геолокация и адрес места.
	Latitude  float64
	Longitude float64
	Address   string
	// PhoneNumber, FirstName, LastName — контакт.
	PhoneNumber string
	FirstName   string
	LastName    string
	// PollQuestion, Options — опрос: вопрос и число вариантов.
	PollQuestion string
	Options      int
	// ServiceType — «сырой» тип служебного сообщения без префикса message, для
	// нейтральной подписи.
	ServiceType string
}

// HasPreview — есть ли что показать картинкой.
func (c Content) HasPreview() bool { return c.PreviewFileID > 0 }

// PreviewSupported — формат превью терминал покажет. Всё остальное (webp, gif,
// tgs, webm, mpeg4) не рисуется, и вместо картинки остаётся текст: показать
// пустое место хуже, чем честно сказать, что формат не поддерживается.
func (c Content) PreviewSupported() bool {
	switch strings.ToLower(c.PreviewFormat) {
	case "jpeg", "jpg", "png":
		return true
	default:
		return false
	}
}

// IsAudio — файл, который нужно слушать, а не смотреть.
func (c Content) IsAudio() bool {
	return c.Kind == ContentVoiceNote || c.Kind == ContentAudio
}

// IsVisual — то, для чего есть картинка.
func (c Content) IsVisual() bool {
	switch c.Kind {
	case ContentPhoto, ContentSticker, ContentAnimation, ContentVideo, ContentVideoNote:
		return true
	default:
		return false
	}
}

// serviceTypeName превращает «chatTypeJoinByLink»-подобное имя типа в читаемое
// «присоединился по ссылке»: без префикса message, с пробелами перед заглавными.
func serviceTypeName(contentType string) string {
	name := strings.TrimPrefix(contentType, "message")
	if name == "" {
		return "служебное"
	}
	return name
}

// parseContent разбирает content сообщения TDLib в Content. Один вход на все
// типы: раньше здесь была заглушка, отдающая «[тип сообщения: %s]» для всего, кроме
// messageText, photo и voiceNote.
func parseContent(msg map[string]interface{}) Content {
	content, ok := msg["content"].(map[string]interface{})
	if !ok {
		return Content{Kind: ContentText}
	}
	return parseContentObject(content)
}

// parseContentObject — разбор объекта messageContent. Вынесен отдельно от
// parseContent, потому что тот же самый объект лежит в messageReplyToMessage.content_
// (цитата ответа), и подпись нетекстового оригинала обязана считаться тем же
// разбором, что и основное тело: иначе задача 0105, расширяющая медиа, улучшила бы
// карточки, но оставила бы цитаты позади.
func parseContentObject(content map[string]interface{}) Content {
	contentType, _ := content["@type"].(string)
	switch contentType {
	case "messageText":
		return Content{Kind: ContentText}

	case "messagePhoto":
		parsed := Content{Kind: ContentPhoto}
		parsed.FileID, _ = photoInfo(content)
		parsed.PreviewFileID = parsed.FileID
		// Фото приходит в jpeg/png, поэтому формат превью совпадает с форматом
		// самого файла и рисуется всегда.
		parsed.PreviewFormat = "jpeg"
		return parsed

	case "messageSticker":
		return parseSticker(content)

	case "messageAnimatedEmoji":
		parsed := Content{Kind: ContentSticker, PreviewFormat: "jpeg"}
		parsed.Emoji, _ = content["emoji"].(string)
		// У animatedEmoji в схеме нет ни thumbnail, ни duration — картинка лежит
		// внутри вложенного sticker (см. class animatedEmoji в td_api.h: sticker_,
		// sticker_width_, sticker_height_, fitzpatrick_type_, sound_). Раньше
		// превью читалось из animated_emoji.thumbnail, то есть из поля, которого
		// в протоколе не существует, и картинка молча не появлялась.
		if animated, ok := content["animated_emoji"].(map[string]interface{}); ok {
			if sticker, ok := animated["sticker"].(map[string]interface{}); ok {
				parsed.PreviewFileID = thumbnailFileID(sticker)
				parsed.PreviewFormat = thumbnailFormat(sticker)
			}
		}
		return parsed

	case "messageAnimation":
		parsed := Content{Kind: ContentAnimation}
		if animation, ok := content["animation"].(map[string]interface{}); ok {
			parsed = fillVisual(parsed, animation, "animation")
		}
		return parsed

	case "messageVideo":
		parsed := Content{Kind: ContentVideo}
		if video, ok := content["video"].(map[string]interface{}); ok {
			parsed = fillVisual(parsed, video, "video")
		}
		return parsed

	case "messageVideoNote":
		parsed := Content{Kind: ContentVideoNote}
		if note, ok := content["video_note"].(map[string]interface{}); ok {
			parsed = fillVisual(parsed, note, "video")
		}
		return parsed

	case "messageVoiceNote":
		parsed := Content{Kind: ContentVoiceNote}
		if fileID, duration, size, ok := voiceNoteInfo(content); ok {
			parsed.FileID, parsed.Duration, parsed.Size = fileID, duration, size
		}
		return parsed

	case "messageAudio":
		parsed := Content{Kind: ContentAudio, PreviewFormat: "jpeg"}
		if audio, ok := content["audio"].(map[string]interface{}); ok {
			parsed.FileID = fileID(audio["audio"])
			parsed.Title, _ = audio["title"].(string)
			parsed.Performer, _ = audio["performer"].(string)
			parsed.FileName, _ = audio["file_name"].(string)
			parsed.MimeType, _ = audio["mime_type"].(string)
			parsed.Duration = intField(audio, "duration")
			parsed.Size = sizeOf(audio["audio"])
			// Обложка аудио лежит в album_cover_thumbnail, а не в thumbnail: у
			// class audio в td_api.h полей обложки ровно три, и все три названы
			// album_cover_* (album_cover_minithumbnail, album_cover_thumbnail,
			// external_album_covers). Поля thumbnail у audio нет.
			parsed.PreviewFileID = albumCoverFileID(audio)
			parsed.PreviewFormat = albumCoverFormat(audio)
		}
		return parsed

	case "messageDocument":
		parsed := Content{Kind: ContentDocument}
		if document, ok := content["document"].(map[string]interface{}); ok {
			parsed.FileID = fileID(document["document"])
			parsed.Size = sizeOf(document["document"])
			parsed.FileName, _ = document["file_name"].(string)
			parsed.MimeType, _ = document["mime_type"].(string)
			parsed.PreviewFileID = thumbnailFileID(document)
			parsed.PreviewFormat = thumbnailFormat(document)
		}
		return parsed

	case "messageDice":
		parsed := Content{Kind: ContentDice}
		parsed.Emoji, _ = content["emoji"].(string)
		parsed.Value = intField(content, "value")
		return parsed

	case "messageLocation":
		parsed := Content{Kind: ContentLocation}
		parsed.Latitude = floatField(content, "latitude")
		parsed.Longitude = floatField(content, "longitude")
		return parsed

	case "messageVenue":
		parsed := Content{Kind: ContentVenue}
		parsed.Latitude = floatField(content, "latitude")
		parsed.Longitude = floatField(content, "longitude")
		if venue, ok := content["venue"].(map[string]interface{}); ok {
			parsed.Address, _ = venue["address"].(string)
		}
		return parsed

	case "messageContact":
		parsed := Content{Kind: ContentContact}
		if contact, ok := content["contact"].(map[string]interface{}); ok {
			parsed.PhoneNumber, _ = contact["phone_number"].(string)
			parsed.FirstName, _ = contact["first_name"].(string)
			parsed.LastName, _ = contact["last_name"].(string)
		}
		return parsed

	case "messagePoll":
		parsed := Content{Kind: ContentPoll}
		if poll, ok := content["poll"].(map[string]interface{}); ok {
			parsed.PollQuestion, _ = poll["question"].(string)
			if options, ok := poll["options"].([]interface{}); ok {
				parsed.Options = len(options)
			}
		}
		return parsed
	}

	if contentType == "" {
		return Content{Kind: ContentText}
	}
	if isServiceContentType(contentType) {
		return Content{Kind: ContentService, ServiceType: serviceTypeName(contentType)}
	}
	return Content{Kind: ContentUnsupported, ServiceType: contentType}
}

// parseSticker — стикер: эмодзи для подписи и превью для показа. Сам файл стикера
// приходит в webp/tgs/webm и терминалом не рисуется, поэтому для картинки
// берётся thumbnail.
func parseSticker(content map[string]interface{}) Content {
	parsed := Content{Kind: ContentSticker}
	sticker, ok := content["sticker"].(map[string]interface{})
	if !ok {
		return parsed
	}
	parsed.FileID = fileID(sticker["sticker"])
	parsed.Emoji, _ = sticker["emoji"].(string)
	parsed.PreviewFileID = thumbnailFileID(sticker)
	parsed.PreviewFormat = thumbnailFormat(sticker)
	parsed.Size = sizeOf(sticker["sticker"])
	return parsed
}

// fillVisual — общие поля для видео, анимации и видеозаметки: файл, превью,
// длительность, размеры.
func fillVisual(parsed Content, holder map[string]interface{}, fileKey string) Content {
	parsed.FileID = fileID(holder[fileKey])
	parsed.FileName, _ = holder["file_name"].(string)
	parsed.MimeType, _ = holder["mime_type"].(string)
	parsed.Duration = intField(holder, "duration")
	parsed.Size = sizeOf(holder[fileKey])
	parsed.PreviewFileID = thumbnailFileID(holder)
	parsed.PreviewFormat = thumbnailFormat(holder)
	return parsed
}

// albumCoverFileID — file.id из album_cover_thumbnail.file у объекта audio.
// Отдельная функция, а не параметр у thumbnailFileID: имя поля у обложки аудио
// другое, и подставлять его строкой значило бы снова спрятать опечатку в вызов.
func albumCoverFileID(audio map[string]interface{}) int32 {
	cover, ok := audio["album_cover_thumbnail"].(map[string]interface{})
	if !ok {
		return 0
	}
	return fileID(cover["file"])
}

// albumCoverFormat — album_cover_thumbnail.format["@type"] без префикса
// thumbnailFormat, тем же приведением к нижнему регистру, что thumbnailFormat.
func albumCoverFormat(audio map[string]interface{}) string {
	cover, ok := audio["album_cover_thumbnail"].(map[string]interface{})
	if !ok {
		return ""
	}
	format, ok := cover["format"].(map[string]interface{})
	if !ok {
		return ""
	}
	name, _ := format["@type"].(string)
	return strings.ToLower(strings.TrimPrefix(name, "thumbnailFormat"))
}

// thumbnailFileID — file.id из thumbnail.file (в схеме thumbnail.file — это
// fileRemote с единственным полем id).
func thumbnailFileID(holder map[string]interface{}) int32 {
	thumbnail, ok := holder["thumbnail"].(map[string]interface{})
	if !ok {
		return 0
	}
	return fileID(thumbnail["file"])
}

// thumbnailFormat — thumbnail.format["@type"] без префикса thumbnailFormat.
func thumbnailFormat(holder map[string]interface{}) string {
	thumbnail, ok := holder["thumbnail"].(map[string]interface{})
	if !ok {
		return ""
	}
	format, ok := thumbnail["format"].(map[string]interface{})
	if !ok {
		return ""
	}
	name, _ := format["@type"].(string)
	// Имя в схеме с заглавной (thumbnailFormatJpeg), а сравнение форматов идёт
	// в нижнем регистре: приводим сразу, чтобы значение в модели было единым.
	return strings.ToLower(strings.TrimPrefix(name, "thumbnailFormat"))
}

// fileID — id из объекта file.
func fileID(raw interface{}) int32 {
	file, ok := raw.(map[string]interface{})
	if !ok {
		return 0
	}
	id, _ := file["id"].(float64)
	return int32(id)
}

// sizeOf — размер файла в байтах.
func sizeOf(raw interface{}) int64 {
	file, ok := raw.(map[string]interface{})
	if !ok {
		return 0
	}
	size, _ := file["size"].(float64)
	return int64(size)
}

func intField(holder map[string]interface{}, key string) int {
	value, _ := holder[key].(float64)
	return int(value)
}

func floatField(holder map[string]interface{}, key string) float64 {
	value, _ := holder[key].(float64)
	return value
}

// serviceContentTypes — служебные типы сообщений. В td_api.h их около
// восьмидесяти, и все они описывают изменение состояния чата, а не содержимое:
// сообщение в ленте не должно занимать карточку как обычное. Список выдержан
// здесь, а не размазан по switch, чтобы пополнение новых типов TDLib было
// одним изменением (тест сверяет его с заголовком td_api.h).
var serviceContentTypes = map[string]bool{
	"messageBasicGroupChatCreate": true, "messageSupergroupChatCreate": true,
	"messageChatChangeTitle": true, "messageChatChangePhoto": true,
	"messageChatDeletePhoto": true, "messageChatOwnerLeft": true,
	"messageChatOwnerChanged": true, "messageChatHasProtectedContentToggled": true,
	"messageChatHasProtectedContentDisableRequested": true, "messageChatAddMembers": true,
	"messageChatJoinByLink": true, "messageChatJoinByRequest": true,
	"messageChatJoinFromCommunity": true, "messageChatDeleteMember": true,
	"messageChatAddedToCommunity": true, "messageChatRemovedFromCommunity": true,
	"messageChatUpgradeTo": true, "messageChatUpgradeFrom": true,
	"messagePinMessage": true, "messageScreenshotTaken": true,
	"messageChatSetBackground": true, "messageChatSetTheme": true,
	"messageChatSetMessageAutoDeleteTime": true, "messageChatBoost": true,
	"messageForumTopicCreated": true, "messageForumTopicEdited": true,
	"messageForumTopicIsClosedToggled": true, "messageForumTopicIsHiddenToggled": true,
	"messageSuggestProfilePhoto": true, "messageSuggestBirthdate": true,
	"messageCustomServiceAction": true, "messageGameScore": true,
	"messageManagedBotCreated": true, "messagePaymentSuccessful": true,
	"messagePaymentSuccessfulBot": true, "messagePaymentRefunded": true,
	"messageGiftedPremium": true, "messagePremiumGiftCode": true,
	"messageGiveawayCreated": true, "messageGiveaway": true,
	"messageGiveawayCompleted": true, "messageGiveawayWinners": true,
	"messageGiftedStars": true, "messageGiftedGrams": true,
	"messageGiveawayPrizeStars": true, "messageGift": true,
	"messageUpgradedGift": true, "messageRefundedUpgradedGift": true,
	"messageUpgradedGiftPurchaseOffer": true, "messageUpgradedGiftPurchaseOfferRejected": true,
	"messagePaidMessagesRefunded": true, "messagePaidMessagePriceChanged": true,
	"messageDirectMessagePriceChanged": true, "messageChecklistTasksDone": true,
	"messageChecklistTasksAdded": true, "messageSuggestedPostApprovalFailed": true,
	"messageSuggestedPostApproved": true, "messageSuggestedPostDeclined": true,
	"messageSuggestedPostPaid": true, "messageSuggestedPostRefunded": true,
	"messageContactRegistered": true, "messageUsersShared": true,
	"messageChatShared": true, "messageBotWriteAccessAllowed": true,
	"messageWebAppDataSent": true, "messageWebAppDataReceived": true,
	"messagePassportDataSent": true, "messagePassportDataReceived": true,
	"messageProximityAlertTriggered": true,
}

func isServiceContentType(contentType string) bool {
	return serviceContentTypes[contentType]
}

// contentText — что показать в карточке вместо отладочной строки. Возвращает
// текст и, если он непустой, заменяет им Text сообщения.
func contentText(c Content) string {
	switch c.Kind {
	case ContentText:
		return ""

	case ContentPhoto:
		return "[фото]"

	case ContentSticker:
		if c.Emoji != "" {
			return "[стикер " + c.Emoji + "]"
		}
		return "[стикер]"

	case ContentAnimation:
		return describeFile("анимация", c)

	case ContentVideo:
		return describeFile("видео", c)

	case ContentVideoNote:
		return describeFile("видеозаметка", c)

	case ContentVoiceNote:
		return "▶ голосовое [" + formatVoiceDuration(c.Duration) + "]"

	case ContentAudio:
		return describeAudio(c)

	case ContentDocument:
		name := c.FileName
		if name == "" {
			return "[документ]"
		}
		return "📄 " + name

	case ContentDice:
		emoji := c.Emoji
		if emoji == "" {
			emoji = "🎲"
		}
		return fmt.Sprintf("%s %d", emoji, c.Value)

	case ContentLocation:
		return fmt.Sprintf("📍 %.5f, %.5f", c.Latitude, c.Longitude)

	case ContentVenue:
		if c.Address != "" {
			return "📍 " + c.Address
		}
		return fmt.Sprintf("📍 %.5f, %.5f", c.Latitude, c.Longitude)

	case ContentContact:
		name := strings.TrimSpace(c.FirstName + " " + c.LastName)
		if name == "" {
			name = "контакт"
		}
		if c.PhoneNumber != "" {
			return "👤 " + name + " · " + c.PhoneNumber
		}
		return "👤 " + name

	case ContentPoll:
		if c.PollQuestion != "" {
			return fmt.Sprintf("📊 %s · %d вариантов", c.PollQuestion, c.Options)
		}
		return fmt.Sprintf("📊 опрос · %d вариантов", c.Options)

	case ContentService:
		return "[служебное: " + c.ServiceType + "]"

	default:
		// Неизвестный тип: честно говорим, что не поддерживается, но НЕ показываем
		// отладочный «[тип сообщения: X]» — он и был исходной жалобой.
		return "[что-то не поддерживается]"
	}
}

// describeFile — подпись файла с длительностью и именем, без обрезки: обрезкой
// занимается отрисовка карточки.
func describeFile(kind string, c Content) string {
	label := "[" + kind
	if c.Duration > 0 {
		label += " " + formatVoiceDuration(c.Duration)
	}
	if c.FileName != "" {
		label += " · " + c.FileName
	}
	return label + "]"
}

// describeAudio — «Исполнитель — Название [3:21]», как в плеере: без исполнителя
// начинаем с названия, без названия — просто «аудио».
func describeAudio(c Content) string {
	parts := make([]string, 0, 3)
	if c.Performer != "" {
		parts = append(parts, c.Performer+" —")
	}
	if c.Title != "" {
		parts = append(parts, c.Title)
	}
	if len(parts) == 0 {
		parts = append(parts, "аудио")
	}
	parts = append(parts, "["+formatVoiceDuration(c.Duration)+"]")
	return "♪ " + strings.Join(parts, " ")
}
