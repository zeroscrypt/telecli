package tui

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
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
	chatsLimit    = 50
	messagesLimit = 50
	foldersPaneW  = 18 // ширина панели папок
	chatsPaneW    = 30
	// collapsedPaneContentW — ширина содержимого свёрнутой панели: "[N]" —
	// ровно 3 колонки: '[', цифра, ']'.
	collapsedPaneContentW = 3
	// collapsedPaneW — полная ширина свёрнутой панели: контент + рамка(2),
	// БЕЗ паддинга — свёрнутая панель рисуется через collapsedPaneBox, где
	// внутреннего отступа от рамки нет, узкая колонка не нуждается в нём
	// (0044, по прямому запросу человека — панель должна быть максимально
	// узкой).
	collapsedPaneW  = collapsedPaneContentW + 2 // рамка(2), без паддинга
	paneTitleHeight = 1                         // одна строка над каждой панелью — не часть рамки, не часть скроллящегося контента viewport
	statusReserve   = 1                         // строка статуса/ошибки внизу экрана
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
	// modeHelp — полноэкранный оверлей-справка (хоткей ShowHelp,
	// по умолчанию "h"), см. View/helpScreen. Из Normal, закрывается назад в
	// Normal по Esc или повторному ShowHelp — тот же принцип toggle, что и у
	// остальных модальных оверлеев (modeCommand/modeSearch).
	modeHelp
	// modeAbout — полноэкранный оверлей «о программе» (хоткей About,
	// по умолчанию "t"). Из Normal, закрывается назад в Normal по Esc или
	// повторному About — тот же принцип toggle, что и у modeHelp.
	modeAbout
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

// voiceFileMsg — результат скачивания файла голосового (auth.WaitForFileDownload).
type voiceFileMsg struct {
	fileID int32
	path   string
	err    error
}

// voicePlayFinishedMsg — внешний плеер (afplay/ffplay/mpv) завершился.
type voicePlayFinishedMsg struct {
	err error
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

// chatReadOutboxUpdateMsg — один апдейт из client.ChatReadOutboxUpdates(),
// последний прочитанный ID исходящего сообщения в чате. valid/closed — тот же
// контракт, что у chatReadInboxUpdateMsg.
type chatReadOutboxUpdateMsg struct {
	chatID                  int64
	lastReadOutboxMessageID int64
	valid                   bool
	closed                  bool
}

// chatTitleUpdateMsg — один апдейт из client.ChatTitleUpdates(), настоящее
// название чата. Для приватных чатов title приходит ПОСЛЕ асинхронного
// резолва собеседника — в момент getChat он мог быть пустым (см. задачу
// 0045); этот апдейт реактивно заполняет m.chats[i].Title. valid/closed —
// тот же контракт, что у chatReadOutboxUpdateMsg.
type chatTitleUpdateMsg struct {
	chatID int64
	title  string
	valid  bool
	closed bool
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

// aboutTickMsg — сообщение для анимации «бегущего блика» на экране «о программе».
// Содержит текущую колонку блика (0-based). Обрабатывается только в modeAbout.
type aboutTickMsg int

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

	messages       []auth.Message
	loadingMsgs    bool
	sendingMsg     bool
	sendingFile    bool
	playingVoice   bool            // true, пока идёт воспроизведение (голосовой файл скачан, плеер запущен)
	displayedChat  int64           // id чата, чья лента отображается/грузится
	chatReadOutbox map[int64]int64 // chatID -> last_read_outbox_message_id

	messageCursor int // индекс в m.messages: какое сообщение выбрано в focusMessages

	focus focus
	// foldersCollapsed/chatsCollapsed — панели 1 (папки) и 2 (чаты) свёрнуты
	// в узкую вертикальную колонку (переключается повторным нажатием своей
	// цифры при уже установленном фокусе). Панель 3 (сообщения) не
	// сворачивается никогда. Zero value false = развёрнуто.
	foldersCollapsed bool
	chatsCollapsed   bool
	viewport         viewport.Model
	// paneRowHeight — общий бюджет высоты строк для ВСЕХ ТРЁХ ПАНЕЛЕЙ
	// (папки/чаты/сообщения), одинаковый для всех. m.viewport.Height —
	// ОТДЕЛЬНОЕ поле: высота именно вьюпорта сообщений, обычно равна
	// paneRowHeight, но в Insert-режиме МЕНЬШЕ на кад единой рамки
	// (paneFrameV) и высоту поля ввода (см. applyLayout/composeCardHeight) —
	// поле встроено чёрной областью внутрь панели сообщений, а не занимает
	// отдельную область под всеми панелями (по правке человека).
	// foldersPane/chatPane/listContentRows используют paneRowHeight (они не
	// сжимаются в Insert-режиме).
	paneRowHeight int

	width, height int
	status        string

	version          string
	updateAvailable  string // "" — обновление не найдено/не проверялось; иначе — тег новой версии
	installingUpdate bool   // защита от повторного запуска установки

	mode         appMode
	keys         KeyMap
	settings     config.Settings
	theme        Theme
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

	// Состояние анимации «бегущего блика» на экране «о программе» (modeAbout).
	// aboutTickCol — текущая колонка блика в итоговом баннере (0-based).
	// Сбрасывается при входе в modeAbout, не переподписывается при выходе.
	aboutTickCol int

	// chatDrafts — несохранённый текст черновика КАЖДОГО открытого чата
	// (chatID -> текст), по прямому запросу человека (0047): у каждого чата
	// свой черновик, не один общий на всё приложение. Текст черновика
	// ТЕКУЩЕГО отображённого чата всегда живёт в m.composeInput; при
	// переключении отображаемого чата он сохраняется сюда, а из карты
	// загружается следующий (см. switchDisplayedChat). Не персистится на
	// диск — только in-memory на время сессии.
	chatDrafts map[int64]string
}

// New создаёт модель с пустым viewport: до первого tea.WindowSizeMsg у нас нет
// размеров терминала, их пересчитываем в Update. Рамка viewport задаётся через
// Style — сам viewport учитывает её при расчёте полезной области. Поля ввода
// создаются расфокусированными и фокусируются при входе в соответствующий режим.
// chromeInputStyles — сплошной чёрный фон листовых стилей однострочных полей
// нижней строки (командная строка, путь файла, поиск): сами поля живут на
// сплошном чёрном фоне нижней области (см. bottomLine/chromeBackground, 0039),
// а без фона на собственных листовых стилях их фрагменты (промпт, текст,
// плейсхолдер, хвостовая доливка до ширины) сбрасывали бы черноту собственными
// \x1b[0m — тот же механизм, что у карточек сообщений (см. messagePanelBg).
func chromeInputStyles(in textinput.Model) textinput.Model {
	in.PromptStyle = in.PromptStyle.Background(chromeBackground)
	in.TextStyle = in.TextStyle.Background(chromeBackground)
	in.PlaceholderStyle = in.PlaceholderStyle.Background(chromeBackground)
	in.CompletionStyle = in.CompletionStyle.Background(chromeBackground)
	in.Cursor.TextStyle = in.Cursor.TextStyle.Background(chromeBackground)
	return in
}

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
	theme := Themes[settings.Theme]
	if theme.Name == "" {
		theme = Themes[DefaultThemeName]
	}
	vp.Style = paneBorderStyle(false, theme, 3)

	commandInput := textinput.New()
	// ":" — фиксированный по вим-конвенции признак командной строки (см.
	// PLAN.md, архитектурные ограничения), отличающий её от строки черновика
	// в Insert-режиме (у той остаётся дефолтный "┃ " от textarea.New()).
	commandInput.Prompt = ":"
	commandInput = chromeInputStyles(commandInput)

	fileInput := textinput.New()
	// Промпт однострочного поля пути к файлу (режим modeFile, ctrl+f) —
	// отдельный от ":" командной строки и от черновика Insert-режима.
	fileInput.Prompt = "Файл: "
	fileInput = chromeInputStyles(fileInput)

	searchInput := textinput.New()
	searchInput.Prompt = "Поиск: "
	searchInput = chromeInputStyles(searchInput)

	composeInput := textarea.New()
	// Номера строк — дефолт редактора кода, для короткого черновика чата не нужны.
	composeInput.ShowLineNumbers = false
	// Base несёт ЧЁРНЫЙ фон хрома на все листовые стили черновика
	// (computedPrompt/Text/Placeholder/EndOfBuffer наследуют Base, а строка с
	// курсором CursorLine сохраняет собственный чёрный фон-выделение, свой фон
	// она не переопределяет) — поле ввода встроено в единую рамку панели как
	// область чистого chromeBackground (0047), а не живёт в своей карточке с
	// цветом панели; без фона на листовых стилях его фрагменты (промпт, текст,
	// хвостовая доливка до ширины) сбрасывали бы черноту \x1b[0m — тот же
	// механизм, что у карточек сообщений (см. messagePanelBg, 0039).
	composeInput.FocusedStyle.Base = composeInput.FocusedStyle.Base.Background(chromeBackground)
	composeInput.BlurredStyle.Base = composeInput.BlurredStyle.Base.Background(chromeBackground)
	// Enter зарезервирован под отправку (перехватывается в Update раньше, чем
	// дойдёт до composeInput.Update), перенос строки — ctrl+j. Дефолтный
	// InsertNewline ("enter"/"ctrl+m") противоречил бы этому — перебиндиваем,
	// чтобы KeyMap компонента отражал реальное поведение.
	composeInput.KeyMap.InsertNewline.SetKeys("ctrl+j")

	th := Themes[settings.Theme]
	if th.Name == "" {
		th = Themes[DefaultThemeName]
	}

	return Model{
		client:         client,
		ctx:            ctx,
		version:        version,
		viewport:       vp,
		keys:           newKeyMap(keys),
		settings:       settings,
		theme:          th,
		composeInput:   composeInput,
		commandInput:   commandInput,
		fileInput:      fileInput,
		searchInput:    searchInput,
		folderUnread:   make(map[int32]int32),
		chatReadOutbox: make(map[int64]int64),
		chatDrafts:     make(map[int64]string),
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
	m.viewport.Width = max(0, m.width-m.foldersPaneWidth()-m.chatsPaneWidth())
	m.viewport.Height = m.paneRowHeight
	if m.mode == modeInsert {
		// Единая рамка панели (paneBox в msgPane) «съедает» КАД панели целиком
		// (рамка+паддинг, paneFrameV), а чёрная область поля ввода — ещё
		// m.composeInput.Height() строк снизу внутренней области. Лента
		// сжимается ровно на это, так что сумма (высота ленты + высота чёрной
		// области) ТОЧНО равна внутренней высоте единой рамки
		// (paneRowHeight-paneFrameV) — тот же класс проверки, что уже ловил
		// баги в этом файле (см. msgPane, 0047).
		m.viewport.Height = max(0, m.paneRowHeight-paneFrameV-m.composeInput.Height())
	}
	m.commandInput.Width = max(0, m.width-lipgloss.Width(m.commandInput.Prompt)-1)
	m.fileInput.Width = max(0, m.width-lipgloss.Width(m.fileInput.Prompt)-1)
	m.searchInput.Width = max(0, m.width-lipgloss.Width(m.searchInput.Prompt)-1)
	// Ширина черновика — под внутреннюю ширину единой панели (минус кад:
	// рамка+паддинг внешнего paneBox), без отдельной рамки поля — та же, что
	// у ленты сообщений (см. msgPane/panelContentStyle, 0047).
	m.composeInput.SetWidth(max(0, m.viewport.Width-paneFrameH))
}

// refreshMessagesContent перерисовывает уже загруженную ленту сообщений под
// текущую m.viewport.Width — вызывать после ЛЮБОГО изменения, которое меняет
// эффективную ширину ленты (ресайз терминала, сворачивание/разворачивание
// боковых панелей — 0040), иначе контент остаётся "запечён" под старую
// ширину (0043).
func (m *Model) refreshMessagesContent() {
	if len(m.messages) == 0 {
		return
	}
	contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
	content, _ := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, m.messageCursor, m.theme, m.chatReadOutbox[m.displayedChat])
	m.viewport.SetContent(content)
}

