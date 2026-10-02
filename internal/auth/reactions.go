package auth

import (
	"context"
	"fmt"
)

// MessageReaction — одна реакция под сообщением: какой эмодзи, сколько раз
// поставлен и поставил ли его текущий пользователь.
//
// Сверено с td_api.tl (tdlib/td@master): счётчики лежат не на верхнем уровне
// message, а вложенно — message.interaction_info.reactions.reactions[], и каждый
// элемент это messageReaction{type:ReactionType total_count:int32 is_chosen:Bool
// used_sender_id:MessageSender recent_sender_ids:vector<MessageSender>}.
//
// Тип реакции — ReactionType, у которого есть три варианта, и только один из них
// пригоден для показа в терминале:
//
//	reactionTypeEmoji      {emoji: string}        — обычный эмодзи, единственный показываемый
//	reactionTypeCustomEmoji{custom_emoji_id}      — кастомный эмодзи, рисовать нечем
//	reactionTypePaid                               — платная реакция, платить не будем
//
// Два последних намеренно НЕ попадают в этот срез (осознанное упрощение, см.
// задачу 0127): у них нет текста эмодзи, а подставлять вместо него id или
// заглушку значило бы показывать человеку выдуманное содержимое. При этом они не
// ломают разбор — просто не добавляются.
type MessageReaction struct {
	Emoji      string // из reactionTypeEmoji.emoji
	TotalCount int32  // messageReaction.total_count
	IsChosen   bool   // messageReaction.is_chosen — поставил ли реакцию текущий пользователь
}

// parseReactions — разбирает message.interaction_info.reactions.reactions[].
// Отсутствие или битое значение даёт nil, а не панику: у большинства сообщений
// реакций нет вовсе, и это самый частый случай, а не ошибка.
func parseReactions(msgMap map[string]interface{}) []MessageReaction {
	interaction, ok := msgMap["interaction_info"].(map[string]interface{})
	if !ok {
		return nil
	}
	reactions, ok := interaction["reactions"].(map[string]interface{})
	if !ok {
		return nil
	}
	list, ok := reactions["reactions"].([]interface{})
	if !ok || len(list) == 0 {
		return nil
	}
	parsed := make([]MessageReaction, 0, len(list))
	for _, raw := range list {
		item, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		emoji := reactionEmojiOf(item["type"])
		if emoji == "" {
			continue
		}
		totalCount, _ := item["total_count"].(float64)
		chosen, _ := item["is_chosen"].(bool)
		parsed = append(parsed, MessageReaction{
			Emoji:      emoji,
			TotalCount: int32(totalCount),
			IsChosen:   chosen,
		})
	}
	if len(parsed) == 0 {
		return nil
	}
	return parsed
}

// reactionEmojiOf — эмодзи типа реакции, либо "" для не-эмодзи вариантов.
func reactionEmojiOf(reactionType interface{}) string {
	typeRaw, ok := reactionType.(map[string]interface{})
	if !ok {
		return ""
	}
	if name, _ := typeRaw["@type"].(string); name != "reactionTypeEmoji" {
		return ""
	}
	emoji, _ := typeRaw["emoji"].(string)
	return emoji
}

// addMessageReactionRequest — тело запроса addMessageReaction. Вынесено отдельно,
// чтобы тест проверял ровно тот JSON, который уходит в TDLib.
func addMessageReactionRequest(chatID, messageID int64, emoji string) map[string]interface{} {
	return map[string]interface{}{
		"@type":                   "addMessageReaction",
		"chat_id":                 chatID,
		"message_id":              messageID,
		"reaction_type":           reactionTypeEmojiJSON(emoji),
		"is_big":                  false,
		"update_recent_reactions": true,
	}
}

// removeMessageReactionRequest — тело запроса removeMessageReaction. Отличается от
// добавления отсутствием is_big и update_recent_reactions: по схеме у remove есть
// только chat_id, message_id и reaction_type.
func removeMessageReactionRequest(chatID, messageID int64, emoji string) map[string]interface{} {
	return map[string]interface{}{
		"@type":         "removeMessageReaction",
		"chat_id":       chatID,
		"message_id":    messageID,
		"reaction_type": reactionTypeEmojiJSON(emoji),
	}
}

// reactionTypeEmojiJSON — обёртка типа реакции. В TDLib это не просто строка,
// а объект reactionTypeEmoji, поэтому экранировать надо его, а не само поле
// reaction_type.
func reactionTypeEmojiJSON(emoji string) map[string]interface{} {
	return map[string]interface{}{"@type": "reactionTypeEmoji", "emoji": emoji}
}

