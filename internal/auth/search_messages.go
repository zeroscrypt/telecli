package auth

import (
	"context"
	"fmt"
	"strings"
)

// Поиск по сообщениям. Схема сверена с td_api.h собранной версии и с
// core.telegram.org/tdlib/docs/class-td_api__search_messages.html:
//
//   - searchMessages(chat_list_, query_, offset_, limit_, filter_,
//     chat_type_filter_, min_date_, max_date_) — «Searches for messages in all
//     chats except secret chats»;
//   - про chat_list_ документация говорит прямо: «pass null to search in all
//     chats regardless of their chat list» — то есть null означает «по всем чатам,
//     включая архив», а не «без ограничений»;
//   - foundMessages(total_count_, messages_, next_offset_) — пагинация через
//     next_offset_, который кладётся в offset_ следующего запроса;
//   - limit «up to 100».
//
// Остальные виды поиска (searchChatsOnServer, searchPublicChats, контакты) уже
// собраны в SearchAll — здесь только то, чего там нет: поиск по тексту сообщений.

// MessageHit — найденное сообщение: идентификатор (по нему лента встаёт на
// сообщение при переходе из поиска), чат, дата и текст.
type MessageHit struct {
	ID     int64
	ChatID int64
	Date   int64
	Text   string
}

// SearchMessages — сообщения по тексту запроса, по всем чатам, включая архив.
//
// offset пустой на первом запросе, дальше — next_offset из ответа. limit
// ограничен 100 самой TDLib, поэтому значения вне диапазона подтягиваются к нему:
// иначе TDLib вернул бы ошибку на лимите.
func SearchMessages(ctx context.Context, client TDClientInterface, query, offset string, limit int) ([]MessageHit, string, int, error) {
	if client == nil {
		return nil, "", 0, fmt.Errorf("TDLib client is nil")
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, "", 0, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	resp, err := client.Send(ctx, map[string]interface{}{
		"@type":     "searchMessages",
		"chat_list": nil,
		"query":     query,
		"offset":    offset,
		"limit":     limit,
		"filter":    nil,
		"min_date":  0,
		"max_date":  0,
	})
	if err != nil {
		return nil, "", 0, fmt.Errorf("searchMessages failed: %w", err)
	}
	if resp["@type"] != "foundMessages" {
		// TDLib вернул error, а не foundMessages: например, 400 на слишком
		// короткий запрос или 404 на недоступный чат. Текст ошибки берётся из
		// объекта error, чтобы вызывающий знал, что запрос не прошёл, и не
		// превратил это в «ничего не найдено».
		code, _ := resp["code"].(float64)
		message, _ := resp["message"].(string)
		return nil, "", 0, fmt.Errorf("searchMessages: TDLib error %d: %s", int(code), message)
	}
	totalCount, _ := resp["total_count"].(float64)
	nextOffset, _ := resp["next_offset"].(string)
	messagesRaw, _ := resp["messages"].([]interface{})
	hits := make([]MessageHit, 0, len(messagesRaw))
	for _, raw := range messagesRaw {
		message, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		id, _ := message["id"].(float64)
		chatID, _ := message["chat_id"].(float64)
		date, _ := message["date"].(float64)
		hits = append(hits, MessageHit{
			ID:     int64(id),
			ChatID: int64(chatID),
			Date:   int64(date),
			Text:   messageText(message),
		})
	}
	return hits, nextOffset, int(totalCount), nil
}
