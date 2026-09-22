package tui

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"telecli/internal/auth"
	"telecli/internal/config"
	"telecli/internal/update"
)

const (
	chatsLimit      = 50
	messagesLimit   = 50
	foldersPaneW    = 18 // ширина панели папок
	chatsPaneW      = 30
	paneTitleHeight = 1 // одна строка над каждой панелью — не часть рамки, не часть скроллящегося контента viewport
	statusReserve   = 1 // строка статуса/ошибки внизу экрана
	// composeAreaHeight — высота textarea черновика в Insert-режиме (число
	// строк самого поля, без строки-подсказки под ним). Insert-режим
	// резервирует внизу composeAreaHeight+1 строк (черновик + подсказка),
	// остальные режимы — statusReserve; см. applyLayout.
	composeAreaHeight = 3

	// insertHint — подсказка под черновиком в Insert-режиме. Рендерится
	// отдельной строкой снизу (не дописывается в конец строки ввода), поэтому
	// в расчёте ширины composeInput не участвует — см. applyLayout.
	insertHint = " (Enter — отправить, ctrl+j — перенос строки, ctrl+e — редактор, Esc — отмена)"
)

// focus — какая из трёх панелей активна для клавиатурного ввода. Порядок
// значений = порядок панелей слева направо (папки | список чатов | лента
// сообщений), на него опирается цикл FocusNext (Tab).
type focus int

const (
	focusFolders focus = iota
	focusChats
	focusMessages
)

// appMode — vim-модальность ввода: Normal — навигация, Insert — набор текста
// черновика, Command — однострочная командная строка (`:q`/`:quit`).
// Visual — отдельный Roadmap-пункт, в это перечисление не входит.
type appMode int

const (
	modeNormal appMode = iota
	modeInsert
	modeCommand
	modeFile
)

// chatsLoadedMsg — результат первичной загрузки списка чатов (tea.Cmd
// выполняет блокирующий auth.GetChats в отдельной горутине bubbletea).
type chatsLoadedMsg struct {
	folderID int32
	chats    []auth.Chat
	err      error
}

// messagesLoadedMsg — результат загрузки ленты выбранного чата. chatID нужен
// в Update для отбрасывания устаревших ответов: пользователь мог успеть
// выбрать другой чат, пока запрос летел.
type messagesLoadedMsg struct {
	chatID   int64
	messages []auth.Message
	err      error
}

// sendMessageMsg — результат отправки сообщения через auth.SendMessage.
// chatID нужен в Update для отбрасывания устаревших ответов (тот же паттерн,
// что у messagesLoadedMsg).
type sendMessageMsg struct {
	chatID  int64
	message auth.Message
	err     error
}

// sendFileMsg — результат отправки файла (auth.SendFile), отдельный тип от
// sendMessageMsg: намеренно другое поведение после успеха — modeFile
// закрывается (возврат в modeNormal), в отличие от "липкого" modeInsert
// (см. 0022) — это одноразовое действие, не поток сообщений.
type sendFileMsg struct {
	chatID  int64
	message auth.Message
	err     error
}

// newMessageUpdateMsg — один апдейт из client.MessageUpdates(), уже
// распарсенный. closed == true означает, что канал закрылся (клиент
// остановлен/контекст отменён) — единственный случай, когда НЕ нужно
// переподписываться заново.
type newMessageUpdateMsg struct {
	chatID  int64
	message auth.Message
	closed  bool
}

// chatFoldersUpdateMsg — апдейт списка папок из client.ChatFolderUpdates().
// closed == true — канал закрылся (клиент остановлен/контекст отменён),
// единственный случай, когда переподписываться не нужно (тот же контракт,
// что у newMessageUpdateMsg).
type chatFoldersUpdateMsg struct {
	folders []auth.Folder
	closed  bool
}

// updateCheckMsg — результат проверки обновлений (update.CheckLatest).
// explicit — true, если вызвано командой :update (тогда результат ВСЕГДА
// показывается статусом — успех, "уже последняя" или ошибка); false — фоновая
// проверка при старте (тихий фейл: ошибка сети не должна дёргать
// пользователя, он её не просил).
type updateCheckMsg struct {
	release  update.Release
	err      error
	explicit bool
}

// Model — bubbletea-модель основного экрана: папки слева, список чатов
// выбранной папки в центре, лента сообщений выбранного чата справа. Ввод —
// vim-модальный, биндинги читаются из конфигурации (config.KeyBindings); из
// Insert-режима отправляются текстовые сообщения (auth.SendMessage) с
// возможностью редактирования черновика во внешнем $EDITOR (см. editor.go).
type Model struct {
	client auth.TDClientInterface
	ctx    context.Context

	folders          []auth.Folder
	folderCursor     int   // индекс в отображаемом списке: 0 = "Все чаты" (синтетический пункт), 1..N = folders[i-1]
	selectedFolderID int32 // 0 = "Все чаты"/Main, иначе id активной папки — тот же id, что у auth.Folder.ID

	chats      []auth.Chat
	chatCursor int

	messages      []auth.Message
	loadingMsgs   bool
	sendingMsg    bool
	sendingFile   bool
	displayedChat int64 // id чата, чья лента отображается/грузится
	focus         focus
	viewport      viewport.Model

	width, height int
	status        string

	version         string
	updateAvailable string // "" — обновление не найдено/не проверялось; иначе — тег новой версии

	mode         appMode
	keys         KeyMap
	settings     config.Settings
	composeInput textarea.Model
	commandInput textinput.Model
	fileInput    textinput.Model
}

