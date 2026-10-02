package tgwall

import (
	"strings"
	"testing"
)

// Мутационная проверка обязательных требований задачи 0167.
//
// Файл существует ради одной цели: убедиться, что проверки из paneloutline_test.go
// ЛОВЯТ отказ от каждого из них. Обычный тест проходит и на неправильном коде, если
// неправильность живёт в ветке, которую этот тест не задевает; здесь каждая мутация
// — это проверка ДРУГОГО теста, а не новый тест.
//
// Помощники (copyGoModule, applyMutation, runGoTest) — те же, что у мутационной
// проверки задачи 0155 (zoom_mutation_test.go): отдельная копия этого механизма
// разошлась бы с уже проверенной при первой правке.

// panelOutlineMutantCases — по мутации на каждое требование: контур отмечает панель в
// фокусе, у другой панели он невидим, цвет берётся у типа открытого чата, без
// открытой переписки контура нет вовсе, и содержимое панели нарисовано в область,
// оставленную под контур.
var panelOutlineMutantCases = []mutantCase{
	{
		// (1) Контур панели в фокусе обязан быть виден. Если забыть про focused,
		// обе панели остаются невидимыми, и переключение по Tab не даёт ничего.
		name:      "контур не зависит от фокуса панели",
		file:      "zoom.go",
		from:      "\tborderColor := PalettePanelBorder\n\tif focused {\n\t\tborderColor = accent\n\t}",
		to:        "\tborderColor := PalettePanelBorder\n\tif false {\n\t\tborderColor = accent\n\t}",
		wantFails: "TestWidePanelOutlineMarksFocusedPanel",
	},
	{
		// (1) У панели БЕЗ фокуса контур невидим, а не акцентный: иначе видна
		// была бы рамка сразу у обеих панелей и переключение фокуса ничего бы не
		// означало.
		name:      "контур невыделенной панели тоже акцентный",
		file:      "zoom.go",
		from:      "\tborderColor := PalettePanelBorder\n\tif focused {\n\t\tborderColor = accent\n\t}",
		to:        "\tborderColor := accent\n\tif focused {\n\t\tborderColor = accent\n\t}",
		wantFails: "TestWidePanelOutlineMarksFocusedPanel",
	},
	{
		// (1) Колонка стены в фокусе, когда фокус на СПИСКЕ: с перепутанным
		// условием контур уезжал бы на переписку, то есть ровно туда, где фокуса
		// нет.
		name:      "контур колонки стены отмечает не тот фокус",
		file:      "zoom.go",
		from:      "\t\tcolumns[0] = panelOutline(left, wallPanelColumnWidth, height, !m.focusPanel.isChatPanel(), m.wallAccent())",
		to:        "\t\tcolumns[0] = panelOutline(left, wallPanelColumnWidth, height, m.focusPanel.isChatPanel(), m.wallAccent())",
		wantFails: "TestWidePanelOutlineMarksFocusedPanel",
	},
	{
		// (1) Панель переписки в фокусе, когда фокус на ПЕРЕПИСКЕ.
		name:      "контур панели переписки отмечает не тот фокус",
		file:      "zoom.go",
		from:      "\t\t\tcolumns[index+1] = panelOutline(columns[index+1], m.wallPanelWidth(panel), height,\n\t\t\t\tm.panelFocused(panel), m.panelAccent(panel))",
		to:        "\t\t\tcolumns[index+1] = panelOutline(columns[index+1], m.wallPanelWidth(panel), height,\n\t\t\t\t!m.panelFocused(panel), m.panelAccent(panel))",
		wantFails: "TestWidePanelOutlineMarksFocusedPanel",
	},
	{
		// (2) Цвет контура — тип ОТКРЫТОГО чата. Постоянный цвет вместо него
		// означал бы, что на канале, в чате и в личном диалоге контур выглядит
		// одинаково, и подсказки «здесь фокус» он бы не нёс.
		name:      "цвет контура не зависит от типа чата",
		file:      "zoom.go",
		from:      "\tzoom := m.panelZoom(panel)\n\tif zoom == nil {\n\t\treturn PaletteBackgroundMain\n\t}\n\tif index := indexOfChatCard(m.source, zoom.chatID); index >= 0 {\n\t\treturn m.source[index].accent()\n\t}",
		to:        "\tzoom := m.panelZoom(panel)\n\tif zoom == nil {\n\t\treturn PaletteBackgroundMain\n\t}\n\tif index := indexOfChatCard(m.source, zoom.chatID); index >= 0 {\n\t\treturn PaletteText\n\t}",
		wantFails: "TestWidePanelOutlineColorFollowsOpenChatType",
	},
	{
		// (2) Тип берётся у того источника, чья переписка открыта, а не у первого
		// попавшегося на стене.
		name:      "цвет контура взят у чужой карточки стены",
		file:      "zoom.go",
		from:      "\tzoom := m.panelZoom(panel)\n\tif zoom == nil {\n\t\treturn PaletteBackgroundMain\n\t}\n\tif index := indexOfChatCard(m.source, zoom.chatID); index >= 0 {\n\t\treturn m.source[index].accent()\n\t}",
		to:        "\tzoom := m.panelZoom(panel)\n\tif zoom == nil {\n\t\treturn PaletteBackgroundMain\n\t}\n\tif index := indexOfChatCard(m.source, zoom.chatID); index >= 0 {\n\t\treturn m.source[0].accent()\n\t}",
		wantFails: "TestWidePanelOutlineColorFollowsOpenChatType",
	},
	{
		// (3) Контур рисуется в широком режиме ВСЕГДА, даже когда переписка ещё
		// не открыта: он отвечает на вопрос «где фокус», а не «есть ли чат».
		// Мутация возвращает прежнее требование открытой переписки — и на свежей
		// стене снова не видно ни одного контура.
		name:      "контур ждёт открытой переписки",
		file:      "zoom.go",
		from:      "\treturn m.wallPanelMode()",
		to:        "\treturn m.wallPanelMode() && m.zoom != nil",
		wantFails: "TestWideModeWithoutZoomStillDrawsPanelOutline",
	},
	{
		// (4) Окно прокрутки стены и переписки считается по высоте содержимого
		// панели, а не по высоте зоны. Ловится значением, а не экраном: отрисовка
		// пересчитывает окно сама уже по правильной высоте, и на экране такая
		// ошибка почти нигде не видна (см. комментарий к
		// TestPanelContentSizeIsPanelSizeMinusOutline).
		name:      "окно панелей считается по высоте зоны, а не содержимого",
		file:      "zoom.go",
		from:      "\tif m.wallPanelMode() {\n\t\treturn panelContentHeightFor(m.wallHeight())\n\t}\n\treturn m.wallHeight()",
		to:        "\treturn m.wallHeight()",
		wantFails: "TestPanelContentSizeIsPanelSizeMinusOutline",
	},
	{
		// (4, вторая половина) Место под контур вычитается из содержимого ДО
		// отрисовки. Мутируется общая функция (panelContentHeightFor), а не место
		// вызова в renderWallZone: у экранной проверки этот класс ошибки в принципе
		// не виден (panelOutline защитно обрезает содержимое по хвосту до
		// правильной, немутированной высоты кадра — см. комментарий у
		// panelContentHeightFor и у TestPanelContentHeightForSubtractsOutlineInset),
		// поэтому ловит её проверка по значению, а не по экрану.
		name:      "место под контур содержимому не вычитается",
		file:      "zoom.go",
		from:      "\treturn max(0, zoneHeight-wallPanelOutlineInset)",
		to:        "\treturn zoneHeight",
		wantFails: "TestPanelContentHeightForSubtractsOutlineInset",
	},
}

// TestPanelOutlineMutationsAreCaught — каждая мутация из panelOutlineMutantCases
// обязана быть поймана указанным тестом. Механизм копирования модуля и прогона — тот
// же, что у TestZoomMutationsAreCaught, и на общих помощниках.
func TestPanelOutlineMutationsAreCaught(t *testing.T) {
	for _, mutation := range panelOutlineMutantCases {
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
