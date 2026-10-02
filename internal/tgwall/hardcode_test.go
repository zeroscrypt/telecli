package tgwall

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"telecli/internal/auth"
	"telecli/internal/config"
)

// Проверки из задачи 0159 (аудит на хардкод). Каждая закрывает ровно одну
// найденную находку и проверяет ИНВАРИАНТ, а не совпадение строк: иначе
// следующая правка кода тихо вернула бы хардкод, и тест остался бы зелёным.

// TestMissingTDLibClientIsTheSameErrorEverywhere — клиента нет, и об этом
// сообщают пять команд стены: перезапрос последнего сообщения источника
// добавлен задачей 0165. Сообщение одно (errNoTDLibClient), а не
// пять одинаковых errors.New в пяти файлах: разходиться им нечего, но
// при первой правке формулировки половина стен сказала бы одно, а половина —
// другое, и заметить это можно было бы только вживую.
func TestMissingTDLibClientIsTheSameErrorEverywhere(t *testing.T) {
	m := New(context.Background(), nil, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)

	loadErr := func() error {
		msg, ok := m.wallLoadedCmd()().(wallLoadedErrorMsg)
		if !ok {
			t.Fatalf("команда загрузки стены вернула не wallLoadedErrorMsg")
		}
		return msg.err
	}
	zoomErr := func() error {
		msg, ok := m.wallZoomLoadedCmd(panelChat1, 7, 3)().(wallZoomLoadedMsg)
		if !ok {
			t.Fatalf("команда загрузки переписки вернула не wallZoomLoadedMsg")
		}
		return msg.err
	}
	sendErr := func() error {
		msg, ok := m.sendWallMessageCmd(7, 0, "текст")().(wallSendMessageMsg)
		if !ok {
			t.Fatalf("команда отправки вернула не wallSendMessageMsg")
		}
		return msg.err
	}
	deleteErr := func() error {
		msg, ok := m.deleteMessageCmd(7, 11)().(deleteMessageMsg)
		if !ok {
			t.Fatalf("команда удаления вернула не deleteMessageMsg")
		}
		return msg.err
	}
	latestErr := func() error {
		msg, ok := m.wallCardLatestCmd(7, []int64{11})().(wallCardLatestMsg)
		if !ok {
			t.Fatalf("команда перезапроса вернула не wallCardLatestMsg")
		}
		return msg.err
	}

	for _, test := range []struct {
		name string
		err  error
	}{
		{"загрузка стены", loadErr()},
		{"история переписки", zoomErr()},
		{"отправка", sendErr()},
		{"удаление", deleteErr()},
		{"перезапрос последнего сообщения", latestErr()},
	} {
		if !errors.Is(test.err, errNoTDLibClient) {
			t.Errorf("%s: ошибка %v — не общая errNoTDLibClient", test.name, test.err)
		}
		// Формулировка тоже зафиксирована: она попадает в текст на стене при
		// сорванной загрузке (см. wallNotice), и смена текста — это уже другое
		// решение, а не часть выноса константы.
		if got := test.err.Error(); got != "TDLib client is nil" {
			t.Errorf("%s: текст ошибки = %q, ждали %q", test.name, got, "TDLib client is nil")
		}
	}
}