// New создаёт модель с пустым viewport: до первого tea.WindowSizeMsg у нас нет
// размеров терминала, их пересчитываем в Update. Рамка viewport задаётся через
// Style — сам viewport учитывает её при расчёте полезной области. Поля ввода
// создаются расфокусированными и фокусируются при входе в соответствующий режим.
func New(client auth.TDClientInterface, ctx context.Context, keys config.KeyBindings, settings config.Settings, version string) Model {
	vp := viewport.New(0, 0)
	vp.Style = lipgloss.NewStyle().Border(lipgloss.RoundedBorder())

	commandInput := textinput.New()
	// ":" — фиксированный по вим-конвенции признак командной строки (см.
	// PLAN.md, архитектурные ограничения), отличающий её от строки черновика
	// в Insert-режиме (у той остаётся дефолтный "┃ " от textarea.New()).
	commandInput.Prompt = ":"

	fileInput := textinput.New()
	// Промпт однострочного поля пути к файлу (режим modeFile, ctrl+f) —
	// отдельный от ":" командной строки и от черновика Insert-режима.
	fileInput.Prompt = "Файл: "

	composeInput := textarea.New()
	// Номера строк — дефолт редактора кода, для короткого черновика чата не нужны.
	composeInput.ShowLineNumbers = false
	// Enter зарезервирован под отправку (перехватывается в Update раньше, чем
	// дойдёт до composeInput.Update), перенос строки — ctrl+j. Дефолтный
	// InsertNewline ("enter"/"ctrl+m") противоречил бы этому — перебиндиваем,
	// чтобы KeyMap компонента отражал реальное поведение.
	composeInput.KeyMap.InsertNewline.SetKeys("ctrl+j")

	return Model{
		client:       client,
		ctx:          ctx,
		version:      version,
		viewport:     vp,
		keys:         newKeyMap(keys),
		settings:     settings,
		composeInput: composeInput,
		commandInput: commandInput,
		fileInput:    fileInput,
	}
}

// applyLayout пересчитывает ширину/высоту панелей и полей ввода под текущие
// m.width/m.height/m.mode. Метод на указателе (не значение) — вызывается из
// Update() как m.applyLayout(), мутирует локальную копию m внутри Update.
// ОБЯЗАТЕЛЬНО вызывать после КАЖДОГО присваивания m.mode = ... (даже там, где
// бюджет фактически не меняется, например Command↔Normal) — единое правило
// проще поддерживать, чем помнить, в каких именно переходах высота меняется.
// Содержимое viewport (лента сообщений) сознательно НЕ трогает: оно зависит
// только от ширины, которая между режимами не меняется, — пересчёт содержимого
// остаётся в обработчике tea.WindowSizeMsg, чтобы вход/выход из Insert-режима
// не сбрасывал прокрутку уже открытой ленты.
func (m *Model) applyLayout() {
	bottomReserve := statusReserve
	if m.mode == modeInsert {
		bottomReserve = composeAreaHeight + 1
	}
	m.viewport.Width = max(0, m.width-foldersPaneW-chatsPaneW)
	m.viewport.Height = max(0, m.height-bottomReserve-paneTitleHeight)
	m.commandInput.Width = max(0, m.width-lipgloss.Width(m.commandInput.Prompt)-1)
	m.fileInput.Width = max(0, m.width-lipgloss.Width(m.fileInput.Prompt)-1)
	m.composeInput.SetWidth(max(0, m.width))
	m.composeInput.SetHeight(composeAreaHeight)
}

// Init запускает первичную загрузку списка чатов и подписку на входящие
// апдейты сообщений и папок. Сам Init/Update не блокируется: bubbletea
// исполняет возвращаемый tea.Cmd в отдельной горутине, вызов возвращается с
// готовым tea.Msg. waitForMessageUpdate и waitForChatFolders переподписываются
// из самого Update (см. комментарии у newMessageUpdateMsg/chatFoldersUpdateMsg).
func (m Model) Init() tea.Cmd {
	loadChats := m.loadChatsCmd(map[string]interface{}{"@type": "chatListMain"}, 0)
	return tea.Batch(loadChats, m.waitForMessageUpdate(), m.waitForChatFolders(), m.checkUpdateCmd(false))
}

// loadChatsCmd — tea.Cmd для загрузки списка чатов из указанного chat_list
// (сырой JSON TDLib ChatList — {"@type": "chatListMain"} или
// {"@type": "chatListFolder", "chat_folder_id": <id>}). Используется и при
// старте (Init), и при переключении папки (см. Select в focusFolders).
func (m Model) loadChatsCmd(chatList map[string]interface{}, folderID int32) tea.Cmd {
	ctx := m.ctx
	client := m.client
	return func() tea.Msg {
		chats, err := auth.GetChats(ctx, client, chatList, chatsLimit)
		return chatsLoadedMsg{folderID: folderID, chats: chats, err: err}
	}
}