// AddMessageReaction ставит реакцию на сообщение.
//
// Схема (td_api.tl, addMessageReaction): chat_id, message_id, reaction_type,
// is_big, update_recent_reactions. is_big — «с большой анимацией», терминалу она
// недоступна, поэтому всегда false. update_recent_reactions — true, чтобы
// эмодзи попал в recent_reactions и дальше всплывал в выдаче
// getMessageAvailableReactions.
func AddMessageReaction(ctx context.Context, client TDClientInterface, chatID int64, messageID int64, emoji string) error {
	return expectOK(client, ctx, addMessageReactionRequest(chatID, messageID, emoji),
		"addMessageReaction", fmt.Sprintf("chat %d, message %d, %q", chatID, messageID, emoji))
}

// RemoveMessageReaction снимает реакцию с сообщения. По схеме снять можно любую
// выбранную реакцию («A chosen reaction can always be removed»).
func RemoveMessageReaction(ctx context.Context, client TDClientInterface, chatID int64, messageID int64, emoji string) error {
	return expectOK(client, ctx, removeMessageReactionRequest(chatID, messageID, emoji),
		"removeMessageReaction", fmt.Sprintf("chat %d, message %d, %q", chatID, messageID, emoji))
}

// expectOK — общая часть трёх методов реакций: отправляет запрос и требует ответа
// ровно "ok". Вынесено, чтобы не повторять одну и ту же проверку в каждом методе.
func expectOK(client TDClientInterface, ctx context.Context, request map[string]interface{}, method, about string) error {
	resp, err := client.Send(ctx, request)
	if err != nil {
		return fmt.Errorf("%s failed (%s): %w", method, about, err)
	}
	if resp["@type"] != "ok" {
		return fmt.Errorf("%s failed (%s): unexpected response type: %v", method, about, resp["@type"])
	}
	return nil
}

// AvailableReactions — то, что TDLib разрешает поставить на сообщение, плюс
// причина, почему поставить нельзя (если нельзя).
//
// Сверено с td_api.tl (getMessageAvailableReactions и availableReactions): список
// приходит тремя частями — top_reactions, recent_reactions, popular_reactions.
// Берём ТОЛЬКО top_reactions (решение человека, задача 0127): это те, что
// Telegram показывает первыми. Остальные два списка не разбираются вовсе —
// «на всякий случай» здесь означало бы лишнее состояние без потребителя.
type AvailableReactions struct {
	Emojis []string
	// UnavailableReason — непусто, если текущий пользователь не может ставить
	// реакции на это сообщение, хотя другие могут. Значения приходят как
	// reactionUnavailabilityReasonRestricted / …Guest / …AnonymousAdministrator.
	UnavailableReason string
}

// GetMessageAvailableReactions запрашивает список реакций для сообщения.
//
// rowSize по схеме обязан быть в диапазоне 5-25; нарушение диапазона TDLib
// считает ошибкой, поэтому значение зажимается here же, а не перекладывается на
// вызывающую сторону. Ноль не значит «без ограничений» — это тоже вне диапазона.
func GetMessageAvailableReactions(ctx context.Context, client TDClientInterface, chatID int64, messageID int64, rowSize int) (AvailableReactions, error) {
	if rowSize < 5 {
		rowSize = 5
	}
	if rowSize > 25 {
		rowSize = 25
	}
	resp, err := client.Send(ctx, map[string]interface{}{
		"@type":      "getMessageAvailableReactions",
		"chat_id":    chatID,
		"message_id": messageID,
		"row_size":   rowSize,
	})
	if err != nil {
		return AvailableReactions{}, fmt.Errorf("getMessageAvailableReactions failed: %w", err)
	}
	if name, _ := resp["@type"].(string); name != "availableReactions" {
		return AvailableReactions{}, fmt.Errorf("getMessageAvailableReactions: unexpected response type: %v", resp["@type"])
	}
	return parseAvailableReactions(resp), nil
}

// parseAvailableReactions разбирает ответ getMessageAvailableReactions.
func parseAvailableReactions(resp map[string]interface{}) AvailableReactions {
	result := AvailableReactions{
		Emojis:            parseAvailableReactionEmojis(resp["top_reactions"]),
		UnavailableReason: unavailabilityReason(resp["unavailability_reason"]),
	}
	return result
}

