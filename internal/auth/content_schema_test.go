package auth

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tdAPIHeaderPath — заголовок со схемой TDLib, эталон для тестов полноты.
// Переопределяется переменной окружения, чтобы тест можно было гонять против
// другой сборки TDLib; если файла нет, тесты полноты пропускаются, а не падают:
// на машине без собранной библиотеки проверять схему нечем.
func tdAPIHeaderPath() string {
	if custom := os.Getenv("TD_API_HEADER"); custom != "" {
		return custom
	}
	// Именно HOME, а не os.UserConfigDir(): на macOS тот возвращает
	// ~/Library/Application Support, а TDLib ставится в ~/.local — по
	// UserConfigDir файл не находился бы и тест молча пропускал сверку.
	home := os.Getenv("HOME")
	if home == "" {
		return ""
	}
	return filepath.Join(home, ".local", "tdlib", "include", "td", "telegram", "td_api.h")
}

// declaredMessageContentTypes — все имена классов, объявленных в td_api.h как
// наследники MessageContent. Читает файл построчно и ищет строку объявления
// вида "class messageXxx final : public MessageContent {".
func declaredMessageContentTypes(t *testing.T) map[string]bool {
	t.Helper()
	path := tdAPIHeaderPath()
	if path == "" {
		t.Skip("путь к td_api.h неизвестен: пропускаю сверку со схемой")
	}
	file, err := os.Open(path)
	if err != nil {
		t.Skipf("td_api.h недоступен (%v): пропускаю сверку со схемой", err)
	}
	defer file.Close()

	found := map[string]bool{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "class message") {
			continue
		}
		if !strings.HasSuffix(line, "final : public MessageContent {") {
			continue
		}
		name := strings.TrimSuffix(strings.TrimPrefix(line, "class "), " final : public MessageContent {")
		found[name] = true
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("чтение td_api.h: %v", err)
	}
	if len(found) == 0 {
		t.Fatalf("в %s не найдено ни одного класса MessageContent — тест сломан, а не схема", path)
	}
	return found
}

// TestServiceContentTypesExistInSchema — каждое имя из serviceContentTypes
// обязано быть реальным классом в td_api.h. Список наполнялся вручную, и
// опечатка в нём выглядела бы неотличимо от «TDLib просто не прислал такой тип»
// на живом клиенте, а расхождение обнаружилось бы только на экране у человека.
//
// Ошибка направлена туда же, где и сам список: одно изменение пополняет его.
func TestServiceContentTypesExistInSchema(t *testing.T) {
	declared := declaredMessageContentTypes(t)
	for name := range serviceContentTypes {
		if !declared[name] {
			t.Errorf("serviceContentTypes содержит %q, которого нет в td_api.h среди MessageContent", name)
		}
	}
}