// Update обрабатывает сообщения bubbletea. Блокирующие TDLib-запросы не
// выполняются здесь — только через tea.Cmd с замыканием, чтобы не держать
// событийный цикл.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.applyLayout()
		// Контент ленты "запечён" под ширину на момент последнего SetContent —
		// при ресайзе терминала, пока чат уже открыт, перерисовываем перенос под
		// новую ширину (позицию прокрутки не трогаем, это вне задачи).
		if len(m.messages) > 0 {
			contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
			m.viewport.SetContent(renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight))
		}
		return m, nil

	case chatsLoadedMsg:
		// Устаревший ответ (пользователь переключил папку, пока запрос летел)
		// молча отбрасываем, чтобы не затирать актуально отображаемый список
		// чатов уже выбранной папки — тот же класс гонки, что и у
		// messagesLoadedMsg.chatID выше.
		if msg.folderID != m.selectedFolderID {
			return m, nil
		}
		m.status = ""
		if msg.err != nil {
			m.status = fmt.Sprintf("Ошибка загрузки чатов: %v", msg.err)
			m.chats = nil
			return m, nil
		}
		m.chats = msg.chats
		return m, nil

	case messagesLoadedMsg:
		// Устаревший ответ (пользователь выбрал другой чат, пока запрос летел)
		// молча отбрасываем, чтобы не затирать актуально отображаемую ленту.
		if msg.chatID != m.displayedChat {
			return m, nil
		}
		m.loadingMsgs = false
		if msg.err != nil {
			m.status = fmt.Sprintf("Ошибка загрузки сообщений: %v", msg.err)
			return m, nil
		}
		m.status = ""
		m.messages = msg.messages
		contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
		m.viewport.SetContent(renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight))
		m.viewport.GotoBottom()
		return m, nil

	case sendMessageMsg:
		m.sendingMsg = false
		if msg.err != nil {
			m.status = fmt.Sprintf("Ошибка отправки: %v", msg.err)
			cmd := m.composeInput.Focus()
			return m, cmd
		}
		// Устаревший ответ невозможен по конструкции (Insert-режим заморожен на
		// время отправки, см. ниже, переключить чат нельзя) — проверка чисто
		// защитная, тот же паттерн, что у messagesLoadedMsg. Проверка hasMessage
		// обязательна: чат уже открыт (openChat, задача 0008), поэтому TDLib
		// присылает updateNewMessage и про наше же исходящее сообщение —
		// newMessageUpdateMsg может добавить его в ленту раньше или позже этого
		// обработчика; без дедупа сообщение реально показывалось дважды
		// (не двойная отправка на сервер — двойное отображение локально).
		if msg.chatID == m.displayedChat && !m.hasMessage(msg.message.ID) {
			m.messages = append(m.messages, msg.message)
			contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
			m.viewport.SetContent(renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight))
			m.viewport.GotoBottom()
		}
		m.status = ""
		m.composeInput.SetValue("")
		return m, nil

	case sendFileMsg:
		m.sendingFile = false
		if msg.err != nil {
			m.status = fmt.Sprintf("Ошибка отправки файла: %v", msg.err)
			cmd := m.fileInput.Focus()
			return m, cmd
		}
		if msg.chatID == m.displayedChat && !m.hasMessage(msg.message.ID) {
			m.messages = append(m.messages, msg.message)
			contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
			m.viewport.SetContent(renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight))
			m.viewport.GotoBottom()
		}
		m.status = ""
		m.fileInput.SetValue("")
		m.fileInput.Blur()
		m.mode = modeNormal
		m.applyLayout()
		return m, nil

	case newMessageUpdateMsg:
		if msg.closed {
			// Подписка естественно завершилась (клиент остановлен/контекст
			// отменён) — переподписываться не на что.
			return m, nil
		}
		if msg.chatID != 0 && msg.chatID == m.displayedChat && !m.hasMessage(msg.message.ID) {
			m.messages = append(m.messages, msg.message)
			contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
			m.viewport.SetContent(renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight))
			m.viewport.GotoBottom()
		}
		// Переподписка обязательна: tea.Cmd срабатывает ровно один раз за
		// вызов (см. комментарий у waitForMessageUpdate) — без неё после
		// первого же апдейта поток обрывается молча.
		return m, m.waitForMessageUpdate()

	case chatFoldersUpdateMsg:
		if msg.closed {
			return m, nil
		}
		m.folders = msg.folders
		if m.folderCursor > len(m.folders) {
			m.folderCursor = len(m.folders) // список папок сократился — не оставляем курсор за концом
		}
		return m, m.waitForChatFolders()

	case updateCheckMsg:
		if msg.err != nil {
			if msg.explicit {
				m.status = fmt.Sprintf("Не удалось проверить обновления: %v", msg.err)
			}
			return m, nil
		}
		if update.IsNewer(m.version, msg.release.TagName) {
			m.updateAvailable = msg.release.TagName
			if msg.explicit {
				m.status = fmt.Sprintf("Доступна версия %s: %s", msg.release.TagName, msg.release.HTMLURL)
			}
		} else if msg.explicit {
			m.status = fmt.Sprintf("Установлена последняя версия (%s)", m.version)
		}
		return m, nil

	case editorFinishedMsg:
		if msg.err != nil {
			if msg.path != "" {
				os.Remove(msg.path)
			}
			m.status = fmt.Sprintf("Ошибка редактора: %v", msg.err)
			cmd := m.composeInput.Focus()
			return m, cmd
		}
		text, readErr := readDraftTempFile(msg.path)
		os.Remove(msg.path) // ошибку удаления сознательно игнорируем — не критично для UX
		if readErr != nil {
			m.status = fmt.Sprintf("Не удалось прочитать черновик: %v", readErr)
			cmd := m.composeInput.Focus()
			return m, cmd
		}
		m.composeInput.SetValue(text)
		m.composeInput.CursorEnd()
		cmd := m.composeInput.Focus()
		return m, cmd

	case tea.KeyMsg:
		// ctrl+c — безусловный аварийный выход, вне зависимости от режима и от
		// конфигурируемых quit-клавиш: в Insert/Command можно случайно зажать
		// модификатор, пользователь не должен остаться запертым в программе.
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}

		switch m.mode {
		case modeCommand:
			switch msg.Type {
			case tea.KeyEsc:
				m.mode = modeNormal
				m.applyLayout()
				m.commandInput.Blur()
				return m, nil
			case tea.KeyEnter:
				cmdText := strings.TrimSpace(m.commandInput.Value())
				m.commandInput.SetValue("")
				m.commandInput.Blur()
				m.mode = modeNormal
				m.applyLayout()
				switch cmdText {
				case "q", "quit":
					return m, tea.Quit
				case "update":
					m.status = "Проверка обновлений…"
					return m, m.checkUpdateCmd(true)
				case "":
					return m, nil
				}
				m.status = fmt.Sprintf("Неизвестная команда: %s", cmdText)
				return m, nil
			}
			updated, cmd := m.commandInput.Update(msg)
			m.commandInput = updated
			return m, cmd

		case modeFile:
			// Пока идёт отправка файла — ввод "заморожен", та же защита от
			// потери данных, что у modeInsert/sendingMsg: без неё введённый
			// путь можно было бы перезаписать до прихода sendFileMsg.
			if m.sendingFile {
				return m, nil
			}
			switch msg.Type {
			case tea.KeyEsc:
				m.mode = modeNormal
				m.applyLayout()
				m.fileInput.Blur()
				return m, nil
			case tea.KeyEnter:
				path := strings.TrimSpace(m.fileInput.Value())
				if path == "" {
					m.fileInput.SetValue("")
					m.fileInput.Blur()
					m.mode = modeNormal
					m.applyLayout()
					return m, nil
				}
				m.sendingFile = true
				return m, m.sendFileCmd(m.displayedChat, path)
			}
			updated, cmd := m.fileInput.Update(msg)
			m.fileInput = updated
			return m, cmd

		case modeInsert:
			// Пока идёт отправка — Insert-режим "заморожен": ни один ввод не
			// пересылается в composeInput.Update. Без этого пользователь мог бы
			// напечатать что-то новое, пока предыдущий Send ещё летит, и итоговая
			// очистка composeInput при успехе стёрла бы этот новый, ещё не
			// отправленный текст — незаметная потеря данных, а не просто визуальный
			// баг. Простое решение вместо очереди/отмены: ничего не принимать,
			// пока не пришёл sendMessageMsg.
			if m.sendingMsg {
				return m, nil
			}
			switch msg.Type {
			case tea.KeyEsc:
				m.mode = modeNormal
				m.applyLayout()
				m.composeInput.Blur()
				return m, nil
			case tea.KeyEnter:
				text := strings.TrimSpace(m.composeInput.Value())
				if text == "" {
					m.composeInput.SetValue("")
					m.composeInput.Blur()
					m.mode = modeNormal
					m.applyLayout()
					return m, nil
				}
				if m.displayedChat == 0 {
					m.status = "Сначала выберите чат (Tab → список чатов → Enter)"
					return m, nil
				}
				m.sendingMsg = true
				return m, m.sendMessageCmd(m.displayedChat, text)
			}
			if key.Matches(msg, m.keys.OpenEditor) {
				return m, m.openEditorCmd()
			}
			updated, cmd := m.composeInput.Update(msg)
			m.composeInput = updated
			return m, cmd
		}

		// modeNormal.
		// Горячие клавиши Normal-режима должны работать независимо от активной
		// раскладки ОС (та же проблема, что решает vim :lmap): при русской
		// раскладке терминал шлёт кириллическую руну, транслитерируем её в
		// латинскую по физическому положению клавиши ДО первой проверки
		// key.Matches. К Insert/Command не применяется — там набор текста как есть.
		msg = translateLayout(msg)
		if key.Matches(msg, m.keys.Quit) {
			return m, tea.Quit
		}
		if key.Matches(msg, m.keys.EnterCommand) {
			m.mode = modeCommand
			m.applyLayout()
			cmd := m.commandInput.Focus()
			return m, cmd
		}
		if key.Matches(msg, m.keys.EnterInsert) {
			if m.displayedChat == 0 {
				m.status = "Сначала выберите чат (Tab → список чатов → Enter)"
				return m, nil
			}
			m.mode = modeInsert
			m.applyLayout()
			cmd := m.composeInput.Focus()
			return m, cmd
		}
		if key.Matches(msg, m.keys.SendFile) {
			if m.displayedChat == 0 {
				m.status = "Сначала выберите чат (Tab → список чатов → Enter)"
				return m, nil
			}
			m.mode = modeFile
			m.applyLayout()
			cmd := m.fileInput.Focus()
			return m, cmd
		}
		if key.Matches(msg, m.keys.FocusNext) {
			// С третьей панелью прежняя проверка len(m.chats) > 0 только усложняет
			// цикл без реальной пользы — пустые панели и так рендерят понятный
			// плейсхолдер ("Нет чатов"/"Выберите чат"), переключаться в них не вредно.
			switch m.focus {
			case focusFolders:
				m.focus = focusChats
			case focusChats:
				m.focus = focusMessages
			case focusMessages:
				m.focus = focusFolders
			}
			return m, nil
		}

		// Стрелки влево/вправо — направленное движение по панелям, в отличие от
		// циклического Tab. На границе (панель папок влево, панель сообщений
		// вправо) ничего не происходит — фокус остаётся как есть: у направленного
		// движения есть естественная граница, в отличие от «дай мне следующую
		// панель, неважно какую».
		if key.Matches(msg, m.keys.FocusLeft) {
			switch m.focus {
			case focusChats:
				m.focus = focusFolders
			case focusMessages:
				m.focus = focusChats
			}
			return m, nil
		}
		if key.Matches(msg, m.keys.FocusRight) {
			switch m.focus {
			case focusFolders:
				m.focus = focusChats
			case focusChats:
				m.focus = focusMessages
			}
			return m, nil
		}

		// Цифровые хоткеи прямого перехода на конкретную панель — действуют из
		// ЛЮБОГО состояния фокуса (независимо от того, где сейчас курсор),
		// в отличие от циклического Tab и направленных стрелок. Цифры верхнего
		// ряда — одни и те же физические клавиши на любой раскладке, поэтому
		// таблица транслитерации их не трогает и трогать не должна.
		if key.Matches(msg, m.keys.FocusPane1) {
			m.focus = focusFolders
			return m, nil
		}
		if key.Matches(msg, m.keys.FocusPane2) {
			m.focus = focusChats
			return m, nil
		}
		if key.Matches(msg, m.keys.FocusPane3) {
			m.focus = focusMessages
			return m, nil
		}

		if m.focus == focusFolders {
			switch {
			case key.Matches(msg, m.keys.MoveUp):
				if m.folderCursor > 0 {
					m.folderCursor--
				}
			case key.Matches(msg, m.keys.MoveDown):
				if m.folderCursor < len(m.folders) {
					m.folderCursor++
				}
			case key.Matches(msg, m.keys.Select):
				var chatList map[string]interface{}
				var selectedID int32
				if m.folderCursor == 0 {
					chatList = map[string]interface{}{"@type": "chatListMain"}
				} else {
					f := m.folders[m.folderCursor-1]
					chatList = map[string]interface{}{"@type": "chatListFolder", "chat_folder_id": f.ID}
					selectedID = f.ID
				}
				cmds := []tea.Cmd{m.loadChatsCmd(chatList, selectedID)}
				if m.displayedChat != 0 {
					cmds = append(cmds, m.closeChatCmd(m.displayedChat))
				}
				m.selectedFolderID = selectedID
				m.chats = nil
				m.chatCursor = 0
				m.messages = nil
				m.displayedChat = 0
				m.viewport.SetContent("")
				m.focus = focusChats
				return m, tea.Batch(cmds...)
			}
			return m, nil
		}

		if m.focus == focusChats {
			switch {
			case key.Matches(msg, m.keys.MoveUp):
				if m.chatCursor > 0 {
					m.chatCursor--
				}
			case key.Matches(msg, m.keys.MoveDown):
				if m.chatCursor < len(m.chats)-1 {
					m.chatCursor++
				}
			case key.Matches(msg, m.keys.Select):
				if m.chatCursor >= 0 && m.chatCursor < len(m.chats) {
					chat := m.chats[m.chatCursor]
					cmds := []tea.Cmd{m.selectChatCmd(chat.ID)}
					if m.displayedChat != 0 && m.displayedChat != chat.ID {
						cmds = append(cmds, m.closeChatCmd(m.displayedChat))
					}
					m.displayedChat = chat.ID
					m.loadingMsgs = true
					m.focus = focusMessages // переключение фокуса не ждёт ответа
					return m, tea.Batch(cmds...)
				}
			}
			return m, nil
		}

		// focusMessages.
		if key.Matches(msg, m.keys.Back) {
			m.focus = focusChats
			return m, nil
		}
		// up/down/pgup/pgdown и прочие клавиши прокрутки не перехватываем
		// вручную — дефолтные биндинги bubbles/viewport уже их обрабатывают.
		newVP, cmd := m.viewport.Update(msg)
		m.viewport = newVP
		return m, cmd
	}

	return m, nil
}

