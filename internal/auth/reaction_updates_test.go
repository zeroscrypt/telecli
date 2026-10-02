package auth

import "testing"

// TestParseMessageInteractionInfoUpdateReadsCounters — апдейт разбирается по схеме
// updateMessageInteractionInfo: chat_id, message_id, interaction_info, внутри
// которого reactions — это messageReactions{reactions:[messageReaction]}, а у
// каждого messageReaction есть type/total_count/is_chosen.
//
// Сверено с td_api.tl: updateMessageInteractionInfo chat_id:int53 message_id:int53
// interaction_info:messageInteractionInfo, где
// messageInteractionInfo reactions:messageReactions, а
// messageReactions reactions:vector<messageReaction> are_tags:Bool ...,
// и messageReaction type:ReactionType total_count:int32 is_chosen:Bool ...
func TestParseMessageInteractionInfoUpdateReadsCounters(t *testing.T) {
	update := map[string]interface{}{
		"@type":      "updateMessageInteractionInfo",
		"chat_id":    float64(7),
		"message_id": float64(11),
		"interaction_info": map[string]interface{}{
			"view_count":    float64(3),
			"forward_count": float64(1),
			"reactions": map[string]interface{}{
				"reactions": []interface{}{
					map[string]interface{}{
						"type":        map[string]interface{}{"@type": "reactionTypeEmoji", "emoji": "🔥"},
						"total_count": float64(5),
						"is_chosen":   true,
					},
					map[string]interface{}{
						"type":        map[string]interface{}{"@type": "reactionTypeEmoji", "emoji": "❤"},
						"total_count": float64(1),
						"is_chosen":   false,
					},
				},
				"are_tags": false,
			},
		},
	}

	chatID, messageID, reactions, ok := ParseMessageInteractionInfoUpdate(update)
	if !ok {
		t.Fatal("разбор апдейта вернул ok = false на заведомо верном апдейте")
	}
	if chatID != 7 || messageID != 11 {
		t.Fatalf("chat/message = %d/%d, want 7/11", chatID, messageID)
	}
	if len(reactions) != 2 {
		t.Fatalf("реакций %d, want 2", len(reactions))
	}
	if reactions[0] != (MessageReaction{Emoji: "🔥", TotalCount: 5, IsChosen: true}) {
		t.Errorf("первая реакция = %+v", reactions[0])
	}
	if reactions[1] != (MessageReaction{Emoji: "❤", TotalCount: 1}) {
		t.Errorf("вторая реакция = %+v", reactions[1])
	}
}

// TestParseMessageInteractionInfoUpdateSkipsNonEmoji — кастомные и платные
// реакции пропускаются наравне с разбором счётчиков у сообщений: рисовать их в
// терминале нечем, а подставлять id значило бы показать человеку выдуманное
// содержимое.
func TestParseMessageInteractionInfoUpdateSkipsNonEmoji(t *testing.T) {
	update := map[string]interface{}{
		"@type":      "updateMessageInteractionInfo",
		"chat_id":    float64(1),
		"message_id": float64(2),
		"interaction_info": map[string]interface{}{
			"reactions": map[string]interface{}{
				"reactions": []interface{}{
					map[string]interface{}{
						"type":        map[string]interface{}{"@type": "reactionTypeCustomEmoji", "custom_emoji_id": "999"},
						"total_count": float64(2),
						"is_chosen":   true,
					},
					map[string]interface{}{
						"type":        map[string]interface{}{"@type": "reactionTypeEmoji", "emoji": "👍"},
						"total_count": float64(4),
						"is_chosen":   false,
					},
				},
			},
		},
	}
	_, _, reactions, ok := ParseMessageInteractionInfoUpdate(update)
	if !ok {
		t.Fatal("разбор апдейта вернул ok = false")
	}
	if len(reactions) != 1 || reactions[0].Emoji != "👍" {
		t.Fatalf("реакции = %+v, want только 👍 — кастомный эмодзи рисовать нечем", reactions)
	}
}

// TestParseMessageInteractionInfoUpdateHandlesEmptyAndBroken — interaction_info по
// схеме nullable, то есть отсутствие — обычное дело: это снятие реакций, а не
// ошибка. Плюс контракт тот же, что у остальных парсеров: на чужом @type или битом
// объекте ok == false, без паники.
func TestParseMessageInteractionInfoUpdateHandlesEmptyAndBroken(t *testing.T) {
	for name, update := range map[string]map[string]interface{}{
		"нет interaction_info": {
			"@type": "updateMessageInteractionInfo", "chat_id": float64(1), "message_id": float64(2),
		},
		"interaction_info = nil": {
			"@type": "updateMessageInteractionInfo", "chat_id": float64(1), "message_id": float64(2),
			"interaction_info": nil,
		},
		"пустой список реакций": {
			"@type": "updateMessageInteractionInfo", "chat_id": float64(1), "message_id": float64(2),
			"interaction_info": map[string]interface{}{
				"reactions": map[string]interface{}{"reactions": []interface{}{}},
			},
		},
		"реакции битого типа": {
			"@type": "updateMessageInteractionInfo", "chat_id": float64(1), "message_id": float64(2),
			"interaction_info": map[string]interface{}{
				"reactions": map[string]interface{}{"reactions": "не список"},
			},
		},
	} {
		chatID, messageID, reactions, ok := ParseMessageInteractionInfoUpdate(update)
		if !ok {
			t.Errorf("%s: ok = false, want true — отсутствие реакций это не ошибка", name)
		}
		if chatID != 1 || messageID != 2 {
			t.Errorf("%s: chat/message = %d/%d, want 1/2", name, chatID, messageID)
		}
		if len(reactions) != 0 {
			t.Errorf("%s: реакций %d, want 0", name, len(reactions))
		}
	}

	for name, update := range map[string]map[string]interface{}{
		"чужой @type":   {"@type": "updateMessageReaction", "chat_id": float64(1), "message_id": float64(2)},
		"нет chat_id":   {"@type": "updateMessageInteractionInfo", "message_id": float64(2)},
		"нет message":   {"@type": "updateMessageInteractionInfo", "chat_id": float64(1)},
		"chat не число": {"@type": "updateMessageInteractionInfo", "chat_id": "один", "message_id": float64(2)},
	} {
		if _, _, _, ok := ParseMessageInteractionInfoUpdate(update); ok {
			t.Errorf("%s: ok = true, want false", name)
		}
	}
}