// TestUnreadMarkerCapAgreesWithItsLabel — предел счётчика и подпись в маркере
// обязаны быть одним числом. Пока «99» было зашито дважды (в сравнении и внутри
// «[99+]»), правка одной из двух записей оставила бы на экране маркер, который
// не сходится с пределом, и заметить это можно было бы только на живом счётчике
// вида «150».
func TestUnreadMarkerCapAgreesWithItsLabel(t *testing.T) {
	if !strings.Contains(unreadMarkerCapLabel, strconv.Itoa(unreadMarkerCap)) {
		t.Fatalf("подпись %q не содержит предел %d — маркер и сравнение разошлись",
			unreadMarkerCapLabel, unreadMarkerCap)
	}

	// Ровно на пределе — ещё точный счётчик, выше — подпись предела.
	if got := unreadMarker(card{UnreadCount: unreadMarkerCap}); got != "[99]" {
		t.Fatalf("на пределе маркер = %q, ждали точный счётчик %q", got, "[99]")
	}
	for _, count := range []int32{unreadMarkerCap + 1, 100, 1000, 1 << 30} {
		want := "[" + unreadMarkerCapLabel + "]"
		if got := unreadMarker(card{UnreadCount: count}); got != want {
			t.Fatalf("выше предела маркер = %q, ждали %q", got, want)
		}
	}

	// Подпись предела обязана влезать в колонку, зарезервированную под маркер:
	// cardUnreadMarkerMaxWidth выведен из «[99+]», и если подпись вырастет, а
	// колонка останется прежней, правый край карточки уедет.
	if got := cellWidth("[" + unreadMarkerCapLabel + "]"); got > cardUnreadMarkerMaxWidth {
		t.Fatalf("маркер предела шириной %d ячеек не влезает в колонку cardUnreadMarkerMaxWidth=%d",
			got, cardUnreadMarkerMaxWidth)
	}
}

// TestCardTagKeepsItsReservedWidth — блок тега занимает ровно отведённую колонку,
// а зазор между названием и маркером равен cardTagLabelGapWidth. Зазор зашивался
// дважды (вычитался из ширины и заполнялся пробелами), и любая из двух записей
// могла разъехаться с другой незаметно — ровно поэтому он теперь один.
func TestCardTagKeepsItsReservedWidth(t *testing.T) {
	marker := "[" + unreadMarkerCapLabel + "]"
	// Короткий тег — настоящий случай: все теги стены выдаёт cardTagOf, и
	// длиннейший из них («#личное», 7 ячеек) влезает в колонку вместе с
	// маркером ровно. Длинный тег — отдельно: он недостижим на живых данных, но
	// показывает границу, за которой блок выходит за cardTagWidth (находка
	// аудита 0159, см. REPORT.md). Закреплено как есть: исправление меняет
	// видимое поведение, и решать его — не этой задаче.
	for _, test := range []struct {
		name     string
		tag      string
		wantWide int
	}{
		{"короткий тег", "#чат", cardTagWidth},
		{"тег длиннее колонки", "#личное-и-ещё-что-то", cardTagWidth + cardUnreadMarkerMaxWidth},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := card{Type: cardChat, Name: "Соседи", Author: "Марина", Time: "13:04",
				Text: "текст", Tag: test.tag, UnreadCount: 150}

			block := ansi.Strip(renderCardTag(c, PaletteBackgroundMain, true, cardTagWidth))
			if got := cellWidth(block); got != test.wantWide {
				t.Fatalf("блок тега шириной %d ячеек, ждали %d (%q)", got, test.wantWide, block)
			}
			if !strings.Contains(block, marker) {
				t.Fatalf("маркер %q не найден в блоке %q", marker, block)
			}

			// Название тега либо влезло целиком, либо обрезано ровно под остаток
			// после зазора: минус ячейка отсюда — это уже другая константа зазора.
			name := strings.TrimSpace(strings.TrimSuffix(strings.TrimRight(block, " "), marker))
			room := cardTagWidth - cardTagLabelGapWidth
			if cellWidth(test.tag) <= room {
				if name != test.tag {
					t.Fatalf("тег %q обрезан до %q, хотя влезал целиком (%q)", test.tag, name, block)
				}
			} else if want := truncateVisible(test.tag, room); name != want {
				t.Fatalf("длинный тег обрезан до %q, ждали %q (%q)", name, want, block)
			}

			// Зазор между названием и маркером ровно cardTagLabelGapWidth: он
			// вычитался из ширины колонки и заполнялся пробелами по отдельности,
			// и разъехаться могла любая из двух записей.
			before := block[:strings.Index(block, marker)]
			gap := cellWidth(before) - cellWidth(strings.TrimRight(before, " "))
			if gap != cardTagLabelGapWidth {
				t.Fatalf("зазор между тегом и маркером = %d ячеек, ждали cardTagLabelGapWidth=%d (%q)",
					gap, cardTagLabelGapWidth, block)
			}
		})
	}
}