// hasMessage — защита от дублирования в ленте: разовый снимок истории
// (selectChatCmd/GetMessages) и постоянная подписка на live-апдейты
// (waitForMessageUpdate) — два независимых асинхронных потока, ничем не
// упорядоченных друг относительно друга. Если новое сообщение придёт по
// updateNewMessage раньше, чем завершится GetMessages, а сам GetMessages к
// этому моменту уже успеет захватить его в своём снимке (оно уже в локальном
// кэше TDLib) — messagesLoadedMsg заменит m.messages целиком, и последующий
// (более медленный) newMessageUpdateMsg для того же сообщения добавил бы его
// ещё раз, если бы не эта проверка по ID.
func (m Model) hasMessage(id int64) bool {
	for _, existing := range m.messages {
		if existing.ID == id {
			return true
		}
	}
	return false
}

// selectChatCmd открывает чат в TDLib и ТОЛЬКО ПОСЛЕ этого запрашивает историю
// — порядок важен (см. коммент в файле задачи): если запросить историю раньше
// openChat, TDLib может не успеть отдать весь локальный кэш (это и есть баг,
// который чинит эта задача). Ошибку OpenChat не считаем фатальной для загрузки
// истории — историю всё равно пробуем получить.
func (m Model) selectChatCmd(chatID int64) tea.Cmd {
	ctx := m.ctx
	client := m.client
	return func() tea.Msg {
		_ = auth.OpenChat(ctx, client, chatID)
		msgs, err := auth.GetMessages(ctx, client, chatID, messagesLimit)
		return messagesLoadedMsg{chatID: chatID, messages: msgs, err: err}
	}
}

