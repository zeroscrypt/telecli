package tui

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
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
	// composeAreaHeight — МИНИМАЛЬНАЯ высота textarea черновика в
	// Insert-режиме (число строк самого поля, без строки-подсказки под ним).
	// Insert-режим резервирует внизу m.composeInput.Height()+1 строк
	// (черновик + подсказка), остальные режимы — statusReserve; см.
	// applyLayout. Фактическая высота растёт вместе с числом строк черновика
	// (см. syncComposeHeight) — по правке человека: поле ввода должно
	// "подниматься" при добавлении строк, поднимая вместе с собой ленту
	// сообщений, а не резать черновик своим внутренним скроллом на фиксированной
	// высоте.
	composeAreaHeight = 3
	// composeAreaMaxHeight — потолок роста черновика: без него длинный
	// черновик мог бы съесть весь экран, не оставив места ленте сообщений.
	composeAreaMaxHeight = 8

	// insertHint — подсказка под черновиком в Insert-режиме. Рендерится
	// отдельной строкой снизу (не дописывается в конец строки ввода), поэтому
	// в расчёте ширины composeInput не участвует — см. applyLayout.
	insertHint = " (Enter — отправить, ctrl+j — перенос строки, Esc — отмена)"
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
	modeSearch
	// modeHelp — полноэкранный оверлей "о программе" (хоткей ShowHelp,
	// по умолчанию "t"), см. View/helpScreen. Из Normal, закрывается назад в
	// Normal по Esc или повторному ShowHelp — тот же принцип toggle, что и у
	// остальных модальных оверлеев (modeCommand/modeSearch).
	modeHelp
	// modeConfirmDelete — двухшаговое подтверждение удаления/покидания чата
	// (hotkey DeleteChat, по умолчанию "d"). Группу/канал можно только покинуть
	// (один шаг подтверждения), личный/секретный чат — удалить, с выбором
	// revoke на втором шаге ("также у собеседника?"). Только из Normal.
	modeConfirmDelete
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

// searchResultMsg — результат поиска SearchAll по запросу из modeSearch.
// Возвращается из searchCmd; обработчик переводит m.searchActive=true и
// показывает результаты в панели чатов (см. п.3/п.4 файла задачи).
type searchResultMsg struct {
	results auth.SearchResults
	err     error
}

