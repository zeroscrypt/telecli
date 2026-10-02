package tgwall

import (
	"image/color"
	"regexp"
	"strings"
)

// Смысловая подсветка текста карточки: ссылки, @упоминания, #теги, /команды,
// английские слова и числа красятся отдельно от обычного текста — то же самое,
// что уже годами работает в ленте internal/tgclitui (highlight.go), перенесено
// сюда по прямому указанию человека («как было раньше»), а не придумано заново.

// semanticKind — смысловая роль фрагмента текста.
type semanticKind uint8

const (
	semanticPlain semanticKind = iota
	semanticURL
	semanticMention
	semanticTag
	semanticCommand
	semanticNumber
	semanticEnglish
)

// semanticTokenPattern разбивает текст на смысловые токены. Порядок альтернатив
// значим: Go RE2 выбирает первую подошедшую альтернативу в самой левой позиции,
// поэтому URL проверяется раньше отдельных слов и цифр внутри него, а
// /command — раньше обычных слов.
var semanticTokenPattern = regexp.MustCompile(`(?i)` +
	`(?P<url>(?:https?://|www\.)\S+)` +
	`|(?P<mention>@[A-Za-z0-9_]+)` +
	`|(?P<tag>#[A-Za-z0-9_]+)` +
	`|(?P<command>/[A-Za-z][A-Za-z0-9_.-]*)` +
	`|(?P<english>[A-Za-z]+(?:['’-][A-Za-z]+)*)` +
	`|(?P<number>[0-9]+(?:[.,][0-9]+)*)`)

// semanticKindByGroup сопоставляет порядок групп в semanticTokenPattern с ролями.
// Именно на этом порядке держится classifySemanticToken.
var semanticKindByGroup = [...]semanticKind{
	semanticURL,
	semanticMention,
	semanticTag,
	semanticCommand,
	semanticEnglish,
	semanticNumber,
}

type textSpan struct {
	text string
	kind semanticKind
}

// semanticSpans разбивает текст на чередующиеся смысловые и обычные фрагменты.
// Пробелы и знаки препинания сохраняются как semanticPlain — текст карточки
// должен читаться без потерь, подсветка ничего не съедает.
func semanticSpans(text string) []textSpan {
	if text == "" {
		return nil
	}
	matches := semanticTokenPattern.FindAllStringSubmatchIndex(text, -1)
	if len(matches) == 0 {
		return []textSpan{{text: text, kind: semanticPlain}}
	}

	spans := make([]textSpan, 0, 2*len(matches)+1)
	position := 0
	for _, match := range matches {
		start, end := match[0], match[1]
		if start > position {
			spans = append(spans, textSpan{text: text[position:start], kind: semanticPlain})
		}
		spans = append(spans, textSpan{text: text[start:end], kind: classifySemanticToken(match)})
		position = end
	}
	if position < len(text) {
		spans = append(spans, textSpan{text: text[position:], kind: semanticPlain})
	}
	return spans
}

func classifySemanticToken(match []int) semanticKind {
	for group := range semanticKindByGroup {
		if 2*(group+1) < len(match) && match[2*(group+1)] >= 0 {
			return semanticKindByGroup[group]
		}
	}
	return semanticPlain
}

// semanticColor возвращает цвет роли; semanticPlain идёт базовым цветом
// текста сообщения, чтобы обычный текст не ломался.
func semanticColor(kind semanticKind, base color.Color) color.Color {
	switch kind {
	case semanticURL:
		return PaletteTextURL
	case semanticMention:
		return PaletteTextMention
	case semanticTag:
		return PaletteTextTag
	case semanticCommand:
		return PaletteTextCommand
	case semanticNumber:
		return PaletteTextNumber
	case semanticEnglish:
		return PaletteTextEnglish
	default:
		return base
	}
}

// highlightText раскрашивает смысловые токены, сохраняя исходный текст
// посимвольно. Каждый фрагмент несёт СВОИ fg+bg, а не полагается на стиль
// обёртки: lipgloss при переносе строк вставляет reset, и внешний Foreground
// после него уже не действовал бы — из-за этого все фрагменты выглядели бы
// дефолтным цветом терминала.
func highlightText(text string, base, background color.Color) string {
	spans := semanticSpans(text)
	if len(spans) == 0 {
		return foregroundBackgroundStyle(base, background).Render(text)
	}
	var builder strings.Builder
	for _, span := range spans {
		builder.WriteString(foregroundBackgroundStyle(semanticColor(span.kind, base), background).Render(span.text))
	}
	return builder.String()
}