// closeChatCmd закрывает чат в TDLib (снимает пометку "открыт" — останавливает
// доставку updateNewMessage для него после ухода). Ошибка не считаем
// фатальной — это состояние синхронизации, а не пользовательское действие.
func (m Model) closeChatCmd(chatID int64) tea.Cmd {
	ctx := m.ctx
	client := m.client
	return func() tea.Msg {
		_ = auth.CloseChat(ctx, client, chatID)
		return nil
	}
}

// waitForMessageUpdate — tea.Cmd, блокирующийся на ОДНОМ значении из
// client.MessageUpdates(). ВАЖНО: как любой tea.Cmd, срабатывает ровно один
// раз за вызов — bubbletea не вызывает его снова сам. Каждая ветка Update,
// обрабатывающая newMessageUpdateMsg, ОБЯЗАНА вернуть m.waitForMessageUpdate()
// заново (кроме случая msg.closed == true) — иначе после первого же пришедшего
// апдейта поток обрывается молча, без ошибки, без паники, просто перестаёт
// что-либо доставлять. Это не гипотетический риск, а конкретная причина,
// по которой эта задача расписана подробно, а не оставлена на общее решение.
func (m Model) waitForMessageUpdate() tea.Cmd {
	ch := m.client.MessageUpdates()
	ctx := m.ctx
	client := m.client
	return func() tea.Msg {
		select {
		case upd, ok := <-ch:
			if !ok {
				return newMessageUpdateMsg{closed: true}
			}
			chatID, parsed, valid := auth.ParseNewMessageUpdate(ctx, client, upd)
			if !valid {
				return newMessageUpdateMsg{} // chatID==0 — ниже безвредно проигнорируется, но переподписка продолжится
			}
			return newMessageUpdateMsg{chatID: chatID, message: parsed}
		case <-ctx.Done():
			return newMessageUpdateMsg{closed: true}
		}
	}
}

// waitForChatFolders — tea.Cmd, блокирующийся на ОДНОМ значении из
// client.ChatFolderUpdates(). Срабатывает один раз за вызов — ОБЯЗАТЕЛЬНО
// переподписываться в обработчике (см. комментарий у waitForMessageUpdate,
// тот же класс ловушки: без переподписки поток апдейтов обрывается молча).
func (m Model) waitForChatFolders() tea.Cmd {
	ch := m.client.ChatFolderUpdates()
	ctx := m.ctx
	return func() tea.Msg {
		select {
		case upd, ok := <-ch:
			if !ok {
				return chatFoldersUpdateMsg{closed: true}
			}
			folders, valid := auth.ParseChatFoldersUpdate(upd)
			if !valid {
				return chatFoldersUpdateMsg{} // безвредно проигнорируется, но переподписка продолжится
			}
			return chatFoldersUpdateMsg{folders: folders}
		case <-ctx.Done():
			return chatFoldersUpdateMsg{closed: true}
		}
	}
}

// sendMessageCmd возвращает tea.Cmd для отправки текста в чат. Контекст и
// клиент захватываются в замыкание в момент создания команды — та же
// схема, что у loadMessagesCmd.
func (m Model) sendMessageCmd(chatID int64, text string) tea.Cmd {
	ctx := m.ctx
	client := m.client
	return func() tea.Msg {
		msg, err := auth.SendMessage(ctx, client, chatID, text)
		return sendMessageMsg{chatID: chatID, message: msg, err: err}
	}
}

func (m Model) sendFileCmd(chatID int64, path string) tea.Cmd {
	ctx := m.ctx
	client := m.client
	return func() tea.Msg {
		msg, err := auth.SendFile(ctx, client, chatID, path, "")
		return sendFileMsg{chatID: chatID, message: msg, err: err}
	}
}

// checkUpdateCmd — одноразовая (без переподписки, в отличие от
// waitForMessageUpdate/waitForChatFolders) проверка обновлений. Таймаут 5
// секунд — не должен ощутимо блокировать закрытие приложения/зависать при
// недоступном GitHub (см. update.CheckLatest).
func (m Model) checkUpdateCmd(explicit bool) tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		rel, err := update.CheckLatest(ctx, http.DefaultClient, 5*time.Second)
		return updateCheckMsg{release: rel, err: err, explicit: explicit}
	}
}

