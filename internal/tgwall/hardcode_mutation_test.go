package tgwall

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Мутационная проверка правок задачи 0159 (аудит на хардкод).
//
// Файл существует ради одной цели: доказать, что проверки из hardcode_test.go
// ЛОВЯТ возврат каждого хардкода, который они закрывают. Обычная проверка
// проходит и на неправильном коде, если неправильность живёт в ветке, которую
// она не задевает; здесь каждая мутация — это проверка ДРУГОГО теста, а не
// новый тест.
//
// Механика — та же, что у проверок задач 0155/0158 (zoom_mutation_test.go,
// keymap_mutation_test.go): мутируется КОПИЯ пакета в t.TempDir, а `go test
// -run` гоняется по СОДЕРЖИМОМУ копии, то есть по проверкам, а не по
// мутированному коду. Помощники (copyGoModule, runGoTest, firstFailure, tail) —
// общие с теми файлами, здесь переиспользуются.
//
// Отличие от соседних файлов: мутация может состоять из нескольких правок сразу
// (hardcodeMutation.edits). Это нужно ровно для одной находки: «ширина префикса
// строки ответа зашита числом» неотличима от правильного подсчёта, пока знак
// ответа одноклеточный, и поймать её можно только вместе со второй правкой —
// знак становится двухклеточным. Такие пары правок и объясняют, почему здесь
// свой набор правок, а не готовый mutantCase.

// hardcodeEdit — одна замена в одном файле копии.
type hardcodeEdit struct {
	file string
	from string
	to   string
}

// hardcodeMutation — набор правок и проверка, которая обязана на них упасть.
type hardcodeMutation struct {
	name      string
	edits     []hardcodeEdit
	wantFails string
}