// knownUnclassifiedContentTypes — типы содержимого, которые СЕЙЧАС не разбираются
// и честно уезжают в ContentUnsupported («[что-то не поддерживается]»).
//
// Это не «забыли и не заметили»: каждая запись перечислена осознанно, с причиной,
// и все они разобраны в REPORT.md задачи 0133. Важно, что список ОТКРЫТЫЙ и
// проверяется на полноту — если TDLib добавит новый тип содержимого, тест
// упадёт на нём, и новый тип не сможет тихо уехать в неподдерживаемые, как это
// произошло с двумя найденными в 0133 расхождениями по полям.
//
// Часть типов (звонки, видеочаты, изменения опроса) по смыслу СЛУЖЕБНЫЕ и,
// вероятно, должны попасть в serviceContentTypes — но это решение о том, что
// увидит человек, а не исправление неверного имени поля, поэтому здесь они не
// переносились, а вынесены в отчёт.
var knownUnclassifiedContentTypes = map[string]string{
	"messageUnsupported":                 "TDLib сам не знает содержимого — поддерживать нечего, это честный ответ протокола",
	"messageStory":                       "пересланная история: в терминале нет поверхности для показа",
	"messagePaidMedia":                   "платное медиа: покупка не реализована, карточка с ценой вводила бы в заблуждение",
	"messageInvoice":                     "счёт от бота: оплаты в клиенте нет",
	"messageGame":                        "игра: в терминале нечего запускать",
	"messageChecklist":                   "чек-лист: интерактивные галочки в терминале не показываются",
	"messageRichMessage":                 "богатое сообщение веб-страницы: появляется вместе с новыми веб-форматами, разбор впереди",
	"messageLiveLocation":                "трансляция геолокации: живую трансляцию терминал не отображает, по завершении приходит messageLocation",
	"messageStakeDice":                   "ставка на кубик: платная игровая механика, её не поддерживаем",
	"messageExpiredPhoto":                "самоуничтожающееся фото: содержимого уже нет, подписывать нечем",
	"messageExpiredVideo":                "самоуничтожающееся видео: содержимого уже нет",
	"messageExpiredVideoNote":            "самоуничтожающаяся видеозаметка: содержимого уже нет",
	"messageExpiredVoiceNote":            "самоуничтожающееся голосовое: содержимого уже нет",
	"messageCall":                        "служебное по смыслу (звонок завершён) — кандидат в serviceContentTypes, см. отчёт 0133",
	"messageGroupCall":                   "служебное по смыслу (групповой звонок) — кандидат, см. отчёт 0133",
	"messageInviteVideoChatParticipants": "служебное по смыслу (приглашение в видеочат) — кандидат, см. отчёт 0133",
	"messageVideoChatStarted":            "служебное по смыслу (видеочат начался) — кандидат, см. отчёт 0133",
	"messageVideoChatEnded":              "служебное по смыслу (видеочат завершён) — кандидат, см. отчёт 0133",
	"messageVideoChatScheduled":          "служебное по смыслу (видеочат запланирован) — кандидат, см. отчёт 0133",
	"messagePollOptionAdded":             "служебное по смыслу (добавлен вариант опроса) — кандидат, см. отчёт 0133",
	"messagePollOptionDeleted":           "служебное по смыслу (вариант опроса удалён) — кандидат, см. отчёт 0133",
}

func TestEveryServiceLikeContentTypeIsClassified(t *testing.T) {
	declared := declaredMessageContentTypes(t)

	// Что разбирает switch: типы, попавшие в разбор содержимого.
	parsed := map[string]bool{
		"messageText": true, "messagePhoto": true, "messageSticker": true,
		"messageAnimatedEmoji": true, "messageAnimation": true, "messageVideo": true,
		"messageVideoNote": true, "messageVoiceNote": true, "messageAudio": true,
		"messageDocument": true, "messageDice": true, "messageLocation": true,
		"messageVenue": true, "messageContact": true, "messagePoll": true,
	}

	var unclassified []string
	for name := range declared {
		if parsed[name] || serviceContentTypes[name] || knownUnclassifiedContentTypes[name] != "" {
			continue
		}
		unclassified = append(unclassified, name)
	}

	// Порядок сообщений о неразобранных типах не важен для результата, но делает
	// вывод теста воспроизводимым между запусками.
	for i := 0; i < len(unclassified); i++ {
		for j := i + 1; j < len(unclassified); j++ {
			if unclassified[j] < unclassified[i] {
				unclassified[i], unclassified[j] = unclassified[j], unclassified[i]
			}
		}
	}
	for _, name := range unclassified {
		t.Errorf("%s: не разбирается switch'ем, не служебный и не в списке осознанных исключений", name)
	}
}

// TestKnownUnclassifiedContentTypesStillExist — обратная проверка: каждое
// осознанное исключение обязано оставаться в схеме. Иначе список устарел и
// занимает место, не объясняя ничего (и новый тип не будет им помечен).
func TestKnownUnclassifiedContentTypesStillExist(t *testing.T) {
	declared := declaredMessageContentTypes(t)
	for name, reason := range knownUnclassifiedContentTypes {
		if reason == "" {
			t.Errorf("исключение %q перечислено без причины", name)
		}
		if !declared[name] {
			t.Errorf("исключение %q больше не объявлено в td_api.h — список исключений устарел", name)
		}
	}
}
