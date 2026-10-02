package tgwall

import (
	"context"
	"sort"
	"sync"

	tea "charm.land/bubbletea/v2"

	"telecli/internal/auth"
)

// wallSnapshotMessagesPerChat — сколько последних сообщений берётся на чат при
// начальной загрузке стены.
//
// Ровно одно, и это не «оптимизация запросов», а сама модель стены: на каждый
// источник (чат, канал, личный диалог) показывается ровно одна карточка — его
// последнее сообщение. Раньше здесь бралось двадцать сообщений на чат, и при
// сотнях чатов стена становилась тысячами карточек, в которых уже ничего не
// найти; глубже копать незачем и потому, что живой апдейт всё равно заменит
// карточку источника на его новое сообщение, как только оно придёт (см.
// applyWallMessageUpdate).
//
// Чат с пустой историей карточки не даёт вовсе — так было и раньше, отдельного
// решения тут не принимается.
const wallSnapshotMessagesPerChat = 1

// wallMessage — сообщение вместе со своим чатом: стена смешивает сообщения разных
// чатов в один поток, и без чата карточку не собрать (заголовок, тип и тег берутся
// именно из него).
type wallMessage struct {
	auth.Message
	Chat auth.Chat
}

// wallLoadedMsg — результат начальной загрузки стены.
//
// failedChatIDs — чаты, у которых не получилось взять историю. Они не роняют всю
// загрузку: стена показывает то, что загрузилось, и лишь помечает, что часть
// чатов не показана. Один недоступный чат не должен оставлять человека с пустой
// стеной из-за второго чата, который TDLib не смог открыть (тот же принцип
// деградации, что у loadFeedCmd в internal/tgclitui).
//
// chats — сами чаты снимка. Живым апдейтом приходит только сообщение, а собрать
// из него карточку (заголовок, тип, тег) можно лишь вместе с чатом: без этого
// списка апдейт по чату нечем было бы опознать, и стена не узнавала бы о новом
// сообщении ни в одном чате.
type wallLoadedMsg struct {
	cards         []card
	chats         []auth.Chat
	failedChatIDs []int64
}

// wallLoadedErrorMsg — ошибка, из-за которой карточек не получилось ВООБЩЕ
// (например, список чатов не загрузился). Живёт отдельным типом, а не полем err
// в wallLoadedMsg, потому что это разные вещи для интерфейса: сбой одного чата —
// это «часть чатов не показана», сбой загрузки списка — пустая стена, и молчащий
// из них показал бы человеку пустую стену без единого слова о причине.
type wallLoadedErrorMsg struct {
	err error
}

// wallLoadedCmd — команда начальной загрузки: список чатов и последнее сообщение
// каждого.
//
// Запускается из Init() как обычная tea.Cmd и НЕ блокирует запуск программы:
// стена сначала показывается пустой со спиннером, и данные приезжают сообщением
// позже. Этот снимок — по одной карточке на источник (задача 0152); дальше поток
// пополняют живые апдейты, заменяя карточку источника на его новое сообщение.
func (m Model) wallLoadedCmd() tea.Cmd {
	ctx := m.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	client := m.client

	return func() tea.Msg {
		if client == nil {
			return wallLoadedErrorMsg{err: errNoTDLibClient}
		}
		chats, err := auth.GetAllChats(ctx, client, map[string]interface{}{"@type": "chatListMain"})
		if err != nil {
			return wallLoadedErrorMsg{err: err}
		}
		messages, failedChatIDs := loadWallMessages(ctx, client, chats)
		return wallLoadedMsg{cards: wallCards(messages), chats: chats, failedChatIDs: failedChatIDs}
	}
}

// loadWallMessages — история по каждому чату параллельно, затем единый поток из
// сообщений разных чатов.
//
// Схема та же, что у loadFeedCmd в internal/tgclitui, и по тем же причинам:
// горутина на чат, а результаты пишутся в срез по индексу чата — иначе гонка за
// общий срез. Порядок чатов от TDLib ничего не значит, а сообщения разных чатов
// перемешаны по времени, поэтому единственный честный порядок стены — по дате
// сообщения. Сортировка устойчивая (sort.SliceStable): при равных датах порядок
// остаётся порядком чатов, то есть результат не «прыгает» между двумя
// прогонами из-за того, что горутины отработали в разном порядке.
func loadWallMessages(ctx context.Context, client auth.TDClientInterface, chats []auth.Chat) ([]wallMessage, []int64) {
	if len(chats) == 0 {
		return nil, nil
	}

	messagesByChat := make([][]auth.Message, len(chats))
	failed := make([]bool, len(chats))
	var waitGroup sync.WaitGroup
	waitGroup.Add(len(chats))
	for index := range chats {
		go func(chatIndex int) {
			defer waitGroup.Done()
			// openChat и getChatHistory — те же два запроса, что и у ленты tgcli.
			// Сбой openChat НЕ помечает чат как неудачный: getChatHistory после
			// него всё равно может ответить, и тогда чат вполне попадёт на стену.
			_ = auth.OpenChat(ctx, client, chats[chatIndex].ID)
			messages, err := auth.GetMessages(ctx, client, chats[chatIndex].ID, wallSnapshotMessagesPerChat)
			if err != nil {
				failed[chatIndex] = true
				return
			}
			messagesByChat[chatIndex] = messages
		}(index)
	}
	waitGroup.Wait()

	messages := make([]wallMessage, 0, len(chats)*wallSnapshotMessagesPerChat)
	failedChatIDs := make([]int64, 0)
	for index, chatMessages := range messagesByChat {
		if failed[index] {
			failedChatIDs = append(failedChatIDs, chats[index].ID)
			continue
		}
		for _, message := range chatMessages {
			messages = append(messages, wallMessage{Message: message, Chat: chats[index]})
		}
	}

	sort.SliceStable(messages, func(left, right int) bool {
		return messages[left].Date < messages[right].Date
	})
	return messages, failedChatIDs
}

// wallCards — карточки потока в том же порядке, в каком идут сообщения: сортировка
// уже сделана по дате, и повторная сортировка карточек (у них время — готовая
// строка) дала бы другой результат.
func wallCards(messages []wallMessage) []card {
	if len(messages) == 0 {
		return nil
	}
	cards := make([]card, 0, len(messages))
	for _, message := range messages {
		cards = append(cards, wallCard(message.Chat, message.Message))
	}
	return cards
}
