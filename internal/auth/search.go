package auth

import (
	"context"
	"fmt"
	"strings"
)

// SearchResultChat — найденный чат/канал (уже известный человеку или
// публичный, ещё не открытый) — не путать с Chat (там только уже загруженные
// в текущий список чаты).
type SearchResultChat struct {
	ID    int64
	Title string
}

// SearchResultContact — найденный контакт (человек).
type SearchResultContact struct {
	UserID int64
	Name   string // first_name + " " + last_name, обрезанное по пробелам; пусто — "user#<id>"
}

// SearchResults — объединённый результат SearchAll. Chats — уже
// СДЕДУПЛИЦИРОВАННЫЙ по ID список (searchChatsOnServer и searchPublicChats
// могут вернуть один и тот же chat_id дважды — приоритет отдаётся первому
// найденному вхождению, из searchChatsOnServer, порядок вызовов ниже это и
// обеспечивает).
type SearchResults struct {
	Chats    []SearchResultChat
	Contacts []SearchResultContact
}

const searchLimit = 20

// SearchAll ищет одновременно (а) уже известные человеку чаты/каналы —
// поиск на сервере, не только по локальной памяти, (б) любые публичные
// чаты/каналы Telegram, ещё не открытые человеком, (в) контакты. Порядок
// вызовов (сначала known, потом public) важен для дедупликации — известные
// чаты предпочтительнее (заголовок уже точный, не placeholder).
func SearchAll(ctx context.Context, client TDClientInterface, query string) (SearchResults, error) {
	var result SearchResults
	seenChatIDs := make(map[int64]bool)

	knownResp, err := client.Send(ctx, map[string]interface{}{
		"@type": "searchChatsOnServer",
		"query": query,
		"limit": searchLimit,
	})
	if err == nil {
		for _, id := range chatIDsFromResponse(knownResp) {
			if seenChatIDs[id] {
				continue
			}
			seenChatIDs[id] = true
			result.Chats = append(result.Chats, resolveSearchChat(ctx, client, id))
		}
	}

	publicResp, err := client.Send(ctx, map[string]interface{}{
		"@type": "searchPublicChats",
		"query": query,
	})
	if err == nil {
		for _, id := range chatIDsFromResponse(publicResp) {
			if seenChatIDs[id] {
				continue
			}
			seenChatIDs[id] = true
			result.Chats = append(result.Chats, resolveSearchChat(ctx, client, id))
		}
	}

	contactsResp, err := client.Send(ctx, map[string]interface{}{
		"@type": "searchContacts",
		"query": query,
		"limit": searchLimit,
	})
	if err == nil {
		userIDsRaw, _ := contactsResp["user_ids"].([]interface{})
		for _, idRaw := range userIDsRaw {
			id, ok := idRaw.(float64)
			if !ok {
				continue
			}
			result.Contacts = append(result.Contacts, resolveSearchContact(ctx, client, int64(id)))
		}
	}

	// Ошибка возвращается, только если ВСЕ ТРИ запроса ничего не дали (ни
	// одного результата и хотя бы одна реальная ошибка) — частичный успех
	// (например, searchPublicChats недоступен, но контакты нашлись) не
	// должен прятать то, что реально нашлось.
	if len(result.Chats) == 0 && len(result.Contacts) == 0 {
		return result, fmt.Errorf("ничего не найдено или сервис поиска недоступен")
	}
	return result, nil
}

// chatIDsFromResponse разбирает {"@type":"chats","chat_ids":[...]} — общий
// хелпер для searchChatsOnServer/searchPublicChats, обе отдают одну и ту же
// форму ответа.
func chatIDsFromResponse(resp map[string]interface{}) []int64 {
	if resp["@type"] != "chats" {
		return nil
	}
	idsRaw, ok := resp["chat_ids"].([]interface{})
	if !ok {
		return nil
	}
	ids := make([]int64, 0, len(idsRaw))
	for _, idRaw := range idsRaw {
		id, ok := idRaw.(float64)
		if !ok {
			continue
		}
		ids = append(ids, int64(id))
	}
	return ids
}

// resolveSearchChat — getChat по id, тот же паттерн деградации при ошибке,
// что уже есть в GetChats (заголовок пуст/недоступен — не фатально).
func resolveSearchChat(ctx context.Context, client TDClientInterface, id int64) SearchResultChat {
	resp, err := client.Send(ctx, map[string]interface{}{
		"@type":   "getChat",
		"chat_id": id,
	})
	if err != nil {
		return SearchResultChat{ID: id, Title: fmt.Sprintf("chat#%d", id)}
	}
	title, _ := resp["title"].(string)
	return SearchResultChat{ID: id, Title: title}
}

// resolveSearchContact — getUser по id, имя first_name+" "+last_name.
func resolveSearchContact(ctx context.Context, client TDClientInterface, id int64) SearchResultContact {
	resp, err := client.Send(ctx, map[string]interface{}{
		"@type":   "getUser",
		"user_id": id,
	})
	if err != nil {
		return SearchResultContact{UserID: id, Name: fmt.Sprintf("user#%d", id)}
	}
	first, _ := resp["first_name"].(string)
	last, _ := resp["last_name"].(string)
	name := strings.TrimSpace(first + " " + last)
	if name == "" {
		name = fmt.Sprintf("user#%d", id)
	}
	return SearchResultContact{UserID: id, Name: name}
}
