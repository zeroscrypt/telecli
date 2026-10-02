package tgwall

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Мутационная проверка правок задачи 0166 (цвета палитры источников, галка в меню
// фильтра, переключение по Space, цвет границы поля ввода).
//
// Файл существует ради одной цели: убедиться, что проверки из filtermenu_test.go,
// input_test.go и hardcode_test.go ЛОВЯТ возврат каждого требования задачи. Обычный
// тест проходит и на неправильном коде, если неправильность живёт в ветке, которую
// этот тест не задевает; здесь каждая мутация — это проверка ДРУГОГО теста, а не
// новый тест.
//
// Механика — та же, что у проверок задач 0155/0158/0159 (zoom_mutation_test.go,
// keymap_mutation_test.go, hardcode_mutation_test.go): мутируется КОПИЯ пакета в
// t.TempDir, а `go test -run` гоняется по СОДЕРЖИМОМУ копии, то есть по проверкам,
// а не по мутированному коду. Помощники (copyGoModule, runGoTest, applyMutation,
// firstFailure, tail) — общие с теми файлами.

// paletteMutantCases — по несколько мутаций на каждое из требований задачи.
var paletteMutantCases = []mutantCase{
	{
		// Shift над служебной клавишей обязан давать своё имя. Без этого
		// shift+tab неотличим от tab: обратного хода по кругу фокуса не
		// существует, а нажатие уходит в поле ввода символом.
		//
		// Мутируется keyinput, а не tgwall: потеря происходит в общем слое
		// имён клавиш, и правкой в tgwall она не лечится вовсе.
		name:      "обратный круг фокуса: shift+tab неотличим от tab",
		file:      "keyinput/keyinput.go",
		from:      "\t\treturn modifiersPrefix(key.Mod) + name",
		to:        "\t\treturn name",
		wantFails: "TestShiftTabGoesBackAroundTheFocusCycle",
	},
	{
		// (А) Цвета личного диалога и чата вернулись к прежним. Проверка, на
		// которой падает мутация, сверяет цвета всех трёх типов источников по
		// ТИПУ карточки, поэтому ловит смену любого из них, а не только личного.
		name:      "палитра: цвета типов источников не те",
		file:      "palette.go",
		from:      "PalettePersonal = lipgloss.Color(\"#1A7C33\")",
		to:        "PalettePersonal = lipgloss.Color(\"#fab283\")",
		wantFails: "TestCardAccentFollowsTheSourceType",
	},
	{
		// (А) Тот же класс для чата: прежний сиреневый отличается от нового
		// оранжевого, и проверка обязана видеть разницу, а не «цвет какой-то».
		name:      "палитра: чат вернулся к прежнему цвету",
		file:      "palette.go",
		from:      "PaletteChat     = lipgloss.Color(\"#E99B79\")",
		to:        "PaletteChat     = lipgloss.Color(\"#9d7cd8\")",
		wantFails: "TestCardAccentFollowsTheSourceType",
	},
	{
		// (Б) Отмеченная галка снова красится как подпись строки, а не по типу
		// источника. Именно тот откат, который задача и закрывала: галка была бы
		// видна, но перестала бы узнаваться «своим» цветом. Мутация собирается:
		// вызов accent() остаётся (иначе о нём напомнит компилятор), но его
		// результат не используется.
		name: "меню фильтра: галка снова обычным цветом подписи",
		file: "filtermenu.go",
		from: "\t\tif accent, ok := row.accent(); ok {\n\t\t\tboxLabel = foregroundBackgroundStyle(accent, background)\n\t\t}",
		to:   "\t\tif _, ok := row.accent(); ok {\n\t\t\tboxLabel = label\n\t\t}",
		// Проверка обязана увидеть и сам факт вызова accent(), и его результат —
		// иначе мутация «вызвать и выбросить» прошла бы незамеченной.
		wantFails: "TestFilterMenuCheckedBoxTakesItsTypeAccent",
	},
	{
		// (Б) Тип приписан строкам, у которых его нет. Мутация собирается и здесь:
		// пара «цвет, есть_ли_он» возвращается вместо «нет цвета», и вызывающий код
		// получает «есть» для строк, которым тип не полагается.
		name:      "меню фильтра: строкам без типа приписан чужой цвет",
		file:      "filter.go",
		from:      "default:\n\t\treturn nil, false",
		to:        "default:\n\t\treturn PaletteText, true",
		wantFails: "TestFilterMenuCheckedBoxTakesItsTypeAccent",
	},
	{
		// (Б) Акцент достался всей строке, а не одной галке: подпись уехала в чужой
		// цвет. Ловится отдельной проверкой подписи, а не общей на строку.
		name:      "меню фильтра: акцент достался подписи строки",
		file:      "filtermenu.go",
		from:      "content := strings.Repeat(\" \", filterRowIndent) +\n\t\tboxLabel.Render(box) + \" \" +\n\t\tlabel.Render(truncateVisible(row.label, max(0, width-filterRowPrefix())))",
		to:        "content := strings.Repeat(\" \", filterRowIndent) +\n\t\tboxLabel.Render(box) + \" \" +\n\t\tboxLabel.Render(truncateVisible(row.label, max(0, width-filterRowPrefix())))",
		wantFails: "TestFilterMenuCheckedBoxTakesItsTypeAccent",
	},
	{
		// (Б) Снятая галка получила акцентный цвет типа: внутри пустых скобок цвет
		// значил бы «включено» там, где выключено, и по цвету нельзя было бы
		// отличить состояние строки.
		name:      "меню фильтра: цвет галки не зависит от отметки",
		file:      "filtermenu.go",
		from:      "box, boxLabel := filterBoxOff, label\n\tif row.checked {\n\t\tbox = filterBoxOn\n\t\tif accent, ok := row.accent(); ok {\n\t\t\tboxLabel = foregroundBackgroundStyle(accent, background)\n\t\t}\n\t}",
		to:        "box, boxLabel := filterBoxOff, label\n\tif row.checked {\n\t\tbox = filterBoxOn\n\t}\n\tif accent, ok := row.accent(); ok {\n\t\tboxLabel = foregroundBackgroundStyle(accent, background)\n\t}",
		wantFails: "TestFilterMenuUncheckedBoxStaysPlain",
	},
	{
		// (Б) Галочка снова буква x: на экране это читалось как «здесь что-то не
		// то», а не как «включено».
		name:      "меню фильтра: отмеченный чекбокс снова буква x",
		file:      "filtermenu.go",
		from:      "filterBoxOn  = \"[✓]\"",
		to:        "filterBoxOn  = \"[x]\"",
		wantFails: "TestFilterBoxOnIsACheckOfTheSameWidthAsTheEmptyOne",
	},
	{
		// (Б) Ширина галки снова считается в байтах, а не в ячейках. У ✓ три байта,
		// и строка «прыгала» бы на две ячейки, а обрезка подписи отставала бы от
		// отрисовки ровно настолько же.
		name:      "меню фильтра: ширина галки считается в байтах",
		file:      "filtermenu.go",
		from:      "return filterRowIndent + cellWidth(filterBoxOn) + 1",
		to:        "return filterRowIndent + len(filterBoxOn) + 1",
		wantFails: "TestFilterBoxOnIsACheckOfTheSameWidthAsTheEmptyOne",
	},
	{
		// (В) Переключение галки снова на keySelect, то есть на Enter стены. Тогда
		// Enter в меню переключал бы галку (и обещал бы отправку текста), а
		// пробел — нет.
		name:      "меню фильтра: переключение снова на Enter",
		file:      "filtermenu.go",
		from:      "if m.keymap.pressed(keyToggle, msg) {",
		to:        "if m.keymap.pressed(keySelect, msg) {",
		wantFails: "TestFilterMenuTogglesByConfiguredToggleKey",
	},
	{
		// (В) Пробел в меню печатался бы в поле текстом, а галка не переключалась
		// бы: нажатие ушло бы мимо меню. Самая дорогая из мутаций этого списка —
		// тихий символ в поле вместо действия.
		name:      "меню фильтра: пробел больше не переключает",
		file:      "filtermenu.go",
		from:      "if m.keymap.pressed(keyToggle, msg) {\n\t\treturn m.toggleFilterMenuRow()\n\t}",
		to:        "if m.keymap.pressed(keyToggle, msg) {\n\t\treturn m, nil\n\t}",
		wantFails: "TestFilterMenuTogglesByConfiguredToggleKey",
	},
	{
		// (В) Подсказка меню снова обещает Enter, хотя переключение живёт на
		// пробеле: подсказка, обещающая не ту клавишу, хуже отсутствия подсказки.
		name:      "меню фильтра: подсказка обещает не ту клавишу",
		file:      "filtermenu.go",
		from:      "toggle: m.keymap.label(keyToggle),",
		to:        "toggle: m.keymap.label(keySelect),",
		wantFails: "TestFilterMenuHintShowsTheConfiguredToggleKey",
	},
	{
		// (В) Переключение настроено на Enter вместо пробела — дефолт сменился, и
		// человек нажал бы не ту клавишу, полагаясь на подсказку из конфигурации.
		// Файл лежит не в пакете стены, а в internal/config, поэтому путь задан
		// от корня модуля (см. paletteMutantPath).
		name:      "конфигурация: переключение не на пробеле",
		file:      "config/keybindings.go",
		from:      "Toggle: []string{\"space\"},",
		to:        "Toggle: []string{\"enter\"},",
		wantFails: "TestToggleIsItsOwnConfiguredAction",
	},
	{
		// (Г) Цвет границы поля снова зашит в личный диалог. Именно то, что задача
		// и переделывала: граница показывала «личный» независимо от того, куда
		// человек печатает.
		name:      "поле ввода: цвет границы снова зашит",
		file:      "input.go",
		from:      "border := foregroundBackgroundStyle(accent, background).Render(\"┃\")",
		to:        "border := foregroundBackgroundStyle(PalettePersonal, background).Render(\"┃\")",
		wantFails: "TestInputBorderTakesTheAccentOfTheCardUnderCursor",
	},
	{
		// (Г) Переданный цвет принимается, но игнорируется в рисовании — ровно тот
		// случай, когда сигнатура новая, поведение прежнее, и проверка цели ввода
		// прошла бы, а граница осталась бы одноцветной.
		name:      "поле ввода: переданный цвет не рисуется",
		file:      "input.go",
		from:      "border := foregroundBackgroundStyle(accent, background).Render(\"┃\")",
		to:        "border := foregroundBackgroundStyle(background, background).Render(\"┃\")",
		wantFails: "TestRenderInputZoneUsesTheAccentItIsGiven",
	},
	{
		// (Г) Цель ввода берётся по курсору стены даже при фокусе на переписке:
		// граница обещала бы отправку не туда, куда текст уйдёт на самом деле.
		// Самая опасная мутация списка — человек узнаёт о ней уже после отправки.
		name:      "поле ввода: цель ввода по курсору стены, а не по переписке",
		file:      "input.go",
		from:      "if m.zoomHasFocus() {\n\t\t// Акцент по ЧАТУ В ФОКУСЕ, а не по первой панели: при двух панелях\n\t\t// граница красилась бы цветом чата, в который текст не уйдёт.\n\t\tif index := indexOfChatCard(m.source, m.focusedZoom().chatID); index >= 0 {\n\t\t\treturn m.source[index].accent()\n\t\t}\n\t\treturn PalettePersonal\n\t}",
		to:        "",
		wantFails: "TestInputBorderFollowsTheFocusedConversationNotTheWallCursor",
	},
	{
		// (Г) Акцент типа у карточки перестал доходить до границы: поле рисует
		// общий откат, а не цвет цели. Ловится проверкой по карточке каждого типа
		// отдельно, а не «хоть какой-то цвет».
		name:      "поле ввода: акцент карточки заменён общим откатом",
		file:      "input.go",
		from:      "return m.cards[clampWallCursor(m.cursor, len(m.cards))].accent()",
		to:        "return PalettePersonal",
		wantFails: "TestInputBorderTakesTheAccentOfTheCardUnderCursor",
	},
	{
		// (Г) Источник переписки ищется среди ВИДИМЫХ карточек стены, а не в полном
		// потоке: карточка источника, скрытая фильтром, не нашлась бы — и граница
		// молча откатилась бы вместо цвета переписки, хотя текст уходит туда же.
		name:      "поле ввода: источник переписки ищется среди видимых карточек",
		file:      "input.go",
		from:      "if index := indexOfChatCard(m.source, m.focusedZoom().chatID); index >= 0 {",
		to:        "if index := indexOfChatCard(m.cards, m.focusedZoom().chatID); index >= 0 {",
		wantFails: "TestInputTargetAccentSurvivesTheSourceBeingFilteredOut",
	},
}

// applyPaletteMutation — замена в файле копии. Отличие от applyMutation одно: путь
// ищется не только в пакете стены, но и в остальных пакетах модуля (в этой задаче —
// в internal/config, где живут умолчания клавиш). Имена файлов без слеша относятся к
// пакету стены, как и у общих мутаций, поэтому большинство проверок здесь записаны
// ровно так же.
func applyPaletteMutation(t *testing.T, root string, mutation mutantCase) error {
	t.Helper()
	dir := "tgwall"
	if name, _, found := strings.Cut(mutation.file, "/"); found {
		dir = name
	}
	path := filepath.Join(root, "internal", dir, filepath.Base(mutation.file))
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	count := strings.Count(string(content), mutation.from)
	if count != 1 {
		return fmt.Errorf("мутация %q: образец встречается %d раз (ждали ровно 1) в %s:\n%s",
			mutation.name, count, mutation.file, mutation.from)
	}
	return os.WriteFile(path, []byte(strings.Replace(string(content), mutation.from, mutation.to, 1)), 0o644)
}

// TestPaletteMutationsAreCaught — каждая мутация из paletteMutantCases обязана быть
// поймана указанным тестом. Смысл и требования к выводу — как у
// TestKeymapMutationsAreCaught, поэтому и проверки те же (помощники общие).
func TestPaletteMutationsAreCaught(t *testing.T) {
	for _, mutation := range paletteMutantCases {
		t.Run(mutation.name, func(t *testing.T) {
			source := t.TempDir()
			if err := copyGoModule(t, source); err != nil {
				t.Fatal(err)
			}
			if err := applyPaletteMutation(t, source, mutation); err != nil {
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
