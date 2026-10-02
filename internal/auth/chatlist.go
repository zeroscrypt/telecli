package auth

import (
	"context"
	"fmt"
)

// ChatKind — категория чата по значению chat.type из TDLib. Меню панели чатов
// фильтрует по ней, поэтому канал и группа — разные значения, а не один флаг.
// Раньше был флаг IsGroup, и в него попадали только базовая группа и
// супергруппа: канал считался личным чатом, хотя комментарий у поля обещал
// «группа/канал».
type ChatKind int

const (
	// ChatPrivate — личный или секретный чат.
	ChatPrivate ChatKind = iota
	// ChatGroup — базовая группа или супергруппа.
	ChatGroup
	// ChatChannel — канал.
	ChatChannel
)

// IsGroupOrChannel — «не личный чат»: для удаления это leaveChat (покинуть), для
// личного — deleteChatHistory.
func (k ChatKind) IsGroupOrChannel() bool { return k != ChatPrivate }

// String — имя категории по-русски: используется в меню панели.
func (k ChatKind) String() string {
	switch k {
	case ChatGroup:
		return "Чаты"
	case ChatChannel:
		return "Каналы"
	default:
		return "Личные"
	}
}

// chatPeerIDOf — идентификатор объекта, на который указывает тип чата.
// Сверено с ~/.local/tdlib/include/td/telegram/td_api.h (сборка из master):
//
//	chatTypePrivate      { int53 user_id_; }          ID 1579049844
//	chatTypeBasicGroup   { int53 basic_group_id_; }   ID 973884508
//	chatTypeSupergroup   { int53 supergroup_id_;
//	                       bool is_channel_; }         ID -1472570774
//
// Проверено вживью на настоящем аккаунте (задача 0163): на 200 чатов реально
// встретились только chatTypePrivate и chatTypeSupergroup, с полями user_id и
// supergroup_id соответственно — ровно как здесь.
//
// Тип без нужного поля (в том числе chatTypeSecret, где поле называется
// secret_chat_id, а не user_id/basic_group_id) читается как 0: у секретного
// чата счётчика участников и статуса в этом смысле не существует, и искать его
// по нулю значило бы подставить чужие данные.
func chatPeerIDOf(chatType map[string]interface{}) int64 {
	var field string
	switch chatType["@type"] {
	case "chatTypePrivate":
		field = "user_id"
	case "chatTypeBasicGroup":
		field = "basic_group_id"
	case "chatTypeSupergroup":
		field = "supergroup_id"
	default:
		return 0
	}
	id, _ := chatType[field].(float64)
	return int64(id)
}

// chatKindOf разбирает объект chat.type TDLib. Отдельного типа «канал» в
// TDLib нет: канал — это супергруппа с is_channel=true прямо в том же объекте,
// отдельный запрос getSupergroup не нужен. Сверено с td_api.tl: "chatTypeSupergroup
// supergroup_id:int53 is_channel:Bool = ChatType;" — других вариантов ChatType
// там ровно четыре, и типа-канала среди них нет. Неизвестный или отсутствующий
// тип считаем личным чатом: это поведение было и до разделения на категории, а
// молча пропасть не должны новые значения из TDLib.
func chatKindOf(chatType map[string]interface{}) ChatKind {
	switch chatType["@type"] {
	case "chatTypeBasicGroup":
		return ChatGroup
	case "chatTypeSupergroup":
		if isChannel, _ := chatType["is_channel"].(bool); isChannel {
			return ChatChannel
		}
		return ChatGroup
	default:
		return ChatPrivate
	}
}

type Chat struct {
	ID                      int64
	Title                   string
	UnreadCount             int32
	LastReadOutboxMessageID int64
	Kind                    ChatKind // категория чата: личный, группа, канал
	// PeerID — id объекта, на который указывает ТИП чата (см. chatPeerIDOf):
	// user_id для chatTypePrivate, basic_group_id для chatTypeBasicGroup,
	// supergroup_id для chatTypeSupergroup. По нему находятся живые значения
	// источника — число участников и статус собеседника (см.
	// ParseGroupMemberCountUpdate и ParseUserStatusUpdate), потому что приходят
	// они апдейтами про сам объект, а не про чат.
	//
	// Поле одно на все три типа чата, а не три отдельных: у личного диалога
	// идентификатор группы всё равно не бывает, и держать под него третье
	// всегда-нулевое поле незачем. Что именно лежит в PeerID, говорит Kind —
	// без него это просто число из чужого пространства идентификаторов.
	PeerID int64
	// Muted — чат заглушен по-своему (см. chatMutedOf): его сообщения не
	// приходят уведомлениями. Поле нужно только фильтру «приглушённые» в
	// интерфейсе, поэтому одного факта достаточно — на сколько именно заглушено,
	// показывать не требуется.
	Muted bool
}