// currentChatTitle — название чата, открытого в msgPane, для заголовка панели.
// m.displayedChat всегда либо 0 (ничего не открыто), либо id чата из ТЕКУЩЕГО
// m.chats — переключение папки сбрасывает displayedChat в 0 ДО того, как можно
// выбрать чат из другой папки (см. обработчик Select в focusFolders), так что
// рассинхронизации с m.chats из другой папки быть не может.
func (m Model) currentChatTitle() string {
	if m.displayedChat == 0 {
		return "Сообщения"
	}
	for _, c := range m.chats {
		if c.ID == m.displayedChat {
			return c.Title
		}
	}
	return "Сообщения"
}

// View собирает картинку экрана из трёх вертикальных панелей (папки | список
// чатов | лента сообщений) и нижней области (её высота зависит от режима:
// composeAreaHeight+1 строк в Insert-режиме, statusReserve в остальных — см.
// applyLayout). Над каждым рядом панелей — строка-заголовок панели (paneTitle).
func (m Model) View() string {
	titles := lipgloss.JoinHorizontal(lipgloss.Top,
		paneTitle(foldersPaneW, 1, "Папки", m.focus == focusFolders),
		paneTitle(chatsPaneW, 2, "Чаты", m.focus == focusChats),
		paneTitle(m.viewport.Width, 3, m.currentChatTitle(), m.focus == focusMessages),
	)
	panes := lipgloss.JoinHorizontal(lipgloss.Top, m.foldersPane(), m.chatPane(), m.msgPane())
	body := titles + "\n" + panes + "\n" + m.bottomLine()
	return body
}

// bottomLine — нижняя область зарезервированной высоты: командная строка,
// многострочное поле черновика с подсказкой или статус/индикатор режима.
func (m Model) bottomLine() string {
	switch m.mode {
	case modeCommand:
		return m.commandInput.View()
	case modeFile:
		hint := ""
		if m.sendingFile {
			hint = " (отправка…)"
		}
		return m.fileInput.View() + hint
	case modeInsert:
		hint := insertHint
		if m.sendingMsg {
			hint = " (отправка…)"
		}
		return m.composeInput.View() + "\n" + lipgloss.NewStyle().Faint(true).Render(hint)
	default:
		if m.status != "" {
			return lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(m.status)
		}
		pill := lipgloss.NewStyle().Background(activeBorderColor).Foreground(pillTextColor).
			Bold(true).Padding(0, 1).Render("NORMAL")
		hint := lipgloss.NewStyle().Faint(true).
			Render(" tab — переключить панель · ←/→ — фокус влево/вправо · i — ввод · : — команда")
		left := pill + hint

		versionText := m.version
		if m.updateAvailable != "" {
			versionText = fmt.Sprintf("%s → %s (:update)", m.version, m.updateAvailable)
		}
		versionRendered := lipgloss.NewStyle().Faint(true).Render(versionText)

		pad := m.width - lipgloss.Width(left) - lipgloss.Width(versionRendered)
		if pad < 1 {
			// Не помещается рядом с версией на узком терминале — показываем
			// только левую часть (пилюля+подсказка), не ломаем раскладку.
			return left
		}
		return left + strings.Repeat(" ", pad) + versionRendered
	}
}

// foldersPane — панель слева: "Все чаты" (синтетический пункт, всегда первый)
// + m.folders. Курсор — треугольник-маркер "▸" слева + жирное название (по
// правке человека, без цветной пилюли — так папки отличаются от подсветки
// курсора в списке чатов, см. chatPane/chatSelectionColor). У строк без
// курсора — те же 2 колонки заняты пробелами, чтобы текст не "прыгал" по
// горизонтали при переключении курсора.
func (m Model) foldersPane() string {
	var sb strings.Builder
	contentW := foldersPaneW - 2 - 2*panePaddingH // минус рамка (2) и паддинг (2*panePaddingH), тот же приём, что в chatPane
	renderRow := func(idx int, label string) {
		prefix := "  "
		style := lipgloss.NewStyle()
		if idx == m.folderCursor {
			prefix = "▸ "
			style = style.Bold(true)
		}
		textW := contentW - runewidth.StringWidth(prefix)
		content := prefix + runewidth.FillRight(runewidth.Truncate(label, textW, "…"), textW)
		sb.WriteString(style.Width(contentW).Render(content))
		sb.WriteString("\n")
	}
	renderRow(0, "Все чаты")
	for i, f := range m.folders {
		name := f.Name
		if name == "" {
			name = fmt.Sprintf("Папка #%d", f.ID)
		}
		renderRow(i+1, name)
	}
	return paneBox(foldersPaneW, m.viewport.Height, strings.TrimRight(sb.String(), "\n"), m.focus == focusFolders)
}