var hardcodeMutations = []hardcodeMutation{
	{
		// (а) Ошибка «клиента нет» снова зашита в один из файлов вместо общей
		// errNoTDLibClient. Проверка ловит это по errors.Is: текст тот же, а общей
		// ошибки у этой копии на месте правки больше нет. Правок две, потому что
		// после выноса константы пакет больше не импортирует errors, и мутация без
		// возврата импорта не собралась бы — а не собравшаяся копия доказывает
		// ровно ноль (такое падение поймала бы проверка «--- FAIL», см. ниже).
		name: "клиент: ошибка зашита в файл вместо общей (удаление)",
		edits: []hardcodeEdit{
			{
				file: "confirm.go",
				from: "import (\n\t\"context\"\n\t\"strings\"",
				to:   "import (\n\t\"context\"\n\t\"errors\"\n\t\"strings\"",
			},
			{
				file: "confirm.go",
				from: "return deleteMessageMsg{chatID: chatID, messageID: messageID, err: errNoTDLibClient}",
				to:   "return deleteMessageMsg{chatID: chatID, messageID: messageID, err: errors.New(\"TDLib client is nil\")}",
			},
		},
		wantFails: "TestMissingTDLibClientIsTheSameErrorEverywhere",
	},
	{
		// (а) Та же находка в другом файле: проверка обязана ловить её где угодно,
		// а не только там, где автор смотрел.
		name: "клиент: ошибка зашита в файл вместо общей (отправка)",
		edits: []hardcodeEdit{
			{
				file: "send.go",
				from: "import (\n\t\"context\"\n\t\"strings\"",
				to:   "import (\n\t\"context\"\n\t\"errors\"\n\t\"strings\"",
			},
			{
				file: "send.go",
				from: "inputValue: text, err: errNoTDLibClient}",
				to:   "inputValue: text, err: errors.New(\"TDLib client is nil\")}",
			},
		},
		wantFails: "TestMissingTDLibClientIsTheSameErrorEverywhere",
	},
	{
		// (а) Формулировка общей ошибки тоже зафиксирована: она попадает в текст
		// на стене при сорванной загрузке (см. wallNotice), и её смена — это
		// отдельное решение, а не часть выноса константы.
		name: "клиент: формулировка общей ошибки изменена",
		edits: []hardcodeEdit{{
			file: "model.go",
			from: "var errNoTDLibClient = errors.New(\"TDLib client is nil\")",
			to:   "var errNoTDLibClient = errors.New(\"TDLib client отсутствует\")",
		}},
		wantFails: "TestMissingTDLibClientIsTheSameErrorEverywhere",
	},
	{
		// (б) Предел счётчика и подпись маркера снова живут отдельно: предел стал
		// 999, а подпись по-прежнему обещает «99+» — ровно тот случай, который на
		// живых данных заметил бы только человек со счётчиком вида 150.
		name: "маркер: предел счётчика оторван от подписи",
		edits: []hardcodeEdit{{
			file: "wall.go",
			from: "unreadMarkerCap      = 99",
			to:   "unreadMarkerCap      = 999",
		}},
		wantFails: "TestUnreadMarkerCapAgreesWithItsLabel",
	},
	{
		// (б) Колонка под маркер сузилась, а подпись в неё уже не влезает: правый
		// край карточки уехал бы на ячейку.
		name: "маркер: колонка уже подписи маркера",
		edits: []hardcodeEdit{{
			file: "wall.go",
			from: "cardUnreadMarkerMaxWidth = 5",
			to:   "cardUnreadMarkerMaxWidth = 2",
		}},
		wantFails: "TestUnreadMarkerCapAgreesWithItsLabel",
	},
	{
		// (в) Зазор между тегом и маркером снова зашит числом в одном из двух
		// мест: блок стал шире отведённой колонки, то есть уехал край карточки.
		name: "тег: зазор зашит числом вместо константы",
		edits: []hardcodeEdit{{
			file: "wall.go",
			from: "content = label + renderedFill(cardTagLabelGapWidth, background) + content",
			to:   "content = label + renderedFill(3, background) + content",
		}},
		wantFails: "TestCardTagKeepsItsReservedWidth",
	},
	{
		// (в) Та же находка с другой стороны: обрезка имени тега считает другой
		// зазор, и название тега срезается лишней ячейкой.
		name: "тег: обрезка имени считает другой зазор",
		edits: []hardcodeEdit{{
			file: "wall.go",
			from: "name := truncateVisible(c.Tag, max(0, width-cardTagLabelGapWidth))",
			to:   "name := truncateVisible(c.Tag, max(0, width-cardTagLabelGapWidth-1))",
		}},
		wantFails: "TestCardTagKeepsItsReservedWidth",
	},
	{
		// (г) Ширина префикса строки ответа снова зашита числом. Проверка ловит
		// это только в паре со второй правкой — знак ответа становится
		// двухклеточным: с одноклеточным знаком зашитое «width-3» и правильный
		// подсчёт дают один и тот же результат, и в этом вся суть находки
		// (хардкод, который сегодня случаен, завтра становится ошибкой).
		name: "ответ: ширина префикса зашита числом (плюс двухклеточный знак)",
		edits: []hardcodeEdit{
			{
				file: "input.go",
				from: "Render(replyPrefix + truncateVisible(replyLabel, max(0, width-cellWidth(replyPrefix))))",
				to:   "Render(replyPrefix + truncateVisible(replyLabel, max(0, width-3)))",
			},
			{
				file: "input.go",
				from: "const wallReplyMarker = \"↩\"",
				to:   "const wallReplyMarker = \"↩↩\"",
			},
		},
		wantFails: "TestInputZoneLinesFitTheFieldWidth",
	},
	{
		// (д) Многоточие снова зашито в одном из мест обрезки: знак из трёх точек
		// занимает три ячейки и разошёлся с остальными двумя.
		name: "многоточие: знак зашит вместо общей константы",
		edits: []hardcodeEdit{{
			file: "confirm.go",
			from: "return strings.TrimSpace(string(runes[:limit])) + ellipsis",
			to:   "return strings.TrimSpace(string(runes[:limit])) + \"...\"",
		}},
		wantFails: "TestTruncationEllipsisIsTheSameGlyphEverywhere",
	},
	{
		// (е) Подпись ответа переписки снова собирается по-своему: свой формат и
		// своя склейка, ровно как было до выноса общей сборки.
		name: "ответ: подпись переписки снова собрана отдельно",
		edits: []hardcodeEdit{{
			file: "zoom.go",
			from: "return replyPreviewLabel(zoom.title, normalizeMessageText(message.Text))",
			to:   "return zoom.title + \": \" + truncateVisible(flattenToSingleLine(message.Text), wallReplyPreviewWidth)",
		}},
		wantFails: "TestReplyLabelsUseOneFormatOnWallAndInZoom",
	},
	{
		// (ж) Подсказка модалки снова рисуется не из тех кусков, по которым
		// считается ширина блока. Расхождение на экране не выглядит поломкой —
		// просто тихо уезжает центрирование, — и поймать его можно только такой
		// сверкой.
		name: "модалка: подсказка рисуется не из посчитанной строки",
		edits: []hardcodeEdit{{
			file: "confirm.go",
			from: "muted.Render(confirmHintYesSuffix+wallHintSeparator)",
			to:   "muted.Render(\" — да | \")",
		}},
		wantFails: "TestConfirmRenderedLinesMatchTheirWidthFloor",
	},
	{
		// (ж) Строка выбора: пока обе её части и её ширина берутся из одних
		// констант, правка в любой из двух строк видна проверке. Само «правятся
		// ли они отдельно» тестом не доказывается — пока обе сборки дают один
		// текст, они неразличимы; проверяется ровно то, что видно на экране.
		name: "модалка: изменён текст строки выбора",
		edits: []hardcodeEdit{{
			file: "confirm.go",
			from: "confirmChoiceNo      = \" / Нет\"",
			to:   "confirmChoiceNo      = \" — Нет\"",
		}},
		wantFails: "TestConfirmRenderedLinesMatchTheirWidthFloor",
	},
}

