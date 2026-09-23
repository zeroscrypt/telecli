package auth

import (
	"context"
	"testing"
)

// searchChatsResponse строит ответ {"@type":"chats","chat_ids":[...]} —
// форму, которую отдают searchChatsOnServer/searchPublicChats.
func searchChatsResponse(ids ...int64) map[string]interface{} {
	list := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		list = append(list, float64(id))
	}
	return map[string]interface{}{"@type": "chats", "chat_ids": list}
}

// searchContactsResponse строит ответ {"@type":"users","user_ids":[...]} —
// форму, которую отдаёт searchContacts.
func searchContactsResponse(ids ...int64) map[string]interface{} {
	list := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		list = append(list, float64(id))
	}
	return map[string]interface{}{"@type": "users", "user_ids": list}
}

// getChatResponse строит ответ getChat с нужным заголовком.
func getChatResponse(chatID int64, title string) map[string]interface{} {
	return map[string]interface{}{"@type": "chat", "id": float64(chatID), "title": title}
}

// getUserResponse строит ответ getUser с нужным именем.
func getUserResponse(userID int64, first, last string) map[string]interface{} {
	return map[string]interface{}{"@type": "user", "id": float64(userID), "first_name": first, "last_name": last}
}

// TestSearchAllCombinesKnownAndPublicChatsAndContacts — мок отвечает на все
// три запроса непустыми результатами (разные chat_id/user_id) —
// SearchResults содержит все чаты и контакты с правильными заголовками/именами.
func TestSearchAllCombinesKnownAndPublicChatsAndContacts(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		searchChatsResponse(1, 2),
		getChatResponse(1, "Известный чат"),
		getChatResponse(2, "Ещё один"),
		searchChatsResponse(3),
		getChatResponse(3, "Публичный канал"),
		searchContactsResponse(100, 200),
		getUserResponse(100, "Иван", "Петров"),
		getUserResponse(200, "Анна", ""),
	}

	res, err := SearchAll(context.Background(), mock, "query")
	if err != nil {
		t.Fatalf("SearchAll failed: %v", err)
	}

	if len(res.Chats) != 3 {
		t.Fatalf("expected 3 chats, got %d: %+v", len(res.Chats), res.Chats)
	}
	wantChats := []SearchResultChat{
		{ID: 1, Title: "Известный чат"},
		{ID: 2, Title: "Ещё один"},
		{ID: 3, Title: "Публичный канал"},
	}
	for i, want := range wantChats {
		if res.Chats[i] != want {
			t.Errorf("chats[%d] = %+v, want %+v", i, res.Chats[i], want)
		}
	}

	if len(res.Contacts) != 2 {
		t.Fatalf("expected 2 contacts, got %d: %+v", len(res.Contacts), res.Contacts)
	}
	wantContacts := []SearchResultContact{
		{UserID: 100, Name: "Иван Петров"},
		{UserID: 200, Name: "Анна"},
	}
	for i, want := range wantContacts {
		if res.Contacts[i] != want {
			t.Errorf("contacts[%d] = %+v, want %+v", i, res.Contacts[i], want)
		}
	}
}

// TestSearchAllDeduplicatesChatsAcrossKnownAndPublic — searchChatsOnServer и
// searchPublicChats возвращают ОДИН И ТОТ ЖЕ chat_id — в итоговом Chats он
// встречается ровно один раз (приоритет — первому вхождению, из
// searchChatsOnServer с точным заголовком).
func TestSearchAllDeduplicatesChatsAcrossKnownAndPublic(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		searchChatsResponse(42),
		getChatResponse(42, "Точный заголовок"),
		searchChatsResponse(42),
		searchContactsResponse(),
	}

	res, err := SearchAll(context.Background(), mock, "query")
	if err != nil {
		t.Fatalf("SearchAll failed: %v", err)
	}

	if len(res.Chats) != 1 {
		t.Fatalf("expected exactly 1 chat after dedup, got %d: %+v", len(res.Chats), res.Chats)
	}
	if res.Chats[0].ID != 42 || res.Chats[0].Title != "Точный заголовок" {
		t.Errorf("deduped chat = %+v, want {42, Точный заголовок}", res.Chats[0])
	}

	// getChat должен быть вызван ровно один раз (для дубликата повторно не ходим).
	var getChatCalls int
	for _, req := range mock.requests {
		if req["@type"] == "getChat" {
			getChatCalls++
		}
	}
	if getChatCalls != 1 {
		t.Errorf("expected exactly 1 getChat call, got %d", getChatCalls)
	}
}

// TestSearchAllPartialFailureStillReturnsWhatWorked — searchPublicChats отдаёт
// ошибку, остальные два — нормальный ответ — SearchAll возвращает err == nil
// и непустой результат из тех двух, что сработали.
func TestSearchAllPartialFailureStillReturnsWhatWorked(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		searchChatsResponse(1),
		getChatResponse(1, "Известный"),
		{"@type": "error", "code": 400, "message": "SEARCH_QUERY_EMPTY"},
		searchContactsResponse(7),
		getUserResponse(7, "Контакт", ""),
	}

	res, err := SearchAll(context.Background(), mock, "query")
	if err != nil {
		t.Fatalf("SearchAll must return nil err on partial failure, got %v", err)
	}
	if len(res.Chats) != 1 || res.Chats[0].ID != 1 {
		t.Errorf("expected chats from searchChatsOnServer to survive, got %+v", res.Chats)
	}
	if len(res.Contacts) != 1 || res.Contacts[0].UserID != 7 {
		t.Errorf("expected contacts to survive, got %+v", res.Contacts)
	}
}

// TestSearchAllAllFailedReturnsError — все три запроса дают ошибку (или пустые
// результаты) — SearchAll возвращает непустую ошибку.
func TestSearchAllAllFailedReturnsError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "error", "code": 400, "message": "boom known"},
		{"@type": "error", "code": 400, "message": "boom public"},
		{"@type": "error", "code": 400, "message": "boom contacts"},
	}

	res, err := SearchAll(context.Background(), mock, "query")
	if err == nil {
		t.Fatal("expected error when all three queries fail")
	}
	if len(res.Chats) != 0 || len(res.Contacts) != 0 {
		t.Errorf("expected empty result on total failure, got %+v", res)
	}
}

// TestSearchAllAllEmptyResultsReturnsError — те же ошибки, но в форме пустых
// (но валидных) ответов: ни одного чата, ни одного контакта — тоже ошибка
// (ничего не найдено).
func TestSearchAllAllEmptyResultsReturnsError(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		searchChatsResponse(),
		searchChatsResponse(),
		searchContactsResponse(),
	}

	_, err := SearchAll(context.Background(), mock, "query")
	if err == nil {
		t.Fatal("expected error when all three queries return empty results")
	}
}
