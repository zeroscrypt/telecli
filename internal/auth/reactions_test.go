package auth

import (
	"context"
	"testing"
)

// messageWithReactions — кусок ответа TDLib, где счётчики реакций лежат по
// реальному пути из td_api.tl: message → interaction_info → reactions →
// reactions[]. Верхнего уровня message.reactions в схеме НЕТ, и если разбор
// читает не оттуда, тест обязан это заметить.
func messageWithReactions() map[string]interface{} {
	return map[string]interface{}{
		"@type":   "message",
		"id":      float64(10),
		"chat_id": float64(5),
		"interaction_info": map[string]interface{}{
			"@type":      "messageInteractionInfo",
			"view_count": float64(3),
			"reactions": map[string]interface{}{
				"@type": "messageReactions",
				"reactions": []interface{}{
					map[string]interface{}{
						"@type":       "messageReaction",
						"type":        map[string]interface{}{"@type": "reactionTypeEmoji", "emoji": "❤"},
						"total_count": float64(7),
						"is_chosen":   true,
					},
					map[string]interface{}{
						"@type":       "messageReaction",
						"type":        map[string]interface{}{"@type": "reactionTypeEmoji", "emoji": "👍"},
						"total_count": float64(2),
						"is_chosen":   false,
					},
				},
			},
		},
	}
}

// TestParseReactionsReadsNestedInteractionInfo — счётчики читаются именно из
// interaction_info.reactions, и разбираются эмодзи, число и «своя ли реакция».
func TestParseReactionsReadsNestedInteractionInfo(t *testing.T) {
	got := parseReactions(messageWithReactions())

	if len(got) != 2 {
		t.Fatalf("разобрано %d реакций, want 2: %+v", len(got), got)
	}
	if got[0].Emoji != "❤" || got[0].TotalCount != 7 || !got[0].IsChosen {
		t.Errorf("первая реакция = %+v, want ❤/7/своя", got[0])
	}
	if got[1].Emoji != "👍" || got[1].TotalCount != 2 || got[1].IsChosen {
		t.Errorf("вторая реакция = %+v, want 👍/2/чужая", got[1])
	}
}

// TestParseReactionsTolerantToMissingAndBrokenShapes — у большинства сообщений
// реакций нет вовсе, это норма и самый частый случай. Разбор обязан молча давать
// nil, а не паниковать, и то же самое для битых значений на каждом уровне.
func TestParseReactionsTolerantToMissingAndBrokenShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]interface{}
	}{
		{"нет interaction_info", map[string]interface{}{"@type": "message", "id": float64(1)}},
		{"interaction_info не объект", map[string]interface{}{"interaction_info": "мусор"}},
		{"нет reactions", map[string]interface{}{"interaction_info": map[string]interface{}{"@type": "messageInteractionInfo"}}},
		{"reactions не объект", map[string]interface{}{"interaction_info": map[string]interface{}{"reactions": float64(1)}}},
		{"пустой список", map[string]interface{}{"interaction_info": map[string]interface{}{
			"reactions": map[string]interface{}{"reactions": []interface{}{}}}}},
		{"элемент списка не объект", map[string]interface{}{"interaction_info": map[string]interface{}{
			"reactions": map[string]interface{}{"reactions": []interface{}{"строка"}}}}},
		{"нет type", map[string]interface{}{"interaction_info": map[string]interface{}{
			"reactions": map[string]interface{}{"reactions": []interface{}{
				map[string]interface{}{"total_count": float64(1)}}}}}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := parseReactions(testCase.raw); got != nil {
				t.Fatalf("разбор дал %+v, want nil", got)
			}
		})
	}
}

