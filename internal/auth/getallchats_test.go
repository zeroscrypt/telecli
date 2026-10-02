package auth

import (
	"context"
	"testing"
	"time"
)

// GetAllChats — функция, которой tgwall грузит список чатов исчерпывающе. Проверяется
// на мок-клиенте, по уже принятому в пакете образцу (mockTDClient из auth_test.go).
// Проверяется именно то, ради чего функция написана: loadChats зовётся В ЦИКЛЕ до
// первой ошибки, а getChats — ровно один раз после цикла, с тем же chat_list и без
// искусственного потолка.

// tdlibChats — ответ getChats со списком id чатов.
func tdlibChats(ids ...int64) map[string]interface{} {
	raw := make([]interface{}, 0, len(ids))
	for _, id := range ids {
		raw = append(raw, float64(id))
	}
	return map[string]interface{}{"@type": "chats", "chat_ids": raw}
}

// tdlibOK — ответ loadChats.
func tdlibOK() map[string]interface{} { return map[string]interface{}{"@type": "ok"} }

// testContext — контекст с таймаутом для тестов GetAllChats.
//
// Не формальность: функция крутит цикл догружки, и если бы на мутированном коде
// (выход по ошибке loadChats убран) цикл стал бесконечным, тест без таймаута
// ПОВЕСИЛ БЫ СЬЮТ до общего 90-секундного лимита go test. С таймаутом на самом
// контексте тест падает сам, за секунды и с внятным текстом «контекст истёк»,
// вместо того чтобы выглядеть как «завис пакет». Ровно это требовала задача:
// поймать мутацию через таймаут контекста, а не зависанием сьюта.
//
// На живом коде таймаут не срабатывает: весь цикл проходит за микросекунды на
// моке. Пять секунд — с запасом на медленную машину, а не «почти никогда».
func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// requestsOf — типы запросов мока по порядку, для читаемых проверок.
func requestsOf(mock *mockTDClient) []string {
	types := make([]string, 0, len(mock.requests))
	for _, request := range mock.requests {
		name, _ := request["@type"].(string)
		types = append(types, name)
	}
	return types
}

// TestGetAllChatsLoadsUntilLoadChatsFails — цикл догружки идёт, пока loadChats отвечает,
// и останавливается на первой ошибке. Именно этот порядок и даёт полный список: у
// аккаунта с сотнями чатов одного вызова loadChats мало, а getChats до догружки
// отдаёт только часть.
func TestGetAllChatsLoadsUntilLoadChatsFails(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		tdlibOK(), // loadChats #1
		tdlibOK(), // loadChats #2
		tdlibOK(), // loadChats #3
		{"@type": "error", "message": "Not Found"}, // loadChats #4 — конец списка
		tdlibChats(1, 2), // getChats
		{"@type": "chat", "id": float64(1), "title": "Первый", "type": map[string]interface{}{"@type": "chatTypePrivate"}},
		{"@type": "chat", "id": float64(2), "title": "Второй", "type": map[string]interface{}{"@type": "chatTypeSupergroup", "is_channel": true}},
	}

	chats, err := GetAllChats(testContext(t), mock, map[string]interface{}{"@type": "chatListMain"})
	if err != nil {
		t.Fatalf("GetAllChats failed: %v", err)
	}

	// Ровно четыре вызова loadChats: три успешных и один, на котором цикл встал.
	loadChatsCount, getChatsCount := 0, 0
	for _, name := range requestsOf(mock) {
		switch name {
		case "loadChats":
			loadChatsCount++
		case "getChats":
			getChatsCount++
		}
	}
	if loadChatsCount != 4 {
		t.Fatalf("loadChats вызван %d раз, ждали 4 (три успешных + один, на котором цикл остановился)", loadChatsCount)
	}
	// getChats — ровно один раз, ПОСЛЕ цикла. Если бы он звался в цикле, дырка была
	// бы не видна: лишний getChats вернул бы те же чаты и тест прошёл бы.
	if getChatsCount != 1 {
		t.Fatalf("getChats вызван %d раз, ждали 1", getChatsCount)
	}
	if types := requestsOf(mock); types[getChatsIndex(t, types)] != "getChats" || getChatsIndex(t, types) < loadChatsCount-1 {
		t.Fatalf("getChats вызван не после цикла догружки: %v", types)
	}

	if len(chats) != 2 {
		t.Fatalf("чатов %d, ждали 2: %+v", len(chats), chats)
	}
	if chats[0].ID != 1 || chats[0].Title != "Первый" {
		t.Errorf("первый чат разобран неверно: %+v", chats[0])
	}
	if chats[1].ID != 2 || chats[1].Title != "Второй" {
		t.Errorf("второй чат разобран неверно: %+v", chats[1])
	}
	// Категория не теряется на догружке: канал остался каналом.
	if chats[1].Kind != ChatChannel {
		t.Errorf("второй чат: Kind = %v, ждали ChatChannel", chats[1].Kind)
	}
}