func (m Model) chatPane() string {
	var sb strings.Builder
	contentW := chatsPaneW - 2 - 2*panePaddingH // минус рамка (2) и паддинг (2*panePaddingH)
	for i, chat := range m.chats {
		line := lipgloss.NewStyle().
			Width(contentW).
			Render(runewidth.FillRight(runewidth.Truncate(chat.Title, contentW, "…"), contentW))
		if i == m.chatCursor {
			line = lipgloss.NewStyle().Background(chatSelectionColor).Foreground(pillTextColor).Width(contentW).Render(line)
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	if len(m.chats) == 0 {
		sb.WriteString("Нет чатов")
	}
	return paneBox(chatsPaneW, m.viewport.Height, sb.String(), m.focus == focusChats)
}

func (m Model) msgPane() string {
	switch {
	case m.loadingMsgs:
		return paneBox(m.viewport.Width, m.viewport.Height, "Загрузка сообщений…", m.focus == focusMessages)
	case len(m.messages) == 0:
		return paneBox(m.viewport.Width, m.viewport.Height, "Выберите чат и нажмите Enter", m.focus == focusMessages)
	default:
		// bubbles/viewport рисует свою рамку сам через поле Style — цвет фокуса
		// выставляем перед View(). Это локальная копия Model (value-receiver),
		// поле ctx не персистится за пределы msgPane — так же, как остальной код.
		m.viewport.Style = paneBorderStyle(m.focus == focusMessages)
		return m.viewport.View()
	}
}

// renderMessageCard рисует одно сообщение как отдельную рамку (одинарная
// скруглённая — не путать с двойной рамкой активной панели из 0020),
// приглушённого цвета отправителя; имя+время встроены прямо в верхнюю линию
// рамки, как в старых BBS-программах. width — ПОЛНАЯ ширина карточки вместе
// с рамкой (тот же принцип, что у paneBox). alignRight — рисовать метку
// (время+имя) у правого края верхней рамки, иначе — метку (имя+время) у
// левого края.
func renderMessageCard(msg auth.Message, width int, alignRight bool) string {
	b := lipgloss.RoundedBorder()

	bodyColor := messageColor(msg.IsOutgoing)
	sender := msg.SenderName
	if sender == "" {
		sender = "?"
	}
	var nameCol, borderCol lipgloss.Color
	if msg.IsOutgoing {
		nameCol = ownColor
		borderCol = ownBorderColor
	} else {
		nameCol = nickColor(sender)
		borderCol = nickBorderColor(sender)
	}

	borderStyle := lipgloss.NewStyle().Foreground(borderCol)
	// Обычное (не жирное) начертание — по правке человека, "тонкий" шрифт
	// имени; размер шрифта терминал не даёт менять посимвольно (настройка
	// эмулятора, не приложения) — Bold(false) это ближайшее достижимое.
	nameStyle := lipgloss.NewStyle().Foreground(nameCol)
	timeText := time.Unix(msg.Date, 0).Local().Format("15:04")

	horizontalSpan := max(0, width-2) // без двух угловых символов

	// gap/fixedW считаем ДО решения "есть ли место для метки" — порог должен
	// зависеть от реальной фиксированной ширины (время+отступ), а не быть
	// угаданным числом: иначе (ревью-находка) при horizontalSpan, которого
	// хватает только впритык, maxNameW уходил в отрицательные значения,
	// max(1, maxNameW) искусственно возвращал 1 символ имени вместо 0 — и
	// итоговая метка вылезала за пределы horizontalSpan на 1+ колонку (та же
	// природа бага, что уже поймана в textWidth выше, для другой переменной).
	// minSpanForLabel — минимум, при котором после вычитания fixedW и 2 тире
	// по краям гарантированно остаётся ХОТЯ БЫ 1 колонка на имя.
	gap := "  "
	fixedW := lipgloss.Width(timeText) + lipgloss.Width(gap)
	minSpanForLabel := fixedW + 3 // +2 тире по краям, +1 минимум на имя

	var top string
	if horizontalSpan < minSpanForLabel {
		// Слишком узко для метки — пустая верхняя линия без имени/времени.
		top = borderStyle.Render(b.TopLeft + strings.Repeat(b.Top, horizontalSpan) + b.TopRight)
	} else {
		maxNameW := horizontalSpan - 2 - fixedW // -2: минимум по одному тире с каждого края метки; гарантированно >= 1 по построению minSpanForLabel
		name := runewidth.Truncate(sender, maxNameW, "…")

		var label string
		if alignRight {
			label = timeStyle.Render(timeText) + gap + nameStyle.Render(name)
		} else {
			label = nameStyle.Render(name) + gap + timeStyle.Render(timeText)
		}
		labelW := lipgloss.Width(label)
		dashesTotal := max(0, horizontalSpan-labelW)

		if alignRight {
			rightDashes := 1
			leftDashes := max(0, dashesTotal-rightDashes)
			top = borderStyle.Render(b.TopLeft+strings.Repeat(b.Top, leftDashes)) +
				label + borderStyle.Render(strings.Repeat(b.Top, rightDashes)+b.TopRight)
		} else {
			leftDashes := 1
			rightDashes := max(0, dashesTotal-leftDashes)
			top = borderStyle.Render(b.TopLeft+strings.Repeat(b.Top, leftDashes)) +
				label + borderStyle.Render(strings.Repeat(b.Top, rightDashes)+b.TopRight)
		}
	}

	// pad — отступ в 1 пробел с каждой стороны текста, если для него есть
	// место; сумма border+pad+textWidth+pad+border всегда равна ИМЕННО width.
	// textWidth НИКОГДА не бывает 0 — проверено эмпирически (тот же класс
	// сюрприза lipgloss, что уже ловили в 0004/0005/0013/0015):
	// lipgloss.NewStyle().Width(0).Render(...) трактует 0 как "без
	// ограничения" (не переносит и не возвращает пустую строку), а не как
	// "нулевая ширина" — значит, при textWidth=0 тело сообщения рендерилось
	// бы БЕЗ переноса на произвольную длину и ломало бы инвариант ширины
	// карточки. Поэтому padW включается только тогда, когда после него
	// остаётся МИНИМУМ 1 колонка на текст.
	contentSlot := max(0, width-2)
	padW := 0
	if contentSlot >= 3 { // 1 колонка тексту + по 1 пробелу с каждой стороны
		padW = 1
	}
	pad := strings.Repeat(" ", padW)
	textWidth := max(1, contentSlot-2*padW)
	bodyRendered := lipgloss.NewStyle().Foreground(bodyColor).Width(textWidth).Render(msg.Text)

	var sb strings.Builder
	sb.WriteString(top)
	sb.WriteString("\n")
	for _, line := range strings.Split(bodyRendered, "\n") {
		sb.WriteString(borderStyle.Render(b.Left) + pad + line + pad + borderStyle.Render(b.Right))
		sb.WriteString("\n")
	}
	sb.WriteString(borderStyle.Render(b.BottomLeft + strings.Repeat(b.Bottom, horizontalSpan) + b.BottomRight))
	return sb.String()
}

// renderMessages превращает загруженные сообщения в многострочный текст
// ленты — каждое сообщение отдельной рамкой (renderMessageCard). width —
// содержательная ширина ленты (без рамки viewport), при width <= 0 —
// прежнее (до этой задачи) плоское поведение без рамок и переноса, только
// имя+время строкой и текст под ней — тот же формат, что был всегда, нужен
// как деградация на слишком узких терминалах (см.
// TestRenderMessagesZeroWidthDoesNotWrap, не меняй смысл этого теста).
// alignOwnRight — сужать и прижимать вправо мои
// карточки (и симметрично сужать чужие, оставляя их у левого края) — см.
// Settings.AlignOwnRight.
func renderMessages(msgs []auth.Message, width int, alignOwnRight bool) string {
	if width <= 0 {
		var sb strings.Builder
		for _, msg := range msgs {
			sender := msg.SenderName
			if sender == "" {
				sender = "?"
			}
			color := messageColor(msg.IsOutgoing)
			nameStyle := lipgloss.NewStyle().Foreground(color)
			header := nameStyle.Render(sender) + "  " + timeStyle.Render(time.Unix(msg.Date, 0).Local().Format("15:04"))
			fmt.Fprintf(&sb, "%s\n%s\n\n", header, lipgloss.NewStyle().Foreground(color).Render(msg.Text))
		}
		return strings.TrimRight(sb.String(), "\n")
	}

	var sb strings.Builder
	for _, msg := range msgs {
		cardWidth := width
		rightAlign := false
		if alignOwnRight {
			// min(width, ...) — минимум 20 колонок карточке ПРИ НАЛИЧИИ места,
			// но не шире самой ленты: на узком терминале (width < 20*4/3)
			// max(20, width*3/4) давал бы cardWidth > width — карточка
			// вылезала бы за пределы ленты (тот же класс бага ширины, что
			// уже ловили 4 раза в этом проекте, здесь — правка ревью).
			cardWidth = min(width, max(20, width*3/4))
			rightAlign = msg.IsOutgoing
		}
		card := renderMessageCard(msg, cardWidth, rightAlign)
		if rightAlign {
			pad := strings.Repeat(" ", max(0, width-cardWidth))
			lines := strings.Split(card, "\n")
			for i, l := range lines {
				lines[i] = pad + l
			}
			card = strings.Join(lines, "\n")
		}
		sb.WriteString(card)
		sb.WriteString("\n\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// spaceOutRunes вставляет один пробел между каждой парой рун ("ПАПКИ" →
// "П А П К И") — классический приём "разрядки" в ретро-консольных
// интерфейсах, единственный доступный способ имитировать "пиксельный" шрифт
// без реального контроля над шрифтом терминала (согласовано с человеком).
// budget — доступная ширина В КОЛОНКАХ для результата.
//
// Резервирует колонку под "…" ДО заполнения, а не постфактум: жадное
// заполнение всего budget с последующей попыткой "если после этого ещё есть
// место — допишем …" ломается ровно на границе (ревью-находка) — если
// разрядка вплотную исчерпывает budget, для "…" уже не остаётся ни одной
// колонки, хотя обрезка произошла. Поэтому сперва считаем полную ширину без
// обрезки: если она укладывается в budget — возвращаем как есть; если нет —
// заполняем ТОЛЬКО budget-1 колонку и добавляем "…" — так место под "…"
// гарантировано всегда, когда обрезка вообще происходит.
func spaceOutRunes(s string, budget int) string {
	runes := []rune(s)

	full := 0
	for i, r := range runes {
		if i > 0 {
			full++ // разделяющий пробел
		}
		full += runewidth.RuneWidth(r)
	}
	if full <= budget {
		var b strings.Builder
		for i, r := range runes {
			if i > 0 {
				b.WriteByte(' ')
			}
			b.WriteRune(r)
		}
		return b.String()
	}

	ellipsisBudget := budget - 1
	var b strings.Builder
	width := 0
	for i, r := range runes {
		rw := runewidth.RuneWidth(r)
		sep := 0
		if i > 0 {
			sep = 1
		}
		if width+sep+rw > ellipsisBudget {
			break
		}
		if sep == 1 {
			b.WriteByte(' ')
			width++
		}
		b.WriteRune(r)
		width += rw
	}
	if ellipsisBudget >= 0 {
		b.WriteString("…")
	}
	return b.String()
}

// paneTitle — заголовочная строка НАД панелью, той же ширины, что и сама
// панель целиком (см. комментарий у paneBox — тот же класс ширины). num —
// номер панели для хоткея прямого перехода (см. FocusPane1/2/3), показывается
// как "[N] " перед названием. Название — ВСЕМИ КАПС с разрядкой между буквами
// (spaceOutRunes) — реального "пиксельного" шрифта терминал не даёт, это
// имитация в пределах одной строки, ширина/высота панелей не меняются.
func paneTitle(width, num int, text string, focused bool) string {
	color := inactiveBorderColor
	if focused {
		color = activeBorderColor
	}
	prefix := fmt.Sprintf("[%d] ", num)
	budget := max(0, width-runewidth.StringWidth(prefix))
	spaced := spaceOutRunes(strings.ToUpper(text), budget)
	padded := runewidth.FillRight(prefix+spaced, width)
	return lipgloss.NewStyle().Bold(true).Foreground(color).Width(width).Render(padded)
}

// paneBox — общий стиль панели с рамкой, синхронизирован со стилем viewport.
// width/height — ПОЛНЫЕ размеры блока вместе с рамкой и паддингом (так же
// трактует Width свой viewport.Model — см. bubbles/viewport.View(): контент
// заранее ужимается до w-Style.GetHorizontalFrameSize(), а сам Style
// рендерится через Render() с UnsetWidth()/UnsetHeight(), то есть рамка И
// паддинг у viewport добавляются ПОВЕРХ уже готового по размеру контента).
//
// У paneBox контент строится по-другому (уже собран строкой снаружи и
// передаётся в style.Width(n).Render(...) напрямую, БЕЗ Unset) — а для
// такого вызова "сырой" lipgloss.Style ведёт себя иначе, чем можно ожидать
// по аналогии с viewport (проверено эмпирически, см. REPORT.md — правка на
// внутренний отступ панелей): .Width(n) — это ширина СОДЕРЖИМОГО ВМЕСТЕ С
// ПАДДИНГОМ (паддинг расходует часть n), а рамка добавляется ДОПОЛНИТЕЛЬНО
// поверх n. Поэтому вычитать нужно ИМЕННО GetHorizontalBorderSize()/
// GetVerticalBorderSize() (только рамка), а НЕ GetHorizontalFrameSize()/
// GetVerticalFrameSize() (рамка+паддинг вместе) — последнее вычло бы паддинг
// дважды и панель оказалась бы у́же/ниже запрошенного width/height. При
// paneBorderStyle без паддинга (до этой правки) оба геттера совпадали, поэтому
// разница не проявлялась. BorderForeground (цвет рамки активной панели) на
// геометрию не влияет — меняет только цвет символов рамки.
func paneBox(width, height int, content string, focused bool) string {
	style := paneBorderStyle(focused)
	return style.
		Width(max(0, width-style.GetHorizontalBorderSize())).
		Height(max(0, height-style.GetVerticalBorderSize())).
		Render(content)
}
