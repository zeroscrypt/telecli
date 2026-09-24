package tdclient

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Файл с общей логикой клиента, без cgo-блока. Функции C API TDLib
// (tdCreateClientID/tdReceive/tdSend/tdExecute) определены в tdclient.go
// (!tdjson_static, динамическая линковка) и tdclient_static.go
// (tdjson_static, статическая) — этот файл работает с любым из двух вариантов.

type Client struct {
	id                     int32
	pending                map[string]chan map[string]interface{}
	pendingMu              sync.Mutex
	authUpdates            chan map[string]interface{}
	messageUpdates         chan map[string]interface{}
	sendStatusUpdates      chan map[string]interface{}
	chatFolderUpdates      chan map[string]interface{}
	chatReadInboxUpdates   chan map[string]interface{}
	chatReadOutboxUpdates  chan map[string]interface{}
	chatTitleUpdates       chan map[string]interface{}
	unreadCountUpdates     chan map[string]interface{}
	unreadChatCountUpdates chan map[string]interface{}
	fileUpdates            chan map[string]interface{}
	nextExtra              int64
	nextExtraMu            sync.Mutex
	closed                 bool
	closedMu               sync.Mutex
}

func NewClient() *Client {
	// Приглушаем встроенное логирование TDLib ДО создания клиента — иначе оно захлёстывает
	// stdout построчными трассами ("Begin/End to wait for updates") каждую секунду, из-за чего
	// интерактивные приглашения (номер телефона/код) физически не видно за потоком логов.
	// Уровень 1 — только ошибки, этого достаточно для диагностики реальных проблем.
	silenceLogging()

	clientID := tdCreateClientID()
	c := &Client{
		id:                clientID,
		pending:           make(map[string]chan map[string]interface{}),
		authUpdates:       make(chan map[string]interface{}, 10),
		messageUpdates:    make(chan map[string]interface{}, 20),
		sendStatusUpdates: make(chan map[string]interface{}, 20),
		// Папки меняются редко (при AuthorizationStateReady и при изменении папок
		// пользователем) — достаточно небольшого буфера, в отличие от
		// messageUpdates/sendStatusUpdates, которым нужен запас под более частые апдейты.
		chatFolderUpdates: make(chan map[string]interface{}, 5),
		// Апдейты о прочтении внутри чата могут приходить часто (как у
		// messageUpdates) — буфер по аналогии.
		chatReadInboxUpdates: make(chan map[string]interface{}, 20),
		// Апдейты о прочтении своих исходящих сообщений (updateChatReadOutbox)
		// по частоте аналогичны chatReadInboxUpdates — тот же буфер.
		chatReadOutboxUpdates: make(chan map[string]interface{}, 20),
		// updateChatTitle приносит настоящее имя приватного чата ПОСЛЕ
		// асинхронного резолва собеседника (в момент getChat title может быть
		// пустым — см. задачу 0045) — по частоте аналогичен
		// chatReadInboxUpdates, тот же буфер.
		chatTitleUpdates: make(chan map[string]interface{}, 20),
		// Агрегаты по спискам чатов (папкам) меняются редко — буфер как у
		// chatFolderUpdates.
		unreadCountUpdates: make(chan map[string]interface{}, 5),
		// Тот же паттерн, что у unreadCountUpdates — агрегат по чатам с
		// непрочитанным (не по сообщениям), нужен для бейджей папок.
		unreadChatCountUpdates: make(chan map[string]interface{}, 5),
		// Апдейты о состоянии файлов (updateFile) приходят в ответ на
		// изменения файлов, в т.ч. по ходу скачивания голосового —
		// не частые, буфер по аналогии с chatFolderUpdates.
		fileUpdates: make(chan map[string]interface{}, 5),
	}
	go c.receiveLoop()

	// Без этого запроса TDLib не запускает внутренний actor system для нового client_id и
	// никогда не эмитит первый updateAuthorizationState — проверено эмпирически (без этого
	// вызова receiveLoop вечно получает пустой ответ от td_receive). Результат не нужен —
	// важен сам факт первого td_send, а не содержимое ответа.
	if _, err := c.Send(context.Background(), map[string]interface{}{
		"@type": "getAuthorizationState",
	}); err != nil {
		// Не фатально: даже если этот конкретный запрос не получил ответа за 10с,
		// initial-запрос всё равно достиг TDLib и запустил actor system.
		_ = err
	}

	return c
}