func getChatsIndex(t *testing.T, types []string) int {
	t.Helper()
	for index, name := range types {
		if name == "getChats" {
			return index
		}
	}
	t.Fatalf("в запросах нет getChats: %v", types)
	return -1
}

// TestGetAllChatsKeepsLoadedChatsOnEarlyFailure — если loadChats упал сразу (сеть,
// ещё не авторизовались, TDLib занят), цикл встаёт после первого же вызова, и уже
// загруженное не выбрасывается: стена показывает то, что есть, вместо пустой.
func TestGetAllChatsKeepsLoadedChatsOnEarlyFailure(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "error", "message": "Chat list is loading"},
		tdlibChats(5),
		{"@type": "chat", "id": float64(5), "title": "Чат пять", "type": map[string]interface{}{"@type": "chatTypeBasicGroup"}},
	}

	chats, err := GetAllChats(testContext(t), mock, map[string]interface{}{"@type": "chatListMain"})
	if err != nil {
		t.Fatalf("GetAllChats failed: %v", err)
	}
	if len(chats) != 1 || chats[0].ID != 5 {
		t.Fatalf("загруженный чат потерян после сбоя loadChats: %+v", chats)
	}
	if types := requestsOf(mock); len(types) != 3 {
		t.Fatalf("запросов %d (%v), ждали один loadChats, getChats и один getChat", len(types), types)
	}
}

// TestGetAllChatsPassesTheGivenChatList — тот же контракт, что у GetChats: переданный
// chat_list доходит и до loadChats, и до getChats, а не подменяется на chatListMain.
func TestGetAllChatsPassesTheGivenChatList(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		tdlibOK(),
		{"@type": "error", "message": "Not Found"},
		tdlibChats(),
	}

	chatList := map[string]interface{}{"@type": "chatListFolder", "chat_folder_id": 3}
	if _, err := GetAllChats(testContext(t), mock, chatList); err != nil {
		t.Fatalf("GetAllChats failed: %v", err)
	}
	for _, request := range mock.requests {
		switch request["@type"] {
		case "loadChats", "getChats":
		default:
			continue
		}
		got, ok := request["chat_list"].(map[string]interface{})
		if !ok {
			t.Fatalf("запрос %v без chat_list", request["@type"])
		}
		if got["@type"] != "chatListFolder" || got["chat_folder_id"] != 3 {
			t.Errorf("запрос %v: chat_list = %#v, ждали chatListFolder(3)", request["@type"], got)
		}
	}
}

// TestGetAllChatsAsksGetChatsWithoutArtificialCap — главное отличие от GetChats:
// потолка на число чатов тут нет. Значение limit у getChats должно быть заметно
// больше сотни (потолка GetChats), иначе у аккаунта с сотнями чатов часть списка
// молча пропала бы — а по замыслу задачи стена показывает ВСЕ чаты.
func TestGetAllChatsAsksGetChatsWithoutArtificialCap(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		tdlibOK(),
		{"@type": "error", "message": "Not Found"},
		tdlibChats(),
	}
	if _, err := GetAllChats(testContext(t), mock, map[string]interface{}{"@type": "chatListMain"}); err != nil {
		t.Fatalf("GetAllChats failed: %v", err)
	}

	var limit int
	found := false
	for _, request := range mock.requests {
		if request["@type"] != "getChats" {
			continue
		}
		found = true
		value, ok := request["limit"].(int)
		if !ok {
			t.Fatalf("limit у getChats = %#v, ждали int", request["limit"])
		}
		limit = value
	}
	if !found {
		t.Fatal("getChats не вызван")
	}
	if limit < 1000 {
		t.Fatalf("limit у getChats = %d, ждали больше 1000: список чатов должен грузиться без искусственного потолка", limit)
	}
}