// chatMutedOf разбирает объект notification_settings типа
// chatNotificationSettings. Сверено с ~/.local/tdlib/include/td/telegram/td_api.h
// (сборка TDLib из master, та же, что и в остальном проекте):
//
//	bool use_default_mute_for_;
//	int32 mute_for_;
//
// Считаем чат заглушённым только при use_default_mute_for == false && mute_for != 0,
// то есть когда его заглушили явно. Когда use_default_mute_for == true, чат
// наследует mute из scopeNotificationSettings по своему типу (личные, группы,
// каналы) — это отдельный запрос getScopeNotificationSettings, и тянуть его
// ради одной галочки фильтра не будем: в TDLib дефолтный mute для обычных
// аккаунтов выключен, а явное заглушение — то, что человек делает руками. Это
// осознанное упрощение, а не молчаливое умолчание.
//
// Отсутствующий или битый объект считаем «не заглушено»: лучше показать лишний
// чат, чем молча спрятать переписку.
func chatMutedOf(notificationSettings map[string]interface{}) bool {
	useDefaultMuteFor, _ := notificationSettings["use_default_mute_for"].(bool)
	if useDefaultMuteFor {
		return false
	}
	muteFor, ok := notificationSettings["mute_for"].(float64)
	return ok && muteFor != 0
}

// chatFromTDLib собирает Chat из объекта TDLib "chat" целиком (getChat, updateNewChat).
//
// Отдельная функция, а не тело цикла GetChats: updateNewChat несёт РОВНО тот же
// объект chat (сверено с ~/.local/tdlib/include/td/telegram/td_api.h —
// "class updateNewChat { object_ptr<chat> chat_; }"), и разбирать его вторым
// похожим кодом значило бы через первую же правку получить у auth.Chat два
// разных списка полей. Список полей объекта chat сверен с тем же td_api.h:
// id_, type_, title_, unread_count_, last_read_outbox_message_id_,
// notification_settings_.
//
// ok == false — объект битый: нет обязательного id. Типа чата и настроек
// уведомлений нет на этом уровне, они читаются как отсутствующие (личный чат,
// «не заглушено») — ровно как их читает chatKindOf/chatMutedOf на nil.
func chatFromTDLib(chat map[string]interface{}) (Chat, bool) {
	id, idOk := chat["id"].(float64)
	if !idOk {
		return Chat{}, false
	}
	title, _ := chat["title"].(string)
	if title == "" {
		title = fmt.Sprintf("chat#%d", int64(id))
	}
	unreadCount, _ := chat["unread_count"].(float64)
	lastReadOutboxMessageID, _ := chat["last_read_outbox_message_id"].(float64)
	typeRaw, _ := chat["type"].(map[string]interface{})
	notificationSettings, _ := chat["notification_settings"].(map[string]interface{})
	return Chat{
		ID:                      int64(id),
		Title:                   title,
		UnreadCount:             int32(unreadCount),
		LastReadOutboxMessageID: int64(lastReadOutboxMessageID),
		Kind:                    chatKindOf(typeRaw),
		PeerID:                  chatPeerIDOf(typeRaw),
		Muted:                   chatMutedOf(notificationSettings),
	}, true
}

// GetChat загружает полный объект ОДНОГО чата по уже известному id.
//
// Тот же запрос getChat, что GetChats делает на каждый чат списка, и тот же
// разбор ответа — chatFromTDLib. Нужен там, где id уже есть, а сам объект чата
// ещё нет: updateChatAddedToList приносит только chat_id и список, а без полного
// объекта у чата не было бы ни названия, ни категории, ни признака заглушения.
func GetChat(ctx context.Context, client TDClientInterface, chatID int64) (Chat, error) {
	resp, err := client.Send(ctx, map[string]interface{}{
		"@type":   "getChat",
		"chat_id": chatID,
	})
	if err != nil {
		return Chat{}, fmt.Errorf("getChat failed: %w", err)
	}
	chat, ok := chatFromTDLib(resp)
	if !ok {
		return Chat{}, fmt.Errorf("getChat returned malformed chat for %d", chatID)
	}
	return chat, nil
}

