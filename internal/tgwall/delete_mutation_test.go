package tgwall

import (
	"strings"
	"testing"
)

// Мутационная проверка обязательных требований задачи 0165.
//
// Файл существует ради одной цели: убедиться, что проверки из delete_test.go
// ЛОВЯТ отказ от каждого из них. Обычный тест проходит и на неправильном коде,
// если неправильность живёт в ветке, которую этот тест не задевает; здесь каждая
// мутация — это проверка ДРУГОГО теста, а не новый тест.
//
// Помощники (copyGoModule, applyMutation, runGoTest) — те же, что у мутационной
// проверки задачи 0155 (zoom_mutation_test.go): отдельная копия этого механизма
// разошлась бы с уже проверенной при первой правке.

// deleteMutantCases — по несколько мутаций на каждое обязательное требование
// задачи.
var deleteMutantCases = []mutantCase{
	{
		// (1) Подписка на удаления в Init(): без неё стена не узнает ни о чьём
		// удалении, и карточка источника навсегда показывает удалённое сообщение.
		name:      "Init не подписан на удаления",
		file:      "model.go",
		from:      "\tdeleted := m.waitForWallDeleteUpdate()",
		to:        "\tdeleted := func() tea.Msg { return nil }",
		wantFails: "TestInitSubscribesToWallDeleteUpdates",
	},
	{
		// (2) Апдейт удаления обязан вызвать перезапрос последнего сообщения. Без
		// него карточка продолжала бы показывать удалённое сообщение — ровно то,
		// что человек описал вживую.
		name:      "апдейт удаления не вызывает перезапрос",
		file:      "delete.go",
		from:      "\tif indexOfDeletedCard(m.source, msg.chatID, msg.messageIDs) < 0 {\n\t\treturn m, m.waitForWallDeleteUpdate()",
		to:        "\tif true {\n\t\treturn m, m.waitForWallDeleteUpdate()",
		wantFails: "TestWallDeleteUpdateShowsPreviousMessageOfSource",
	},
	{
		// (2, порядок) Новая карточка вставляется по дате, а не дописывается в
		// конец: дописание увезло бы её в конец потока, мимо остальных.
		// Переменная переименована с cards на source ревью-правкой (0165 сведена
		// с 0163/0164: карточки источника правятся в m.source, m.cards
		// пересчитывается rebuildVisible), образец обновлён вслед за этим.
		name:      "заменённая карточка дописывается в конец потока",
		file:      "delete.go",
		from:      "\t_, source = insertSortedCard(source, newCard)",
		to:        "\tsource = append(source, newCard)",
		wantFails: "TestWallDeleteUpdateKeepsDateOrderOfStream",
	},
	{
		// (2, счётчик) Счётчик непрочитанных переносится со старой карточки: без
		// переноса он мигал бы к значению снимка чата при каждой замене.
		name:      "счётчик непрочитанных не переносится на новую карточку",
		file:      "delete.go",
		from:      "\tnewCard.UnreadCount = existing.UnreadCount\n",
		to:        "",
		wantFails: "TestWallDeleteUpdateKeepsUnreadCountOfReplacedCard",
	},
	{
		// (2, пустой источник) У чата не осталось сообщений — карточка снимается.
		name:      "пустой ответ перезапроса не снимает карточку",
		file:      "delete.go",
		from:      "\tif msg.message.ID == 0 {",
		to:        "\tif false {",
		wantFails: "TestWallCardLatestDropsCardWhenSourceHasNoMessagesLeft",
	},
	{
		// (идемпотентность) Ответ на УЖЕ неактуальный запрос не применяется: пока
		// он летел, источник мог прислать своё новое сообщение, и ответ откатил бы
		// карточку назад по времени.
		name:      "опоздавший ответ перезапроса применяется",
		file:      "delete.go",
		from:      "\tindex := indexOfDeletedCard(m.source, msg.chatID, msg.deletedIDs)",
		to:        "\tindex := indexOfChatCard(m.source, msg.chatID)",
		wantFails: "TestStaleCardLatestResponseIsDiscarded",
	},
	{
		// (идемпотентность) Совпадение строго по паре (чат, сообщение): удалённым
		// могло быть не то сообщение, что показано на карточке, и тогда перезапрос
		// зря ходил бы в сеть.
		name:      "совпадение карточки только по чату",
		file:      "delete.go",
		from:      "\t\tif slices.Contains(deletedIDs, item.MessageID) {\n\t\t\treturn index",
		to:        "\t\tif true {\n\t\t\treturn index",
		wantFails: "TestWallDeleteUpdateOfUnrelatedMessageDoesNotRefetch",
	},
	{
		// (идемпотентность) Своё удаление, применённое повторно, не снимает вторую
		// карточку: снимать больше нечего.
		name:      "повторное своё удаление снимает чужую карточку",
		file:      "confirm.go",
		from:      "\tif index < 0 {\n\t\treturn m\n\t}\n\tm = m.dropWallCard(index)",
		to:        "\tif index < 0 {\n\t\tindex = 0\n\t}\n\tm = m.dropWallCard(index)",
		wantFails: "TestWallDeleteUpdateAfterOwnDeleteDoesNotChangeWall",
	},
	{
		// (3) Переписка теряет удалённые сообщения: её список — свой, а не карточки
		// стены, и без этого удалённое сообщение висело бы в ней до переоткрытия.
		name:      "переписка не теряет удалённые сообщения",
		file:      "delete.go",
		from:      "\tm.removeWallZoomMessages(msg.chatID, msg.messageIDs)\n",
		to:        "",
		wantFails: "TestWallDeleteUpdateRemovesDeletedMessageFromOpenZoom",
	},
	{
		// (3, курсор) Курсор переписки на удалённом сообщении зажимается, а не
		// остаётся за границей списка.
		name:      "курсор переписки не зажимается после удаления",
		file:      "delete.go",
		from:      "\tm.zoom.cursor = clampWallCursor(m.zoom.cursor, len(kept))",
		to:        "\tm.zoom.cursor = min(m.zoom.cursor, len(kept))",
		wantFails: "TestWallDeleteUpdateRemovesDeletedMessageFromOpenZoom",
	},
	{
		// (переподписка) Нераспознанный апдейт пропускается молча, но подписка
		// возвращается: без неё удаления перестают жить после первого же чужого
		// формата апдейта.
		name:      "на нераспознанный апдейт подписка не возвращается",
		file:      "delete.go",
		from:      "\tif !msg.ok {\n\t\treturn m, m.waitForWallDeleteUpdate()\n\t}",
		to:        "\tif !msg.ok {\n\t\treturn m, nil\n\t}",
		wantFails: "TestWallDeleteUpdateResubscribesUnlessChannelClosed",
	},
}

