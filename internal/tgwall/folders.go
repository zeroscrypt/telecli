package tgwall

import (
	"context"
	"slices"
	"sync"

	tea "charm.land/bubbletea/v2"

	"telecli/internal/auth"
)

// Папки человека на стене. Механизм целиком перенесён из ленты tgcli
// (waitForChatFolders в internal/tui/model.go): список папок приходит апдейтом
// updateChatFolders из client.ChatFolderUpdates(), разбор апдейта в проекте уже
// есть (auth.ParseChatFoldersUpdate) — переносится, а не изобретается.
//
// Отдельного запроса getChatFolders не делается, ровно как и в tgclitui: TDLib
// присылает updateChatFolders со всем списком сам, в том числе первым после
// входа, и второй источник того же самого был бы лишней расхождаемой версией.

// wallFoldersUpdateMsg — один апдейт из client.ChatFolderUpdates(): список папок
// целиком. Контракт полей тот же, что у wallMessageUpdateMsg: valid == false —
// нераспознанный апдейт (молча пропускается, но переподписка обязательна),
// closed == true — единственный случай, когда переподписываться не нужно.
type wallFoldersUpdateMsg struct {
	closed  bool
	valid   bool
	folders []auth.Folder
}

// wallFolderChatsMsg — состав папок, собранный одной командой: id папки → id её
// чатов.
type wallFolderChatsMsg struct {
	// builtFor — список id папок, для которых собран ответ. Свека обязательна:
	// пока запрос летел, список папок мог смениться (переименование, удаление,
	// добавление), и ответ по прежнему списку либо оставил бы в фильтре чаты
	// удалённой папки, либо потерял бы чаты новой до следующего апдейта.
	builtFor []int32
	byFolder map[int32][]int64
}

// waitForWallFoldersUpdate блокируется на одном updateChatFolders. Скелет один в
// один с waitForWallMessageUpdate: подписка одноразовая, каждый вызов tea.Cmd
// ждёт ровно один апдейт, поэтому обработчик в Update обязан вернуть новый вызов
// после разбора.
func (m Model) waitForWallFoldersUpdate() tea.Cmd {
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client := m.client
	if client == nil {
		return func() tea.Msg { return wallFoldersUpdateMsg{closed: true} }
	}
	ch := client.ChatFolderUpdates()
	if ch == nil {
		return func() tea.Msg { return wallFoldersUpdateMsg{closed: true} }
	}
	return func() tea.Msg {
		select {
		case update, ok := <-ch:
			if !ok {
				return wallFoldersUpdateMsg{closed: true}
			}
			folders, valid := auth.ParseChatFoldersUpdate(update)
			return wallFoldersUpdateMsg{folders: folders, valid: valid}
		case <-ctx.Done():
			return wallFoldersUpdateMsg{closed: true}
		}
	}
}

// wallFolderChatsCmd — состав каждой папки: id чатов, её состоящих.
//
// Принадлежность чата папке приходит из TDLib через СПИСОК чатов этой папки
// (chatListFolder), а не полем самого чата: в объекте chat папок нет, есть
// updateChatAddedToList/updateChatRemovedFromList с chat_list. Запрашивать по
// папке — ровно то, что уже делает лента tgcli (loadChatsCmd в
// internal/tui/model.go), только стене нужен не сам чат, а его id: берётся
// GetAllChatIDs, который не дёргает getChat на каждый чат (см. его комментарий).
//
// Горутина на папку, результаты по индексу — та же схема, что у loadWallMessages:
// общий срез из горутин без синхронизации записать нельзя.
//
// Сбой чтения одной папки молча оставляет её пустой: при отмеченной папке её чаты
// не покажутся, но это честнее, чем показать все чаты, будто человек ими не
// ограничивал. Отдельного сообщения об ошибке у стены нет (то же решение, что у
// applyWallSendMessage).
func (m Model) wallFolderChatsCmd(folders []auth.Folder) tea.Cmd {
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client := m.client
	if len(folders) == 0 {
		return nil
	}
	return func() tea.Msg {
		if client == nil {
			return wallFolderChatsMsg{builtFor: folderIDs(folders)}
		}
		byFolder := make(map[int32][]int64, len(folders))
		var mutex sync.Mutex
		var waitGroup sync.WaitGroup
		waitGroup.Add(len(folders))
		for _, folder := range folders {
			go func(folderID int32) {
				defer waitGroup.Done()
				chatList := map[string]interface{}{"@type": "chatListFolder", "chat_folder_id": folderID}
				ids, err := auth.GetAllChatIDs(ctx, client, chatList)
				if err != nil {
					return
				}
				mutex.Lock()
				defer mutex.Unlock()
				byFolder[folderID] = ids
			}(folder.ID)
		}
		waitGroup.Wait()
		return wallFolderChatsMsg{builtFor: folderIDs(folders), byFolder: byFolder}
	}
}