// GetChats загружает до limit чатов из указанного списка (chatList — сырой
// JSON-объект TDLib ChatList: {"@type": "chatListMain"} для "Все чаты" или
// {"@type": "chatListFolder", "chat_folder_id": <id>} для конкретной папки).
func GetChats(ctx context.Context, client TDClientInterface, chatList map[string]interface{}, limit int) ([]Chat, error) {
	// Актуальная схема (сверено с td_api.tl текущего master TDLib): offset_order/offset_chat_id
	// в getChats больше нет, вместо них — chat_list. Официальная документация прямо говорит, что
	// getChats "for informational purposes only" и ничего не вернёт, пока список не загружен через
	// loadChats — поэтому сначала грузим (updates обрабатываются в receiveLoop и здесь не нужны,
	// нам достаточно самого факта, что после loadChats у TDLib есть что вернуть через getChats).
	// 404 от loadChats означает "все чаты уже загружены" — не ошибка для нас в этом контексте.
	// Ошибку loadChats сознательно игнорируем (в т.ч. документированный 404 "все чаты уже
	// загружены" — не ошибка для нас): не действуем по-разному в зависимости от причины,
	// просто читаем то, что успело оказаться в памяти TDLib, через getChats ниже.
	_, _ = client.Send(ctx, map[string]interface{}{
		"@type":     "loadChats",
		"chat_list": chatList,
		"limit":     limit,
	})

	return getChatsOnce(ctx, client, chatList, limit)
}

// Константы GetAllChats. Схема loadChats сверена с ~/.local/tdlib/include/td/telegram/td_api.h
// (сборка TDLib из master, та же, что и в остальном проекте):
// "loadChats(object_ptr<ChatList> &&chat_list_, int32 limit_);" — то есть плоские
// chat_list и limit, ровно как у getChats.
const (
	// allChatsLoadPage — сколько чатов догружается за один вызов loadChats. Размер
	// страницы не влияет на результат: TDLib отдаёт столько, сколько успевает, и
	// цикл ниже повторяет вызов, пока список не кончится. Взято по размеру
	// страницы, который уже принят в проекте (loadChats в GetChats берёт limit
	// вызывающей стороны; здесь страница фиксированная и не зависит от того,
	// сколько чатов потом попросят показать).
	allChatsLoadPage = 50
	// allChatsLoadAttempts — потолок числа вызовов loadChats. Реально цикл
	// заканчивается раньше, на первой же ошибке (см. GetAllChats), и потолок
	// нужен только на случай, если TDLib никогда её не вернёт: по документации
	// такого быть не должно, но бесконечный цикл в TUI хуже лишнего запроса.
	// 50 страниц по 50 чатов — запас на тысячи чатов, то есть аккаунт, при
	// котором потолка хватит не хватает, — это аномалия, а не норма.
	allChatsLoadAttempts = 50
	// allChatsLimit — сколько чатов просим у getChats после догрузки. С запасом
	// сверх реально наблюдавшихся у живого аккаунта нескольких сотен: getChats
	// отдаёт не больше запрошенного, поэтому занизить лимит означало бы молча
	// потерять часть списка, а завысить — не стоит ничего.
	allChatsLimit = 5000
)

// GetAllChats исчерпывающе догружает список чатов, вызывая loadChats в цикле, пока
// TDLib не ответит "больше нечего грузить" (документированная ошибка "Not Found" на
// этот конкретный запрос — не ошибка вызова, а сигнал конца списка), затем
// возвращает getChats с этим же списком без искусственного потолка.
//
// Отличие от GetChats: там loadChats зовётся один раз, чего хватает не более чем
// на первую сотню чатов, а у живого аккаунта их бывают сотни — и getChats без
// докачки отдаёт только часть списка. Здесь докачка идёт циклом, и верхней
// границы на число чатов нет: ограничение allChatsLimit — это потолок ОДНОГО
// запроса getChats, а не «покажем первые 5000 чатов».
//
// Выход из цикла — на ЛЮБОЙ ошибке loadChats, а не только на «Not Found»: точный
// текст ошибки TDLib склонен менять, и отличить «конец списка» от сбоя по строке
// надёжно нельзя. Разбирать код ошибки ради этого не нужно — и то, и другое
// означает одно и то же: звать loadChats дальше бессмысленно, и читать надо то,
// что уже есть в памяти TDLib.
func GetAllChats(ctx context.Context, client TDClientInterface, chatList map[string]interface{}) ([]Chat, error) {
	if err := loadAllChats(ctx, client, chatList); err != nil {
		return nil, err
	}
	return getChatsOnce(ctx, client, chatList, allChatsLimit)
}