// TestInputZoneLinesFitTheFieldWidth — каждая строка блока ввода (подсказки,
// строка ответа, само поле) занимает ровно ширину блока, а префиксы «пробел» и
// «пробел+знак ответа+пробел» — ровно те, что задуманы. Ширина префикса ответа
// раньше была зашита числом 3, то есть молча предполагала одноклеточность знака
// ответа; здесь проверяется, что превью обрезается по-настоящему, а не по
// счастливому совпадению.
func TestInputZoneLinesFitTheFieldWidth(t *testing.T) {
	const width = 20
	// Подпись ответа подаётся сюда текстом: renderInputZone ничего о действиях
	// не знает, и ширину превью считает по тому, что реально рисует. Цвет границы
	// приходит параметром по той же причине: блок рисует, цель ввода выбирает
	// вызывающий (см. inputTargetAccent). Тест про ширины, поэтому любой акцент
	// подходит — берём тот же, что и на стене под личным диалогом.
	reply := strings.Repeat("a", 30)

	lines := renderInputZone(newInput(), width, 1, reply,
		[]hintPair{{key: "ctrl+j", word: "строка"}, {key: "enter", word: "отправить"}},
		PalettePersonal)
	// Блок: поля экрана, левая граница, содержимое шириной width, поля экрана.
	zoneWidth := width + 2*cardMarginH + cardBorderWidth
	if len(lines) != 3 {
		t.Fatalf("блок ввода = %d строк, ждали 3 (подсказка, ответ, поле)", len(lines))
	}
	for index, line := range lines {
		if got := cellWidth(line); got != zoneWidth {
			t.Fatalf("строка %d блока ввода шириной %d ячеек, ждели %d", index, got, zoneWidth)
		}
	}

	// Строка ответа: префикс « ↩ » и превью, обрезанное ровно под остаток. При
	// одноклеточном знаке ответа остаток равен 17 ячейкам: 16 символов и
	// многоточие.
	want := " ↩ " + strings.Repeat("a", width-cellWidth(" ↩ ")-1) + ellipsis
	if got := ansi.Strip(lines[1]); !strings.Contains(got, want) {
		t.Fatalf("строка ответа = %q, ждали префикс и превью %q", got, want)
	}
	// Подсказка начинается с одного пробела, а не впритык к границе.
	if got := ansi.Strip(lines[0]); !strings.HasPrefix(got, strings.Repeat(" ", cardMarginH)+"┃ ") {
		t.Fatalf("строка подсказок = %q, ждали отступ в одну ячейку от границы", got)
	}
}

// TestTruncationEllipsisIsTheSameGlyphEverywhere — обрезанный текст помечается
// одним и тем же знаком в трёх местах: при обрезке по ширине, в «продолжении»
// многострочного сообщения и в превью модалки удаления. Знак взят из одной
// константы ellipsis; «...» из трёх точек в любом из этих мест тихо разошёлся
// бы с остальными двумя, и разница в одну ячейку на экране почти невидима.
func TestTruncationEllipsisIsTheSameGlyphEverywhere(t *testing.T) {
	if cellWidth(ellipsis) != 1 {
		t.Fatalf("многоточие обрезки шириной %d ячеек, а должно быть ровно 1 — иначе обрезка съедает лишнюю ячейку", cellWidth(ellipsis))
	}
	for _, test := range []struct {
		name string
		got  string
	}{
		{"обрезка по ширине", truncateVisible("Купи хлеба", 3)},
		{"продолжение многострочного сообщения", collapsedText("первая строка\nвторая строка", 40)},
		{"превью в модалке удаления", truncateRunes("Купи хлеба по дороге", 5)},
	} {
		if !strings.HasSuffix(test.got, ellipsis) {
			t.Errorf("%s: %q не кончается общим многоточием %q", test.name, test.got, ellipsis)
		}
	}
}