// toggleHelp переключает режим modeHelp <-> modeNormal. Вызывается
// и по хоткею ShowHelp (клавиша 't' в Normal), и по команде :help
// в командной строке — общая логика для избежания дублирования.
func (m *Model) toggleHelp() {
	if m.mode == modeHelp {
		m.mode = modeNormal
	} else {
		m.mode = modeHelp
	}
	m.applyLayout()
}

// toggleAbout переключает режим modeAbout <-> modeNormal. Вызывается
// по хоткею About (клавиша 't' в Normal). Зеркалирует логику toggleHelp.
// При входе в modeAbout запускает первый тик анимации и возвращает его
// команду; при выходе команда nil — следующий тик не планируется, анимация
// естественно прекращается.
func (m *Model) toggleAbout() tea.Cmd {
	if m.mode == modeAbout {
		m.mode = modeNormal
	} else {
		m.mode = modeAbout
		m.aboutTickCol = 0
	}
	m.applyLayout()
	if m.mode == modeAbout {
		return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
			return aboutTickMsg(0)
		})
	}
	return nil
}

// composeCardHeight — высота области поля ввода внутри единой рамки панели:
// просто высота самого поля (рамки у поля больше нет — см. msgPane, 0047).
func (m Model) composeCardHeight() int {
	return m.composeInput.Height()
}

// composePlain — поле ввода как часть единой рамки панели (см. msgPane):
// прямоугольник чистого chromeBackground шириной feedW и высотой поля ввода.
// composeInput.View() уже несёт chromeBackground на каждой своей строке (стили
// Base, см. New), но фон внешнего стиля не переживает вложенных \x1b[0m (тот
// же механизм, что у bgFill/chromeLine, 0039) — оставшиеся пустые колонки и
// строки доливаем bgFill'ом явно, не полагаясь на протекание.
func (m Model) composePlain(feedW int) string {
	w := max(0, feedW)
	lines := strings.Split(m.composeInput.View(), "\n")
	for i, l := range lines {
		if d := max(0, w-lipgloss.Width(l)); d > 0 {
			lines[i] = l + bgFill(chromeBackground, d)
		}
	}
	for len(lines) < m.composeInput.Height() {
		lines = append(lines, bgFill(chromeBackground, w))
	}
	return strings.Join(lines, "\n")
}

// feedContentWidth — внутренняя ширина панели сообщений (ширина панели минус
// кад paneBox/paneBorderStyle: рамка+паддинг). Это ширина контента ленты И
// области поля ввода внутри единой рамки панели (см. msgPane/applyLayout,
// 0047). В обоих режимах кад одинаков (frameSize панельного стиля = 4 колонки),
// поэтому формула не зависит от режима.
func (m Model) feedContentWidth() int {
	return max(0, m.viewport.Width-paneFrameH)
}

// feedPlaceholder — текст-заглушка («Загрузка…»/«Пока нет сообщений») как
// контент области ленты внутри единой рамки панели: без отдельной рамки, фон
// панели сообщений, ровно feedW×feedH (см. msgPane, 0047).
func feedPlaceholder(feedW, feedH int, text string) string {
	return lipgloss.NewStyle().
		Background(messagePanelBg()).
		Width(max(0, feedW)).
		Height(max(0, feedH)).
		Render(text)
}