// TestParseReactionsSkipsNonEmojiTypes — кастомные и платные реакции в схеме
// есть (reactionTypeCustomEmoji, reactionTypePaid), но рисовать их в терминале
// нечем. Они не должны попасть в срез и НЕ должны ломать разбор остальных: список,
// где первая реакция кастомная, обязан дать вторую — обычную.
func TestParseReactionsSkipsNonEmojiTypes(t *testing.T) {
	raw := map[string]interface{}{
		"interaction_info": map[string]interface{}{
			"reactions": map[string]interface{}{
				"@type": "messageReactions",
				"reactions": []interface{}{
					// Кастомный эмодзи СОЗНАТЕЛЬНО несёт поле emoji. В настоящей
					// схеме у reactionTypeCustomEmoji его нет, и без этой приписки
					// проверка «оставить только reactionTypeEmoji» проходила бы
					// вхолостую: элемент отсекался бы из-за отсутствия поля, а не
					// из-за типа, и снятая проверка типа тесту была бы не помеха.
					map[string]interface{}{
						"@type": "messageReaction",
						"type": map[string]interface{}{
							"@type":           "reactionTypeCustomEmoji",
							"custom_emoji_id": float64(999),
							"emoji":           "🙂",
						},
					},
					map[string]interface{}{
						"@type": "messageReaction",
						"type":  map[string]interface{}{"@type": "reactionTypePaid", "emoji": "💎"},
					},
					map[string]interface{}{
						"@type":       "messageReaction",
						"type":        map[string]interface{}{"@type": "reactionTypeEmoji", "emoji": "🔥"},
						"total_count": float64(4),
					},
				},
			},
		},
	}

	got := parseReactions(raw)

	if len(got) != 1 {
		t.Fatalf("разобрано %d реакций, want 1 (только эмодзи): %+v", len(got), got)
	}
	if got[0].Emoji != "🔥" || got[0].TotalCount != 4 {
		t.Errorf("реакция = %+v, want 🔥/4", got[0])
	}
}

// TestParseMessageCarriesReactions — разбор подключён к сообщению, а не живёт сам
// по себе. Без этого счётчики есть, но никто их не получит.
func TestParseMessageCarriesReactions(t *testing.T) {
	mock := newMockTDClient()
	msg := parseMessage(context.Background(), mock, messageWithReactions())

	if len(msg.Reactions) != 2 {
		t.Fatalf("у сообщения %d реакций, want 2: %+v", len(msg.Reactions), msg.Reactions)
	}
	if !msg.Reactions[0].IsChosen {
		t.Error("первая реакция помечена как своя — разбор потерял is_chosen")
	}
}

// TestAddMessageReactionSendsNativeRequest — проверяем ровно тот JSON, который
// уходит в TDLib. Схема addMessageReaction: chat_id, message_id, reaction_type,
// is_big, update_recent_reactions. Реакция — объект reactionTypeEmoji, а не
// строка: ошибка здесь тихо не сработала бы на сервере.
func TestAddMessageReactionSendsNativeRequest(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{{"@type": "ok"}}

	if err := AddMessageReaction(context.Background(), mock, 5, 10, "❤"); err != nil {
		t.Fatalf("AddMessageReaction: %v", err)
	}
	if len(mock.requests) != 1 {
		t.Fatalf("запросов %d, want 1", len(mock.requests))
	}
	req := mock.requests[0]
	if req["@type"] != "addMessageReaction" {
		t.Errorf("@type = %v, want addMessageReaction", req["@type"])
	}
	if req["chat_id"] != int64(5) || req["message_id"] != int64(10) {
		t.Errorf("адресат или сообщение неверны: %+v", req)
	}
	reaction, ok := req["reaction_type"].(map[string]interface{})
	if !ok {
		t.Fatalf("reaction_type = %T, want map: %+v", req["reaction_type"], req)
	}
	if reaction["@type"] != "reactionTypeEmoji" || reaction["emoji"] != "❤" {
		t.Errorf("reaction_type = %+v, want reactionTypeEmoji/❤", reaction)
	}
	if req["is_big"] != false || req["update_recent_reactions"] != true {
		t.Errorf("флаги неверны: %+v", req)
	}
}

// TestRemoveMessageReactionSendsNativeRequest — у remove в схеме только три поля,
// без is_big и update_recent_reactions. Лишний флаг TDLib отвергнет.
func TestRemoveMessageReactionSendsNativeRequest(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{{"@type": "ok"}}

	if err := RemoveMessageReaction(context.Background(), mock, 5, 10, "❤"); err != nil {
		t.Fatalf("RemoveMessageReaction: %v", err)
	}
	req := mock.requests[0]
	if req["@type"] != "removeMessageReaction" {
		t.Errorf("@type = %v, want removeMessageReaction", req["@type"])
	}
	if len(req) != 4 {
		t.Errorf("в запросе %d полей, want 4 (без is_big/update_recent_reactions): %+v", len(req), req)
	}
}