// TestHardcodeMutationsAreCaught — каждая мутация обязана быть поймана указанной
// проверкой. Смысл и требования к выводу — как у TestZoomMutationsAreCaught.
func TestHardcodeMutationsAreCaught(t *testing.T) {
	for _, mutation := range hardcodeMutations {
		t.Run(mutation.name, func(t *testing.T) {
			// Свежая копия на каждую мутацию: часть из них правит один и тот же
			// файл, и на общем дереве вторая не нашла бы свой образец уже
			// изменённым первой.
			source := t.TempDir()
			if err := copyGoModule(t, source); err != nil {
				t.Fatal(err)
			}
			for _, edit := range mutation.edits {
				if err := applyHardcodeEdit(t, source, mutation.name, edit); err != nil {
					t.Fatal(err)
				}
			}
			output, err := runGoTest(t, source, mutation.wantFails)
			if err == nil {
				t.Fatalf("мутация НЕ поймана: %s прошёл на изменённом коде\n%s", mutation.wantFails, tail(output))
			}
			if !strings.Contains(string(output), "--- FAIL") {
				t.Fatalf("падение выглядит не как провал теста, а как ошибка сборки —\n"+
					"такая мутация ничего не доказывает:\n%s", tail(output))
			}
			// Падать должна ИМЕННО та проверка: если упала соседняя, нужная может
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

// applyHardcodeEdit — замена `from` на `to` в указанном файле копии. Замена
// ровно одна: ноль означал бы, что образец устарел и мутация ничего не проверила,
// а несколько — что мутировалось не то место.
func applyHardcodeEdit(t *testing.T, root, name string, edit hardcodeEdit) error {
	t.Helper()
	path := filepath.Join(root, "internal", "tgwall", edit.file)
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	count := strings.Count(string(content), edit.from)
	if count != 1 {
		return fmt.Errorf("мутация %q: образец встречается %d раз (ждали ровно 1) в %s:\n%s",
			name, count, edit.file, edit.from)
	}
	return os.WriteFile(path, []byte(strings.Replace(string(content), edit.from, edit.to, 1)), 0o644)
}