// switchDisplayedChat переключает m.displayedChat на newChatID: сохраняет
// черновик ТЕКУЩЕГО чата в m.chatDrafts, затем загружает черновик нового чата
// в m.composeInput (пусто, если черновика нет) — по прямому запросу человека,
// у каждого открытого чата свой черновик, не общий на всё приложение (0047).
// No-op, если newChatID уже текущий (защита от повторного вызова — например,
// messagesLoadedMsg подтверждает то же значение, что уже выставил обработчик
// выбора чата чуть раньше).
func (m *Model) switchDisplayedChat(newChatID int64) {
	if newChatID == m.displayedChat {
		return
	}
	if m.displayedChat != 0 {
		m.chatDrafts[m.displayedChat] = m.composeInput.Value()
	}
	m.displayedChat = newChatID
	m.composeInput.SetValue(m.chatDrafts[newChatID]) // "" для чата без черновика или newChatID==0
	m.syncComposeHeight()
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
	content, offsets := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, m.messageCursor, m.theme, m.chatReadOutbox[m.displayedChat])
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
		m.waitForChatReadInboxUpdate(), m.waitForChatReadOutboxUpdate(),
		m.waitForChatTitleUpdate(),
		m.waitForUnreadCountUpdate(), m.waitForUnreadChatCountUpdate(),
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
		m.refreshMessagesContent()
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
		// Заполняем карту last_read_outbox_message_id из списка чатов.
		for _, chat := range msg.chats {
			m.chatReadOutbox[chat.ID] = chat.LastReadOutboxMessageID
		}
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
		m.switchDisplayedChat(msg.chatID) // no-op: значение уже выставлено обработчиком выбора чата
		m.messages = msg.messages
		m.messageCursor = max(0, len(m.messages)-1) // курсор всегда синхронизирован с последним сообщением (см. п.5)
		contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
		content, _ := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, m.messageCursor, m.theme, m.chatReadOutbox[m.displayedChat])
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
			content, _ := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, m.messageCursor, m.theme, m.chatReadOutbox[m.displayedChat])
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
			content, _ := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, m.messageCursor, m.theme, m.chatReadOutbox[m.displayedChat])
			m.viewport.SetContent(content)
			m.viewport.GotoBottom()
		}
		m.status = ""
		m.fileInput.SetValue("")
		m.fileInput.Blur()
		m.mode = modeNormal
		m.applyLayout()
		return m, nil

	case voiceFileMsg:
		if msg.err != nil {
			m.status = fmt.Sprintf("Ошибка скачивания голосового: %v", msg.err)
			return m, nil
		}
		m.status = ""
		m.playingVoice = true
		return m, m.playProcessCmd(msg.path)

	case voicePlayFinishedMsg:
		m.playingVoice = false
		if msg.err != nil {
			m.status = fmt.Sprintf("Ошибка воспроизведения: %v", msg.err)
		}
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
			m.switchDisplayedChat(0)
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
			content, _ := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, m.messageCursor, m.theme, m.chatReadOutbox[m.displayedChat])
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

	case chatReadOutboxUpdateMsg:
		if msg.closed {
			return m, nil
		}
		if !msg.valid {
			return m, m.waitForChatReadOutboxUpdate() // нераспознанный апдейт — переподписка обязательна
		}
		m.chatReadOutbox[msg.chatID] = msg.lastReadOutboxMessageID
		// Если это текущий открытый чат — перерисуем ленту, чтобы обновить глифы.
		if msg.chatID == m.displayedChat {
			contentWidth := max(0, m.viewport.Width-m.viewport.Style.GetHorizontalFrameSize())
			content, _ := renderMessages(m.messages, contentWidth, m.settings.AlignOwnRight, m.messageCursor, m.theme, m.chatReadOutbox[m.displayedChat])
			m.viewport.SetContent(content)
		}
		return m, m.waitForChatReadOutboxUpdate()

	case chatTitleUpdateMsg:
		if msg.closed {
			return m, nil
		}
		if !msg.valid {
			return m, m.waitForChatTitleUpdate() // нераспознанный апдейт — переподписка обязательна
		}
		for i := range m.chats {
			if m.chats[i].ID == msg.chatID {
				m.chats[i].Title = msg.title
				break
			}
		}
		return m, m.waitForChatTitleUpdate()

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

	case aboutTickMsg:
		// Анимация «бегущего блика» — работает только в modeAbout.
		// При выходе из modeAbout переподписка не происходит (тик естественно
		// прекращается): следующий tea.Tick планируем только из modeAbout.
		if m.mode == modeAbout {
			m.aboutTickCol = int(msg)
			nextCol := int(msg) + 1
			return m, tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg {
				return aboutTickMsg(nextCol)
			})
		}
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
				case "help":
					m.toggleHelp()
					return m, nil
				case "theme":
					m.status = "Укажите имя темы: :theme <имя>"
					return m, nil
				case "":
					return m, nil
				}
				// :theme <name> — парсим аргумент команды
				if strings.HasPrefix(cmdText, "theme ") {
					themeName := strings.TrimSpace(strings.TrimPrefix(cmdText, "theme"))
					if themeName == "" {
						m.status = "Укажите имя темы: :theme <имя>"
						return m, nil
					}
					if _, ok := Themes[themeName]; !ok {
						m.status = fmt.Sprintf("Неизвестная тема: %s. Доступные: %s", themeName, strings.Join(themeNames(), ", "))
						return m, nil
					}
					m.theme = Themes[themeName]
					m.settings.Theme = themeName
					m.status = fmt.Sprintf("Тема изменена на: %s (до перезапуска; чтобы сохранить — впишите theme = %q в settings.toml)", themeName, themeName)
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
		case modeAbout:
			// Оверлей «о программе» — закрывается по Esc или повторному About.
			// translateLayout нужен по той же причине, что и в modeHelp.
			if msg.Type == tea.KeyEsc || key.Matches(translateLayout(msg), m.keys.About) {
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
			m.toggleHelp()
			return m, nil
		}
		if key.Matches(msg, m.keys.About) {
			cmd := m.toggleAbout()
			return m, cmd
		}
		if key.Matches(msg, m.keys.PlayVoice) {
			if m.focus != focusMessages {
				m.status = "воспроизведение голосового — только в панели сообщений"
				return m, nil
			}
			if len(m.messages) == 0 || m.messageCursor < 0 || m.messageCursor >= len(m.messages) {
				m.status = "нет сообщения под курсором"
				return m, nil
			}
			if !m.messages[m.messageCursor].IsVoiceNote {
				m.status = "под курсором не голосовое сообщение"
				return m, nil
			}
			return m, m.playVoiceCmd(m.messages[m.messageCursor])
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
			if m.focus == focusFolders {
				m.foldersCollapsed = !m.foldersCollapsed
			} else {
				m.focus = focusFolders
				m.foldersCollapsed = false
			}
			m.applyLayout()            // ширина панелей изменилась — пересчитать m.viewport.Width
			m.refreshMessagesContent() // контент ленты перерисовать под новую ширину (0043)
			return m, nil
		}
		if key.Matches(msg, m.keys.FocusPane2) {
			if m.focus == focusChats {
				m.chatsCollapsed = !m.chatsCollapsed
			} else {
				m.focus = focusChats
				m.chatsCollapsed = false
			}
			m.applyLayout()            // ширина панелей изменилась — пересчитать m.viewport.Width
			m.refreshMessagesContent() // контент ленты перерисовать под новую ширину (0043)
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
				m.switchDisplayedChat(0)
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
						m.switchDisplayedChat(chat.ID)
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
					m.switchDisplayedChat(chat.ID)
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

// waitForChatReadOutboxUpdate — tea.Cmd, блокирующийся на ОДНОМ значении из
// client.ChatReadOutboxUpdates(). Срабатывает один раз за вызов — обязательна
// переподписка в обработчике (тот же класс ловушки, что у waitForMessageUpdate).
func (m Model) waitForChatReadOutboxUpdate() tea.Cmd {
	ch := m.client.ChatReadOutboxUpdates()
	ctx := m.ctx
	return func() tea.Msg {
		select {
		case upd, ok := <-ch:
			if !ok {
				return chatReadOutboxUpdateMsg{closed: true}
			}
			chatID, lastReadOutboxMessageID, valid := auth.ParseChatReadOutboxUpdate(upd)
			if !valid {
				return chatReadOutboxUpdateMsg{} // безвредно проигнорируется, но переподписка продолжится
			}
			return chatReadOutboxUpdateMsg{chatID: chatID, lastReadOutboxMessageID: lastReadOutboxMessageID, valid: true}
		case <-ctx.Done():
			return chatReadOutboxUpdateMsg{closed: true}
		}
	}
}

// waitForChatTitleUpdate — tea.Cmd, блокирующийся на ОДНОМ значении из
// client.ChatTitleUpdates(). Срабатывает один раз за вызов — обязательна
// переподписка в обработчике (тот же класс ловушки, что у
// waitForChatReadOutboxUpdate).
func (m Model) waitForChatTitleUpdate() tea.Cmd {
	ch := m.client.ChatTitleUpdates()
	ctx := m.ctx
	return func() tea.Msg {
		select {
		case upd, ok := <-ch:
			if !ok {
				return chatTitleUpdateMsg{closed: true}
			}
			chatID, title, valid := auth.ParseChatTitleUpdate(upd)
			if !valid {
				return chatTitleUpdateMsg{} // безвредно проигнорируется, но переподписка продолжится
			}
			return chatTitleUpdateMsg{chatID: chatID, title: title, valid: true}
		case <-ctx.Done():
			return chatTitleUpdateMsg{closed: true}
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

// playVoiceCmd — шаг скачивания: downloadFile + ожидание готового пути.
// Тот же паттерн захвата m.ctx/m.client, что у sendFileCmd/sendMessageCmd.
// После готового пути — ещё короткая проверка размера файла на диске
// (waitForVoiceFileReady): TDLib может пометить скачивание завершённым, пока
// последние байты дописываются, и плеер прочитает обрезанный файл (задача
// 0042, обрыв голосового через ~2с). Выполняется в goroutine команды, не
// блокируя цикл сообщений TUI.
func (m Model) playVoiceCmd(msg auth.Message) tea.Cmd {
	ctx := m.ctx
	client := m.client
	fileID := msg.VoiceFileID
	voiceSize := msg.VoiceSize
	return func() tea.Msg {
		// 60с — разумный потолок для сети; дольше файл не скачивается в норме.
		dlCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
		path, err := auth.WaitForFileDownload(dlCtx, client, fileID)
		if err == nil {
			waitForVoiceFileReady(path, voiceSize)
		}
		return voiceFileMsg{fileID: fileID, path: path, err: err}
	}
}

// playProcessCmd — запуск внешнего плеера через tea.ExecProcess (паттерн из
// задачи 0007, удалённой в 0033 — терминал на время уходит в child-режим).
func (m Model) playProcessCmd(path string) tea.Cmd {
	player, args, err := resolvePlayer(runtime.GOOS)
	if err != nil {
		return func() tea.Msg { return voicePlayFinishedMsg{err: err} }
	}
	cmd := exec.Command(player, append(args, path)...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return voicePlayFinishedMsg{err: err}
	})
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
	if m.mode == modeAbout {
		return m.aboutScreen()
	}
	fStart, fEnd := visibleWindow(len(m.folders)+1, m.folderCursor, m.listContentRows())
	cStart, cEnd := visibleWindow(m.chatListLen(), m.chatListCursor(), m.listContentRows())
	foldersTitle := paneTitle(m.foldersPaneWidth(), 1, "Папки", m.focus == focusFolders, m.theme, fStart > 0, fEnd < len(m.folders)+1)
	if m.foldersCollapsed {
		// Свёрнутая панель: в заголовке только "[N]" без названия и без
		// scroll-индикатора (название целиком уходит в тело панели по буквам).
		foldersTitle = paneTitle(m.foldersPaneWidth(), 1, "", m.focus == focusFolders, m.theme)
	}
	chatsTitle := paneTitle(m.chatsPaneWidth(), 2, "Чаты", m.focus == focusChats, m.theme, cStart > 0, cEnd < m.chatListLen())
	if m.chatsCollapsed {
		chatsTitle = paneTitle(m.chatsPaneWidth(), 2, "", m.focus == focusChats, m.theme)
	}
	titles := lipgloss.JoinHorizontal(lipgloss.Top,
		foldersTitle,
		chatsTitle,
		paneTitle(m.viewport.Width, 3, m.currentChatTitle(), m.focus == focusMessages, m.theme, !m.viewport.AtTop(), !m.viewport.AtBottom()),
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
	sectionStyle := lipgloss.NewStyle().Bold(true).Foreground(m.theme.ActiveBorderColor)
	// Каждый листовой стиль несёт chromeBackground (0042): строки здесь
	// склеиваются из НЕСКОЛЬКИХ Render(...)-фрагментов подряд (заголовок,
	// keyLine) — фон внешнего стиля (paneBox/paneBorderStyle, paneNum=0)
	// переживает только первый вложенный \x1b[0m, дальше по строке нужны дыры.
	// Однофрагментные строки (sectionStyle, голый текст) фона не требуют — их
	// закрывает внешний стиль окна.
	descStyle := lipgloss.NewStyle().Faint(true).Background(chromeBackground)
	keyLine := func(key, desc string) string {
		return "  " + hintKeyStyle(m.theme).Background(chromeBackground).Render(runewidth.FillRight(key, 10)) + descStyle.Render(desc)
	}

	lines := []string{
		lipgloss.NewStyle().Bold(true).Background(chromeBackground).Render("TELECLi") + descStyle.Render(" — терминальный клиент Telegram ("+m.version+")"),
		"Vim-модальный интерфейс: три панели (папки, чаты, сообщения), навигация с клавиатуры.",
		"",
		sectionStyle.Render("НАВИГАЦИЯ (Normal)"),
		keyLine("tab", "переключить панель (циклически)"),
		keyLine("j/k/↑↓", "курсор вверх/вниз в активной панели"),
		keyLine("←/→", "фокус влево/вправо (без зацикливания)"),
		keyLine("1/2/3", "прямой переход к панели: папки/чаты/сообщения"),
		keyLine("enter", "открыть чат / выбрать папку"),
		keyLine("i", "ввод сообщения (нужен выбранный чат)"),
		keyLine(":", "командная строка"),
		keyLine("/", "поиск чатов/каналов/контактов"),
		keyLine("ctrl+f", "отправить файл (ввод пути)"),
		keyLine("p", "воспроизвести голосовое под курсором"),
		keyLine("d", "покинуть/удалить чат под курсором — далее y/Y подтвердить, любая другая клавиша/esc отменить"),
		keyLine("h", "это окно (то же самое, что :help)"),
		keyLine("t", "экран «о программе» — логотип TELECLi, версия, ссылка, автор"),
		keyLine("q", "выход"),
		keyLine("ctrl+c", "аварийный выход из любого режима (работает всегда, не переопределяется)"),
		"",
		sectionStyle.Render("ВВОД СООБЩЕНИЯ (Insert)"),
		keyLine("enter", "отправить"),
		keyLine("ctrl+j", "перенос строки (поле растёт вниз; не Shift+Enter — см. README, там же почему)"),
		keyLine("esc", "отмена, назад в Normal"),
		"",
		sectionStyle.Render("КОМАНДНАЯ СТРОКА (:)"),
		keyLine(":q", "выход (тоже :quit)"),
		keyLine(":help", "показать это окно (то же самое, что h)"),
		keyLine(":theme <имя>", "сменить тему интерфейса на текущий запуск"),
		"",
		descStyle.Render("  Доступные темы: " + strings.Join(themeNames(), ", ") + " (сейчас: " + m.theme.Name + ")."),
		descStyle.Render("  :theme меняет только текущую сессию — не пишет в settings.toml. Чтобы тема"),
		descStyle.Render("  осталась по умолчанию при следующем запуске, впишите theme = \"имя\" в файл сами."),
		"",
		sectionStyle.Render("ОБНОВЛЕНИЯ"),
		descStyle.Render("  При старте telecli тихо проверяет в фоне, нет ли версии новее (GitHub"),
		descStyle.Render("  Releases этого репозитория) — если есть, справа в нижней строке появится"),
		descStyle.Render("  vX.Y.Z → vX.Y.Z+1 (:update). Молчание не значит ошибку — сеть могла быть"),
		descStyle.Render("  недоступна, фоновая проверка её никак не показывает (в отличие от явной ниже)."),
		"",
		keyLine(":update", "проверить явно — статус покажет: доступна версия / уже последняя / ошибка сети"),
		keyLine(":update install", "скачать подходящий бинарник и заменить им текущий файл на диске"),
		"",
		descStyle.Render("  :update install сама сначала делает то же, что :update — новую версию не"),
		descStyle.Render("  нужно проверять отдельно. Замена атомарна: если скачивание оборвётся на"),
		descStyle.Render("  середине, рабочий файл останется нетронутым, ошибка — в статусе. Файл на"),
		descStyle.Render("  диске меняется СРАЗУ, но уже запущенный процесс работает со старым кодом в"),
		descStyle.Render("  памяти — новая версия начнёт действовать после выхода (q/ctrl+c) и"),
		descStyle.Render("  повторного запуска telecli."),
		descStyle.Render("  Готовые бинарники — только для macOS arm64 и Linux amd64. На других"),
		descStyle.Render("  платформах :update install покажет ошибку со ссылкой на релиз — там"),
		descStyle.Render("  обновляются пересборкой из исходников (см. README, Option C)."),
		"",
		sectionStyle.Render("КОНФИГУРАЦИЯ"),
		descStyle.Render("  <config dir>/telecli/ — config.toml (доступ к Telegram), keybindings.toml"),
		descStyle.Render("  (горячие клавиши), settings.toml (тема, выравнивание своих сообщений)."),
		"",
		descStyle.Render("  Лицензия MIT · github.com/zeroscrypt/telecli"),
	}

	// footer — подсказка закрытия ("Esc / t — закрыть"), ВСЕГДА последняя
	// видимая строка, даже если остальной контент пришлось обрезать снизу
	// (см. maxRows ниже) — иначе на низком терминале человек не увидит, чем
	// закрыть оверлей.
	footer := descStyle.Render("Esc / t — закрыть")

	borderRows := paneBorderStyle(true, m.theme, 0).GetVerticalBorderSize()
	maxRows := max(0, m.height-borderRows-2*panePaddingV)
	switch {
	case maxRows <= 0:
		lines = nil
	case len(lines)+1 > maxRows: // +1 — место под footer
		lines = append(lines[:maxRows-1], footer)
	default:
		lines = append(lines, "", footer)
	}

	return paneBox(m.width, m.height, strings.Join(lines, "\n"), true, m.theme, 0)
}

// aboutScreen — полноэкранный оверлей «о программе» (modeAbout):
// крупный анимированный ASCII-логотип TELECLi, описание, версия, ссылка,
// автор, благодарность. Анимация — «бегущий блик» по вертикальным колонкам
// логотипа (акцентный цвет темы t.ActiveBorderColor, остальное — t.OwnColor).
func (m Model) aboutScreen() string {
	// Тот же принцип «фон на каждом листовом стиле», что в helpScreen (0042):
	// сейчас все строки однофрагментные и их закрывает внешний фон окна, но
	// единообразие защищает от будущего многофрагментного добавления.
	descStyle := lipgloss.NewStyle().Faint(true).Background(chromeBackground)

	// Генерируем ASCII-логотип с анимацией блика. m.aboutTickCol — текущая
	// колонка блика (0-based), растёт на 1 каждый тик; renderAboutBanner
	// зацикливает её по ширине баннера (взятие модуля).
	bannerLines := m.renderAboutBanner(m.aboutTickCol)

	lines := []string{}
	lines = append(lines, bannerLines...)
	lines = append(lines, "")
	lines = append(lines, descStyle.Render("Терминальный клиент Telegram с vim-подобной модальностью ввода"))
	lines = append(lines, "")
	lines = append(lines, descStyle.Render("Версия: "+m.version))
	lines = append(lines, descStyle.Render("GitHub: github.com/zeroscrypt/telecli"))
	lines = append(lines, descStyle.Render("Автор: @zeroscrypt"))
	lines = append(lines, descStyle.Render("Благодарность: @hakatao"))
	lines = append(lines, "")

	// footer — подсказка закрытия ("Esc / t — закрыть"), ВСЕГДА последняя
	// видимая строка, даже если остальной контент пришлось обрезать снизу.
	footer := descStyle.Render("Esc / t — закрыть")

	borderRows := paneBorderStyle(true, m.theme, 0).GetVerticalBorderSize()
	maxRows := max(0, m.height-borderRows-2*panePaddingV)
	switch {
	case maxRows <= 0:
		lines = nil
	case len(lines)+1 > maxRows: // +1 — место под footer
		lines = append(lines[:maxRows-1], footer)
	default:
		lines = append(lines, "", footer)
	}

	return paneBox(m.width, m.height, strings.Join(lines, "\n"), true, m.theme, 0)
}

// renderAboutBanner генерирует ASCII-баннер TELECLi с анимацией «бегущего блика».
// tickCol — текущая колонка блика (0-based). Возвращает 6 строк баннера.
// Логотип: 5 колонок на букву, 1 пробел между буквами. Слово TELECLI = 7 букв.
// Уникальные глифы: T, E, L, C, I (E и L повторяются).
// Цвета: колонка блика — t.ActiveBorderColor, остальные закрашенные — t.OwnColor.
func (m Model) renderAboutBanner(tickCol int) []string {
	// Глифы 5x6 (# → закрашено, . → пусто)
	glyphs := map[rune][]string{
		'T': {
			"#####",
			"..#..",
			"..#..",
			"..#..",
			"..#..",
			"..#..",
		},
		'E': {
			"#####",
			"#....",
			"####.",
			"#....",
			"#....",
			"#####",
		},
		'L': {
			"#....",
			"#....",
			"#....",
			"#....",
			"#....",
			"#####",
		},
		'C': {
			".####",
			"#....",
			"#....",
			"#....",
			"#....",
			".####",
		},
		'I': {
			"#####",
			"..#..",
			"..#..",
			"..#..",
			"..#..",
			"#####",
		},
	}

	// Слово TELECLI: T-E-L-E-C-L-I
	letters := []rune{'T', 'E', 'L', 'E', 'C', 'L', 'I'}

	// Собираем 6 строк баннера (построчно)
	const glyphW = 5
	const glyphH = 6
	const gap = 1
	totalCols := len(letters)*glyphW + (len(letters)-1)*gap

	// Сначала собираем «сырые» строки: для каждой ячейки знаем, закрашена ли она
	// rawLines[row][col] = true если закрашено
	rawLines := make([][]bool, glyphH)
	for r := 0; r < glyphH; r++ {
		rawLines[r] = make([]bool, totalCols)
		colIdx := 0
		for li, ch := range letters {
			g := glyphs[ch]
			for c := 0; c < glyphW; c++ {
				if g[r][c] == '#' {
					rawLines[r][colIdx] = true
				}
				colIdx++
			}
			if li < len(letters)-1 {
				colIdx += gap // пробел между буквами
			}
		}
	}

	// Теперь рендерим с цветами: колонка блика — ActiveBorderColor, остальные — OwnColor
	// tickCol может быть больше totalCols — берём модуль
	highlightCol := tickCol % totalCols
	if highlightCol < 0 {
		highlightCol = 0
	}

	accentStyle := lipgloss.NewStyle().Foreground(m.theme.ActiveBorderColor).Bold(true).Background(chromeBackground)
	baseStyle := lipgloss.NewStyle().Foreground(m.theme.OwnColor).Bold(true).Background(chromeBackground)

	var result []string
	for r := 0; r < glyphH; r++ {
		var sb strings.Builder
		for c := 0; c < totalCols; c++ {
			if rawLines[r][c] {
				if c == highlightCol {
					sb.WriteString(accentStyle.Render("█"))
				} else {
					sb.WriteString(baseStyle.Render("█"))
				}
			} else {
				// Пустая клетка баннера — тоже на фоне хрома (0042): голый пробел
				// после последнего \x1b[0m не нёс бы фона, и хвост строки баннера
				// оставался незакрашенным.
				sb.WriteString(bgFill(chromeBackground, 1))
			}
		}
		result = append(result, sb.String())
	}
	return result
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
func telecliLogo(t Theme) string {
	tPart := lipgloss.NewStyle().Background(t.ActiveBorderColor).Foreground(lipgloss.Color("#FFFFFF")).
		Bold(true).Render(" T")
	restPart := lipgloss.NewStyle().Background(t.ActiveBorderColor).Foreground(pillTextColor).
		Bold(true).Render("ELECLi ")
	return tPart + restPart
}

// playbackHintSuffix — индикатор идущего воспроизведения для нижней строки.
func playbackHintSuffix(playing bool) string {
	if !playing {
		return ""
	}
	return " · ▶ воспроизведение"
}

// bottomLine — нижняя область зарезервированной высоты: командная строка,
// многострочное поле черновика с подсказкой или статус/индикатор режима.
func (m Model) bottomLine() string {
	switch m.mode {
	case modeCommand:
		return chromeLine(m.width, m.commandInput.View())
	case modeFile:
		hint := ""
		if m.sendingFile {
			hint = " (отправка…)"
		}
		return chromeLine(m.width, m.fileInput.View()+chromeText(hint))
	case modeSearch:
		hint := ""
		if m.searchingNow {
			hint = " (поиск…)"
		}
		return chromeLine(m.width, m.searchInput.View()+chromeText(hint))
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
		logo := telecliLogo(m.theme)
		modeTag := lipgloss.NewStyle().Foreground(m.theme.ChatSelectionColor).Bold(true).Background(chromeBackground).Render(" INP")
		return chromeLine(m.width, logo+modeTag+lipgloss.NewStyle().Faint(true).Background(chromeBackground).Render(hint))
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
		return chromeLine(m.width, lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true).Background(chromeBackground).Render(prompt))
	default:
		if m.status != "" {
			return chromeLine(m.width, lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Background(chromeBackground).Render(m.status))
		}
		logo := telecliLogo(m.theme)
		modeTag := lipgloss.NewStyle().Foreground(m.theme.ActiveBorderColor).Bold(true).Background(chromeBackground).Render(" NAV")
		// Сначала — общие хоткеи (работают при любом фокусе), затем — контекстные
		// для панели, которая сейчас в фокусе (по прямому запросу человека).
		pairs := [][2]string{
			{"tab", "панели"},
			{"←/→", "фокус"},
			{"j/k/↑↓", "курсор"},
			{"i", "ввод"},
			{":", "команда"},
			{"h", "справка"},
			{"q", "выход"},
		}
		switch m.focus {
		case focusChats:
			pairs = append(pairs, [2]string{"/", "поиск"}, [2]string{"d", "удалить чат"})
		case focusMessages:
			pairs = append(pairs, [2]string{"ctrl+f", "файл"}, [2]string{"p", "голосовое"})
		}
		hint := bgFill(chromeBackground, 1) + renderHint(" · ", m.theme, pairs...) + chromeText(playbackHintSuffix(m.playingVoice && m.mode == modeNormal))
		left := logo + modeTag + hint

		versionText := m.version
		if m.updateAvailable != "" {
			versionText = fmt.Sprintf("%s → %s (:update)", m.version, m.updateAvailable)
		}
		versionRendered := lipgloss.NewStyle().Faint(true).Background(chromeBackground).Render(versionText)

		pad := m.width - lipgloss.Width(left) - lipgloss.Width(versionRendered)
		if pad < 1 {
			// Не помещается рядом с версией на узком терминале — показываем
			// только левую часть (пилюля+подсказка), не ломаем раскладку.
			return chromeLine(m.width, left)
		}
		return chromeLine(m.width, left+bgFill(chromeBackground, pad)+versionRendered)
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
	borderRows := paneBorderStyle(false, m.theme, 0).GetVerticalBorderSize()
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

// foldersPaneWidth — эффективная ширина панели папок: свёрнутая панель
// рисуется узкой вертикальной колонкой (collapsedPaneW), развёрнутая —
// обычной шириной foldersPaneW.
func (m Model) foldersPaneWidth() int {
	if m.foldersCollapsed {
		return collapsedPaneW
	}
	return foldersPaneW
}

// chatsPaneWidth — эффективная ширина панели чатов: свёрнутая панель рисуется
// узкой вертикальной колонкой (collapsedPaneW), развёрнутая — обычной
// шириной chatsPaneW.
func (m Model) chatsPaneWidth() int {
	if m.chatsCollapsed {
		return collapsedPaneW
	}
	return chatsPaneW
}

// collapsedPaneBody — тело свёрнутой панели: пустая строка (продолжение "[N]"
// из заголовка над панелью), затем название капсом по одной букве на строку.
// Если высоты не хватает (contentRows < 1+len(letters)) — лишние буквы с
// конца названия обрезаются молча, без "…" (вертикальное многоточие не
// умещается по смыслу так же, как горизонтальное в paneTitle).
func collapsedPaneBody(name string, contentRows int) string {
	if contentRows <= 0 {
		return ""
	}
	letters := []rune(strings.ToUpper(name))
	lines := make([]string, 0, 1+len(letters))
	lines = append(lines, "") // пустая строка сразу после "[N]" в заголовке
	for _, r := range letters {
		if len(lines) >= contentRows {
			break
		}
		lines = append(lines, string(r))
	}
	// Каждая строка рендерится в ширину collapsedPaneContentW тем же приёмом,
	// что в foldersPane/chatPane (Width на КАЖДУЮ строку до strings.Join) —
	// без него строки разной ширины внутри рамки смотрелись бы рваными.
	// Align(Center) — буква ровно посередине трёх колонок (" Ч "), а не
	// прижата к левому краю (0046, по запросу человека).
	rendered := make([]string, len(lines))
	for i, line := range lines {
		rendered[i] = lipgloss.NewStyle().Width(collapsedPaneContentW).Align(lipgloss.Center).Render(line)
	}
	return strings.Join(rendered, "\n")
}

// collapsedPaneBox — рамка свёрнутой панели БЕЗ внутреннего паддинга (в
// отличие от paneBox/paneBorderStyle, общих для развёрнутых панелей) — узкая
// колонка не нуждается в отступе от рамки, контент ("[N]"/буквы) и так
// занимает всю доступную ширину (0044, по прямому запросу человека —
// свёрнутая панель должна быть МАКСИМАЛЬНО узкой).
func collapsedPaneBox(width, height int, content string, focused bool, t Theme, paneNum int) string {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.InactiveBorderColor).
		BorderBackground(chromeBackground)
	if focused {
		style = style.Border(lipgloss.DoubleBorder()).BorderForeground(t.ActiveBorderColor)
	}
	if paneNum >= 1 {
		style = style.Background(GetPanelBackground(paneNum))
	}
	return style.
		Width(max(0, width-style.GetHorizontalBorderSize())).
		Height(max(0, height-style.GetVerticalBorderSize())).
		Render(content)
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
	if m.foldersCollapsed {
		body := collapsedPaneBody("Папки", m.listContentRows())
		return collapsedPaneBox(m.foldersPaneWidth(), m.paneRowHeight, body, m.focus == focusFolders, m.theme, 1)
	}
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

	contentW := m.foldersPaneWidth() - 2 - 2*panePaddingH // минус рамка (2) и паддинг (2*panePaddingH), тот же приём, что в chatPane
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
	return paneBox(m.foldersPaneWidth(), m.paneRowHeight, strings.TrimRight(sb.String(), "\n"), m.focus == focusFolders, m.theme, 1)
}

// renderCursorList — общий рендер списка строк с курсором-пилюлей
// (chatSelectionColor), используется и для обычного списка чатов, и для
// результатов поиска (та же визуальная семантика "это список, можно
// выбрать"). badges — бейдж непрочитанных для каждой строки (прижат к
// правому краю, см. alignBadge), может быть короче labels или nil —
// недостающие элементы трактуются как "без бейджа" (результаты поиска их
// не имеют).
func renderCursorList(labels []string, badges []string, cursor int, contentW int, t Theme) string {
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
			line = lipgloss.NewStyle().Background(t.ChatSelectionColor).Foreground(pillTextColor).Width(contentW).Render(line)
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
	if m.chatsCollapsed {
		body := collapsedPaneBody("Чаты", m.listContentRows())
		return collapsedPaneBox(m.chatsPaneWidth(), m.paneRowHeight, body, m.focus == focusChats, m.theme, 2)
	}
	contentW := m.chatsPaneWidth() - 2 - 2*panePaddingH // минус рамка (2) и паддинг (2*panePaddingH)
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
		content := renderCursorList(labels[start:end], nil, m.searchCursor-start, contentW, m.theme)
		if len(labels) == 0 {
			content = "Ничего не найдено"
		}
		return paneBox(m.chatsPaneWidth(), m.paneRowHeight, content, m.focus == focusChats, m.theme, 2)
	}
	titles := chatTitles(m.chats)
	badges := chatBadges(m.chats)
	start, end := visibleWindow(len(titles), m.chatCursor, contentRows)
	content := renderCursorList(titles[start:end], badges[start:end], m.chatCursor-start, contentW, m.theme)
	if len(m.chats) == 0 {
		content = "Нет чатов"
	}
	return paneBox(m.chatsPaneWidth(), m.paneRowHeight, content, m.focus == focusChats, m.theme, 2)
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
			return paneBox(m.viewport.Width, m.viewport.Height, "Загрузка сообщений…", m.focus == focusMessages, m.theme, 3)
		case len(m.messages) == 0:
			return paneBox(m.viewport.Width, m.viewport.Height, "Выберите чат и нажмите Enter", m.focus == focusMessages, m.theme, 3)
		default:
			// bubbles/viewport рисует свою рамку сам через поле Style — цвет фокуса
			// выставляем перед View(). Это локальная копия Model (value-receiver),
			// поле ctx не персистится за пределы msgPane — так же, как остальной код.
			m.viewport.Style = paneBorderStyle(m.focus == focusMessages, m.theme, 3)
			return m.viewport.View()
		}
	}

	// Insert-режим: ОДНА рамка на всю панель сообщений (внешний paneBox,
	// как в обычном режиме); внутри неё сверху лента, снизу — область поля
	// ввода на сплошном чёрном фоне (chromeBackground) без собственной
	// рамки (по прямому запросу человека, 0047). m.viewport.Height уже
	// уменьшен в applyLayout() на кад панели (paneFrameV) и высоту поля, так
	// что сумма (лента + чёрная область) ТОЧНО равна внутренней высоте
	// единой рамки (m.paneRowHeight-paneFrameV).
	paneW := m.viewport.Width
	feedW := m.feedContentWidth()
	var feed string
	switch {
	case m.loadingMsgs:
		feed = feedPlaceholder(feedW, m.viewport.Height, "Загрузка сообщений…")
	case len(m.messages) == 0:
		feed = feedPlaceholder(feedW, m.viewport.Height, "Пока нет сообщений")
	default:
		// Безрамочный стиль ленты (рамка+паддинг уже нарисованы ОДИН раз
		// внешним paneBox — см. panelContentStyle). Ширина viewport локально
		// сужается до feedW (value-receiver, правка не персистится), иначе
		// View() отдал бы контент во всю ширину панели и распёр внутреннюю
		// область единой рамки.
		m.viewport.Style = panelContentStyle(m.focus == focusMessages, m.theme, 3)
		m.viewport.Width = feedW
		feed = m.viewport.View()
	}
	joined := lipgloss.JoinVertical(lipgloss.Left, feed, m.composePlain(feedW))
	return paneBox(paneW, m.paneRowHeight, joined, m.focus == focusMessages, m.theme, 3)
}

// renderMessageCard рисует одно сообщение как отдельную рамку (одинарная
// скруглённая — не путать с двойной рамкой активной панели из 0020),
// приглушённого цвета отправителя; имя+время встроены прямо в верхнюю линию
// рамки, как в старых BBS-программах. width — ПОЛНАЯ ширина карточки вместе
// с рамкой (тот же принцип, что у paneBox). alignRight — рисовать метку
// (время+имя) у правого края верхней рамки, иначе — метку (имя+время) у
// левого края. lastReadOutboxMessageID — ID последнего прочитанного исходящего
// сообщения в чате (0, если данных нет); используется для глифа прочтения на
// своих сообщениях.
func renderMessageCard(msg auth.Message, width int, alignRight bool, selected bool, t Theme, lastReadOutboxMessageID int64) string {
	b := lipgloss.RoundedBorder()
	if selected {
		// Двойная рамка — тот же визуальный язык "это выделено", что у
		// активной панели (0020): переиспользуем его для выбранного сообщения,
		// не выдумываем новый цвет/маркер. Геометрия не меняется — обе рамки
		// однорунные с обеих сторон (проверено TestActiveAndInactiveBorderFrameSizesEqual).
		b = lipgloss.DoubleBorder()
	}

	bodyColor := messageColor(msg.IsOutgoing, t)
	sender := msg.SenderName
	if sender == "" {
		sender = "?"
	}
	var nameCol, borderCol lipgloss.Color
	if msg.IsOutgoing {
		nameCol = t.OwnColor
		borderCol = t.OwnBorderColor
	} else {
		nameCol = nickColor(sender, t)
		borderCol = nickBorderColor(sender, t)
	}

	// Фон панели (#212121 для панели №3) проставляется на КАЖДЫЙ листовой
	// фрагмент строки карточки (см. messagePanelBg): фоновый цвет внешнего
	// стиля ленты после первого же вложенного \x1b[0m перестаёт действовать
	// до конца строки, поэтому без собственного фона на фрагментах хвост
	// строки (и разделители-пробелы) оставались бы непрокрашенными (0039).
	panelBg := messagePanelBg()
	borderStyle := lipgloss.NewStyle().Foreground(borderCol).Background(panelBg)
	// Обычное (не жирное) начертание + Faint — по правке человека, "меньше
	// и тоньше" шрифт имени; реальный размер шрифта терминал не даёт менять
	// посимвольно (настройка эмулятора, не приложения) — Faint (приглушённая
	// яркость) это ближайшее достижимое: имя выглядит подписью, а не акцентом.
	nameStyle := lipgloss.NewStyle().Foreground(nameCol).Faint(true).Background(panelBg)
	timeText := time.Unix(msg.Date, 0).Local().Format("15:04")
	timeTextRendered := timeStyle.Background(panelBg)

	// Глиф прочтения для своих сообщений: "✓" (Faint) — отправлено,
	// "✓✓" (OwnColor) — прочитано. Для чужих сообщений глиф не рисуется.
	var readGlyph string
	var readGlyphStyle lipgloss.Style
	if msg.IsOutgoing {
		if msg.ID <= lastReadOutboxMessageID {
			readGlyph = "✓✓"
			readGlyphStyle = lipgloss.NewStyle().Foreground(t.OwnColor).Background(panelBg)
		} else {
			readGlyph = "✓"
			readGlyphStyle = nameStyle // Faint, как имя отправителя
		}
	}

	horizontalSpan := max(0, width-2) // без двух угловых символов

	// gap/fixedW считаем ДО решения "есть ли место для метки" — порог должен
	// зависеть от реальной фиксированной ширины (время+отступ+глиф), а не быть
	// угаданным числом.
	gap := "  "
	gapRendered := bgFill(panelBg, lipgloss.Width(gap))
	readGlyphSuffix := ""
	if readGlyph != "" {
		readGlyphSuffix = bgFill(panelBg, 1) + readGlyphStyle.Render(readGlyph)
	}
	fixedW := lipgloss.Width(timeText) + lipgloss.Width(gap) + lipgloss.Width(readGlyphSuffix)
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
			label = timeTextRendered.Render(timeText) + readGlyphSuffix + gapRendered + nameStyle.Render(name)
		} else {
			label = nameStyle.Render(name) + gapRendered + timeTextRendered.Render(timeText) + readGlyphSuffix
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
	padRendered := bgFill(panelBg, padW)
	textWidth := max(1, contentSlot-2*padW)
	bodyRendered := lipgloss.NewStyle().Foreground(bodyColor).Width(textWidth).Background(panelBg).Render(msg.Text)

	var sb strings.Builder
	sb.WriteString(top)
	sb.WriteString("\n")
	for _, line := range strings.Split(bodyRendered, "\n") {
		sb.WriteString(borderStyle.Render(b.Left) + padRendered + line + padRendered + borderStyle.Render(b.Right))
		sb.WriteString("\n")
	}
	sb.WriteString(borderStyle.Render(b.BottomLeft + strings.Repeat(b.Bottom, horizontalSpan) + b.BottomRight))
	return sb.String()
}

// naturalCardWidth — минимально необходимая ширина карточки (вместе с
// рамкой) для сообщения msg: максимум из (а) ширины шапки "Имя  ЧЧ:ММ  ✓✓" БЕЗ
// обрезки (с учётом глифа прочтения для исходящих) и (б) ширины тела после
// word-wrap по максимально доступной ширине (maxHorizontalSpan — содержательная
// ширина карточки, если бы она заняла всю ленту, т.е. width-2) — но не шире
// maxHorizontalSpan+2. По прямому запросу человека — карточка не должна
// растягиваться на всю ширину ленты, если контенту столько не нужно
// ("минимальная ширина рамки"). Используется в renderMessages ДО вызова
// renderMessageCard — сама renderMessageCard не меняется, её контракт
// "рендерит ровно переданную width" остаётся прежним (см.
// TestRenderMessageCardNarrowWidthLineWidthsMatch), просто ей отдаётся уже
// вычисленная здесь, более узкая ширина. lastReadOutboxMessageID — ID
// последнего прочитанного исходящего сообщения в чате (0, если данных нет);
// учитывается для ширины глифа прочтения ("✓" или "✓✓") на своих сообщениях.
func naturalCardWidth(msg auth.Message, maxHorizontalSpan int, lastReadOutboxMessageID int64) int {
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
	// Ширина глифа прочтения для исходящих сообщений — тот же контракт, что у
	// renderMessageCard: "✓✓" при msg.ID <= lastReadOutboxMessageID (2 руны + 1
	// пробел-разделитель = 3 колонки), иначе спокойная "✓" (1 руна + пробел =
	// 2 колонки). Считаем ПО ФАКТУ сообщения, а не худший случай — иначе
	// непрочитанные исходящие получали бы лишнюю колонку (карточка шире, чем
	// реально нужно).
	readGlyphW := 0
	if msg.IsOutgoing {
		if msg.ID <= lastReadOutboxMessageID {
			readGlyphW = 3
		} else {
			readGlyphW = 2
		}
	}
	headerSpan := lipgloss.Width(sender) + lipgloss.Width(gap) + lipgloss.Width(timeText) + readGlyphW + 2 // +2: минимум по 1 тире с каждого края метки

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
// режиме). lastReadOutboxMessageID — ID последнего прочитанного исходящего
// сообщения в текущем чате (0, если данных нет); используется для глифа
// прочтения на своих сообщениях.
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
func renderMessages(msgs []auth.Message, width int, alignOwnRight bool, selectedIdx int, t Theme, lastReadOutboxMessageID int64) (string, []int) {
	offsets := make([]int, len(msgs))
	var allLines []string

	if width <= 0 {
		for i, msg := range msgs {
			offsets[i] = len(allLines)
			sender := msg.SenderName
			if sender == "" {
				sender = "?"
			}
			color := messageColor(msg.IsOutgoing, t)
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
		cardWidth := min(width, naturalCardWidth(msg, max(0, width-2), lastReadOutboxMessageID))
		rightAlign := alignOwnRight && msg.IsOutgoing
		card := renderMessageCard(msg, cardWidth, rightAlign, i == selectedIdx, t, lastReadOutboxMessageID)
		lines := strings.Split(card, "\n")
		if rightAlign {
			// Прижим вправо: ведущая часть строки — поздний свободный фон
			// панели (не голые пробелы — те не несут фона, см. bgFill).
			lead := bgFill(messagePanelBg(), max(0, width-cardWidth))
			for j := range lines {
				lines[j] = lead + lines[j]
			}
		}
		// Доливка фона панели до полной ширины ленты: строки карточки уже
		// непрозрачны (см. renderMessageCard), но справа от узкой карточки
		// пустые колонки остаются незакрашенными (viewport добавил бы туда
		// голые пробелы после последнего \x1b[0m строки) — заполняем их
		// фоновым цветом явно.
		for j, l := range lines {
			if gap := width - lipgloss.Width(l); gap > 0 {
				lines[j] = l + bgFill(messagePanelBg(), gap)
			}
		}
		card = strings.Join(lines, "\n")
		allLines = append(allLines, strings.Split(card, "\n")...)
		if i != len(msgs)-1 {
			allLines = append(allLines, "")
		}
	}
	return strings.Join(allLines, "\n"), offsets
}

// paneTitle — заголовочная строка НАД панелью, той же ширины, что и сама
// панель целиком (см. комментарий у paneBox — тот же класс ширины). num —
// номер панели для хоткея прямого перехода (см. FocusPane1/2/3), показывается
// как "[N] " перед названием. Название — ВСЕМИ КАПС, без разрядки между
// буквами (раньше была — убрана по прямому запросу человека); длинные имена
// (например, название открытого чата у панели сообщений) обрезаются по
// ширине через runewidth.Truncate с многоточием "…" на конце.
//
// scroll — необязательная пара (hasAbove, hasBelow): есть ли скрытый контент
// выше/ниже видимой области панели. Вариадик, а не обычные bool-параметры —
// чтобы существующие вызовы/тесты с 4 аргументами (без индикатора)
// компилировались без изменений; передаётся ровно 0 или 2 значения, другое
// количество (в т.ч. 1) трактуется как "нет данных о прокрутке" — индикатор
// не показывается. По прямому запросу человека: должно быть видно, когда
// прокрутка панели вообще доступна (например, при сжатии терминала по
// высоте) — см. scrollIndicatorSuffix.
func paneTitle(width, num int, text string, focused bool, t Theme, scroll ...bool) string {
	color := t.InactiveBorderColor
	if focused {
		color = t.ActiveBorderColor
	}
	prefix := fmt.Sprintf("[%d] ", num)
	suffix := ""
	if len(scroll) == 2 {
		suffix = scrollIndicatorSuffix(scroll[0], scroll[1])
	}
	suffixW := runewidth.StringWidth(suffix)
	budget := max(0, width-runewidth.StringWidth(prefix)-suffixW)
	truncated := runewidth.Truncate(strings.ToUpper(text), budget, "…")
	padded := runewidth.FillRight(prefix+truncated, max(0, width-suffixW)) + suffix
	// Заголовок панели — часть "хрома" на сплошном чёрном фоне (0039); padded
	// — чистый текст без вложенных сбросов, поэтому внешнего Background()
	// здесь достаточно (в отличие от строк с вложенными стилями, см. bgFill).
	return lipgloss.NewStyle().Bold(true).Foreground(color).Background(chromeBackground).Width(width).Render(padded)
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
func paneBox(width, height int, content string, focused bool, t Theme, paneNum int) string {
	style := paneBorderStyle(focused, t, paneNum)
	return style.
		Width(max(0, width-style.GetHorizontalBorderSize())).
		Height(max(0, height-style.GetVerticalBorderSize())).
		Render(content)
}
