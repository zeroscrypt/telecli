package tgwall

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Найдено человеком вживую: сообщение с «ℹ️» (ℹ + U+FE0F, вариационный
// модификатор «эмодзи-презентация») съезжало вправо относительно соседних
// карточек. Причина — activeWidthMethod (model.go) был жёстко GraphemeWidth
// (через голый lipgloss.Width), а bubbletea на конкретном терминале
// человека не подтвердил Unicode-режим DEC 2027 и остался на WcWidth — наша
// вёрстка и настоящий рендер терминала считали ширину по-разному.
//
// withWidthMethod возвращает activeWidthMethod к тому, что было, после теста:
// это пакетная переменная, и тест, оставивший её в GraphemeWidth, тихо
// сломал бы соседний тест, ожидающий дефолт.
func withWidthMethod(t *testing.T, method ansi.Method) {
	t.Helper()
	previous := activeWidthMethod
	activeWidthMethod = method
	t.Cleanup(func() { activeWidthMethod = previous })
}

// По умолчанию, пока терминал ничего не подтвердил, счёт — WcWidth, тот же,
// с которого стартует и сам renderer bubbletea (проверено чтением исходников
// ultraviolet.NewScreenBuffer: "Method: ansi.WcWidth") — не GraphemeWidth,
// которым голый lipgloss.Width считает всегда.
//
// Сравнивается КОНСТАНТА defaultWidthMethod, а не текущее значение
// activeWidthMethod: последняя — общая для пакета переменная, и любой другой
// тест мог уже её поменять до этого (порядок тестов Go не гарантирует), так
// что проверка через неё ловила бы чужое состояние, а не объявленный дефолт.
func TestDefaultWidthMethodIsWcWidth(t *testing.T) {
	if defaultWidthMethod != ansi.WcWidth {
		t.Fatalf("defaultWidthMethod = %v, ждали ansi.WcWidth", defaultWidthMethod)
	}
}

// Терминал подтвердил Unicode-режим (DEC 2027) — cellWidth обязан считать так
// же, как теперь считает и сам терминал: GraphemeWidth, «ℹ️» занимает 2 ячейки.
// Три значения ModeReportMsg (Set/Reset/PermanentlySet) — тот же список, что
// использует сам bubbletea (tea.go) для решения «терминал подтвердил» — ни
// одно не пропущено.
func TestModeReportMsgUpgradesToGraphemeWidth(t *testing.T) {
	cases := []struct {
		name  string
		value ansi.ModeSetting
	}{
		{"ModeSet", ansi.ModeSet},
		{"ModeReset", ansi.ModeReset},
		{"ModePermanentlySet", ansi.ModePermanentlySet},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			withWidthMethod(t, ansi.WcWidth)
			m := newTestModel(t, 100, 30)
			next, _ := m.Update(tea.ModeReportMsg{Mode: ansi.ModeUnicodeCore, Value: testCase.value})
			_ = next.(Model)
			if got := cellWidth("ℹ️"); got != 2 {
				t.Fatalf("после ModeReportMsg{%s} cellWidth(\"ℹ️\") = %d, ждали 2", testCase.name, got)
			}
		})
	}
}

// Апдейт про ЧУЖОЙ режим (не Unicode core) не трогает activeWidthMethod —
// иначе синхронизированный вывод или любой другой отчёт о режиме мог бы
// случайно переключить счёт ширины не по делу.
func TestModeReportMsgIgnoresOtherModes(t *testing.T) {
	withWidthMethod(t, ansi.WcWidth)
	m := newTestModel(t, 100, 30)
	next, _ := m.Update(tea.ModeReportMsg{Mode: ansi.ModeSynchronizedOutput, Value: ansi.ModeSet})
	_ = next.(Model)
	if got := cellWidth("ℹ️"); got != 1 {
		t.Fatalf("апдейт про синхронизированный вывод переключил ширину: cellWidth(\"ℹ️\") = %d, ждали 1", got)
	}
}