// chatActionDoneMsg — результат leaveChat/deleteChatHistory (оба ведут к
// одному и тому же локальному эффекту: чат пропадает из списка).
type chatActionDoneMsg struct {
	chatID int64
	err    error
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

// chatReadInboxUpdateMsg — один апдейт из client.ChatReadInboxUpdates(),
// новое количество непрочитанных В КОНКРЕТНОМ чате. valid — апдейт успешно
// распознан парсером; valid == false при нераспознанном апдейте (тогда апдейт
// игнорируется ниже, но переподписка обязательна). closed == true —
// канал закрылся (клиент остановлен/контекст отменён), единственный случай,
// когда переподписываться не нужно.
type chatReadInboxUpdateMsg struct {
	chatID      int64
	unreadCount int32
	valid       bool
	closed      bool
}

// unreadCountUpdateMsg — один апдейт из client.UnreadCountUpdates(),
// СУММА НЕПРОЧИТАННЫХ СООБЩЕНИЙ по ЦЕЛОМУ списку чатов. В Update()
// применяется ТОЛЬКО для folderID == 0 ("Все чаты"/Main) — для остальных
// папок Telegram показывает другую метрику (число чатов, не сумму
// сообщений), см. unreadChatCountUpdateMsg. valid/closed — тот же
// контракт, что у chatReadInboxUpdateMsg.
type unreadCountUpdateMsg struct {
	folderID    int32
	unreadCount int32
	valid       bool
	closed      bool
}

// unreadChatCountUpdateMsg — один апдейт из client.UnreadChatCountUpdates(),
// ЧИСЛО ЧАТОВ (не сообщений) с непрочитанным по ЦЕЛОМУ списку. В Update()
// применяется ТОЛЬКО для folderID != 0 (конкретные папки) — "Все чаты"
// использует unreadCountUpdateMsg (сумму сообщений), не эту метрику; живая
// проверка человека: Telegram считает у папки именно число чатов, включая
// замьюченные (12 замьюченных + 1 незамьюченный чат с непрочитанным = "13").
// valid/closed — тот же контракт, что у остальных апдейтов этого файла.
type unreadChatCountUpdateMsg struct {
	folderID  int32
	chatCount int32
	valid     bool
	closed    bool
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

// updateInstallMsg — результат установки обновления (update.InstallBinary).
type updateInstallMsg struct {
	version string // тег версии, на которую обновились (для статуса при успехе)
	err     error
}

// Model — bubbletea-модель основного экрана: папки слева, список чатов
// выбранной папки в центре, лента сообщений выбранного чата справа. Ввод —
// vim-модальный, биндинги читаются из конфигурации (config.KeyBindings); из
// Insert-режима отправляются текстовые сообщения (auth.SendMessage).
type Model struct {
	client auth.TDClientInterface
	ctx    context.Context

	folders          []auth.Folder
	folderCursor     int   // индекс в отображаемом списке: 0 = "Все чаты" (синтетический пункт), 1..N = folders[i-1]
	selectedFolderID int32 // 0 = "Все чаты"/Main, иначе id активной папки — тот же id, что у auth.Folder.ID

	// folderUnread — количество непрочитанных по папкам, ключ — тот же id, что
	// у auth.Folder.ID (0 — синтетическая "Все чаты"/chatListMain). ОТДЕЛЬНО от
	// []auth.Folder: сам срез folders целиком перезаписывается на каждый
	// chatFoldersUpdateMsg (см. case ниже), а счётчики приходят независимым
	// каналом (updateUnreadMessageCount) и должны переживать это
	// перезаписывание — карта хранится отдельно и НЕ трогается в обработчике
	// chatFoldersUpdateMsg.
	folderUnread map[int32]int32

	chats      []auth.Chat
	chatCursor int

	messages      []auth.Message
	loadingMsgs   bool
	sendingMsg    bool
	sendingFile   bool
	displayedChat int64 // id чата, чья лента отображается/грузится

	messageCursor int // индекс в m.messages: какое сообщение выбрано в focusMessages

	focus    focus
	viewport viewport.Model
	// paneRowHeight — общий бюджет высоты строк для ВСЕХ ТРЁХ панелей
	// (папки/чаты/сообщения), одинаковый для всех. m.viewport.Height —
	// ОТДЕЛЬНОЕ поле: высота именно вьюпорта сообщений, обычно равна
	// paneRowHeight, но в Insert-режиме МЕНЬШЕ на высоту карточки
	// черновика (см. applyLayout/composeCardHeight) — черновик встроен
	// внутрь панели сообщений, а не занимает отдельную область под всеми
	// панелями (по правке человека). foldersPane/chatPane/listContentRows
	// используют paneRowHeight (они не сжимаются в Insert-режиме).
	paneRowHeight int

	width, height int
	status        string

	version          string
	updateAvailable  string // "" — обновление не найдено/не проверялось; иначе — тег новой версии
	installingUpdate bool   // защита от повторного запуска установки

	mode         appMode
	keys         KeyMap
	settings     config.Settings
	composeInput textarea.Model
	commandInput textinput.Model
	fileInput    textinput.Model
	searchInput  textinput.Model

	searchActive  bool // true — chatPane показывает m.searchResults вместо m.chats
	searchResults auth.SearchResults
	searchCursor  int  // индекс в объединённом списке (сначала Chats, потом Contacts — см. п.3)
	searchingNow  bool // идёт запрос SearchAll — тот же паттерн заморозки, что sendingMsg/sendingFile

	// Состояние двухшагового подтверждения удаления/покидания чата (modeConfirmDelete).
	// Сохраняем chatID (а не индекс в m.chats): между шагами спиcок чатов не
	// перезагружается (никакой сетевой активности), так что id надёжнее индекса.
	deleteTargetChatID int64
	deleteTargetTitle  string
	deleteTargetGroup  bool // true — группа/канал (leaveChat), false — личный чат (deleteChatHistory)
	deleteStep         int  // 0 — "покинуть/удалить?", 1 — "также у собеседника?" (только для НЕ группы)
}

// New создаёт модель с пустым viewport: до первого tea.WindowSizeMsg у нас нет
// размеров терминала, их пересчитываем в Update. Рамка viewport задаётся через
// Style — сам viewport учитывает её при расчёте полезной области. Поля ввода
// создаются расфокусированными и фокусируются при входе в соответствующий режим.
func New(client auth.TDClientInterface, ctx context.Context, keys config.KeyBindings, settings config.Settings, version string) Model {
	vp := viewport.New(0, 0)
	// paneBorderStyle(false), не голый Border(...): m.viewport.Style должен
	// нести ПРАВИЛЬНЫЙ вертикальный "frame size" (рамка+паддинг) с самого
	// начала, а не только после первого рендера с сообщениями (msgPane
	// переустанавливает Style лишь при len(m.messages)>0). До этой правки
	// GotoBottom() при самой первой загрузке сообщений считал позицию по
	// заниженной (без паддинга) рамке, YOffset "запоминал" неверный сдвиг —
	// низ последней карточки обрезался. GetVerticalBorderSize()/паддинг
	// одинаковы у focused/unfocused (см. paneBorderStyle) — цвет рамки здесь
	// не важен, msgPane() всё равно переустановит его перед каждым View().
	vp.Style = paneBorderStyle(false)

	commandInput := textinput.New()
	// ":" — фиксированный по вим-конвенции признак командной строки (см.
	// PLAN.md, архитектурные ограничения), отличающий её от строки черновика
	// в Insert-режиме (у той остаётся дефолтный "┃ " от textarea.New()).
	commandInput.Prompt = ":"

	fileInput := textinput.New()
	// Промпт однострочного поля пути к файлу (режим modeFile, ctrl+f) —
	// отдельный от ":" командной строки и от черновика Insert-режима.
	fileInput.Prompt = "Файл: "

	searchInput := textinput.New()
	searchInput.Prompt = "Поиск: "

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
		searchInput:  searchInput,
		folderUnread: make(map[int32]int32),
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
	// bottomReserve больше не растёт в Insert-режиме — черновик по правке
	// человека встроен ВНУТРЬ панели сообщений (см. m.paneRowHeight выше,
	// composeCardHeight, msgPane), а не занимает отдельную область под
	// всеми тремя панелями. Одна строка снизу — статус/подсказка режима,
	// как и во всех остальных режимах.
	m.paneRowHeight = max(0, m.height-statusReserve-paneTitleHeight)
	m.viewport.Width = max(0, m.width-foldersPaneW-chatsPaneW)
	m.viewport.Height = m.paneRowHeight
	if m.mode == modeInsert {
		// Карточка черновика "съедает" часть высоты панели сообщений
		// снизу — лента сообщений сжимается ровно настолько, чтобы вместе
		// с карточкой суммарная высота осталась равна paneRowHeight (как у
		// остальных панелей, см. foldersPane/chatPane/listContentRows,
		// которые используют m.paneRowHeight, НЕ m.viewport.Height).
		m.viewport.Height = max(0, m.paneRowHeight-m.composeCardHeight())
	}
	m.commandInput.Width = max(0, m.width-lipgloss.Width(m.commandInput.Prompt)-1)
	m.fileInput.Width = max(0, m.width-lipgloss.Width(m.fileInput.Prompt)-1)
	m.searchInput.Width = max(0, m.width-lipgloss.Width(m.searchInput.Prompt)-1)
	// Ширина черновика — под внутреннюю ширину его собственной карточки
	// (ширина панели сообщений минус рамка карточки), не во весь экран.
	composeInnerW := max(0, m.viewport.Width-composeCardStyle().GetHorizontalBorderSize())
	m.composeInput.SetWidth(composeInnerW)
}

// composeCardHeight — полная высота карточки черновика вместе с рамкой
// (RoundedBorder, верх+низ = 2 строки, без паддинга — см. composeCardStyle).
func (m Model) composeCardHeight() int {
	return composeCardStyle().GetVerticalBorderSize() + m.composeInput.Height()
}

// composeCard — черновик Insert-режима как отдельная карточка с белой
// рамкой, встроенная в низ панели сообщений (по прямому запросу
// человека) — визуально "как ещё одно сообщение" внизу ленты, а не
// отдельная область под всеми тремя панелями.
//
// ВАЖНО: ширина здесь — та же формула, что и в applyLayout() при
// m.composeInput.SetWidth(...), НЕ m.composeInput.Width() (геттер): у
// textarea SetWidth(w) резервирует место под prompt ВНУТРИ w, а Width()
// возвращает урезанное (без prompt) значение — на 2 колонки меньше
// реально занятой ширины (prompt "┃ " — 2 руны). Подстановка этого
// урезанного числа сюда не просто рисовала рамку уже вьюпорта — она ещё
// заставляла lipgloss переносить уже отрендеренные (реально более
// широкие) строки черновика по переполнению, добавляя лишнюю строку
// высоты (пойман эмпирически: composeInput.View() отдавал 3 строки,
// итоговая карточка — 6 вместо 5, ширина — на 2 колонки уже вьюпорта).
func (m Model) composeCard() string {
	innerW := max(0, m.viewport.Width-composeCardStyle().GetHorizontalBorderSize())
	return composeCardStyle().Width(innerW).Render(m.composeInput.View())
}

// syncComposeHeight подгоняет высоту черновика под текущее число строк —
// от composeAreaHeight (минимум) до composeAreaMaxHeight (потолок), и сразу
// пересчитывает layout (applyLayout читает m.composeInput.Height() для
// bottomReserve). По прямому запросу человека: поле ввода растёт вниз при
// добавлении строк, "поднимая" ленту сообщений вверх — GotoBottom()
// гарантирует, что нижняя граница ленты (последнее сообщение) остаётся
// видна при каждом таком росте/сжатии, а не уезжает за пределы экрана.
// Вызывается после КАЖДОГО изменения содержимого m.composeInput (набор
// текста, вставка, очистка после отправки) и при входе в
// Insert-режим (там же восстанавливает высоту под уже сохранённый черновик,
// если он был).
func (m *Model) syncComposeHeight() {
	desired := m.composeInput.LineCount()
	if desired < composeAreaHeight {
		desired = composeAreaHeight
	}
	if desired > composeAreaMaxHeight {
		desired = composeAreaMaxHeight
	}
	m.composeInput.SetHeight(desired)
	m.applyLayout()
	m.viewport.GotoBottom()
}

// rerenderMessagesAndScrollToCursor перестраивает контент ленты с подсветкой
// m.messageCursor и минимально прокручивает viewport так, чтобы верхняя
// строка выбранной карточки была видна: если она выше текущей видимой
// области — скроллим вверх до неё; если ниже — скроллим вниз ровно настолько,
// чтобы она стала последней видимой строкой. Если уже видна — YOffset не
// трогаем (не дёргаем прокрутку зря).
func (m *Model) rerenderMessagesAndScrollToCursor() {
	contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
	content, offsets := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, m.messageCursor)
	m.viewport.SetContent(content)
	if m.messageCursor < 0 || m.messageCursor >= len(offsets) {
		return
	}
	start := offsets[m.messageCursor]
	switch {
	case start < m.viewport.YOffset:
		m.viewport.SetYOffset(start)
	case start >= m.viewport.YOffset+m.viewport.Height:
		m.viewport.SetYOffset(start - m.viewport.Height + 1)
	}
}

// Init запускает первичную загрузку списка чатов и подписку на входящие
// апдейты сообщений и папок. Сам Init/Update не блокируется: bubbletea
// исполняет возвращаемый tea.Cmd в отдельной горутине, вызов возвращается с
// готовым tea.Msg. waitForMessageUpdate и waitForChatFolders переподписываются
// из самого Update (см. комментарии у newMessageUpdateMsg/chatFoldersUpdateMsg).
func (m Model) Init() tea.Cmd {
	loadChats := m.loadChatsCmd(map[string]interface{}{"@type": "chatListMain"}, 0)
	return tea.Batch(loadChats, m.waitForMessageUpdate(), m.waitForChatFolders(),
		m.waitForChatReadInboxUpdate(), m.waitForUnreadCountUpdate(), m.waitForUnreadChatCountUpdate(),
		m.checkUpdateCmd(false))
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
			content, _ := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, m.messageCursor)
			m.viewport.SetContent(content)
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
		// Ошибка загрузки — терминальное состояние: показываем статус и снимаем
		// заморозку (для openContactChatCmd chatID при ошибке createPrivateChat
		// равен 0 и в сравнении не участвует). Устаревший успешный ответ
		// (пользователь выбрал другой чат, пока запрос летел) молча отбрасываем
		// — не затираем актуально отображаемую ленту и не трогаем loadingMsgs.
		// Для открытия контакта из поиска chatID заранее неизвестен (приходит из
		// ответа createPrivateChat), поэтому проверка устаревания справедлива
		// только для успешных ответов.
		if msg.err != nil {
			m.loadingMsgs = false
			m.status = fmt.Sprintf("Ошибка загрузки сообщений: %v", msg.err)
			return m, nil
		}
		if msg.chatID != m.displayedChat {
			return m, nil
		}
		m.loadingMsgs = false
		m.status = ""
		m.displayedChat = msg.chatID
		m.messages = msg.messages
		m.messageCursor = max(0, len(m.messages)-1) // курсор всегда синхронизирован с последним сообщением (см. п.5)
		contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
		content, _ := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, m.messageCursor)
		m.viewport.SetContent(content)
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
			m.messageCursor = max(0, len(m.messages)-1)
			contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
			content, _ := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, m.messageCursor)
			m.viewport.SetContent(content)
			m.viewport.GotoBottom()
		}
		m.status = ""
		m.composeInput.SetValue("")
		// Insert-режим НЕ закрывается после отправки (в отличие от
		// sendFileMsg ниже) — черновик очищен, но нужно сразу же сжать
		// выросшее поле обратно к composeAreaHeight, иначе следующее
		// сообщение начнёт печататься в уже большом (и теперь пустом) поле.
		m.syncComposeHeight()
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
			m.messageCursor = max(0, len(m.messages)-1)
			contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
			content, _ := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, m.messageCursor)
			m.viewport.SetContent(content)
			m.viewport.GotoBottom()
		}
		m.status = ""
		m.fileInput.SetValue("")
		m.fileInput.Blur()
		m.mode = modeNormal
		m.applyLayout()
		return m, nil

	case chatActionDoneMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("Не удалось: %v", msg.err)
			return m, nil
		}
		// Локально убираем чат из списка — TDLib пришлёт свои push-апдейты
		// (updateChatRemovedFromList и т.п.), но эта модель на них сейчас не
		// подписана (вне охвата задачи), поэтому обновляем m.chats сами, тем же
		// паттерном "оптимистичного" локального обновления, что уже применяется
		// в других местах этого файла.
		newChats := make([]auth.Chat, 0, len(m.chats))
		for _, c := range m.chats {
			if c.ID != msg.chatID {
				newChats = append(newChats, c)
			}
		}
		m.chats = newChats
		if m.chatCursor >= len(m.chats) {
			m.chatCursor = max(0, len(m.chats)-1)
		}
		if m.displayedChat == msg.chatID {
			// Удалённый/покинутый чат был открыт в msgPane — закрыть его.
			m.displayedChat = 0
			m.messages = nil
			m.messageCursor = 0
			m.viewport.SetContent("")
		}
		m.status = "Готово"
		return m, nil

	case newMessageUpdateMsg:
		if msg.closed {
			// Подписка естественно завершилась (клиент остановлен/контекст
			// отменён) — переподписываться не на что.
			return m, nil
		}
		if msg.chatID != 0 && msg.chatID == m.displayedChat && !m.hasMessage(msg.message.ID) {
			// Автопрокрутка вниз — только если человек и так был внизу
			// (следит за перепиской вживую). Если он прокрутил вверх читать
			// историю, входящее сообщение не должно дёргать его обратно —
			// по правке человека, реальный баг: лента "сама возвращалась
			// вниз" при новом сообщении, даже пока читаешь старые.
			wasAtBottom := m.viewport.AtBottom()
			m.messages = append(m.messages, msg.message)
			m.messageCursor = max(0, len(m.messages)-1)
			contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
			content, _ := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, m.messageCursor)
			m.viewport.SetContent(content)
			if wasAtBottom {
				m.viewport.GotoBottom()
			}
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
		// folderUnread не трогаем: у карты свой независимый канал, и она не
		// должна сбрасываться на каждый chatFoldersUpdateMsg (см. поле folderUnread).
		return m, m.waitForChatFolders()

	case chatReadInboxUpdateMsg:
		if msg.closed {
			return m, nil
		}
		if !msg.valid {
			return m, m.waitForChatReadInboxUpdate() // нераспознанный апдейт — переподписка обязательна
		}
		for i := range m.chats {
			if m.chats[i].ID == msg.chatID {
				m.chats[i].UnreadCount = msg.unreadCount
				break
			}
		}
		return m, m.waitForChatReadInboxUpdate()

	case unreadCountUpdateMsg:
		if msg.closed {
			return m, nil
		}
		if !msg.valid {
			return m, m.waitForUnreadCountUpdate() // нераспознанный апдейт — переподписка обязательна
		}
		// ТОЛЬКО "Все чаты" (folderID == 0) — сумма непрочитанных СООБЩЕНИЙ.
		// Для остальных папок эта метрика не подходит (см. тип
		// unreadCountUpdateMsg) — их бейджи приходят отдельным каналом,
		// unreadChatCountUpdateMsg ниже.
		if msg.folderID == 0 {
			m.folderUnread[0] = msg.unreadCount
		}
		return m, m.waitForUnreadCountUpdate()

	case unreadChatCountUpdateMsg:
		if msg.closed {
			return m, nil
		}
		if !msg.valid {
			return m, m.waitForUnreadChatCountUpdate() // нераспознанный апдейт — переподписка обязательна
		}
		// ТОЛЬКО папки (folderID != 0) — число чатов с непрочитанным. "Все
		// чаты" использует другую метрику (unreadCountUpdateMsg выше).
		if msg.folderID != 0 {
			m.folderUnread[msg.folderID] = msg.chatCount
		}
		return m, m.waitForUnreadChatCountUpdate()

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

	case updateInstallMsg:
		m.installingUpdate = false
		if msg.err != nil {
			m.status = fmt.Sprintf("Обновление не удалось: %v", msg.err)
			return m, nil
		}
		m.updateAvailable = ""
		m.status = fmt.Sprintf("Обновлено до %s — перезапустите telecli, чтобы применить", msg.version)
		return m, nil

	case searchResultMsg:
		m.searchingNow = false
		if msg.err != nil {
			m.status = fmt.Sprintf("Поиск не удался: %v", msg.err)
			m.mode = modeNormal
			m.applyLayout()
			m.searchInput.Blur()
			return m, nil
		}
		m.status = ""
		m.searchResults = msg.results
		m.searchActive = true
		m.searchCursor = 0
		m.mode = modeNormal
		m.applyLayout()
		m.searchInput.Blur()
		m.focus = focusChats
		return m, nil

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
				case "update install":
					if m.installingUpdate {
						m.status = "Установка уже идёт…"
						return m, nil
					}
					m.installingUpdate = true
					m.status = "Скачивание обновления…"
					return m, m.installUpdateCmd()
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

		case modeSearch:
			// Пока идёт поиск — ввод "заморожен", тот же паттерн, что
			// sendingMsg/sendingFile: новый текст в поле не принимается до
			// прихода searchResultMsg (результат всё равно применяется к уже
			// отправленному запросу, не к введённому заново).
			if m.searchingNow {
				return m, nil
			}
			switch msg.Type {
			case tea.KeyEsc:
				m.mode = modeNormal
				m.applyLayout()
				m.searchInput.Blur()
				m.searchActive = false
				m.searchResults = auth.SearchResults{}
				return m, nil
			case tea.KeyEnter:
				query := strings.TrimSpace(m.searchInput.Value())
				if query == "" {
					m.searchInput.Blur()
					m.mode = modeNormal
					m.applyLayout()
					return m, nil
				}
				m.searchingNow = true
				m.status = "Поиск…"
				return m, m.searchCmd(query)
			}
			updated, cmd := m.searchInput.Update(msg)
			m.searchInput = updated
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
					// Пустой черновик: Enter зарезервирован под отправку и НЕ
					// закрывает Insert-режим сам по себе — no-op (снимает
					// режим только Esc, см. ветку выше). Раньше эта ветка
					// ошибочно выкидывала в Normal.
					return m, nil
				}
				if m.displayedChat == 0 {
					m.status = "Сначала выберите чат (Tab → список чатов → Enter)"
					return m, nil
				}
				m.sendingMsg = true
				return m, m.sendMessageCmd(m.displayedChat, text)
			}
			updated, cmd := m.composeInput.Update(msg)
			m.composeInput = updated
			m.syncComposeHeight()
			return m, cmd

		case modeHelp:
			// Любая клавиша, кроме Esc/ShowHelp, здесь ничего не делает —
			// оверлей только читает, никакого ввода не принимает (тот же
			// принцип "модальный оверлей", что у modeCommand/modeSearch).
			// translateLayout — та же причина, что и у modeNormal ниже:
			// хоткей ShowHelp должен срабатывать независимо от раскладки ОС.
			if msg.Type == tea.KeyEsc || key.Matches(translateLayout(msg), m.keys.ShowHelp) {
				m.mode = modeNormal
				m.applyLayout()
			}
			return m, nil
		case modeConfirmDelete:
			yes := msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && (msg.Runes[0] == 'y' || msg.Runes[0] == 'Y')
			no := msg.Type == tea.KeyRunes && len(msg.Runes) == 1 && (msg.Runes[0] == 'n' || msg.Runes[0] == 'N')
			cancel := msg.Type == tea.KeyEsc

			if cancel {
				m.mode = modeNormal
				m.applyLayout()
				return m, nil
			}
			if !yes && !no {
				return m, nil // игнорируем любую другую клавишу
			}

			if m.deleteTargetGroup {
				// Группа/канал: единственный шаг — "покинуть?".
				if !yes {
					m.mode = modeNormal
					m.applyLayout()
					return m, nil
				}
				chatID := m.deleteTargetChatID
				m.mode = modeNormal
				m.applyLayout()
				m.status = "Покидаем чат…"
				return m, m.leaveChatCmd(chatID)
			}

			// Личный/секретный чат: два шага, шаг 1 — "удалить?", шаг 2 —
			// "также у собеседника?". На шаге 2 yes → revoke=true, no →
			// revoke=false: ОБА ведут к удалению, разница только в revoke
			// (cancel уже отфильтрован веткой cancel выше).
			if m.deleteStep == 0 {
				if !yes {
					m.mode = modeNormal
					m.applyLayout()
					return m, nil
				}
				m.deleteStep = 1
				return m, nil
			}
			chatID := m.deleteTargetChatID
			revoke := yes
			m.mode = modeNormal
			m.applyLayout()
			m.status = "Удаляем чат…"
			return m, m.deleteChatCmd(chatID, revoke)
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
			// syncComposeHeight, не голый applyLayout: восстанавливает высоту
			// под уже сохранённый черновик (несколько строк), если он остался
			// с прошлого раза (Esc не чистит текст), плюс делает applyLayout.
			m.syncComposeHeight()
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
		if key.Matches(msg, m.keys.Search) {
			m.mode = modeSearch
			m.applyLayout()
			cmd := m.searchInput.Focus()
			return m, cmd
		}
		if key.Matches(msg, m.keys.ShowHelp) {
			m.mode = modeHelp
			m.applyLayout()
			return m, nil
		}
		// Удаление/покидание чата — работает только в обычном списке m.chats:
		// вне панели чатов, в результатах поиска (там чат может быть ещё не
		// "своим") или при пустом списке хоткей ничего не делает.
		if key.Matches(msg, m.keys.DeleteChat) {
			if m.focus != focusChats || m.searchActive || m.chatCursor >= len(m.chats) {
				return m, nil
			}
			chat := m.chats[m.chatCursor]
			m.deleteTargetChatID = chat.ID
			m.deleteTargetTitle = chat.Title
			m.deleteTargetGroup = chat.IsGroup
			m.deleteStep = 0
			m.mode = modeConfirmDelete
			m.applyLayout()
			return m, nil
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
			if m.searchActive {
				total := len(m.searchResults.Chats) + len(m.searchResults.Contacts)
				switch {
				case key.Matches(msg, m.keys.MoveUp):
					if m.searchCursor > 0 {
						m.searchCursor--
					}
				case key.Matches(msg, m.keys.MoveDown):
					if m.searchCursor < total-1 {
						m.searchCursor++
					}
				case key.Matches(msg, m.keys.Back):
					m.searchActive = false
					m.searchResults = auth.SearchResults{}
					m.searchCursor = 0
				case key.Matches(msg, m.keys.Select):
					if m.searchCursor < len(m.searchResults.Chats) {
						chat := m.searchResults.Chats[m.searchCursor]
						m.searchActive = false
						cmds := []tea.Cmd{m.selectChatCmd(chat.ID)}
						if m.displayedChat != 0 && m.displayedChat != chat.ID {
							cmds = append(cmds, m.closeChatCmd(m.displayedChat))
						}
						m.displayedChat = chat.ID
						m.loadingMsgs = true
						m.focus = focusMessages
						return m, tea.Batch(cmds...)
					}
					contactIdx := m.searchCursor - len(m.searchResults.Chats)
					if contactIdx >= 0 && contactIdx < len(m.searchResults.Contacts) {
						contact := m.searchResults.Contacts[contactIdx]
						m.searchActive = false
						m.loadingMsgs = true
						m.focus = focusMessages
						return m, m.openContactChatCmd(contact.UserID)
					}
				}
				return m, nil
			}
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
		if len(m.messages) > 0 {
			switch {
			case key.Matches(msg, m.keys.MoveUp):
				if m.messageCursor > 0 {
					m.messageCursor--
				}
				m.rerenderMessagesAndScrollToCursor()
				return m, nil
			case key.Matches(msg, m.keys.MoveDown):
				if m.messageCursor < len(m.messages)-1 {
					m.messageCursor++
				}
				m.rerenderMessagesAndScrollToCursor()
				return m, nil
			}
		}
		// Остальные клавиши прокрутки (PageUp/PageDown и т.п.) — по-прежнему
		// дефолтные биндинги bubbles/viewport, курсор не трогают.
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

// waitForChatReadInboxUpdate — tea.Cmd, блокирующийся на ОДНОМ значении из
// client.ChatReadInboxUpdates(). Срабатывает один раз за вызов — обязательна
// переподписка в обработчике (тот же класс ловушки, что у waitForMessageUpdate).
func (m Model) waitForChatReadInboxUpdate() tea.Cmd {
	ch := m.client.ChatReadInboxUpdates()
	ctx := m.ctx
	return func() tea.Msg {
		select {
		case upd, ok := <-ch:
			if !ok {
				return chatReadInboxUpdateMsg{closed: true}
			}
			chatID, unreadCount, valid := auth.ParseChatReadInboxUpdate(upd)
			if !valid {
				return chatReadInboxUpdateMsg{} // безвредно проигнорируется, но переподписка продолжится
			}
			return chatReadInboxUpdateMsg{chatID: chatID, unreadCount: unreadCount, valid: true}
		case <-ctx.Done():
			return chatReadInboxUpdateMsg{closed: true}
		}
	}
}

// waitForUnreadCountUpdate — tea.Cmd, блокирующийся на ОДНОМ значении из
// client.UnreadCountUpdates(). Срабатывает один раз за вызов — обязательна
// переподписка в обработчике (тот же класс ловушки, что у waitForMessageUpdate).
func (m Model) waitForUnreadCountUpdate() tea.Cmd {
	ch := m.client.UnreadCountUpdates()
	ctx := m.ctx
	return func() tea.Msg {
		select {
		case upd, ok := <-ch:
			if !ok {
				return unreadCountUpdateMsg{closed: true}
			}
			folderID, unreadCount, valid := auth.ParseUnreadMessageCountUpdate(upd)
			if !valid {
				return unreadCountUpdateMsg{} // безвредно проигнорируется, но переподписка продолжится
			}
			return unreadCountUpdateMsg{folderID: folderID, unreadCount: unreadCount, valid: true}
		case <-ctx.Done():
			return unreadCountUpdateMsg{closed: true}
		}
	}
}

// waitForUnreadChatCountUpdate — tea.Cmd, блокирующийся на ОДНОМ значении из
// client.UnreadChatCountUpdates(). Срабатывает один раз за вызов — обязательна
// переподписка в обработчике (тот же класс ловушки, что у waitForMessageUpdate).
func (m Model) waitForUnreadChatCountUpdate() tea.Cmd {
	ch := m.client.UnreadChatCountUpdates()
	ctx := m.ctx
	return func() tea.Msg {
		select {
		case upd, ok := <-ch:
			if !ok {
				return unreadChatCountUpdateMsg{closed: true}
			}
			folderID, chatCount, valid := auth.ParseUnreadChatCountUpdate(upd)
			if !valid {
				return unreadChatCountUpdateMsg{} // безвредно проигнорируется, но переподписка продолжится
			}
			return unreadChatCountUpdateMsg{folderID: folderID, chatCount: chatCount, valid: true}
		case <-ctx.Done():
			return unreadChatCountUpdateMsg{closed: true}
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

// leaveChatCmd — tea.Cmd для auth.LeaveChat (покинуть группу/канал).
// Тот же паттерн захвата m.ctx/m.client в замыкание, что у sendMessageCmd.
func (m Model) leaveChatCmd(chatID int64) tea.Cmd {
	ctx := m.ctx
	client := m.client
	return func() tea.Msg {
		err := auth.LeaveChat(ctx, client, chatID)
		return chatActionDoneMsg{chatID: chatID, err: err}
	}
}

// deleteChatCmd — tea.Cmd для auth.DeleteChatHistory (удалить личный/секретный
// чат), revoke=true — также у собеседника, revoke=false — только у себя.
func (m Model) deleteChatCmd(chatID int64, revoke bool) tea.Cmd {
	ctx := m.ctx
	client := m.client
	return func() tea.Msg {
		err := auth.DeleteChatHistory(ctx, client, chatID, revoke)
		return chatActionDoneMsg{chatID: chatID, err: err}
	}
}

// searchCmd — tea.Cmd, выполняющий auth.SearchAll по запросу из modeSearch.
func (m Model) searchCmd(query string) tea.Cmd {
	ctx := m.ctx
	client := m.client
	return func() tea.Msg {
		res, err := auth.SearchAll(ctx, client, query)
		return searchResultMsg{results: res, err: err}
	}
}

// openContactChatCmd — createPrivateChat по найденному в поиске контакту,
// затем та же загрузка истории, что и у обычного выбора чата (selectChatCmd),
// но chatID заранее неизвестен — получаем его из ответа createPrivateChat.
func (m Model) openContactChatCmd(userID int64) tea.Cmd {
	ctx := m.ctx
	client := m.client
	return func() tea.Msg {
		resp, err := client.Send(ctx, map[string]interface{}{
			"@type":   "createPrivateChat",
			"user_id": userID,
			"force":   true,
		})
		if err != nil {
			return messagesLoadedMsg{chatID: 0, err: err}
		}
		chatIDRaw, _ := resp["id"].(float64)
		chatID := int64(chatIDRaw)
		_ = auth.OpenChat(ctx, client, chatID)
		msgs, err := auth.GetMessages(ctx, client, chatID, messagesLimit)
		return messagesLoadedMsg{chatID: chatID, messages: msgs, err: err}
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

// installUpdateCmd — tea.Cmd для скачивания и установки обновления.
// Выполняет полный цикл: CheckLatest -> IsNewer -> AssetNameForPlatform ->
// FindAsset -> DownloadBinary -> os.Executable/EvalSymlinks -> InstallBinary.
// Любая ошибка на любом шаге возвращается через updateInstallMsg{err: ...}.
func (m Model) installUpdateCmd() tea.Cmd {
	ctx := m.ctx
	return func() tea.Msg {
		rel, err := update.CheckLatest(ctx, http.DefaultClient, 5*time.Second)
		if err != nil {
			return updateInstallMsg{err: fmt.Errorf("проверка обновлений: %w", err)}
		}
		if !update.IsNewer(m.version, rel.TagName) {
			return updateInstallMsg{err: fmt.Errorf("уже установлена последняя версия")}
		}
		assetName, ok := update.AssetNameForPlatform()
		if !ok {
			return updateInstallMsg{err: fmt.Errorf("нет готового бинарника для этой платформы (GOOS=%s GOARCH=%s) — скачайте вручную: %s", runtime.GOOS, runtime.GOARCH, rel.HTMLURL)}
		}
		asset, ok := update.FindAsset(rel, assetName)
		if !ok {
			return updateInstallMsg{err: fmt.Errorf("ассет %q не найден в релизе %s — возможно, сборка для этой платформы не удалась; скачайте вручную: %s", assetName, rel.TagName, rel.HTMLURL)}
		}
		data, err := update.DownloadBinary(ctx, http.DefaultClient, asset.BrowserDownloadURL, 2*time.Minute)
		if err != nil {
			return updateInstallMsg{err: fmt.Errorf("скачивание бинарника: %w", err)}
		}
		execPath, err := os.Executable()
		if err != nil {
			return updateInstallMsg{err: fmt.Errorf("определение пути исполняемого файла: %w", err)}
		}
		execPath, err = filepath.EvalSymlinks(execPath)
		if err != nil {
			return updateInstallMsg{err: fmt.Errorf("разрешение симлинков исполняемого файла: %w", err)}
		}
		if err := update.InstallBinary(data, execPath); err != nil {
			return updateInstallMsg{err: fmt.Errorf("установка бинарника: %w", err)}
		}
		return updateInstallMsg{version: rel.TagName, err: nil}
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
	if m.mode == modeHelp {
		return m.helpScreen()
	}
	fStart, fEnd := visibleWindow(len(m.folders)+1, m.folderCursor, m.listContentRows())
	cStart, cEnd := visibleWindow(m.chatListLen(), m.chatListCursor(), m.listContentRows())
	titles := lipgloss.JoinHorizontal(lipgloss.Top,
		paneTitle(foldersPaneW, 1, "Папки", m.focus == focusFolders, fStart > 0, fEnd < len(m.folders)+1),
		paneTitle(chatsPaneW, 2, "Чаты", m.focus == focusChats, cStart > 0, cEnd < m.chatListLen()),
		paneTitle(m.viewport.Width, 3, m.currentChatTitle(), m.focus == focusMessages, !m.viewport.AtTop(), !m.viewport.AtBottom()),
	)
	panes := lipgloss.JoinHorizontal(lipgloss.Top, m.foldersPane(), m.chatPane(), m.msgPane())
	body := titles + "\n" + panes + "\n" + m.bottomLine()
	return body
}

// helpScreen — полноэкранный оверлей "о программе" (modeHelp): описание +
// полный список горячих клавиш по режимам + пути к конфиг-файлам. По
// прямому запросу человека — "окно полной информации о приложении... что
// будет полезно для пользователя". Статичный контент (не скроллится) —
// при нехватке высоты терминала строки снизу обрезаются (см. max(0, ...)
// ниже), не переполняя экран (та же дисциплина, что и у остальных панелей
// после фикса прокрутки папок/чатов — контент никогда не должен рендерить
// больше строк, чем реально помещается).
func (m Model) helpScreen() string {
	sectionStyle := lipgloss.NewStyle().Bold(true).Foreground(activeBorderColor)
	descStyle := lipgloss.NewStyle().Faint(true)
	keyLine := func(key, desc string) string {
		return "  " + hintKeyStyle.Render(runewidth.FillRight(key, 10)) + descStyle.Render(desc)
	}

	lines := []string{
		lipgloss.NewStyle().Bold(true).Render("TELECLi") + descStyle.Render(" — терминальный клиент Telegram ("+m.version+")"),
		"Vim-модальный интерфейс: три панели (папки, чаты, сообщения), навигация с клавиатуры.",
		"",
		sectionStyle.Render("НАВИГАЦИЯ (Normal)"),
		keyLine("tab", "переключить панель"),
		keyLine("j/k/↑↓", "курсор вверх/вниз"),
		keyLine("←/→", "фокус влево/вправо"),
		keyLine("1/2/3", "прямой переход к панели"),
		keyLine("enter", "открыть чат / выбрать папку"),
		keyLine("i", "ввод сообщения"),
		keyLine(":", "командная строка"),
		keyLine("/", "поиск чатов/каналов/контактов"),
		keyLine("ctrl+f", "отправить файл"),
		keyLine("d", "покинуть/удалить чат под курсором (с подтверждением)"),
		keyLine("t", "это окно"),
		keyLine("q", "выход"),
		"",
		sectionStyle.Render("ВВОД СООБЩЕНИЯ (Insert)"),
		keyLine("enter", "отправить"),
		keyLine("ctrl+j", "перенос строки (поле растёт вниз)"),
		keyLine("esc", "отмена, назад в Normal"),
		"",
		sectionStyle.Render("КОМАНДНАЯ СТРОКА (:)"),
		keyLine(":q", "выход (тоже :quit)"),
		keyLine(":update", "проверить обновления вручную"),
		keyLine(":update install", "скачать и установить доступное обновление"),
		"",
		sectionStyle.Render("КОНФИГУРАЦИЯ"),
		descStyle.Render("  <config dir>/telecli/ — config.toml (доступ к Telegram), keybindings.toml"),
		descStyle.Render("  (горячие клавиши), settings.toml (опции интерфейса)."),
		"",
		descStyle.Render("  Лицензия MIT · github.com/zeroscrypt/telecli"),
	}

	// footer — подсказка закрытия ("Esc / t — закрыть"), ВСЕГДА последняя
	// видимая строка, даже если остальной контент пришлось обрезать снизу
	// (см. maxRows ниже) — иначе на низком терминале человек не увидит, чем
	// закрыть оверлей.
	footer := descStyle.Render("Esc / t — закрыть")

	borderRows := paneBorderStyle(true).GetVerticalBorderSize()
	maxRows := max(0, m.height-borderRows-2*panePaddingV)
	switch {
	case maxRows <= 0:
		lines = nil
	case len(lines)+1 > maxRows: // +1 — место под footer
		lines = append(lines[:maxRows-1], footer)
	default:
		lines = append(lines, "", footer)
	}

	return paneBox(m.width, m.height, strings.Join(lines, "\n"), true)
}

// chatListLen/chatListCursor — число элементов и позиция курсора в панели
// чатов с учётом того, что там показывается: обычный список m.chats или (в
// modeSearch/searchActive) результаты поиска. Общие для visibleWindow
// (прокрутка панели, см. chatPane) и для индикатора прокрутки в заголовке
// (см. View) — единственный источник этой логики, чтобы оба места не
// разъехались друг с другом.
func (m Model) chatListLen() int {
	if m.searchActive {
		return len(m.searchResults.Chats) + len(m.searchResults.Contacts)
	}
	return len(m.chats)
}

func (m Model) chatListCursor() int {
	if m.searchActive {
		return m.searchCursor
	}
	return m.chatCursor
}

// telecliLogo — фирменный бейдж "TELECLi" (последняя буква строчная — по
// прямому правку человека, написание именно так и нигде иначе), слева в
// нижней строке перед индикатором режима. Фон буквы T — тот же синий, что и
// у остального бейджа (по правке человека; до этого пробовали отдельный
// чёрный/белый фон — оказалось лишним), текст T — белым (не тёмным
// pillTextColor, как у остальных букв) — лёгкий акцент, всё ещё намекающий,
// что это же буква хоткея ShowHelp (см. helpScreen), но без отдельного
// цветового блока. Общая ширина не меняется (было " TELECLI " =
// Padding(0,1) вокруг 7 букв = 9 колонок; стало " T"+"ELECLi"+" " = те же
// 9), так что весь расчёт padding в bottomLine() (lipgloss.Width(left))
// остаётся верным без изменений там.
func telecliLogo() string {
	tPart := lipgloss.NewStyle().Background(activeBorderColor).Foreground(lipgloss.Color("#FFFFFF")).
		Bold(true).Render(" T")
	restPart := lipgloss.NewStyle().Background(activeBorderColor).Foreground(pillTextColor).
		Bold(true).Render("ELECLi ")
	return tPart + restPart
}

// hintKeyStyle — цвет названия клавиши в подсказках нижней строки (синим, тем
// же акцентом, что рамка/логотип) — отдельно от тусклого текста описания,
// чтобы клавиша не сливалась с объяснением, по правке человека.
var hintKeyStyle = lipgloss.NewStyle().Foreground(activeBorderColor)

// renderHint склеивает пары "клавиша"/"описание" через " — " (клавиша синим,
// описание тусклым), записи между собой — через sep (тоже тусклым).
func renderHint(sep string, pairs ...[2]string) string {
	descStyle := lipgloss.NewStyle().Faint(true)
	parts := make([]string, len(pairs))
	for i, p := range pairs {
		parts[i] = hintKeyStyle.Render(p[0]) + descStyle.Render(" — "+p[1])
	}
	return strings.Join(parts, descStyle.Render(sep))
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
	case modeSearch:
		hint := ""
		if m.searchingNow {
			hint = " (поиск…)"
		}
		return m.searchInput.View() + hint
	case modeInsert:
		// Сам черновик (composeInput.View()) здесь больше НЕ рендерится —
		// он переехал в msgPane() как отдельная карточка с белой рамкой
		// внутри панели сообщений (по прямому запросу человека). Эта
		// строка — та же 1-строчная подсказка режима, что и в остальных
		// режимах, statusReserve больше не растёт для Insert.
		hint := insertHint
		if m.sendingMsg {
			hint = " (отправка…)"
		}
		logo := telecliLogo()
		modeTag := lipgloss.NewStyle().Foreground(insertModeColor).Bold(true).Render(" INP")
		return logo + modeTag + lipgloss.NewStyle().Faint(true).Render(hint)
	case modeConfirmDelete:
		var prompt string
		switch {
		case m.deleteTargetGroup:
			prompt = fmt.Sprintf("Покинуть чат «%s»? (y/n)", m.deleteTargetTitle)
		case m.deleteStep == 0:
			prompt = fmt.Sprintf("Удалить чат «%s»? (y/n)", m.deleteTargetTitle)
		default:
			prompt = "Удалить также у собеседника? (y/n, Esc — отмена)"
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true).Render(prompt)
	default:
		if m.status != "" {
			return lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Render(m.status)
		}
		logo := telecliLogo()
		modeTag := lipgloss.NewStyle().Foreground(activeBorderColor).Bold(true).Render(" NAV")
		// Сначала — общие хоткеи (работают при любом фокусе), затем — контекстные
		// для панели, которая сейчас в фокусе (по прямому запросу человека).
		pairs := [][2]string{
			{"tab", "панели"},
			{"←/→", "фокус"},
			{"j/k/↑↓", "курсор"},
			{"i", "ввод"},
			{":", "команда"},
			{"t", "справка"},
			{"q", "выход"},
		}
		switch m.focus {
		case focusChats:
			pairs = append(pairs, [2]string{"/", "поиск"}, [2]string{"d", "удалить чат"})
		case focusMessages:
			pairs = append(pairs, [2]string{"ctrl+f", "файл"})
		}
		hint := " " + renderHint(" · ", pairs...)
		left := logo + modeTag + hint

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

// listContentRows — сколько строк контента реально помещается в панель
// списка (папки/чаты) при текущей высоте терминала: из общего бюджета
// m.paneRowHeight (ОБЩИЙ для всех трёх панелей — папки/чаты НЕ сжимаются в
// Insert-режиме, см. комментарий у поля paneRowHeight; НЕ m.viewport.Height,
// та отдельная величина только для самого вьюпорта сообщений) вычитаем рамку
// (border — одинаково 2 строки что у фокусной, что у нефокусной панели, см.
// paneBorderStyle) и паддинг СВЕРХУ И СНИЗУ (2*panePaddingV — паддинг
// симметричный, см. styles.go). До правки про прокрутку список рендерился
// БЕЗ учёта этого бюджета вовсе (все элементы всегда, сколько бы их ни
// было) — на низком терминале (или просто при большом числе папок/чатов)
// это раздувало итоговый вывод выше реальной высоты терминала без
// какой-либо прокрутки — прямой репорт человека ("не помещаются, нет
// прокрутки"). Правильный fix — не худеть контент, а показывать
// скользящее окно вокруг курсора (см. visibleWindow), тот же принцип, что
// уже применён к messageCursor в focusMessages.
func (m Model) listContentRows() int {
	borderRows := paneBorderStyle(false).GetVerticalBorderSize()
	return max(0, m.paneRowHeight-borderRows-2*panePaddingV)
}

// visibleWindow вычисляет полуоткрытый диапазон [start, end) из total
// строк, который умещается в rows строк экрана и всегда содержит cursor —
// минимальная прокрутка (сдвигается ровно настолько, чтобы курсор попал в
// границу), не наматывает лишнего. rows<=0 или total<=0 — пустое окно.
func visibleWindow(total, cursor, rows int) (start, end int) {
	if rows <= 0 || total <= 0 {
		return 0, 0
	}
	if total <= rows {
		return 0, total
	}
	start = 0
	if cursor >= rows {
		start = cursor - rows + 1
	}
	if start+rows > total {
		start = total - rows
	}
	if start < 0 {
		start = 0
	}
	return start, start + rows
}

// foldersPane — панель слева: "Все чаты" (синтетический пункт, всегда первый)
// + m.folders. Курсор — треугольник-маркер "▸" слева + жирное название (по
// правке человека, без цветной пилюли — так папки отличаются от подсветки
// курсора в списке чатов, см. chatPane/chatSelectionColor). У строк без
// курсора — те же 2 колонки заняты пробелами, чтобы текст не "прыгал" по
// горизонтали при переключении курсора. Показывается только видимое окно
// вокруг m.folderCursor (см. listContentRows/visibleWindow) — весь список
// длиннее окна прокручивается курсором, а не рендерится целиком.
func (m Model) foldersPane() string {
	names := make([]string, len(m.folders)+1)
	badges := make([]string, len(m.folders)+1)
	names[0] = "Все чаты"
	badges[0] = unreadSuffix(m.folderUnread[0])
	for i, f := range m.folders {
		name := f.Name
		if name == "" {
			name = fmt.Sprintf("Папка #%d", f.ID)
		}
		names[i+1] = name
		badges[i+1] = unreadSuffix(m.folderUnread[f.ID])
	}

	contentW := foldersPaneW - 2 - 2*panePaddingH // минус рамка (2) и паддинг (2*panePaddingH), тот же приём, что в chatPane
	start, end := visibleWindow(len(names), m.folderCursor, m.listContentRows())

	var sb strings.Builder
	for idx := start; idx < end; idx++ {
		prefix := "  "
		style := lipgloss.NewStyle()
		if idx == m.folderCursor {
			prefix = "▸ "
			style = style.Bold(true)
		}
		textW := contentW - runewidth.StringWidth(prefix)
		content := prefix + alignBadge(names[idx], badges[idx], textW)
		sb.WriteString(style.Width(contentW).Render(content))
		sb.WriteString("\n")
	}
	return paneBox(foldersPaneW, m.paneRowHeight, strings.TrimRight(sb.String(), "\n"), m.focus == focusFolders)
}

// renderCursorList — общий рендер списка строк с курсором-пилюлей
// (chatSelectionColor), используется и для обычного списка чатов, и для
// результатов поиска (та же визуальная семантика "это список, можно
// выбрать"). badges — бейдж непрочитанных для каждой строки (прижат к
// правому краю, см. alignBadge), может быть короче labels или nil —
// недостающие элементы трактуются как "без бейджа" (результаты поиска их
// не имеют).
func renderCursorList(labels []string, badges []string, cursor int, contentW int) string {
	var sb strings.Builder
	for i, label := range labels {
		badge := ""
		if i < len(badges) {
			badge = badges[i]
		}
		line := lipgloss.NewStyle().
			Width(contentW).
			Render(alignBadge(label, badge, contentW))
		if i == cursor {
			line = lipgloss.NewStyle().Background(chatSelectionColor).Foreground(pillTextColor).Width(contentW).Render(line)
		}
		sb.WriteString(line)
		sb.WriteString("\n")
	}
	return strings.TrimRight(sb.String(), "\n")
}

// chatPane — панель чатов/результатов поиска. Как и foldersPane, показывает
// только видимое окно вокруг курсора (см. listContentRows/visibleWindow) —
// без этого длинный список чатов/результатов поиска рендерился бы целиком и
// ломал высоту терминала так же, как папки (см. комментарий listContentRows).
func (m Model) chatPane() string {
	contentW := chatsPaneW - 2 - 2*panePaddingH // минус рамка (2) и паддинг (2*panePaddingH)
	contentRows := m.listContentRows()
	if m.searchActive {
		labels := make([]string, 0, len(m.searchResults.Chats)+len(m.searchResults.Contacts))
		for _, c := range m.searchResults.Chats {
			labels = append(labels, c.Title)
		}
		for _, c := range m.searchResults.Contacts {
			labels = append(labels, "👤 "+c.Name)
		}
		start, end := visibleWindow(len(labels), m.searchCursor, contentRows)
		content := renderCursorList(labels[start:end], nil, m.searchCursor-start, contentW)
		if len(labels) == 0 {
			content = "Ничего не найдено"
		}
		return paneBox(chatsPaneW, m.paneRowHeight, content, m.focus == focusChats)
	}
	titles := chatTitles(m.chats)
	badges := chatBadges(m.chats)
	start, end := visibleWindow(len(titles), m.chatCursor, contentRows)
	content := renderCursorList(titles[start:end], badges[start:end], m.chatCursor-start, contentW)
	if len(m.chats) == 0 {
		content = "Нет чатов"
	}
	return paneBox(chatsPaneW, m.paneRowHeight, content, m.focus == focusChats)
}

// unreadSuffix — бейдж счётчика непрочитанных для названий папок/чатов:
// "[N]" при N > 0, и пустая строка при N <= 0 (папка "Все чаты" с нулём
// непрочитанных по-прежнему рисуется просто как "Все чаты", без "[0]").
// БЕЗ ведущего пробела — разделяющий пробел перед бейджем добавляет
// alignBadge (по правке человека — бейдж должен быть прижат к правому
// краю строки, не просто дописан сразу после названия).
func unreadSuffix(count int32) string {
	if count <= 0 {
		return ""
	}
	return fmt.Sprintf("[%d]", count)
}

// alignBadge — строка длиной ровно width: text (обрезанный при нехватке
// места, с "…") слева, badge прижат к правому краю, между ними минимум 1
// пробел-разделитель. badge == "" — обычное Truncate+FillRight без бейджа.
// Если места не хватает даже на badge+пробел — badge отбрасывается целиком
// (не обрезается посимвольно: "[1" без закрывающей скобки выглядело бы
// сломанным), деградация до обычного текста без бейджа — тот же принцип,
// что уже применяется к "слишком узко для метки" в renderMessageCard.
func alignBadge(text, badge string, width int) string {
	if width <= 0 {
		return ""
	}
	if badge == "" {
		return runewidth.FillRight(runewidth.Truncate(text, width, "…"), width)
	}
	badgeW := runewidth.StringWidth(badge)
	if badgeW+1 > width {
		return runewidth.FillRight(runewidth.Truncate(text, width, "…"), width)
	}
	textSlot := width - badgeW - 1 // -1 — обязательный пробел-разделитель
	truncated := runewidth.Truncate(text, textSlot, "…")
	pad := width - runewidth.StringWidth(truncated) - badgeW
	return truncated + strings.Repeat(" ", pad) + badge
}

// chatTitles — заголовки m.chats для renderCursorList.
func chatTitles(chats []auth.Chat) []string {
	titles := make([]string, len(chats))
	for i, c := range chats {
		titles[i] = c.Title
	}
	return titles
}

// chatBadges — бейджи непрочитанных m.chats для renderCursorList, тем же
// порядком/длиной, что и chatTitles (см. alignBadge — бейдж прижимается к
// правому краю, поэтому держится отдельно от названия, не дописывается в
// него).
func chatBadges(chats []auth.Chat) []string {
	badges := make([]string, len(chats))
	for i, c := range chats {
		badges[i] = unreadSuffix(c.UnreadCount)
	}
	return badges
}

func (m Model) msgPane() string {
	if m.mode != modeInsert {
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

	// Insert-режим: лента (сообщения/плейсхолдер) сверху + карточка
	// черновика снизу, единым столбцом внутри панели сообщений — по
	// прямому запросу человека ("поле ввода перенести в окно чата... с
	// белой рамкой... по стилю как рамка сообщения... поднимала содержимое
	// чата вверх"). m.viewport.Height уже уменьшен в applyLayout() на
	// composeCardHeight(), так что суммарная высота (лента+карточка)
	// остаётся равна m.paneRowHeight — как и у остальных панелей.
	var top string
	switch {
	case m.loadingMsgs:
		top = paneBox(m.viewport.Width, m.viewport.Height, "Загрузка сообщений…", m.focus == focusMessages)
	case len(m.messages) == 0:
		top = paneBox(m.viewport.Width, m.viewport.Height, "Пока нет сообщений", m.focus == focusMessages)
	default:
		m.viewport.Style = paneBorderStyle(m.focus == focusMessages)
		top = m.viewport.View()
	}
	return lipgloss.JoinVertical(lipgloss.Left, top, m.composeCard())
}

// renderMessageCard рисует одно сообщение как отдельную рамку (одинарная
// скруглённая — не путать с двойной рамкой активной панели из 0020),
// приглушённого цвета отправителя; имя+время встроены прямо в верхнюю линию
// рамки, как в старых BBS-программах. width — ПОЛНАЯ ширина карточки вместе
// с рамкой (тот же принцип, что у paneBox). alignRight — рисовать метку
// (время+имя) у правого края верхней рамки, иначе — метку (имя+время) у
// левого края.
func renderMessageCard(msg auth.Message, width int, alignRight bool, selected bool) string {
	b := lipgloss.RoundedBorder()
	if selected {
		// Двойная рамка — тот же визуальный язык "это выделено", что у
		// активной панели (0020): переиспользуем его для выбранного сообщения,
		// не выдумываем новый цвет/маркер. Геометрия не меняется — обе рамки
		// однорунные с обеих сторон (проверено TestActiveAndInactiveBorderFrameSizesEqual).
		b = lipgloss.DoubleBorder()
	}

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
	// Обычное (не жирное) начертание + Faint — по правке человека, "меньше
	// и тоньше" шрифт имени; реальный размер шрифта терминал не даёт менять
	// посимвольно (настройка эмулятора, не приложения) — Faint (приглушённая
	// яркость) это ближайшее достижимое: имя выглядит подписью, а не акцентом.
	nameStyle := lipgloss.NewStyle().Foreground(nameCol).Faint(true)
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

// naturalCardWidth — минимально необходимая ширина карточки (вместе с
// рамкой) для сообщения msg: максимум из (а) ширины шапки "Имя  ЧЧ:ММ" БЕЗ
// обрезки и (б) ширины тела после word-wrap по максимально доступной ширине
// (maxHorizontalSpan — содержательная ширина карточки, если бы она заняла
// всю ленту, т.е. width-2) — но не шире maxHorizontalSpan+2. По прямому
// запросу человека — карточка не должна растягиваться на всю ширину ленты,
// если контенту столько не нужно ("минимальная ширина рамки"). Используется
// в renderMessages ДО вызова renderMessageCard — сама renderMessageCard не
// меняется, её контракт "рендерит ровно переданную width" остаётся прежним
// (см. TestRenderMessageCardNarrowWidthLineWidthsMatch), просто ей отдаётся
// уже вычисленная здесь, более узкая ширина.
func naturalCardWidth(msg auth.Message, maxHorizontalSpan int) int {
	if maxHorizontalSpan <= 0 {
		return 2
	}
	padW := 0
	if maxHorizontalSpan >= 3 {
		padW = 1
	}
	textWidth := max(1, maxHorizontalSpan-2*padW)
	wrapped := lipgloss.NewStyle().Width(textWidth).Render(msg.Text)
	longestBodyLine := 0
	for _, l := range strings.Split(wrapped, "\n") {
		// TrimRight(" ") — lipgloss.NewStyle().Width(n).Render(s) ДОПОЛНЯЕТ
		// каждую строку пробелами до n (проверено эмпирически), а не просто
		// переносит s без изменений — без обрезки lipgloss.Width(l) всегда
		// вернул бы textWidth целиком, даже для однословного "ок", и вся
		// идея "минимальной ширины" не работала бы (реальный баг, пойманный
		// именно так — до этого naturalCardWidth всегда давала maxHorizontalSpan).
		if w := lipgloss.Width(strings.TrimRight(l, " ")); w > longestBodyLine {
			longestBodyLine = w
		}
	}
	bodySpan := longestBodyLine + 2*padW

	sender := msg.SenderName
	if sender == "" {
		sender = "?"
	}
	gap := "  "
	timeText := time.Unix(msg.Date, 0).Local().Format("15:04")
	headerSpan := lipgloss.Width(sender) + lipgloss.Width(gap) + lipgloss.Width(timeText) + 2 // +2: минимум по 1 тире с каждого края метки

	return min(maxHorizontalSpan, max(bodySpan, headerSpan)) + 2 // +2 — рамка
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
// Settings.AlignOwnRight. selectedIdx — индекс сообщения, чья карточка
// выделяется курсором (двойная рамка, либо маркер "▸" в деградированном
// режиме).
//
// Второй возврат lineOffsets[i] — номер строки (считая с 0) внутри content,
// на которой начинается верхняя рамка (или, в деградированном режиме
// width<=0, строка-заголовок) сообщения msgs[i]. Нужен вызывающему коду,
// чтобы прокрутить viewport ровно к выбранному сообщению (см.
// rerenderMessagesAndScrollToCursor) — без этого пришлось бы заново парсить
// уже отрендеренный текст.
//
// Контент собирается построчно ([]string + strings.Join), а не вручную
// подсчётом добавленных '\n' — так lineOffsets не разъедутся с реальным
// разбиением на строки (тот же класс бага, что уже дважды ловили в этой
// функции).
func renderMessages(msgs []auth.Message, width int, alignOwnRight bool, selectedIdx int) (string, []int) {
	offsets := make([]int, len(msgs))
	var allLines []string

	if width <= 0 {
		for i, msg := range msgs {
			offsets[i] = len(allLines)
			sender := msg.SenderName
			if sender == "" {
				sender = "?"
			}
			color := messageColor(msg.IsOutgoing)
			nameStyle := lipgloss.NewStyle().Foreground(color)
			marker := ""
			if i == selectedIdx {
				// Тот же маркер курсора, что в foldersPane — деградированный
				// режим без рамок, других средств выделения нет.
				marker = "▸ "
			}
			header := marker + nameStyle.Render(sender) + "  " + timeStyle.Render(time.Unix(msg.Date, 0).Local().Format("15:04"))
			body := lipgloss.NewStyle().Foreground(color).Render(msg.Text)
			allLines = append(allLines, header)
			allLines = append(allLines, strings.Split(body, "\n")...)
			if i != len(msgs)-1 {
				allLines = append(allLines, "")
			}
		}
		return strings.Join(allLines, "\n"), offsets
	}

	for i, msg := range msgs {
		offsets[i] = len(allLines)
		// min(width, ...) — та же защита, что была раньше: naturalCardWidth
		// не должна давать cardWidth > width (тот же класс бага ширины, что
		// уже ловили 4 раза в этом проекте) — страховка на всякий случай,
		// сама naturalCardWidth уже клэмпит внутри себя.
		cardWidth := min(width, naturalCardWidth(msg, max(0, width-2)))
		rightAlign := alignOwnRight && msg.IsOutgoing
		card := renderMessageCard(msg, cardWidth, rightAlign, i == selectedIdx)
		if rightAlign {
			pad := strings.Repeat(" ", max(0, width-cardWidth))
			lines := strings.Split(card, "\n")
			for j, l := range lines {
				lines[j] = pad + l
			}
			card = strings.Join(lines, "\n")
		}
		allLines = append(allLines, strings.Split(card, "\n")...)
		if i != len(msgs)-1 {
			allLines = append(allLines, "")
		}
	}
	return strings.Join(allLines, "\n"), offsets
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
//
// scroll — необязательная пара (hasAbove, hasBelow): есть ли скрытый контент
// выше/ниже видимой области панели. Вариадик, а не обычные bool-параметры —
// чтобы существующие вызовы/тесты с 4 аргументами (без индикатора)
// компилировались без изменений; передаётся ровно 0 или 2 значения, другое
// количество (в т.ч. 1) трактуется как "нет данных о прокрутке" — индикатор
// не показывается. По прямому запросу человека: должно быть видно, когда
// прокрутка панели вообще доступна (например, при сжатии терминала по
// высоте) — см. scrollIndicatorSuffix.
func paneTitle(width, num int, text string, focused bool, scroll ...bool) string {
	color := inactiveBorderColor
	if focused {
		color = activeBorderColor
	}
	prefix := fmt.Sprintf("[%d] ", num)
	suffix := ""
	if len(scroll) == 2 {
		suffix = scrollIndicatorSuffix(scroll[0], scroll[1])
	}
	suffixW := runewidth.StringWidth(suffix)
	budget := max(0, width-runewidth.StringWidth(prefix)-suffixW)
	spaced := spaceOutRunes(strings.ToUpper(text), budget)
	padded := runewidth.FillRight(prefix+spaced, max(0, width-suffixW)) + suffix
	return lipgloss.NewStyle().Bold(true).Foreground(color).Width(width).Render(padded)
}

// scrollIndicatorSuffix — " ▲"/" ▼"/" ⇅" в конце заголовка панели: часть
// контента скрыта прокруткой выше/ниже видимой области. Пусто, если весь
// контент помещается целиком (индикатор должен ИСЧЕЗАТЬ, когда прокрутка не
// нужна, не просто становиться тусклым — по прямому запросу человека).
func scrollIndicatorSuffix(hasAbove, hasBelow bool) string {
	switch {
	case hasAbove && hasBelow:
		return " ⇅"
	case hasAbove:
		return " ▲"
	case hasBelow:
		return " ▼"
	default:
		return ""
	}
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