func (c *Client) receiveLoop() {
	for {
		c.closedMu.Lock()
		closed := c.closed
		c.closedMu.Unlock()
		if closed {
			return
		}

		result := tdReceive(1.0)
		if result == "" {
			continue
		}

		var resp map[string]interface{}
		if err := json.Unmarshal([]byte(result), &resp); err != nil {
			continue
		}

		if authState, ok := resp["@type"].(string); ok && authState == "updateAuthorizationState" {
			// Недоблокирующая отправка с дропом при переполнении — сознательный выбор:
			// после authorizationStateReady никто не читает этот канал, а блокирующая
			// отправка застопорила бы receiveLoop навсегда на первом же лишнем апдейте,
			// заодно остановив обработку всех остальных запросов (getChats и т.п.).
			// Буфер (10) с запасом покрывает реальную последовательность переходов логина.
			select {
			case c.authUpdates <- resp:
			default:
			}
			continue
		}

		if updType, ok := resp["@type"].(string); ok && updType == "updateNewMessage" {
			// Тот же паттерн, что у authUpdates: неблокирующая отправка с дропом при
			// переполнении — receiveLoop не должен зависать, если получатель временно
			// не читает канал (или его вообще нет — до задачи 0008 никто не читал).
			select {
			case c.messageUpdates <- resp:
			default:
			}
			continue
		}

		if updType, ok := resp["@type"].(string); ok && (updType == "updateMessageSendSucceeded" || updType == "updateMessageSendFailed") {
			// Тот же паттерн, что у authUpdates/messageUpdates: неблокирующая отправка
			// с дропом при переполнении. Канал отдельный от messageUpdates — другой
			// смысл (статус именно отправки, не новое сообщение) и другой потребитель
			// (CLI-режим перед выходом из процесса, а не TUI/live-лента). Этот `continue`
			// стоит ДО проверки @extra: апдейты не привязаны к конкретному запросу и не
			// должны попадать в pending-каналы.
			select {
			case c.sendStatusUpdates <- resp:
			default:
			}
			continue
		}

		if updType, ok := resp["@type"].(string); ok && updType == "updateChatFolders" {
			// Тот же паттерн, что у authUpdates/messageUpdates: неблокирующая отправка
			// с дропом при переполнении. Панель «Папки» (задача 0018) подпишется на
			// этот канал; до подписки избыточные апдейты просто отбрасываются.
			select {
			case c.chatFolderUpdates <- resp:
			default:
			}
			continue
		}

		if updType, ok := resp["@type"].(string); ok && updType == "updateChatReadInbox" {
			// Тот же паттерн, что у chatFolderUpdates: неблокирующая отправка
			// с дропом при переполнении. Счётчики непрочитанных в чатах (задача
			// 0028) читаются этим каналом; до подписки избыточные апдейты
			// просто отбрасываются.
			select {
			case c.chatReadInboxUpdates <- resp:
			default:
			}
			continue
		}

		if updType, ok := resp["@type"].(string); ok && updType == "updateChatReadOutbox" {
			// Тот же паттерн, что у chatReadInboxUpdates: неблокирующая отправка
			// с дропом при переполнении. Счётчики прочтения исходящих сообщений
			// (updateChatReadOutbox) читаются этим каналом; до подписки
			// избыточные апдейты просто отбрасываются.
			select {
			case c.chatReadOutboxUpdates <- resp:
			default:
			}
			continue
		}

		if updType, ok := resp["@type"].(string); ok && updType == "updateChatTitle" {
			// Тот же паттерн, что у chatReadOutboxUpdates: неблокирующая
			// отправка с дропом при переполнении. Реальное имя чата (для
			// приватных — имя собеседника) приходит этим апдейтом после
			// асинхронного резолва пользователя; обработчик в TUI заменяет
			// m.chats[i].Title (задача 0045). До подписки избыточные апдейты
			// просто отбрасываются.
			select {
			case c.chatTitleUpdates <- resp:
			default:
			}
			continue
		}

		if updType, ok := resp["@type"].(string); ok && updType == "updateUnreadMessageCount" {
			// Тот же паттерн, что у chatFolderUpdates: неблокирующая отправка
			// с дропом при переполнении. Сумма непрочитанных СООБЩЕНИЙ по
			// списку чатов (для "Все чаты", задача 0028) читается этим
			// каналом; до подписки избыточные апдейты просто отбрасываются.
			select {
			case c.unreadCountUpdates <- resp:
			default:
			}
			continue
		}

		if updType, ok := resp["@type"].(string); ok && updType == "updateUnreadChatCount" {
			// Тот же паттерн — число ЧАТОВ (не сообщений) с непрочитанным по
			// списку, нужно для бейджей ПАПОК (не "Все чаты", см.
			// ParseUnreadChatCountUpdate — другая метрика, чем у
			// updateUnreadMessageCount).
			select {
			case c.unreadChatCountUpdates <- resp:
			default:
			}
			continue
		}

		if updType, ok := resp["@type"].(string); ok && updType == "updateFile" {
			// Тот же паттерн, что у chatFolderUpdates: неблокирующая отправка
			// с дропом при переполнении. Скачивание голосового (задача 0041)
			// ждёт готовый локальный путь файла через этот канал; до
			// подписки избыточные апдейты просто отбрасываются.
			select {
			case c.fileUpdates <- resp:
			default:
			}
			continue
		}

		if extraVal, ok := resp["@extra"]; ok {
			if extraStr, ok := extraVal.(string); ok {
				c.pendingMu.Lock()
				if ch, ok := c.pending[extraStr]; ok {
					select {
					case ch <- resp:
					default:
					}
					delete(c.pending, extraStr)
				}
				c.pendingMu.Unlock()
			}
		}
	}
}

