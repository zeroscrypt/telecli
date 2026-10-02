package tgwall

import (
	"strings"
	"testing"
)

// Мутационная проверка обязательных требований верхней строки.
//
// Каждая мутация — отказ от одного требования, и падать обязан ровно тот тест,
// который это требование проверяет. Обычная проверка через пример здесь
// недостаточна: строка фильтра и название чата на экране почти всегда
// присутствуют, и «мутация не поймана» чаще всего означает, что тест смотрит не
// туда (например, на отрендеренную строку там, где надо на число).
//
// Механика и хелперы — те же, что в notice_mutation_test.go и
// statusbar_mutation_test.go, свои дубли не заводились.

var topBarMutants = []mutantCase{
	{
		// Требование «строка без вертикальной линии». Линия слева от строки читалась
		// бы как граница панели, которой на экране нет.
		name:      "строка нарисована без обрезки по ширине терминала",
		file:      "topbar.go",
		from:      "return fitLine(line, width, PaletteBackgroundMain)",
		to:        "return line",
		wantFails: "TestTopBarSpansFullWidthWithoutBorderOrMargins",
	},
	{
		// Требование «фон строки — основной, а не панельный»: панельный фон отделил
		// бы строку от стены, то есть она выглядела бы отдельным блоком.
		name:      "строка нарисована панельным фоном",
		file:      "topbar.go",
		from:      "line := foregroundBackgroundStyle(PaletteTextMuted, PaletteBackgroundMain).\n\t\tRender(truncateVisible(source, sourceCells))",
		to:        "line := foregroundBackgroundStyle(PaletteTextMuted, PaletteBackgroundPanel).\n\t\tRender(truncateVisible(source, sourceCells))",
		wantFails: "TestTopBarSpansFullWidthWithoutBorderOrMargins",
	},
	{
		// Требование «подпись фильтра берётся из строк меню, а не из отдельного
		// перечисления». Отдельное перечисление ничего не проверяет, но разъехаться
		// с меню может — и подпись станет врать о том, что на экране настроено.
		name:      "подпись фильтра собирается мимо строк меню",
		file:      "topbar.go",
		from:      "for _, row := range filterMenuRows(m.filter, m.folders) {",
		to:        "for _, row := range []filterRow{{kind: filterRowAll, label: filterLabelAll, checked: true}} {",
		wantFails: "TestTopBarSourceLabelReportsSelectedFilterRows",
	},
	{
		// Требование «„Все“ заменяет собою три типа». Без замены подпись при всех
		// включённых типах читалась бы «Все·Каналы·Чаты·Личные» — одно и то же
		// дважды, и подпись перестала бы влезать в строку.
		name:      "«Все» не заменяет собою три типа",
		file:      "topbar.go",
		from:      "if allTypes && row.kind.isSourceType() {",
		to:        "if allTypes && false {",
		wantFails: "TestTopBarSourceLabelReportsSelectedFilterRows",
	},
	{
		// Требование «название чата только в широком режиме». В узком переписка
		// занимает весь экран и называет себя шапкой, а вторая копия названия в
		// строке шириной во весь экран — шум.
		name:      "название чата рисуется и в узком режиме",
		file:      "topbar.go",
		from:      "if !m.wallPanelMode() || m.zoom == nil {",
		to:        "if m.zoom == nil {",
		wantFails: "TestTopBarTitleStandsOverPanelOnlyInWideMode",
	},
	{
		// Требование «название начинается с колонки панели, а не с нуля». С нуля
		// оно налезало бы на подпись фильтра, и строка читалась бы как одна длинная
		// простыня из двух разных вещей.
		name:      "название чата встаёт с нулевой колонки",
		file:      "topbar.go",
		from:      "return m.zoom.title, topBarTitleStart",
		to:        "return m.zoom.title, 0",
		wantFails: "TestTopBarTitleStandsOverPanelOnlyInWideMode",
	},
	{
		// Требование «шапка переписки только в узком режиме». В широком название
		// уже стоит в верхней строке, и шапка панели была бы вторым его экземпляром.
		name:      "шапка переписки рисуется и в широком режиме",
		file:      "zoom.go",
		from:      "if m.wallPanelMode() {\n\t\treturn 0\n\t}",
		to:        "if false {\n\t\treturn 0\n\t}",
		wantFails: "TestZoomHeaderOnlyTakesRowInCompactMode",
	},
	{
		// Требование «полосы не наезжают друг на друга». Обе подписи получают всю
		// ширину терминала, строка вылезает за край, и хвост названия уезжает за
		// обрезку renderScreen.
		name:      "полосы не ограничены своей шириной",
		file:      "topbar.go",
		from:      "return min(sourceWidth, anchor), min(titleWidth, width-anchor)",
		to:        "return sourceWidth, titleWidth",
		wantFails: "TestTopBarWidthsSplitProportionalWhenBothDoNotFit",
	},
	{
		// Требование «не влезающие подписи делят терминал». Без деления длинное
		// название вытесняло бы подпись фильтра, и человек не видел бы, что
		// показывается на стене вообще.
		name:      "полосы не делятся по ширинам подписей",
		file:      "topbar.go",
		from:      "need := sourceWidth + titleWidth\n\treturn min(width*sourceWidth/need, anchor), min(width*titleWidth/need, width-anchor)",
		to:        "need := sourceWidth + titleWidth\n\tif need < 0 {\n\t\treturn 0, 0\n\t}\n\treturn min(sourceWidth, anchor), min(titleWidth, width-anchor)",
		wantFails: "TestTopBarWidthsSplitProportionalWhenBothDoNotFit",
	},
	{
		// Требование «названию нет — вся строка отдана подписи». Иначе при
		// закрытой переписке справа зияла бы пустая полоса в половину экрана.
		name:      "подпись фильтра ужимается, когда названия нет",
		file:      "topbar.go",
		from:      "if titleWidth <= 0 || anchor <= 0 || anchor >= width {",
		to:        "if false {",
		wantFails: "TestTopBarWidthsSplitProportionalWhenBothDoNotFit",
	},
}

func TestTopBarMutationsAreCaught(t *testing.T) {
	for _, mutation := range topBarMutants {
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
