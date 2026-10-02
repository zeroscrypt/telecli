package tgwall

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"telecli/internal/auth"
)

// Живое число непрочитанных. Механизм целиком перенесён из ленты tgcli
// (waitForChatReadInboxUpdate/applyChatReadInboxUpdate в
// internal/tgclitui/live.go): TDLib шлёт updateChatReadInbox и на новое
// сообщение, и на прочтение, разбор апдейта в проекте уже есть — переносится,
// а не изобретается.

// wallUnreadUpdateMsg — один апдейт из client.ChatReadInboxUpdates(): новое
// количество непрочитанных В КОНКРЕТНОМ чате. Контракт полей тот же, что у
// wallMessageUpdateMsg: valid == false — нераспознанный апдейт (молча
// пропускается, но переподписка обязательна), closed == true — единственный
// случай, когда переподписываться не нужно.
type wallUnreadUpdateMsg struct {
	closed      bool
	valid       bool
	chatID      int64
	unreadCount int32
}

// waitForWallUnreadUpdate блокируется на одном updateChatReadInbox. Скелет
// один в один с waitForWallMessageUpdate: подписка одноразовая, каждый вызов
// tea.Cmd ждёт ровно один апдейт, поэтому обработчик в Update обязан вернуть
// новый вызов после разбора.
func (m Model) waitForWallUnreadUpdate() tea.Cmd {
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client := m.client
	if client == nil {
		return func() tea.Msg { return wallUnreadUpdateMsg{closed: true} }
	}
	ch := client.ChatReadInboxUpdates()
	if ch == nil {
		return func() tea.Msg { return wallUnreadUpdateMsg{closed: true} }
	}
	return func() tea.Msg {
		select {
		case update, ok := <-ch:
			if !ok {
				return wallUnreadUpdateMsg{closed: true}
			}
			chatID, unreadCount, valid := auth.ParseChatReadInboxUpdate(update)
			return wallUnreadUpdateMsg{chatID: chatID, unreadCount: unreadCount, valid: valid}
		case <-ctx.Done():
			return wallUnreadUpdateMsg{closed: true}
		}
	}
}

// applyWallUnreadUpdate — обновление числа непрочитанных на карточке источника.
//
// В отличие от applyWallMessageUpdate не двигает карточку и не трогает ни
// курсор, ни окно прокрутки: изменилось только число, а источник с изменившимся
// числом не переставляется и не считается «новым сообщением». Перестановка от
// нового сообщения приедет отдельным апдейтом — ровно тогда, когда она нужна.
//
// Правка идёт в m.source, а видимый список пересобирается: карточка источника,
// скрытая фильтром, всё равно обязана получить новое число — иначе снятие галки
// показало бы ей старый счётчик, будто апдейта не было. Пересборка при этом
// курсор не уводит (rebuildVisible удерживает его на том же источнике), так что
// «не трогает курсор» остаётся в силе.
//
// Апдейт по чату, которого в потоке нет, молча игнорируется: цикл ничего не
// находит, break не наступает. Это нормальное поведение, а не пропуск: у
// источника без карточки нечего обновлять (живость списка чатов — отдельная
// работа, как и в applyWallMessageUpdate), и отдельная проверка «известен ли
// чат» по chatsByID здесь не нужна — в отличие от пути сообщений, где из чата
// ещё собирается сама карточка.
func (m Model) applyWallUnreadUpdate(chatID int64, unreadCount int32) Model {
	for index := range m.source {
		if m.source[index].ChatID == chatID {
			m.source[index].UnreadCount = unreadCount
			break
		}
	}
	return m.rebuildVisible(false)
}