// applyWallFoldersUpdate — новый список папок на стене.
//
// Список сменился, а вместе с ним и состав: у переименованной папки тот же id, у
// удалённой папки id исчезает, и отметка папки, которой больше нет, фильтру
// ничего сказать не может. Поэтому отметки, которых нет в новом списке,
// отбрасываются, а отметки оставшихся сохраняются: иначе переименование папки
// стирало бы выбор человека, а её удаление — оставляло бы настройку, которая тихо
// ничего не ограничивает.
//
// Старая карта «чат → папки» сбрасывается целиком: у папок, которых в новом
// списке нет, данных больше не существует, и оставить их значило бы держать в
// фильтре папку, которой у человека уже нет. Актуальный состав придёт следом
// тем же запросом, что и всегда (см. wallFolderChatsCmd).
func (m Model) applyWallFoldersUpdate(msg wallFoldersUpdateMsg) Model {
	m.folders = msg.folders
	m.filter.folders = m.filter.keepKnownFolders(folderIDs(msg.folders))
	m.chatFolders = nil
	return m
}

// applyWallFolderChats — состав папок пришёл. Карта «чат → папки» пересобирается
// целиком, а не дописывается в старую: состав папки меняется через
// updateChatAddedToList/updateChatRemovedFromList, и дописывание оставило бы чат
// в папке, из которой его уже убрали.
func (m Model) applyWallFolderChats(msg wallFolderChatsMsg) Model {
	if !slices.Equal(msg.builtFor, folderIDs(m.folders)) {
		return m // пока летел запрос, список папок сменился — ответ устарел
	}
	ids := make(chatFolderIDs)
	for folderID, chatIDs := range msg.byFolder {
		for _, chatID := range chatIDs {
			ids.add(chatID, folderID)
		}
	}
	m.chatFolders = ids
	return m
}

// keepKnownFolders — отметки папок, оставшихся в списке. Отдельный метод, а не
// инлайновый цикл в applyWallFoldersUpdate: правило «отметка без папки мертва»
// должно проверяться само по себе, без модели стены вокруг.
//
// В результат попадают ТОЛЬКО отмеченные папки, без записей «здесь не
// отмечено»: пустая карта и карта с нулевыми значениями означают разное —
// первое «папками не ограничено», второе «показывать нечего».
func (f wallFilter) keepKnownFolders(known []int32) folderMarks {
	if len(f.folders) == 0 {
		return f.folders
	}
	kept := make(folderMarks, len(f.folders))
	for _, id := range known {
		if f.folders[id] {
			kept[id] = true
		}
	}
	return kept
}

// folderIDs — id папок в порядке, в каком их прислал TDLib. Порядок этот
// значим: он же порядок строк «Папки» в меню, то есть тот, который задал сам
// человек в Telegram.
func folderIDs(folders []auth.Folder) []int32 {
	ids := make([]int32, 0, len(folders))
	for _, folder := range folders {
		ids = append(ids, folder.ID)
	}
	return ids
}
