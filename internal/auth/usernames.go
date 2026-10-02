package auth

import (
	"context"
	"fmt"
	"strings"
)

// SearchPublicChat ищет чат по публичному нику Telegram: «@hakatao» — и возвращает
// его как обычный Chat.
//
// Запрос — searchPublicChat, сверено с td_api.h собранной версии TDLib 1.8.67 и с
// официальной документацией: «Searches a public chat by its username. Currently,
// only private chats, supergroups and channels can be public. Returns the chat if
// found; otherwise, an error is returned». Поле username — БЕЗ ведущей «@»: TDLib
// ники всегда без неё, и запрос с «@» отвергается ошибкой.
//
// Что этим не покрыто (ограничение Telegram, а не наше): аккаунт без публичного
// ника по нику не находится — публичного ника у него просто нет. Таких находят по
// имени из списка чатов (см. SearchAll) или по номеру телефона в контактах.
func SearchPublicChat(ctx context.Context, client TDClientInterface, username string) (Chat, error) {
	name := strings.TrimPrefix(strings.TrimSpace(username), "@")
	if name == "" {
		return Chat{}, fmt.Errorf("пустой ник")
	}
	if client == nil {
		return Chat{}, fmt.Errorf("TDLib client is nil")
	}
	resp, err := client.Send(ctx, map[string]interface{}{
		"@type":    "searchPublicChat",
		"username": name,
	})
	if err != nil {
		return Chat{}, err
	}
	if message, failed := resp["message"].(string); failed && resp["@type"] == "error" {
		if message == "" {
			message = fmt.Sprintf("чат по нику @%s не найден", name)
		}
		return Chat{}, fmt.Errorf("%s", message)
	}
	if resp["@type"] != "chat" {
		return Chat{}, fmt.Errorf("чат по нику @%s не найден", name)
	}
	id, ok := resp["id"].(float64)
	if !ok || id == 0 {
		return Chat{}, fmt.Errorf("чат по нику @%s не найден", name)
	}
	chat := Chat{ID: int64(id)}
	if title, ok := resp["title"].(string); ok {
		chat.Title = title
	}
	chatType, _ := resp["type"].(map[string]interface{})
	chat.Kind = chatKindOf(chatType)
	return chat, nil
}

// GetOwnUsername — основной публичный ник текущего аккаунта, без ведущей «@».
//
// Запрос — getMe, ответ — объект user. Ник лежит не на самом user, а внутри
// объекта usernames: usernames.editable_username (сверено с
// ~/.local/tdlib/include/td/telegram/td_api.h собранной версии: "class usernames {
// ... string const &editable_username_; ... }", а сам user несёт
// "object_ptr<usernames> usernames_"). Именно editable_username, а не
// active_usernames[0]: у аккаунта их может быть несколько, а показывать надо
// тот, который можно поменять, — тот же, что Telegram показывает «своим».
//
// Пустая строка без ошибки — у аккаунта просто нет ника (такое бывает, если ник
// не задан, и это не поломка). Ошибка возвращается только когда не отвечает
// сам TDLib: подставлять вместо сбоя пустую строку значило бы тихо показать
// «ника нет» там, где на самом деле клиент не ответил.
func GetOwnUsername(ctx context.Context, client TDClientInterface) (string, error) {
	if client == nil {
		return "", fmt.Errorf("TDLib client is nil")
	}
	resp, err := client.Send(ctx, map[string]interface{}{"@type": "getMe"})
	if err != nil {
		return "", fmt.Errorf("getMe failed: %w", err)
	}
	usernames, ok := resp["usernames"].(map[string]interface{})
	if !ok {
		// Битый ответ: объекта usernames нет. Ник при этом неизвестен, и подменять
		// его догадкой нельзя — пустая строка читалась бы как «у аккаунта нет
		// ника», а это не то, что здесь произошло.
		return "", nil
	}
	username, _ := usernames["editable_username"].(string)
	return username, nil
}