// TestReactionMethodsRejectNonOkAnswer — ответ не «ok» обязан давать ошибку, а не
// молча считаться успехом: иначе интерфейс покажет «отправлено» при отказе сервера.
func TestReactionMethodsRejectNonOkAnswer(t *testing.T) {
	for _, method := range []string{"add", "remove"} {
		t.Run(method, func(t *testing.T) {
			mock := newMockTDClient()
			mock.responses = []map[string]interface{}{{"@type": "message", "id": float64(1)}}

			var err error
			if method == "add" {
				err = AddMessageReaction(context.Background(), mock, 5, 10, "❤")
			} else {
				err = RemoveMessageReaction(context.Background(), mock, 5, 10, "❤")
			}
			if err == nil {
				t.Fatal("ожидалась ошибка на ответе не «ok», получен nil")
			}
		})
	}
}

// TestReactionMethodsWrapTransportError — ошибка транспорта оборачивается с
// указанием метода, иначе в логе не понять, что именно не сработало.
func TestReactionMethodsWrapTransportError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{{"@type": "error", "message": "CHAT_NOT_FOUND"}}

	err := AddMessageReaction(context.Background(), mock, 5, 10, "❤")
	if err == nil {
		t.Fatal("ожидалась ошибка транспорта, получен nil")
	}
	if !contains(err.Error(), "addMessageReaction") {
		t.Errorf("в ошибке нет имени метода: %v", err)
	}
}

// TestGetMessageAvailableReactionsReadsTopReactionsOnly — решение человека:
// берём только top_reactions, остальные два списка не разбираем.
func TestGetMessageAvailableReactionsReadsTopReactionsOnly(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{{"@type": "availableReactions", "reactions": []interface{}{
		map[string]interface{}{"type": map[string]interface{}{"@type": "reactionTypeEmoji", "emoji": "👍"}},
		map[string]interface{}{"type": map[string]interface{}{"@type": "reactionTypeEmoji", "emoji": "❤"}},
		map[string]interface{}{"type": map[string]interface{}{"@type": "reactionTypeEmoji", "emoji": "🔥"}},
	}, "top_reactions": []interface{}{
		map[string]interface{}{"type": map[string]interface{}{"@type": "reactionTypeEmoji", "emoji": "❤"}},
		map[string]interface{}{"type": map[string]interface{}{"@type": "reactionTypeCustomEmoji", "custom_emoji_id": float64(1)}},
		map[string]interface{}{"type": map[string]interface{}{"@type": "reactionTypeEmoji", "emoji": "🔥"}},
	}, "unavailability_reason": nil}}

	got, err := GetMessageAvailableReactions(context.Background(), mock, 5, 10, 5)
	if err != nil {
		t.Fatalf("GetMessageAvailableReactions: %v", err)
	}
	if len(got.Emojis) != 2 {
		t.Fatalf("эмодзи %v, want только top_reactions без кастомного", got.Emojis)
	}
	if got.Emojis[0] != "❤" || got.Emojis[1] != "🔥" {
		t.Errorf("эмодзи = %v, want [❤ 🔥]", got.Emojis)
	}
	if got.UnavailableReason != "" {
		t.Errorf("причина = %q, want пусто (поле nullable)", got.UnavailableReason)
	}
}