// TestReplyLabelsUseOneFormatOnWallAndInZoom — подпись ответа одна и та же на
// стене и в открытой переписке: различаются только источник заголовка и то, что
// текст сообщения нормализован. Формат и предел превью были зашиты в двух
// файлах по разу.
func TestReplyLabelsUseOneFormatOnWallAndInZoom(t *testing.T) {
	wallCard := card{Type: cardChat, Name: "Соседи", Author: "Марина"}
	text := "Очень длинный текст сообщения, который обрежется в превью"
	zoom := &wallZoom{title: wallCard.title()}

	onWall := replyLabel(card{Type: wallCard.Type, Name: wallCard.Name, Author: wallCard.Author, Text: text})
	inZoom := zoomReplyLabel(zoom, auth.Message{Text: text})
	if onWall != inZoom {
		t.Fatalf("подписи ответа разошлись:\n стена: %q\n зум:   %q", onWall, inZoom)
	}

	// Превью обрезано по wallReplyPreviewWidth, а не «сколько влезло».
	prefix := wallCard.title() + " — \""
	preview := strings.TrimSuffix(strings.TrimPrefix(onWall, prefix), "\"")
	if cellWidth(preview) != wallReplyPreviewWidth {
		t.Fatalf("превью %q шириной %d ячеек, ждали wallReplyPreviewWidth=%d", preview, cellWidth(preview), wallReplyPreviewWidth)
	}

	// Без текста остаётся один заголовок: разделитель и пустые кавычки после
	// него читались бы как оборванная подпись.
	if got := zoomReplyLabel(zoom, auth.Message{}); got != wallCard.title() {
		t.Fatalf("подпись без превью = %q, ждали один заголовок %q", got, wallCard.title())
	}
}

// TestConfirmRenderedLinesMatchTheirWidthFloor — ширина блока модалки считается
// по keys.hint() и по строке выбора, а рисуются обе отдельно. Расхождение между
// посчитанным и нарисованным уехало бы вместе с центрированием, поэтому оно и
// проверяется явно: нарисованная подсказка обязана быть ровно той строкой, по
// которой считали ширину.
func TestConfirmRenderedLinesMatchTheirWidthFloor(t *testing.T) {
	for _, keys := range []confirmModalKeys{
		{yes: "enter", no: "esc"},
		{yes: "y", no: "n"},
	} {
		for _, prompt := range []string{
			confirmDeleteQuestion,
			confirmDeleteQuestion + "\n" + strings.Repeat("о", confirmPreviewRunes),
		} {
			width := confirmModalContentWidth(prompt, keys)
			lines := confirmModalContent(width, prompt, keys)
			// Последние три строки содержимого одинаковы при любом вопросе: пусто,
			// выбор, пусто, подсказка — их позиции считаются с конца, а не по
			// номеру строки вопроса, который зависит от превью.
			if len(lines) < 4 {
				t.Fatalf("содержимое модалки = %d строк, ждали хотя бы 4", len(lines))
			}
			hint, choice := ansi.Strip(lines[len(lines)-1]), strings.TrimSpace(ansi.Strip(lines[len(lines)-3]))

			if !strings.Contains(hint, keys.hint()) {
				t.Errorf("нарисованная подсказка %q разошлась с той, по которой считали ширину: %q", hint, keys.hint())
			}
			// Подсказка обязана влезать в посчитанную ширину: при другой она была
			// бы обрезана по краю блока, и человек увидел бы половину подписи.
			if got := cellWidth(hint); got > width {
				t.Errorf("строка подсказки шириной %d ячеек не влезает в блок шириной %d", got, width)
			}
			if choice != confirmChoiceYes+confirmChoiceNo {
				t.Errorf("строка выбора = %q, ждали %q", choice, confirmChoiceYes+confirmChoiceNo)
			}
			// И сам текст строки выбора зафиксирован: это то, что человек видит
			// в необратимом вопросе, и смена формулировки — не молчаливая правка
			// константы, а изменение того, что обещано.
			if confirmChoiceYes+confirmChoiceNo != "Да / Нет" {
				t.Errorf("строка выбора = %q, а на экране обещано %q", confirmChoiceYes+confirmChoiceNo, "Да / Нет")
			}
		}
	}
}