// GetAllChatIDs — то же, что GetAllChats, но только id чатов.
//
// Отдельная функция, а не разбор GetAllChats на стороне вызывающего: getChats
// разбирается здесь же, и «только список id» — это половина работы getChatsOnce
// (запрос плюс чтение chat_ids), а вторая половина (getChat на каждый чат) нужна
// совсем не всем. У стены (internal/tgwall) фильтр по папкам спрашивает ровно
// «в каких папках состоит этот чат», и тянуть за этим полные объекты чатов
// (сотни запросов getChat на аккаунте с сотнями чатов) незачем.
func GetAllChatIDs(ctx context.Context, client TDClientInterface, chatList map[string]interface{}) ([]int64, error) {
	if err := loadAllChats(ctx, client, chatList); err != nil {
		return nil, err
	}
	return getChatIDsOnce(ctx, client, chatList, allChatsLimit)
}

// loadAllChats — докачка списка чатов циклом loadChats до первого отказа. Вынесено
// из GetAllChats, потому что теперь этим же циклом пользуется GetAllChatIDs, и
// две копии цикла разошлись бы при правке (в том числе в потолке попыток).
//
// Выход на ЛЮБОЙ ошибке loadChats, а не только на «Not Found»: точный текст
// ошибки TDLib склонен менять, и отличить «конец списка» от сбоя по строке
// надёжно нельзя. Разбирать код ошибки ради этого не нужно — и то, и другое
// означает одно и то же: звать loadChats дальше бессмысленно, и читать надо то,
// что уже есть в памяти TDLib.
func loadAllChats(ctx context.Context, client TDClientInterface, chatList map[string]interface{}) error {
	for range allChatsLoadAttempts {
		_, err := client.Send(ctx, map[string]interface{}{
			"@type":     "loadChats",
			"chat_list": chatList,
			"limit":     allChatsLoadPage,
		})
		if err != nil {
			break
		}
		// Отменённый контекст — не «конец списка»: дальше идти незачем, и
		// getChats ниже вернёт ту же отмену, но уже осмысленным текстом.
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return nil
}

// getChatsOnce — один запрос getChats с разбором ответа в []Chat. Общий
// помощник GetChats и GetAllChats: разбор объектов chat у них совпадает дословно,
// а разойтись он может только в правке одной из двух копий.
func getChatsOnce(ctx context.Context, client TDClientInterface, chatList map[string]interface{}, limit int) ([]Chat, error) {
	chatIDs, err := getChatIDsOnce(ctx, client, chatList, limit)
	if err != nil {
		return nil, err
	}

	var chats []Chat
	for _, chatID := range chatIDs {
		// Ответ getChat разбирает chatFromTDLib — тот же разбор, что у
		// updateNewChat, чтобы поля Chat не разошлись в двух местах.
		chat, err := GetChat(ctx, client, chatID)
		if err != nil {
			continue
		}
		chats = append(chats, chat)
	}

	return chats, nil
}

// getChatIDsOnce — один запрос getChats с чтением chat_ids. Общий помощник
// getChatsOnce и GetAllChatIDs: и запрос, и разбор ответа у них совпадают
// дословно, а разойтись могут только в правке одной из двух копий.
func getChatIDsOnce(ctx context.Context, client TDClientInterface, chatList map[string]interface{}, limit int) ([]int64, error) {
	request := map[string]interface{}{
		"@type":     "getChats",
		"chat_list": chatList,
		"limit":     limit,
	}

	resp, err := client.Send(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("getChats failed: %w", err)
	}

	if resp["@type"] != "chats" {
		return nil, fmt.Errorf("unexpected response type: %v", resp["@type"])
	}

	chatIDsRaw, ok := resp["chat_ids"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("invalid chat_ids format")
	}

	chatIDs := make([]int64, 0, len(chatIDsRaw))
	for _, idRaw := range chatIDsRaw {
		chatID, ok := idRaw.(float64)
		if !ok {
			continue
		}
		chatIDs = append(chatIDs, int64(chatID))
	}

	return chatIDs, nil
}
