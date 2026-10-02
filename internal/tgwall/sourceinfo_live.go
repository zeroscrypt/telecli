package tgwall

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"telecli/internal/auth"
)

// Живые сведения об источнике — число участников у групп и каналов, статус
// собеседника у личных диалогов. Механизм целиком тот же, что у живого числа
// непрочитанных (unread.go) и живых сообщений (live.go): TDLib шлёт апдейты
// сам, разбор апдейтов в проекте уже есть, каналы в tdclient заведены по
// образцу существующих.
//
// Первичного запроса на каждую карточку НЕТ, и это не упрощение, а проверенный
// факт: живой прогон на настоящем аккаунте (задача 0163, bin/liveprobe) показал,
// что при обычной загрузке списка чатов TDLib шлёт updateBasicGroup,
// updateSupergroup и updateUser сам — 7, 99 и 230 апдейтов на 200 чатов. То есть
// снимок значений приезжает тем же каналом, что и живые изменения, и отдельный
// getBasicGroup/getSupergroup/getUser на каждую карточку был бы лишними
// запросами без новой информации.

// wallGroupCountUpdateMsg — один апдейт из client.GroupMemberCountUpdates().
// Контракт полей тот же, что у wallUnreadUpdateMsg: valid == false — нераспознанный
// апдейт (пропускается, но переподписка обязательна), closed == true — канала
// больше нет.
type wallGroupCountUpdateMsg struct {
	closed      bool
	valid       bool
	groupID     int64
	memberCount int32
}

// wallUserProfileUpdateMsg — один апдейт из client.UserUpdates(): updateUser
// целиком (имя и статус вместе). Контракт полей тот же.
type wallUserProfileUpdateMsg struct {
	closed bool
	valid  bool
	userID int64
	status auth.UserStatus
}

// wallUserStatusUpdateMsg — один апдейт из client.UserStatusUpdates(): смена
// статуса собеседника. Контракт полей тот же.
type wallUserStatusUpdateMsg struct {
	closed bool
	valid  bool
	userID int64
	status auth.UserStatus
}

// waitForWallGroupCountUpdate блокируется на одном updateBasicGroup /
// updateSupergroup. Скелет один в один с waitForWallUnreadUpdate: подписка
// одноразовая, каждый вызов tea.Cmd ждёт ровно один апдейт, поэтому обработчик
// в Update обязан вернуть новый вызов после разбора.
func (m Model) waitForWallGroupCountUpdate() tea.Cmd {
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client := m.client
	if client == nil {
		return func() tea.Msg { return wallGroupCountUpdateMsg{closed: true} }
	}
	ch := client.GroupMemberCountUpdates()
	if ch == nil {
		return func() tea.Msg { return wallGroupCountUpdateMsg{closed: true} }
	}
	return func() tea.Msg {
		select {
		case update, ok := <-ch:
			if !ok {
				return wallGroupCountUpdateMsg{closed: true}
			}
			groupID, memberCount, valid := auth.ParseGroupMemberCountUpdate(update)
			return wallGroupCountUpdateMsg{groupID: groupID, memberCount: memberCount, valid: valid}
		case <-ctx.Done():
			return wallGroupCountUpdateMsg{closed: true}
		}
	}
}

// waitForWallUserStatusUpdate блокируется на одном updateUserStatus. Скелет тот
// же.
func (m Model) waitForWallUserStatusUpdate() tea.Cmd {
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client := m.client
	if client == nil {
		return func() tea.Msg { return wallUserStatusUpdateMsg{closed: true} }
	}
	ch := client.UserStatusUpdates()
	if ch == nil {
		return func() tea.Msg { return wallUserStatusUpdateMsg{closed: true} }
	}
	return func() tea.Msg {
		select {
		case update, ok := <-ch:
			if !ok {
				return wallUserStatusUpdateMsg{closed: true}
			}
			userID, status, valid := auth.ParseUserStatusUpdate(update)
			return wallUserStatusUpdateMsg{userID: userID, status: status, valid: valid}
		case <-ctx.Done():
			return wallUserStatusUpdateMsg{closed: true}
		}
	}
}

// waitForWallUserProfileUpdate блокируется на одном updateUser. Скелет тот же.
func (m Model) waitForWallUserProfileUpdate() tea.Cmd {
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client := m.client
	if client == nil {
		return func() tea.Msg { return wallUserProfileUpdateMsg{closed: true} }
	}
	ch := client.UserUpdates()
	if ch == nil {
		return func() tea.Msg { return wallUserProfileUpdateMsg{closed: true} }
	}
	return func() tea.Msg {
		select {
		case update, ok := <-ch:
			if !ok {
				return wallUserProfileUpdateMsg{closed: true}
			}
			userID, _, status, valid := auth.ParseUserUpdate(update)
			return wallUserProfileUpdateMsg{userID: userID, status: status, valid: valid}
		case <-ctx.Done():
			return wallUserProfileUpdateMsg{closed: true}
		}
	}
}