func (c *Client) generateExtra() string {
	c.nextExtraMu.Lock()
	defer c.nextExtraMu.Unlock()
	c.nextExtra++
	return strconv.FormatInt(c.nextExtra, 10)
}

func (c *Client) Send(ctx context.Context, request map[string]interface{}) (map[string]interface{}, error) {
	extra := c.generateExtra()
	request["@extra"] = extra

	ch := make(chan map[string]interface{}, 1)
	c.pendingMu.Lock()
	c.pending[extra] = ch
	c.pendingMu.Unlock()

	requestJSON, err := json.Marshal(request)
	if err != nil {
		c.pendingMu.Lock()
		delete(c.pending, extra)
		c.pendingMu.Unlock()
		return nil, err
	}

	tdSend(c.id, string(requestJSON))

	select {
	case resp := <-ch:
		if resp["@type"] == "error" {
			message := ""
			if m, ok := resp["message"].(string); ok {
				message = m
			}
			return nil, errors.New(message)
		}
		return resp, nil
	case <-ctx.Done():
		c.pendingMu.Lock()
		delete(c.pending, extra)
		c.pendingMu.Unlock()
		return nil, ctx.Err()
	case <-time.After(10 * time.Second):
		c.pendingMu.Lock()
		delete(c.pending, extra)
		c.pendingMu.Unlock()
		return nil, errors.New("tdlib: request timed out")
	}
}

// silenceLogging разводит потоки логов TDLib и интерактивного ввода-вывода приложения: без
// этого TDLib построчно пишет трассировку ("Begin/End to wait for updates") прямо в stdout/
// stderr каждую секунду, из-за чего интерактивные приглашения (номер телефона/код) физически не
// видно за потоком логов — а позже, в TUI на bubbletea, такой левый вывод в stdout сломает экран.
// Логи TDLib уходят в отдельный файл вне терминала, stdout/stderr — только под наш собственный
// ввод-вывод.
func silenceLogging() {
	logPath := filepath.Join(tdlibConfigDir(), "tdlib.log")
	execJSON(map[string]interface{}{
		"@type": "setLogStream",
		"log_stream": map[string]interface{}{
			"@type":         "logStreamFile",
			"path":          logPath,
			"max_file_size": 10 * 1024 * 1024,
			// redirect_stderr=false: этот флаг перехватывает stderr ВСЕГО процесса на уровне ОС,
			// включая наши собственные сообщения об ошибках (fmt.Fprintf(os.Stderr, ...)) — они
			// молча утекали бы в файл лога вместо терминала пользователя. Обнаружено на практике.
			"redirect_stderr": false,
		},
	})
	execJSON(map[string]interface{}{
		"@type":               "setLogVerbosityLevel",
		"new_verbosity_level": 2,
	})
}

// execJSON — низкоуровневый td_execute без привязки к client_id и без обработки ответа,
// используется только для команд настройки (логирование), где результат нам не нужен.
func execJSON(request map[string]interface{}) {
	req, err := json.Marshal(request)
	if err != nil {
		return
	}
	tdExecute(string(req))
}

// tdlibConfigDir возвращает <UserConfigDir>/telecli, создавая каталог (0700), если его нет.
func tdlibConfigDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = "."
	}
	dir := filepath.Join(configDir, "telecli")
	_ = os.MkdirAll(dir, 0700)
	return dir
}

func (c *Client) Execute(request map[string]interface{}) (map[string]interface{}, error) {
	requestJSON, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	result := tdExecute(string(requestJSON))
	if result == "" {
		return nil, errors.New("td_execute returned nil")
	}

	var resp map[string]interface{}
	if err := json.Unmarshal([]byte(result), &resp); err != nil {
		return nil, err
	}

	if resp["@type"] == "error" {
		message := ""
		if m, ok := resp["message"].(string); ok {
			message = m
		}
		return nil, errors.New(message)
	}

	return resp, nil
}

func (c *Client) AuthUpdates() <-chan map[string]interface{} {
	return c.authUpdates
}

func (c *Client) MessageUpdates() <-chan map[string]interface{} {
	return c.messageUpdates
}

func (c *Client) SendStatusUpdates() <-chan map[string]interface{} {
	return c.sendStatusUpdates
}

func (c *Client) ChatFolderUpdates() <-chan map[string]interface{} {
	return c.chatFolderUpdates
}

func (c *Client) ChatReadInboxUpdates() <-chan map[string]interface{} {
	return c.chatReadInboxUpdates
}

func (c *Client) ChatReadOutboxUpdates() <-chan map[string]interface{} {
	return c.chatReadOutboxUpdates
}

func (c *Client) ChatTitleUpdates() <-chan map[string]interface{} {
	return c.chatTitleUpdates
}

func (c *Client) UnreadCountUpdates() <-chan map[string]interface{} {
	return c.unreadCountUpdates
}

func (c *Client) UnreadChatCountUpdates() <-chan map[string]interface{} {
	return c.unreadChatCountUpdates
}

func (c *Client) FileUpdates() <-chan map[string]interface{} {
	return c.fileUpdates
}

func (c *Client) Close() {
	c.closedMu.Lock()
	c.closed = true
	c.closedMu.Unlock()
}