// TestWallDeleteMutationsAreCaught — каждая мутация из deleteMutantCases обязана
// быть поймана указанным тестом. Механизм копирования модуля и прогона — тот же,
// что у TestZoomMutationsAreCaught, и на общих помощниках: две копии одного
// механизма разошлись бы при первой правке.
func TestWallDeleteMutationsAreCaught(t *testing.T) {
	for _, mutation := range deleteMutantCases {
		t.Run(mutation.name, func(t *testing.T) {
			source := t.TempDir()
			if err := copyGoModule(t, source); err != nil {
				t.Fatal(err)
			}
			if err := applyMutation(t, source, mutation); err != nil {
				t.Fatal(err)
			}
			output, err := runGoTest(t, source, mutation.wantFails)
			if err == nil {
				t.Fatalf("мутация НЕ поймана: %s прошёл на изменённом коде\n%s", mutation.wantFails, tail(output))
			}
			if !strings.Contains(string(output), "--- FAIL") {
				t.Fatalf("падение выглядит не как провал теста, а как ошибка сборки —\n"+
					"такая мутация ничего не доказывает:\n%s", tail(output))
			}
			failed := firstFailure(output)
			if !strings.Contains(failed, mutation.wantFails) {
				t.Fatalf("мутация сломала другой тест (%s), а ждали %s:\n%s",
					failed, mutation.wantFails, tail(output))
			}
			t.Logf("поймана: %s", failed)
		})
	}
}