// applyWallGroupCountUpdate — новое число участников: в кэш и во все карточки
// этого источника.
//
// В отличие от applyWallMessageUpdate не двигает карточку и не трогает ни курсор,
// ни окно прокрутки: изменилось только число, а источник с изменившимся числом
// не переставляется и не считается «новым сообщением» — ровно как у
// applyWallUnreadUpdate.
//
// Кэш обновляется ВСЕГДА, даже когда карточки этого источника на стене нет: на
// стене ровно одна карточка на источник, но апдейты TDLib шлёт по всем чатам
// аккаунта, и пришедший раньше снимка апдейт не должен потеряться. Карточка,
// построенная позже, подхватит значение из кэша (см. syncWallSourceInfo).
func (m Model) applyWallGroupCountUpdate(groupID int64, memberCount int32) Model {
	if groupID == 0 {
		return m
	}
	if m.groupMemberCounts == nil {
		m.groupMemberCounts = map[int64]int32{}
	}
	m.groupMemberCounts[groupID] = memberCount
	return m.syncWallSourceInfo()
}

// applyWallUserStatusUpdate — смена статуса собеседника: в кэш и во все
// карточки этого диалога. Скелет тот же, что у applyWallGroupCountUpdate.
func (m Model) applyWallUserStatusUpdate(userID int64, status auth.UserStatus) Model {
	if userID == 0 {
		return m
	}
	if m.userStatuses == nil {
		m.userStatuses = map[int64]auth.UserStatus{}
	}
	m.userStatuses[userID] = status
	return m.syncWallSourceInfo()
}

// applyWallUserProfileUpdate — апдейт updateUser целиком: имя И статус.
// Отдельный обработчик рядом с applyWallUserStatusUpdate именно потому, что
// updateUser — это снимок пользователя (приезжает пачками на старте, принося
// имя и статус вместе), а updateUserStatus — только смена статуса. На живом
// аккаунте снимок статуса едет именно сюда: при загрузке списка чатов TDLib
// шлёт updateUser, а updateUserStatus не шлёт ни разу (проверено вживью,
// задача 0163).
//
// Само имя на стене не показывается отдельным полем (у личного диалога имя
// собеседника и есть название чата, см. title()), поэтому из апдейта здесь нужен
// только статус.
func (m Model) applyWallUserProfileUpdate(userID int64, status auth.UserStatus) Model {
	return m.applyWallUserStatusUpdate(userID, status)
}

// syncWallSourceInfo — наводит карточки потока на текущие значения кэшей.
//
// Единственное место во всём проекте, где решается, какие сведения показывать
// у карточки, поэтому и вызывается оно отовсюду, где значения могли разойтись с
// карточками: из applyWallGroupCountUpdate / applyWallUserStatusUpdate и после
// прихода снимка (см. wallLoadedMsg в model.go). Проход по всем карточкам
// дешёв (сотня карточек, а апдейты редки) и зато устраняет класс ошибок
// «значение пришло раньше карточки и потерялось».
func (m Model) syncWallSourceInfo() Model {
	// Правит m.source (полный поток), а не m.cards — при активном фильтре
	// m.cards уже НЕ тот же слайс (filterCards делает копию видимых), и правка
	// одного лишь m.cards терялась бы при первом же пересчёте видимого списка
	// после переключения галки (найдено оркестратором при сведении задач 0163
	// и 0164 — они разошлись именно в этом месте). m.cards пересчитывается тут
	// же, без изменения курсора/окна: набор и порядок видимых источников от
	// сведений не зависит, трогать их незачем.
	for index := range m.source {
		m.source[index] = m.source[index].withSourceInfo(m.groupMemberCounts, m.userStatuses)
	}
	m.cards = m.filterCards()
	return m
}

// withSourceInfo — карточка с её сведениями, взятыми из кэшей по PeerID.
// Копия карточки меняется только в этих двух полях, всё остальное остаётся как
// было: та же логика, что у переноса UnreadCount на новую карточку в
// applyWallMessageUpdate.
func (c card) withSourceInfo(memberCounts map[int64]int32, statuses map[int64]auth.UserStatus) card {
	if c.PeerID == 0 {
		return c
	}
	if c.Type == cardPersonal {
		c.Status = statuses[c.PeerID]
		c.MemberCount = 0
		return c
	}
	c.MemberCount = memberCounts[c.PeerID]
	c.Status = auth.UserStatus{}
	return c
}