// TestGetMessageAvailableReactionsClampsRowSize — по схеме row_size обязан быть в
// диапазоне 5-25, и выход за него TDLib считает ошибкой. Значит зажимать надо
// здесь, а не перекладывать на вызывающую сторону.
func TestGetMessageAvailableReactionsClampsRowSize(t *testing.T) {
	for _, testCase := range []struct {
		name string
		give int
		want int
	}{
		{"ноль", 0, 5},
		{"ниже минимума", 1, 5},
		{"в диапазоне", 10, 10},
		{"выше максимума", 100, 25},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			mock := newMockTDClient()
			mock.responses = []map[string]interface{}{{"@type": "availableReactions"}}

			if _, err := GetMessageAvailableReactions(context.Background(), mock, 5, 10, testCase.give); err != nil {
				t.Fatalf("GetMessageAvailableReactions: %v", err)
			}
			if got := mock.requests[0]["row_size"]; got != testCase.want {
				t.Errorf("row_size = %v, want %v", got, testCase.want)
			}
		})
	}
}

// TestGetMessageAvailableReactionsKeepsUnavailabilityReason — когда реакцию
// поставить нельзя, интерфейс обязан это знать, иначе предложит список, который
// не сработает. Причины в схеме три, и они приходят отдельным объектом.
func TestGetMessageAvailableReactionsKeepsUnavailabilityReason(t *testing.T) {
	for _, reason := range []string{
		"reactionUnavailabilityReasonRestricted",
		"reactionUnavailabilityReasonGuest",
		"reactionUnavailabilityReasonAnonymousAdministrator",
	} {
		t.Run(reason, func(t *testing.T) {
			mock := newMockTDClient()
			mock.responses = []map[string]interface{}{{"@type": "availableReactions",
				"unavailability_reason": map[string]interface{}{"@type": reason}}}

			got, err := GetMessageAvailableReactions(context.Background(), mock, 5, 10, 5)
			if err != nil {
				t.Fatalf("GetMessageAvailableReactions: %v", err)
			}
			if got.UnavailableReason != reason {
				t.Errorf("причина = %q, want %q", got.UnavailableReason, reason)
			}
		})
	}
}

// TestGetMessageAvailableRejectsWrongType — ответ не «availableReactions» должен
// давать ошибку, а не пустой список: иначе интерфейс покажет «реакций нет».
func TestGetMessageAvailableRejectsWrongType(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{{"@type": "error", "message": "boom"}}

	if _, err := GetMessageAvailableReactions(context.Background(), mock, 5, 10, 5); err == nil {
		t.Fatal("ожидалась ошибка, получен nil")
	}
}

// TestParseMessageUnreadReactionsUpdate — единственный пользовательский апдейт по
// реакциям: updateMessageReaction(s) помечены в схеме «for bots only», поэтому
// подписка на них была бы кодом, который не сработает никогда.
func TestParseMessageUnreadReactionsUpdate(t *testing.T) {
	chatID, messageID, count, ok := ParseMessageUnreadReactionsUpdate(map[string]interface{}{
		"@type":                 "updateMessageUnreadReactions",
		"chat_id":               float64(5),
		"message_id":            float64(10),
		"unread_reaction_count": float64(3),
		"unread_reactions":      []interface{}{},
	})
	if !ok {
		t.Fatal("ожидался ok == true")
	}
	if chatID != 5 || messageID != 10 || count != 3 {
		t.Errorf("разобрано %d/%d/%d, want 5/10/3", chatID, messageID, count)
	}
}

// TestParseMessageUnreadReactionsUpdateRejectsGarbage — контракт остальных
// парсеров файла: чужой @type, отсутствующие поля и битой вход дают ok == false
// без паники, потому что вызывающая сторона не должна разбирать сырой JSON.
func TestParseMessageUnreadReactionsUpdateRejectsGarbage(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		update map[string]interface{}
	}{
		{"чужой тип", map[string]interface{}{"@type": "updateMessageReaction"}},
		{"пустой апдейт", map[string]interface{}{}},
		{"нет chat_id", map[string]interface{}{"@type": "updateMessageUnreadReactions", "message_id": float64(1)}},
		{"нет message_id", map[string]interface{}{"@type": "updateMessageUnreadReactions", "chat_id": float64(1)}},
		{"chat_id не число", map[string]interface{}{"@type": "updateMessageUnreadReactions",
			"chat_id": "пять", "message_id": float64(1)}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if _, _, _, ok := ParseMessageUnreadReactionsUpdate(testCase.update); ok {
				t.Fatal("ожидался ok == false")
			}
		})
	}
}