// TestGetAllChatsReturnsErrorWhenGetChatsFails — сбой самого getChats, а не догружки,
// обязан дойти до вызывающей стороны ошибкой: тихо вернуть пустой список значило бы
// показать стену без чатов и без единого слова о причине.
func TestGetAllChatsReturnsErrorWhenGetChatsFails(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		tdlibOK(),
		{"@type": "error", "message": "Not Found"},
		{"@type": "error", "message": "Chat list is loading"},
	}
	chats, err := GetAllChats(testContext(t), mock, map[string]interface{}{"@type": "chatListMain"})
	if err == nil {
		t.Fatalf("GetAllChats вернул чатов %d и nil-ошибку на сбое getChats", len(chats))
	}
	if chats != nil {
		t.Errorf("при ошибке вернулись чаты: %+v", chats)
	}
}

// Мутационная проверка обещанного в файле задачи: если убрать выход по ошибке
// loadChats (то есть заменить `if err != nil { break }` на бесконечный цикл), тест
// ОБЯЗАН упасть по таймауту контекста, а не повесить сьют. Реальный такой вариант
// собирается временной правкой GetAllChats; здесь проверяется то же свойство на
// моке-клиенте, который отвечает «успех» бесконечно: цикл обязан быть прерван
// отменой контекста, а не висеть.
//
// Если GetAllChats перестанет уважать ctx (или потеряет потолок попыток и
// отмену), этот тест зависнет на таймауте go test и упадёт — то есть ровно то
// поведение, которое ловит мутацию.
func TestGetAllChatsStopsWhenContextIsCancelled(t *testing.T) {
	// Клиент, который на loadChats всегда отвечает успехом: цикл догружки без
	// выхода по ошибке тут крутился бы вечно.
	mock := &endlessLoadChatsClient{mockTDClient: newMockTDClient()}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		chats, err := GetAllChats(ctx, mock, map[string]interface{}{"@type": "chatListMain"})
		// Отмена контекста обязана дойти до вызывающей стороны: после неё
		// getChats тоже не ответит, и наружу выходит ошибка, а не пустой список.
		if err == nil && len(chats) > 0 {
			t.Errorf("после отмены контекста вернулось %d чатов без ошибки", len(chats))
		}
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("GetAllChats не вернулся после отмены контекста: цикл догружки не прерывается")
	}

	if mock.loadChatsCalls() == 0 {
		t.Fatal("loadChats не вызывался вовсе")
	}
}

// endlessLoadChatsClient — мок, у которого loadChats всегда «успешен», а getChats
// отвечает списком чатов (чтобы функция дошла до конца цикла и вернула наружу).
// Именно он воспроизводит ситуацию «TDLib почему-то никогда не отвечает Not
// Found», ради которой у GetAllChats есть и предел попыток, и проверка отмены
// контекста.
type endlessLoadChatsClient struct {
	*mockTDClient
	mu            chan struct{}
	loadChatsSeen int
}

func (c *endlessLoadChatsClient) Send(ctx context.Context, request map[string]interface{}) (map[string]interface{}, error) {
	if request["@type"] == "loadChats" {
		if c.mu == nil {
			c.mu = make(chan struct{}, 1)
		}
		c.mu <- struct{}{}
		<-c.mu
		c.loadChatsSeen++
		return map[string]interface{}{"@type": "ok"}, nil
	}
	return c.mockTDClient.Send(ctx, request)
}

func (c *endlessLoadChatsClient) loadChatsCalls() int {
	if c.mu == nil {
		return 0
	}
	c.mu <- struct{}{}
	defer func() { <-c.mu }()
	return c.loadChatsSeen
}

