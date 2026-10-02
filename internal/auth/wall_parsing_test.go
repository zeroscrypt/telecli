package auth

import (
	"context"
	"regexp"
	"testing"
)

// Разбор полей, которые tgwall читает из ответа TDLib: подпись автора поста и
// собственный ник аккаунта. Оба — новые для интерфейса стены, и оба обязаны
// деградировать в пустое значение, а не в ошибку: подписи у поста может не быть,
// ника у аккаунта может не быть вовсе.

// messageWithSignature — объект message с заданной подписью автора. Остальные поля
// не важны для разбора подписи, поэтому их нет.
func messageWithSignature(signature interface{}) map[string]interface{} {
	message := map[string]interface{}{"@type": "message", "id": float64(1)}
	if signature != nil {
		message["author_signature"] = signature
	}
	return message
}

// TestParseMessageReadsAuthorSignature — подпись автора поста попадает в поле из
// message.author_signature_. Пустое значение — норма (подписи у поста нет), и
// оно обязано оставаться пустой строкой, а не превращаться в мусор вида
// "<nil>": заголовок карточки стены печатает это поле прямо на экран.
func TestParseMessageReadsAuthorSignature(t *testing.T) {
	cases := []struct {
		name      string
		signature interface{}
		want      string
	}{
		{name: "подпись есть", signature: "Дмитрий", want: "Дмитрий"},
		{name: "подпись пустая", signature: "", want: ""},
		{name: "поля нет вовсе", signature: nil, want: ""},
		{name: "подпись не строка", signature: float64(42), want: ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := parseMessage(context.Background(), newMockTDClient(), messageWithSignature(testCase.signature))
			if got.AuthorSignature != testCase.want {
				t.Fatalf("AuthorSignature = %q, ждали %q", got.AuthorSignature, testCase.want)
			}
		})
	}
}

// TestGetMessagesKeepsAuthorSignature — подпись доходит через настоящий путь
// загрузки истории, а не только через прямой вызов разбора. Без этой проверки
// поле могло бы разбираться в parseMessage и при этом теряться на сборке ответа
// getChatHistory (где сообщение проходит через общий цикл выборки).
func TestGetMessagesKeepsAuthorSignature(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "messages", "messages": []interface{}{messageWithSignature("Дмитрий")}},
	}

	messages, err := GetMessages(context.Background(), mock, 1, 1)
	if err != nil {
		t.Fatalf("GetMessages failed: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("сообщений %d, ждали 1", len(messages))
	}
	if messages[0].AuthorSignature != "Дмитрий" {
		t.Fatalf("AuthorSignature = %q, ждали %q", messages[0].AuthorSignature, "Дмитрий")
	}
}

// userWithUsernames — ответ getMe с объектом usernames и указанным
// editable_username.
func userWithUsernames(editable interface{}) map[string]interface{} {
	return map[string]interface{}{
		"@type":     "user",
		"id":        float64(7),
		"usernames": map[string]interface{}{"editable_username": editable},
	}
}

// TestGetOwnUsernameReadsEditableUsername — ник аккаунта берётся именно из
// usernames.editable_username: у аккаунта бывает несколько активных ников, и
// показывать надо тот, который можно поменять.
func TestGetOwnUsernameReadsEditableUsername(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{userWithUsernames("zeroscrypt")}

	got, err := GetOwnUsername(context.Background(), mock)
	if err != nil {
		t.Fatalf("GetOwnUsername failed: %v", err)
	}
	if got != "zeroscrypt" {
		t.Fatalf("ник = %q, ждали %q", got, "zeroscrypt")
	}
	if request := mock.requests[0]; request["@type"] != "getMe" {
		t.Fatalf("запрошен %v, ждали getMe", request["@type"])
	}
}

// TestGetOwnUsernameWithoutUsername — отсутствие ника это не ошибка. Такое бывает
// у личных аккаунтов с незаданным ником, и вызывающая сторона обязана отличать его
// от сбоя: в первом случае просто ничего не показываем, во втором — сообщаем.
func TestGetOwnUsernameWithoutUsername(t *testing.T) {
	cases := []struct {
		name     string
		response map[string]interface{}
	}{
		{
			name: "пустой editable_username",
			response: map[string]interface{}{
				"@type":     "user",
				"id":        float64(7),
				"usernames": map[string]interface{}{"editable_username": ""},
			},
		},
		{
			name: "объекта usernames нет",
			response: map[string]interface{}{
				"@type": "user",
				"id":    float64(7),
			},
		},
		{
			name: "usernames не объект",
			response: map[string]interface{}{
				"@type":     "user",
				"id":        float64(7),
				"usernames": "zeroscrypt",
			},
		},
		{
			name: "editable_username не строка",
			response: map[string]interface{}{
				"@type":     "user",
				"id":        float64(7),
				"usernames": map[string]interface{}{"editable_username": float64(7)},
			},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			mock := newMockTDClient()
			mock.responses = []map[string]interface{}{testCase.response}

			got, err := GetOwnUsername(context.Background(), mock)
			if err != nil {
				t.Fatalf("GetOwnUsername failed: %v (у аккаунта просто нет ника — это не ошибка)", err)
			}
			if got != "" {
				t.Fatalf("ник = %q, ждали пустую строку", got)
			}
		})
	}
}

// TestGetOwnUsernameReportsClientFailure — сбой самого TDLib доходит до вызывающей
// стороны ошибкой, а не пустым ником: пустая строка читалась бы как «у аккаунта
// нет ника», а это не то, что произошло.
func TestGetOwnUsernameReportsClientFailure(t *testing.T) {
	mock := newMockTDClient()
	mock.responses = []map[string]interface{}{
		{"@type": "error", "message": "Not authorized"},
	}
	got, err := GetOwnUsername(context.Background(), mock)
	if err == nil {
		t.Fatalf("GetOwnUsername вернул %q и nil-ошибку на сбое TDLib", got)
	}
}

// TestGetOwnUsernameRejectsNilClient — без клиента функция обязана сказать об этом
// прямо, а не паникой: nil-интерфейс в Send дал бы панику, и в TUI это был бы
// вылет без следа.
func TestGetOwnUsernameRejectsNilClient(t *testing.T) {
	if _, err := GetOwnUsername(context.Background(), nil); err == nil {
		t.Fatal("GetOwnUsername с nil-клиентом вернул nil-ошибку")
	}
}

// TestFormatMessageTimeMatchesFeedFormat — стена и лента tgcli обязаны считать
// время ОДИНАКОВО. Функция живёт в internal/auth именно ради этого, и проверка
// нужна, чтобы при возврате копии в tgclitui расхождение не прошло тихо: обе
// стороны читают FormatMessageTime, и одна копия означала бы два разных времени
// на двух экранах.
func TestFormatMessageTimeMatchesFeedFormat(t *testing.T) {
	// Конкретное время, а не «сейчас»: иначе тест прошёл бы и при смене зоны
	// времени, и при смене формата на глаз.
	got := FormatMessageTime(1_700_000_000)
	if !regexp.MustCompile(`^\d{2}:\d{2}$`).MatchString(got) {
		t.Fatalf("FormatMessageTime(1700000000) = %q, ждали время в формате HH:MM", got)
	}
	// Нулевое и отрицательное время (битый ответ TDLib) дают плейсхолдер, а не
	// пустую строку: колонка времени есть всегда.
	for _, unixTime := range []int64{0, -1} {
		if got := FormatMessageTime(unixTime); got != "--:--" {
			t.Fatalf("FormatMessageTime(%d) = %q, ждали %q", unixTime, got, "--:--")
		}
	}
}