// parseAvailableReactionEmojis — список эмодзи из одного из массивов ответа.
// Не-эмодзи варианты (кастомные и платные) пропускаются наравне с разбором
// счётчиков: рисовать их в терминале нечем.
func parseAvailableReactionEmojis(raw interface{}) []string {
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	emojis := make([]string, 0, len(list))
	for _, item := range list {
		entry, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		emoji := reactionEmojiOf(entry["type"])
		if emoji == "" {
			continue
		}
		emojis = append(emojis, emoji)
	}
	if len(emojis) == 0 {
		return nil
	}
	return emojis
}

// unavailabilityReason — причина, по которой реакцию ставить нельзя, либо "".
// В схеме поле nullable, поэтому отсутствие — обычное дело, а не ошибка.
func unavailabilityReason(raw interface{}) string {
	reason, ok := raw.(map[string]interface{})
	if !ok {
		return ""
	}
	name, _ := reason["@type"].(string)
	return name
}

// ParseMessageInteractionInfoUpdate разбирает updateMessageInteractionInfo —
// изменились счётчики реакций под сообщением.
//
// Сверено с td_api.tl: updateMessageInteractionInfo chat_id:int53 message_id:int53
// interaction_info:messageInteractionInfo, где messageInteractionInfo reactions —
// это messageReactions{reactions:vector<messageReaction> are_tags:bool ...}.
// То есть по форме это ровно тот же срез, что parseReactions читает из
// message.interaction_info.reactions, и разбор не дублируется: апдейт просто
// переиспользует его.
//
// Зачем он нужен именно здесь. Схема getMessageAvailableReactions прямо обещает,
// что список доступных реакций меняется после updateMessageInteractionInfo:
//
//	The list can change after updateActiveEmojiReactions,
//	updateChatAvailableReactions for the chat, or updateMessageInteractionInfo
//	for the message
//
// Без подписки на этот апдейт список в пикере устаревал: он запрашивался один раз
// по нажатию → и больше не сверялся. Человек ставил реакцию, update_recent_reactions
// передвигал эмодзи в top_reactions, и следующий запрос возвращал уже другой набор —
// окно то показывалось, то нет, и человек описывал это как случайность.
//
// ВАЖНО: updateMessageReaction и updateMessageReactions для этого НЕ годятся — в
// схеме они помечены «for bots only», и учётной записи обычного пользователя
// TDLib их не присылает. Рабочий для живого пользователя сигнал именно этот.
//
// interaction_info по схеме nullable: отсутствие читается как «реакций нет».
func ParseMessageInteractionInfoUpdate(update map[string]interface{}) (chatID int64, messageID int64, reactions []MessageReaction, ok bool) {
	if name, _ := update["@type"].(string); name != "updateMessageInteractionInfo" {
		return 0, 0, nil, false
	}
	chat, chatOK := update["chat_id"].(float64)
	message, messageOK := update["message_id"].(float64)
	if !chatOK || !messageOK {
		return 0, 0, nil, false
	}
	interaction, _ := update["interaction_info"].(map[string]interface{})
	return int64(chat), int64(message), parseReactions(map[string]interface{}{
		"interaction_info": interaction,
	}), true
}

// ParseMessageUnreadReactionsUpdate разбирает updateMessageUnreadReactions — свои
// непрочитанные реакции на сообщении.
//
// Почему именно этот апдейт, а не updateMessageReaction(s): те в схеме помечены
// «for bots only», то есть учётной записи обычного пользователя TDLib их не
// присылает, и подписка на них была бы кодом, который никогда не сработает.
// Этот — единственный пользовательский сигнал по реакциям.
//
// Сверено с td_api.tl: updateMessageUnreadReactions chat_id message_id
// unread_reactions:vector<unreadReaction> unread_reaction_count:int32.
func ParseMessageUnreadReactionsUpdate(update map[string]interface{}) (chatID int64, messageID int64, unreadReactionCount int32, ok bool) {
	if name, _ := update["@type"].(string); name != "updateMessageUnreadReactions" {
		return 0, 0, 0, false
	}
	chat, chatOk := update["chat_id"].(float64)
	message, messageOk := update["message_id"].(float64)
	if !chatOk || !messageOk {
		return 0, 0, 0, false
	}
	count, _ := update["unread_reaction_count"].(float64)
	return int64(chat), int64(message), int32(count), true
}
