package tgwall

import (
	"strings"
	"testing"
)

// Мутационная проверка обязательных требований задачи 0158.
//
// Файл существует ради одной цели: убедиться, что проверки из keymap_test.go
// ЛОВЯТ отказ от двух главных требований задачи — (а) нажатие опознаётся по
// конфигурации, а не по зашитой клавише, и (б) строка подсказок собирается из
// конфигурации, а не осталась константой по факту. Обычный тест проходит и на
// неправильном коде, если неправильность живёт в ветке, которую этот тест не
// задевает; здесь каждая мутация — это проверка ДРУГОГО теста, а не новый тест.
//
// Механика — та же, что у проверки задачи 0155 (zoom_mutation_test.go): мутируется
// КОПИЯ пакета в t.TempDir, а `go test -run` гоняется по СОДЕРЖИМОМУ, то есть по
// проверкам, а не по мутированному коду. Каждая мутация подобрана так, чтобы
// ломала ровно ОДИН тест: иначе «первым упал соседний» и непонятно, поймана ли
// нужная защита.

// keymapMutantCases — по несколько мутаций на каждое из двух требований.
var keymapMutantCases = []mutantCase{
	{
		// (а) Ответ на сообщение: если клавиша берётся из конфигурации, то
		// ctrl+y (настроенный) срабатывает, а ctrl+r (зашитый) — нет. Со
		// зашитым значением в коде наоборот, и правка конфигурации была бы враньём.
		name:      "ответ: клавиша зашита в коде вместо конфигурации",
		file:      "keymap.go",
		from:      "\tcase keyReply:\n\t\tfields = [][]string{k.keys.Reply}",
		to:        "\tcase keyReply:\n\t\tfields = [][]string{{\"ctrl+r\"}}",
		wantFails: "TestUpdateRespondsToConfiguredKeys",
	},
	{
		// (а) Выход: тот же класс, что и ответ, но у последнего действия программы,
		// где цена ошибки — «нечем закрыть».
		name:      "выход: клавиша зашита в коде вместо конфигурации",
		file:      "keymap.go",
		from:      "\tcase keyQuit:\n\t\tfields = [][]string{k.keys.Quit}",
		to:        "\tcase keyQuit:\n\t\tfields = [][]string{{\"ctrl+c\"}}",
		wantFails: "TestQuitRespondsToConfiguredKey",
	},
	{
		// (а) Удаление: зашитая клавиша продолжала бы удалять сообщение, даже
		// когда человек переназначил удаление на другую клавишу.
		name:      "удаление: клавиша зашита в коде вместо конфигурации",
		file:      "keymap.go",
		from:      "\tcase keyDeleteMessage:\n\t\tfields = [][]string{k.keys.DeleteMessage}",
		to:        "\tcase keyDeleteMessage:\n\t\tfields = [][]string{{\"delete\"}}",
		wantFails: "TestDeleteRespondsToConfiguredKey",
	},
	{
		// (а) Раскладка: без сверки русского соседа хоткей-буква работает только в
		// английской раскладке, а человек печатает по-русски.
		name:      "раскладка: сосед по раскладке не сверяется",
		file:      "keymap.go",
		from:      "\tpeer := keyinput.KeyPeer(name)\n\treturn peer != \"\" && slices.Contains(bindings, peer)",
		to:        "\treturn false",
		wantFails: "TestKeyMapWorksOnTheRussianLayout",
	},
	{
		// (б) Подсказка осталась константой: ровно то, чего требовала задача, — вид
		// текста прежний, а источник клавиш по-прежнему не тот.
		name:      "подсказка: строка осталась константой",
		file:      "input.go",
		from:      "\tpairs := make([]hintPair, 0, 5)\n\tif label := m.keymap.label(keyOpenFilter); label != \"\" {\n\t\tpairs = append(pairs, hintPair{key: label, word: wallHintFilter})\n\t}\n\tif label := m.newLineLabel(); label != \"\" {\n\t\tpairs = append(pairs, hintPair{key: label, word: \"строка\"})\n\t}\n\tif label := m.keymap.label(keySelect); label != \"\" {\n\t\tpairs = append(pairs, hintPair{key: label, word: m.selectHintWord()})\n\t}\n\tif label := m.keymap.label(keyMoveUp); label != \"\" {\n\t\tpairs = append(pairs, hintPair{key: label, word: m.moveUpHintWord()})\n\t}\n\t// Круг фокуса — только когда фокусировать есть чем: в узком режиме панели\n\t// переписки нет вовсе, и обещанные в подсказке Tab были бы клавишами,\n\t// которые ничего не делают.\n\tif m.focusable() {\n\t\tif label := m.keymap.label(keyFocusNext); label != \"\" {\n\t\t\tpairs = append(pairs, hintPair{key: label, word: wallHintPanels})\n\t\t}\n\t}\n\treturn pairs",
		to:        "\treturn []hintPair{{key: \"ctrl+p\", word: \"фильтр\"}, {key: \"ctrl+j\", word: \"строка\"}, {key: \"enter\", word: \"отправить\"}, {key: \"↑\", word: \"лента\"}}",
		wantFails: "TestHintLineFollowsConfiguredKeys",
	},
	{
		// Подсказка про круг фокуса обязана быть привязана к focusable: без
		// условия она обещала бы Tab в узком режиме, где панелей нет и клавиша
		// молчит. Проверка живёт в input_test.go отдельным тестом.
		name:      "подсказка: круг фокуса обещан без открытой переписки",
		file:      "input.go",
		from:      "\tif m.focusable() {\n\t\tif label := m.keymap.label(keyFocusNext); label != \"\" {\n\t\t\tpairs = append(pairs, hintPair{key: label, word: wallHintPanels})\n\t\t}\n\t}",
		to:        "\tif label := m.keymap.label(keyFocusNext); label != \"\" {\n\t\tpairs = append(pairs, hintPair{key: label, word: wallHintPanels})\n\t}",
		wantFails: "TestPanelsHintAppearsOnlyWhenFocusIsPossible",
	},
	{
		// (б) Та же строка, но собранная «наполовину»: подпись действия зашита,
		// а перенос строки по-прежнему от виджета. Мутация важна отдельно от
		// предыдущей: она ловит подмену подписи по одной, а не всей строки.
		name:      "подсказка: подпись действия зашита вместо конфигурации",
		file:      "input.go",
		from:      "\tif label := m.keymap.label(keySelect); label != \"\" {\n\t\tpairs = append(pairs, hintPair{key: label, word: m.selectHintWord()})\n\t}",
		to:        "\tpairs = append(pairs, hintPair{key: \"enter\", word: wallHintSend})",
		wantFails: "TestHintLineFollowsConfiguredKeys",
	},
	{
		// (б) Перенос строки: подпись из keymap'а виджета заменена на зашитую, и
		// подсказка снова может разойтись с тем, что реально нажимается.
		name:      "подсказка: перенос строки подписан зашитой клавишей",
		file:      "input.go",
		from:      "\treturn keyinput.DisplayName(keys[0])",
		to:        "\treturn keyinput.DisplayName(\"ctrl+j\")",
		wantFails: "TestHintLineFollowsConfiguredKeys",
	},
	{
		// (б) Модалка подтверждения: та же логика, что и в строке подсказок, но на
		// другом экране. Подписи enter/esc здесь тем более обязаны быть из
		// конфигурации, что сами Enter и Esc — это keySelect/keyBack.
		name:      "модалка: подписи клавиш зашиты вместо конфигурации",
		file:      "confirm.go",
		from:      "\treturn confirmModalKeys{yes: m.keymap.label(keySelect), no: m.keymap.label(keyBack)}",
		to:        "\treturn confirmModalKeys{yes: \"enter\", no: \"esc\"}",
		wantFails: "TestConfirmModalHintFollowsConfiguredKeys",
	},
	{
		// (в) Фильтр: подпись keyOpenFilter зашита, и правка keybindings.toml меняла
		// бы открытие меню, оставляя подсказку с чужой клавишей.
		name:      "фильтр: подпись клавиши зашита вместо конфигурации",
		file:      "input.go",
		from:      "\tif label := m.keymap.label(keyOpenFilter); label != \"\" {\n\t\tpairs = append(pairs, hintPair{key: label, word: wallHintFilter})\n\t}",
		to:        "\tpairs = append(pairs, hintPair{key: \"ctrl+p\", word: wallHintFilter})",
		wantFails: "TestHintLineFollowsConfiguredKeys",
	},
	{
		// (г) Подпись ↑ больше не зависит от фокуса: одно слово на оба состояния.
		// Ровно тот отказ, который чинится состоянием: в переписке подсказка
		// продолжала бы обещать листание ленты, а стрелка листает сообщения.
		name:      "стрелка вверх: подпись не зависит от фокуса",
		file:      "input.go",
		from:      "\tif m.zoomHasFocus() {\n\t\treturn wallHintChat\n\t}\n\treturn wallHintWall",
		to:        "\treturn wallHintWall",
		wantFails: "TestHintLineUpWordFollowsFocus",
	},
	{
		// (д) Подпись Enter больше не зависит от того, пустое ли поле: пустое обещает
		// отправку того, чего отправлять некуда.
		name:      "enter: подпись не зависит от пустоты поля",
		file:      "input.go",
		from:      "\tif m.wallPanelMode() || m.zoom != nil || m.inputHasText() {\n\t\treturn wallHintSend\n\t}\n\treturn wallHintOpenChat",
		to:        "\treturn wallHintSend",
		wantFails: "TestHintLineEnterWordFollowsState",
	},
	{
		// (е) Тот же класс, но с другой стороны: пустое поле обещает «чат» даже
		// тогда, когда Enter ничего не откроет (чат уже открыт) или есть что
		// отправлять. Мутация важна отдельно от предыдущей — она ловит переусердствие
		// в другую сторону, то есть подсказку враньём наоборот.
		name:      "enter: пустое поле всегда обещает «чат»",
		file:      "input.go",
		from:      "\tif m.wallPanelMode() || m.zoom != nil || m.inputHasText() {\n\t\treturn wallHintSend\n\t}\n\treturn wallHintOpenChat",
		to:        "\treturn wallHintOpenChat",
		wantFails: "TestHintLineEnterWordFollowsState",
	},
	{
		// (ж) Из одних пробелов отправлять нечего: submitInput отбрасывает их
		// через TrimSpace, и без такой проверки подсказка про «чат» появлялась бы
		// из-за символа, который уйдёт в никуда.
		name:      "enter: пробелы считаются набранным текстом",
		file:      "input.go",
		from:      "\treturn strings.TrimSpace(m.input.Value()) != \"\"",
		to:        "\treturn m.input.Value() != \"\"",
		wantFails: "TestHintLineEnterWordFollowsState",
	},
	{
		// (з) Обрезка подсказки снова по символам: на строке из пар это даёт
		// обрывок вроде «ctrl+j строк…», который читается как несуществующее
		// действие. Мутация ловит именно отказ резать по границе пары.
		name:      "подсказка: обрезка по символам вместо границы пары",
		file:      "input.go",
		from:      "\treturn strings.Join(pairs[:kept], wallHintSeparator) + ellipsis",
		to:        "\treturn truncateVisible(text, width)",
		wantFails: "TestHintTruncationCutsOnPairBoundary",
	},
	{
		// (и) Обрезка выбрасывает пары, которые помещались: подсказка на узком
		// терминале молча теряет больше, чем пришлось бы. Мутация важна отдельно
		// от предыдущей — она ловит переусердствие в другую сторону.
		name:      "подсказка: обрезка выбрасывает помещавшиеся пары",
		file:      "input.go",
		from:      "\t\tif used+price+cellWidth(ellipsis) > width {\n\t\t\tbreak\n\t\t}",
		to:        "\t\tif used+price+cellWidth(ellipsis) > width {\n\t\t\tbreak\n\t\t}\n\t\tif kept > 0 {\n\t\t\tbreak\n\t\t}",
		wantFails: "TestHintTruncationCutsOnPairBoundary",
	},
	{
		// (к) Подсказка пропадает на узком терминале совсем: молчание там, где
		// подсказка нужнее всего, хуже обрезанной.
		name:      "подсказка: на узком терминале пропадает совсем",
		file:      "input.go",
		from:      "\tif kept == 0 {\n\t\treturn ellipsis\n\t}",
		to:        "\tif kept == 0 {\n\t\treturn \"\"\n\t}",
		wantFails: "TestHintTruncationKeepsTheSharedEllipsisAndNeverGoesSilent",
	},
	{
		// (л) Слово Enter больше не зависит от выбранной цели ответа: вместо
		// «ответить» подсказка обещает «отправить» — то есть не говорит, ЧТО
		// отправится, ровно то, ради чего слово и выбиралось.
		name:      "enter: подпись не зависит от цели ответа",
		file:      "input.go",
		from:      "\tif m.replyTarget != nil {\n\t\treturn wallHintAnswer\n\t}\n",
		to:        "\tif false {\n\t\treturn wallHintAnswer\n\t}\n",
		wantFails: "TestHintLineEnterWordSaysReplyWhenReplyIsChosen",
	},
	{
		// (м) Целевая подпись показывается всегда, а не только при непустом поле.
		// Мутация ловит отказ отвечать «ответить» там, где цель выбрана.
		name:      "enter: цель ответа обещается не при всяком поле",
		file:      "input.go",
		from:      "\tif m.replyTarget != nil {\n\t\treturn wallHintAnswer\n\t}",
		to:        "\tif m.replyTarget != nil && m.inputHasText() {\n\t\treturn wallHintAnswer\n\t}",
		wantFails: "TestHintLineEnterWordSaysReplyWhenReplyIsChosen",
	},
	{
		// (н) Слова подсказок красятся цветом СОЧЕТЕНИЯ, а не своим: человек видит
		// строку, где и то, и другое одного цвета, и разница пропала.
		name:      "подсказка: слова красятся цветом сочетаний",
		file:      "input.go",
		from:      "\t\t\tforegroundBackgroundStyle(PaletteHintWord, background).Render(pair.word),",
		to:        "\t\t\tforegroundBackgroundStyle(PaletteTextMuted, background).Render(pair.word),",
		wantFails: "TestHintKeysAndWordsHaveTheirOwnColors",
	},
	{
		// (о) Сочетания красятся цветом СЛОВ, а не своим.
		name:      "подсказка: сочетания красятся цветом слов",
		file:      "input.go",
		from:      "\t\t\tforegroundBackgroundStyle(PaletteHintKey, background).Render(pair.key),",
		to:        "\t\t\tforegroundBackgroundStyle(PaletteHintWord, background).Render(pair.key),",
		wantFails: "TestHintKeysAndWordsHaveTheirOwnColors",
	},
	{
		// (п) Строка ответа снова приглушённая: на #555555 она тонет в общей
		// серости панели, и ответ можно отправить, не заметив, на что.
		name:      "строка ответа: приглушённый цвет вместо PaletteTextMuted",
		file:      "input.go",
		from:      "\treply := foregroundBackgroundStyle(PaletteTextMuted, background).",
		to:        "\treply := foregroundBackgroundStyle(PaletteTextFaint, background).",
		wantFails: "TestReplyRowIsNotFainterThanTheHint",
	},
	{
		// (р) Сокращение «ctrl» → «ctr» возвращено (задача 0170 его отменила):
		// строка подсказок снова короче на ячейку на каждое сочетание с ctrl, и
		// на экране появляется префикс, которого человек не нажимает.
		name:      "подсказка: сокращение ctrl возвращено",
		file:      "keymap.go",
		from:      "\treturn keyinput.DisplayName(bindings[0])",
		to:        "\treturn strings.Replace(keyinput.DisplayName(bindings[0]), \"ctrl+\", \"ctr+\", 1)",
		wantFails: "TestHintsShowFullCtrlPrefix",
	},
}

// TestKeymapMutationsAreCaught — каждая мутация из keymapMutantCases обязана быть
// поймана указанным тестом. Смысл и требования к выводу — как у
// TestZoomMutationsAreCaught, поэтому и проверки те же (помощники общие).
func TestKeymapMutationsAreCaught(t *testing.T) {
	for _, mutation := range keymapMutantCases {
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
			// Падать должен ИМЕННО тот тест: если упал соседний, нужная защита может
			// быть и не покрыта, просто сломалось что-то по соседству.
			failed := firstFailure(output)
			if !strings.Contains(failed, mutation.wantFails) {
				t.Fatalf("мутация сломала другой тест (%s), а ждали %s:\n%s",
					failed, mutation.wantFails, tail(output))
			}
			t.Logf("поймана: %s", failed)
		})
	}
}