// «Не признанный» ответ (терминал не понял запрос вовсе, ModeNotRecognized —
// нулевое значение) тоже не должен переключать на GraphemeWidth: это ровно
// тот случай, когда терминал точно НЕ поддерживает mode 2027.
func TestModeReportMsgNotRecognizedStaysOnWcWidth(t *testing.T) {
	withWidthMethod(t, ansi.WcWidth)
	m := newTestModel(t, 100, 30)
	next, _ := m.Update(tea.ModeReportMsg{Mode: ansi.ModeUnicodeCore, Value: ansi.ModeNotRecognized})
	_ = next.(Model)
	if got := cellWidth("ℹ️"); got != 1 {
		t.Fatalf("ModeNotRecognized переключил ширину: cellWidth(\"ℹ️\") = %d, ждали 1", got)
	}
}

// Интеграционная проверка самой находки: на терминале без mode 2027 (WcWidth)
// карточка с эмодзи в начале текста держит ту же геометрию, что и карточка без
// такого символа.
//
// После задачи 0156 текст сообщения лежит на строке 2, а блок тега — на строке 1,
// поэтому прежняя проверка «тег на одной и той же колонке» стала тавтологией:
// колонка тега задаётся только длиной заголовка, а эмодзи в тексте её не
// касается. Проверяется то, что всё ещё может разъехаться из-за чужого метода
// ширины: одинаковая видимая ширина ОБЕИХ строк у обеих карточек и одинаковая
// колонка начала текста (отступ строки 2).
func TestCardWithPresentationEmojiAlignsLikeAnyOther(t *testing.T) {
	withWidthMethod(t, ansi.WcWidth)
	emojiPrefix := "\u2139\ufe0f"
	withEmoji := card{Type: cardPersonal, Name: "Kinogo", Text: emojiPrefix + "Новое зеркало", Tag: "#личное"}
	plain := card{Type: cardPersonal, Name: "Andrey", Text: "Новое зеркало", Tag: "#личное"}

	emojiLines := renderCard(withEmoji, 100, false)
	plainLines := renderCard(plain, 100, false)

	indent := cardMarginH + cardBorderWidth
	for index := range emojiLines {
		emojiStripped := ansi.Strip(emojiLines[index])
		plainStripped := ansi.Strip(plainLines[index])
		if w1, w2 := cellWidth(emojiStripped), cellWidth(plainStripped); w1 != w2 {
			t.Fatalf("строка %d разной видимой ширины: %d (с эмодзи) против %d (без)", index, w1, w2)
		}
	}
	// LastIndex даёт БАЙТОВОЕ смещение, а не число клеток (эмодзи из information
	// source — 6 байт UTF-8, но 1-2 ячейки) — колонку сравниваем через cellWidth
	// префикса до него, не через сырой индекс строки.
	plainStripped := ansi.Strip(plainLines[1])
	emojiStripped := ansi.Strip(emojiLines[1])
	plainByteIdx := strings.LastIndex(plainStripped, "Новое зеркало")
	emojiByteIdx := strings.LastIndex(emojiStripped, "Новое зеркало")
	if plainByteIdx < 0 || emojiByteIdx < 0 {
		t.Fatalf("текст «Новое зеркало» не найден: plain=%d, emoji=%d", plainByteIdx, emojiByteIdx)
	}
	textCol := cellWidth(plainStripped[:plainByteIdx])
	textColWithEmoji := cellWidth(emojiStripped[:emojiByteIdx])
	// Начало текста обязано совпасть с отступом у ОБЕИХ карточек плюс, у второй,
	// собственная ширина эмодзи: символ стоит перед текстом и занимает ровно
	// столько клеток, сколько решает метод ширины. Под WcWidth «ℹ️» — две клетки,
	// и проверка обязана это учитывать, иначе она запрещала бы честное поведение.
	if textCol != indent {
		t.Fatalf("текст без эмодзи начинается на колонке %d, ждали %d (отступ строки 2)", textCol, indent)
	}
	wantEmojiCol := indent + cellWidth(emojiPrefix)
	if textColWithEmoji != wantEmojiCol {
		t.Fatalf("текст с эмодзи начинается на колонке %d, ждали %d (отступ %d + ширина эмодзи %d) — карточка съезжает",
			textColWithEmoji, wantEmojiCol, indent, cellWidth(emojiPrefix))
	}
}