// TestGetAllChatsGivesUpAfterAttemptLimit — предохранитель от бесконечного цикла
// срабатывает, когда loadChats отвечает успехом бесконечно (см. задачу: на это
// полагаться вслепую нельзя). Функция обязана выйти по пределу попыток и вернуть
// то, что getChats отдал, а не висеть.
func TestGetAllChatsGivesUpAfterAttemptLimit(t *testing.T) {
	mock := &endlessLoadChatsClient{mockTDClient: newMockTDClient()}
	mock.responses = []map[string]interface{}{
		tdlibChats(9),
		{"@type": "chat", "id": float64(9), "title": "Девятый", "type": map[string]interface{}{"@type": "chatTypePrivate"}},
	}

	done := make(chan struct{})
	var chats []Chat
	var err error
	go func() {
		defer close(done)
		chats, err = GetAllChats(testContext(t), mock, map[string]interface{}{"@type": "chatListMain"})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("GetAllChats не вернулся: цикл loadChats не ограничен числом попыток")
	}
	if err != nil {
		t.Fatalf("GetAllChats failed: %v", err)
	}
	if len(chats) != 1 || chats[0].ID != 9 {
		t.Fatalf("чатов %d (%+v), ждали один девятый", len(chats), chats)
	}
	if got := mock.loadChatsCalls(); got != allChatsLoadAttempts {
		t.Fatalf("loadChats вызван %d раз, ждали ровно предел попыток %d", got, allChatsLoadAttempts)
	}
}

// TestGetAllChatsDoesNotLoseChatsOnParseFailure — битый чат в ответе (getChat упал)
// выбрасывается, остальные остаются: список не должен рассыпаться из-за одного
// недоступного чата, на стене это деградация, а не падение.
func TestGetAllChatsDoesNotLoseChatsOnParseFailure(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		tdlibOK(),
		{"@type": "error", "message": "Not Found"},
		tdlibChats(1, 2, 3),
		{"@type": "chat", "id": float64(1), "title": "Первый", "type": map[string]interface{}{"@type": "chatTypePrivate"}},
		{"@type": "error", "message": "Chat not found"},
		{"@type": "chat", "id": float64(3), "title": "Третий", "type": map[string]interface{}{"@type": "chatTypeBasicGroup"}},
	}
	chats, err := GetAllChats(testContext(t), mock, map[string]interface{}{"@type": "chatListMain"})
	if err != nil {
		t.Fatalf("GetAllChats failed: %v", err)
	}
	if len(chats) != 2 {
		t.Fatalf("чатов %d, ждали 2 (второй не открылся): %+v", len(chats), chats)
	}
	got := map[int64]string{}
	for _, chat := range chats {
		got[chat.ID] = chat.Title
	}
	if got[1] != "Первый" || got[3] != "Третий" {
		t.Errorf("разбор ответа потерял чаты: %+v", got)
	}
}

// GetChats (с его одним вызовом loadChats) обязан остаться рабочим: tgclitui
// пользуется именно им, и задача прямо запрещает его переписывать. Проверка на то,
// что правка GetAllChats не задела общий разбор getChatsOnce.
func TestGetChatsStillLoadsOnceAndParses(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		tdlibOK(),
		tdlibChats(11),
		{"@type": "chat", "id": float64(11), "title": "Одиннадцатый", "type": map[string]interface{}{"@type": "chatTypePrivate"}},
	}
	chats, err := GetChats(context.Background(), mock, map[string]interface{}{"@type": "chatListMain"}, 10)
	if err != nil {
		t.Fatalf("GetChats failed: %v", err)
	}
	if len(chats) != 1 || chats[0].ID != 11 {
		t.Fatalf("чатов %d (%+v), ждали один одиннадцатый", len(chats), chats)
	}
	loadChatsCount := 0
	for _, name := range requestsOf(mock) {
		if name == "loadChats" {
			loadChatsCount++
		}
	}
	if loadChatsCount != 1 {
		t.Fatalf("GetChats вызвал loadChats %d раз, ждали 1: цикл догружки — только в GetAllChats", loadChatsCount)
	}
}
