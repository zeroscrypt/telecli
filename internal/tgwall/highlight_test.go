package tgwall

import (
	"image/color"
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Смысловая подсветка — перенос уже проверенного механизма из internal/tgclitui
// (по прямому указанию человека, «как было раньше»). Тесты здесь — тот же набор
// проверок, что и там, плюс интеграционная проверка, что renderCard реально их
// применяет.

// foregroundSGR — SGR-пролог, которым highlightText красит текст в этот цвет
// переднего плана НА ЭТОМ фоне. Фон обязателен: lipgloss объединяет
// foreground+background в ОДНУ SGR-последовательность (highlightText всегда
// зовёт foregroundBackgroundStyle, никогда голый Foreground), и без фона
// пролог, посчитанный тестом, не совпал бы с тем, что реально в строке.
func foregroundSGR(foreground, background color.Color) string {
	probe := foregroundBackgroundStyle(foreground, background).Render("x")
	start := strings.Index(probe, "\x1b[")
	if start < 0 {
		return ""
	}
	end := strings.Index(probe[start:], "m")
	if end < 0 {
		return ""
	}
	return probe[start : start+end+1]
}

func TestSemanticSpansClassifyTokens(t *testing.T) {
	text := "Загляни на https://example.com/docs @durov #release 42 раза /start English"
	spans := semanticSpans(text)
	var rebuilt strings.Builder
	got := make(map[string]semanticKind)
	for _, span := range spans {
		rebuilt.WriteString(span.text)
		if span.kind != semanticPlain {
			got[span.text] = span.kind
		}
	}
	if rebuilt.String() != text {
		t.Fatalf("токены потеряли текст: %q", rebuilt.String())
	}
	want := map[string]semanticKind{
		"https://example.com/docs": semanticURL,
		"@durov":                   semanticMention,
		"#release":                 semanticTag,
		"42":                       semanticNumber,
		"/start":                   semanticCommand,
		"English":                  semanticEnglish,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("классифицированные токены = %+v, ждали %+v", got, want)
	}
}

func TestSemanticSpansClassifyWWWAndDecimalNumbers(t *testing.T) {
	got := make(map[string]semanticKind)
	for _, span := range semanticSpans("www.example.com 3.14 don't") {
		if span.kind != semanticPlain {
			got[span.text] = span.kind
		}
	}
	want := map[string]semanticKind{
		"www.example.com": semanticURL,
		"3.14":            semanticNumber,
		"don't":           semanticEnglish,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("классифицированные токены = %+v, ждали %+v", got, want)
	}
}

func TestHighlightTextPreservesTextAndAddsEveryRoleColor(t *testing.T) {
	text := "Проверь https://a.ru @bot #tag 7 /run done"
	rendered := highlightText(text, PaletteText, PaletteBackgroundMain)
	if stripped := ansi.Strip(rendered); stripped != text {
		t.Fatalf("подсвеченный текст = %q, ждали %q", stripped, text)
	}
	for _, want := range []color.Color{
		PaletteTextURL,
		PaletteTextMention,
		PaletteTextTag,
		PaletteTextNumber,
		PaletteTextCommand,
		PaletteTextEnglish,
	} {
		if !strings.Contains(rendered, foregroundSGR(want, PaletteBackgroundMain)) {
			t.Fatalf("в отрисованном тексте нет цвета %v: %q", want, rendered)
		}
	}
}

// TestRenderCardHighlightsSemanticTokens — интеграционная проверка: карточка
// стены реально красит смысловые токены, а не только сама функция highlightText
// в изоляции. Карточка выбрана специально: проверка ищет цвета на фоне
// PaletteBackgroundSelected, а он у выбранной карточки один (и на обеих её
// строках — с задачи 0156 текст message лежит на второй).
//
// Проверяется по одному вхождению КАЖДОГО вида токенов: подсветка на стене
// должна работать целиком, а не для URL с числами. Базовый цвет текста при этом
// приглушённый (PaletteTextMuted, см. TestCardTextUsesMutedBaseColor), и
// проверка ищет цвета токенов ПОВЕРХ приглушённого, то есть на том же фоне.
func TestRenderCardHighlightsSemanticTokens(t *testing.T) {
	c := card{Type: cardPersonal, Name: "Андрей",
		Text: "встреча https://t.me/x @durov #release 42 /start English", Tag: "#личное"}
	lines := renderCard(c, 100, true)
	view := strings.Join(lines, "\n")
	for _, want := range []struct {
		name  string
		color color.Color
	}{
		{"URL", PaletteTextURL},
		{"упоминание", PaletteTextMention},
		{"тег", PaletteTextTag},
		{"число", PaletteTextNumber},
		{"команда", PaletteTextCommand},
		{"английское слово", PaletteTextEnglish},
	} {
		if !strings.Contains(view, foregroundSGR(want.color, PaletteBackgroundSelected)) {
			t.Fatalf("в карточке нет подсветки %s (цвет %v): %q", want.name, want.color, view)
		}
	}
}
