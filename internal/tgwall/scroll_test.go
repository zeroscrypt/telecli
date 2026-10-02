package tgwall

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/config"
)

// Окно прокрутки стены. Фикстура здесь своя, не testCards: шести карточек не
// хватает на целый экран, и проверять на них окно прокрутки бессмысленно —
// прокручивать нечего. wallScrollCards — поток заметно длиннее экрана, как у
// аккаунта с сотнями чатов.
const (
	// scrollWidth — ширина терминала для фикстур ОКНА ПРОКРУТКИ СТЕНЫ.
	//
	// Узкая намеренно (не шире wallZoomThreshold): с задачи 0155 на широком
	// терминале стена рисуется суженной колонкой wallPanelColumnWidth рядом с
	// панелью переписки, и проверки «карточка влезла в кадр» считали бы высоту
	// колонки, а не стены. Окно прокрутки стены проверяется на том режиме, где
	// стена занимает всю ширину; раскладку широкого режима проверяет zoom_test.go
	// (TestWallColumnKeepsItsWidthOnWideTerminal).
	scrollWidth      = 60
	scrollCardsCount = 40
	// wallScrollHeight — высота стены на терминале scrollWidth x 30 при пустом
	// поле. В формуле стоят те же константы, что и в wallHeight, иначе фикстура
	// разошлась бы с настоящей высотой ровно на две строки каркаса (верхняя
	// строка и строка сообщений).
	wallScrollHeight = 30 - wallReservedRows - wallTopBarRows - wallNoticeRows - inputMinHeight
)

// wallScrollCards — поток из scrollCardsCount карточек с различимыми
// заголовками и однострочным текстом: карточка стены занимает ровно две строки
// (задача 0156), текст в неё помещается, и геометрия окна считается руками, а не
// подгоняется под тест.
func wallScrollCards() []card {
	return wallScrollCardsFrom("Чат", scrollCardsCount)
}

// wallScrollCardsFrom — та же фикстура, но со своим префиксом в заголовке:
// карточки, добавленные вторым приходом, обязаны отличаться от уже нарисованных,
// иначе поиск строки в окне находит не ту карточку.
func wallScrollCardsFrom(prefix string, count int) []card {
	cards := make([]card, 0, count)
	for index := range count {
		cards = append(cards, card{
			Type: cardChat,
			Name: fmt.Sprintf("%s%02d", prefix, index),
			Time: fmt.Sprintf("%02d:%02d", index/60, index%60),
			Text: "сообщение",
			Tag:  "#чат",
		})
	}
	return cards
}

// scrollModel — модель под размер терминала с потоком wallScrollCards, пришедшим
// через wallLoadedMsg, то есть ровно то состояние, в котором стена работает с
// первой секунды.
func scrollModel(t *testing.T) Model {
	t.Helper()
	m := New(context.Background(), nil, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	next, _ := m.Update(tea.WindowSizeMsg{Width: scrollWidth, Height: 30})
	m = next.(Model)
	next, _ = m.Update(wallLoadedMsg{cards: wallScrollCards()})
	m = next.(Model)
	if m.loading {
		t.Fatal("после wallLoadedMsg стена осталась в состоянии загрузки")
	}
	if m.input == nil {
		t.Fatal("поле ввода не создано")
	}
	_ = m.input.Focus()
	return m
}

// wallContentRows — сколько строк стены занимает весь поток карточек начиная с
// from: сами карточки плюс зазор перед каждой, кроме первой. Та же арифметика,
// что и в отрисовке, только без обрезки по высоте окна.
func wallContentRows(cards []card, from, cursor, width int) int {
	rows := 0
	for index := from; index < len(cards); index++ {
		if index > from {
			rows += wallGap
		}
		rows += wallCardHeight(cards[index], width, index == cursor)
	}
	return rows
}

// wallLines — строки зоны стены на экране без входа в разметку.
func wallLines(t *testing.T, m Model) []string {
	t.Helper()
	return wallZoneLines(t, m, splitLines(ansi.Strip(m.renderScreen())))
}

// wallRowOf — номер строки стены, на которой нарисован заголовок карточки index,
// или -1, если карточки в окне нет. Заголовки у фикстуры различимы, так что
// строка находится однозначно.
func wallRowOf(t *testing.T, m Model, index int) int {
	t.Helper()
	if index < 0 || index >= len(m.cards) {
		t.Fatalf("индекс карточки %d вне потока из %d", index, len(m.cards))
	}
	// Заголовки фикстуры короткие и на тестовых ширинах не обрезаются (см.
	// cardTitleText, задача 0156).
	want := m.cards[index].title()
	for row, line := range wallLines(t, m) {
		if strings.Contains(line, want) {
			return row
		}
	}
	return -1
}

// wallCardBottomOf — номер строки НИЖНЕГО края карточки index на экране, или -1,
// если карточки в окне нет. wallRowOf даёт верхний край (по заголовку), а для
// проверок «карточка поместилась в окно целиком» нужен нижний.
//
// Карточка занимает две строки (задача 0156), поэтому «видна» и «видна целиком» —
// разные вещи: карточка, начавшаяся на последней строке стены, нарисована
// половиной, и её текст сообщения не виден вовсе.
func wallCardBottomOf(t *testing.T, m Model, index int) int {
	t.Helper()
	row := wallRowOf(t, m, index)
	if row < 0 {
		return -1
	}
	return row + len(renderCard(m.cards[index], m.width, index == m.cursor)) - 1
}

// clampScrollToCursor проверяется сам по себе, а не через Update: именно здесь
// живёт вся арифметика окна, и прогонять её через нажатия клавиш означало бы
// тестировать сразу и зажим, и прокрутку, и Update — падение в таком тесте
// ничего не говорит.

// Курсор выше окна: окно подъезжает к нему сам, открывая верхние карточки. Без
// этого шаг ↑ на невидимую карточку оставлял бы на экране старую часть потока.
func TestClampScrollToCursorFollowsCursorUp(t *testing.T) {
	cards := wallScrollCards()
	got := clampScrollToCursor(wallCardHeight, cards, 10, 3, scrollWidth, wallScrollHeight)
	if got != 3 {
		t.Fatalf("scrollTop = %d, ждали 3 (индекс курсора)", got)
	}
}

// Курсор ниже окна: сдвиг ровно настолько, чтобы он попал в кадр. «Ровно» здесь
// проверяется с двух сторон — при найденном окне карточка видна, а на одну
// карточку выше уже нет. Сдвиг «в самый низ» прошёл бы первую проверку и провалил
// вторую, а отсутствие подтяжки вообще провалило бы обе.
func TestClampScrollToCursorShiftsOnlyAsFarAsNeeded(t *testing.T) {
	cards := wallScrollCards()
	const cursor = 20

	scrollTop := clampScrollToCursor(wallCardHeight, cards, 0, cursor, scrollWidth, wallScrollHeight)
	if scrollTop <= 0 {
		t.Fatalf("scrollTop = %d, ждали сдвиг вниз: курсор ниже окна", scrollTop)
	}
	if scrollTop >= cursor {
		t.Fatalf("scrollTop = %d, ждали меньше курсора %d: это прыжок в самый низ окна", scrollTop, cursor)
	}

	// При найденном окне карточка в кадре ЦЕЛИКОМ, и ниже неё пустоты больше,
	// чем поместилась бы ещё карточка, — то есть окно сдвинуто минимально и
	// набито плотно.
	//
	// Раньше здесь стояло «верх карточки == последняя строка стены». При
	// однострочной карточке это было возможно, с двумя строками (задача 0156) —
	// нет: стена в 25 строк набирается из 2 + 3k строк содержимого, и 25 среди
	// них нет. Проверять надо «карточка поместилась целиком», а не «её верх
	// совпал с последней строкой»: верх совпасть не может, а поместиться может.
	m := scrollModel(t)
	m.scrollTop = scrollTop
	m.cursor = cursor
	bottom := wallCardBottomOf(t, m, cursor)
	if bottom < 0 {
		t.Fatalf("при scrollTop = %d карточка %d в окне не нарисована", scrollTop, cursor)
	}
	if bottom > wallScrollHeight-1 {
		t.Fatalf("карточка %d занимает строки до %d, а стена кончается на %d — карточка обрезана",
			cursor, bottom, wallScrollHeight-1)
	}
	if tail := wallScrollHeight - 1 - bottom; tail >= wallGap+len(renderCard(m.cards[cursor], m.width, true)) {
		t.Fatalf("под карточкой %d пустоты на %d строк — окно можно было сдвинуть выше", cursor, tail)
	}

	// На одну карточку выше окна карточка уже не влезает — сдвиг минимальный.
	// Проверяется именно «не влезает целиком», а не «не нарисована»: карточка,
	// начавшаяся на предпоследней строке стены, нарисована частично, и
	// заголовок в ней найти можно.
	m.scrollTop = scrollTop - 1
	if got := wallCardBottomOf(t, m, cursor); got >= 0 && got <= wallScrollHeight-1 {
		t.Fatalf("при scrollTop = %d карточка %d целиком в окне (строки до %d) — сдвиг был лишним",
			scrollTop-1, cursor, got)
	}
}

// Курсор уже в окне: окно не трогаем. Иначе стена ползла бы на каждом шаге
// стрелками, и листать было бы невозможно.
// Курсор в окне — окно остаётся на месте. Числа в таблице пересчитаны под
// двухстрочную карточку (задача 0156) и под нынешнюю высоту стены: с задачей
// 0168 каркас съедает на три строки больше, и на стене в wallScrollHeight строк
// карточка с индексом i целиком помещается в окне от from ровно при i-from <= 6
// (её последняя строка попадает на from*3 + 3*6 + 1 = 19-ю строку от верха при
// высоте стены 22).
//
// Прежние числа таблицы (13 и 32) в новой высоте означали бы обрезанную карточку
// под курсором: при i-from = 7 последняя строка карточки попадает на 22-ю строку
// стены, которой уже нет. Окно обязано спуститься на карточку — это проверяется
// отдельно, в TestClampScrollToCursorFollowsCursorUp.
func TestClampScrollToCursorKeepsWindowWhenCursorVisible(t *testing.T) {
	cards := wallScrollCards()
	for _, test := range []struct {
		scrollTop int
		cursor    int
	}{
		{scrollTop: 14, cursor: 20},
		{scrollTop: 10, cursor: 12},
		{scrollTop: 30, cursor: 36},
		{scrollTop: 0, cursor: 0},
	} {
		got := clampScrollToCursor(wallCardHeight, cards, test.scrollTop, test.cursor, scrollWidth, wallScrollHeight)
		if got != test.scrollTop {
			t.Fatalf("scrollTop(%d) при курсоре %d = %d, ждали без изменений", test.scrollTop, test.cursor, got)
		}
	}
}

// Первая загрузка: курсор на последней карточке, и она стоит у нижнего края
// стены. Не «где-то видна» — именно последняя отрисованная строка: стена,
// открытая с самой старой карточки, при сотнях чатов показывала только древний
// хвост потока, и самое свежее сообщение было за пределами экрана.
//
// Число строк на карточку НЕ зашито: каркас экрана менялся (верхняя строка стала
// одной вместо двух), и зашитый scrollTop рано или поздно разошёлся бы с
// реальной высотой стены, причём падал бы на правке, ничего общего с прокруткой не
// имеющей. Вместо него проверяется само свойство: окно упёрлось в низ.
func TestFirstLoadPutsNewestCardAtBottom(t *testing.T) {
	m := scrollModel(t)

	last := len(m.cards) - 1
	if m.cursor != last {
		t.Fatalf("после первой загрузки курсор = %d, ждали %d (последняя карточка)", m.cursor, last)
	}
	// Стена упёрлась в нижний край окна: карточка под scrollTop помещается целиком,
	// а следующая сверху — уже нет. Считается той же арифметикой, что и в
	// отрисовке (wallContentRows), а не вызовом scrollTopToBottom — иначе проверка
	// просто повторила бы саму себя. Само число строк не зашито: каркас экрана
	// менялся, и зашитый scrollTop рано или поздно разошёлся бы с настоящей
	// высотой стены, упав на правке, ничего общего с прокруткой не имеющей.
	if rows := wallContentRows(m.cards, m.scrollTop, m.cursor, scrollWidth); rows > m.wallHeight() {
		t.Fatalf("в окне %d строк, а стена занимает %d: карточка под scrollTop обрезана снизу",
			rows, m.wallHeight())
	}
	if rows := wallContentRows(m.cards, m.scrollTop-1, m.cursor, scrollWidth); rows <= m.wallHeight() {
		t.Fatalf("карточка выше scrollTop занимает %d строк и влезает в %d: стена не упёрлась в нижний край",
			rows, m.wallHeight())
	}

	assertNewestCardFullyAtBottom(t, m, last)
	// Старая часть потока за экраном: стена показывает конец, а не начало.
	if got := wallRowOf(t, m, 0); got != -1 {
		t.Fatalf("самая старая карточка нарисована на строке %d, ждали её за пределами окна", got)
	}
}

// assertNewestCardFullyAtBottom — последняя карточка нарисована ЦЕЛИКОМ и стоит
// как можно ниже.
//
// Раньше проверка требовала точного совпадения нижнего края карточки с нижним
// краем стены, и при однострочной карточке это выполнялось: 1 + 2k давало любую
// нечётную высоту, в том числе 25. С двухстрочной карточкой высоты содержимого
// равны 2 + 3k (2, 5, 8, …, 23, 26) и 25 среди них НЕТ, поэтому точное
// прижатие к низу геометрически невозможно в принципе — либо карточка
// обрезается снизу (и свежее сообщение теряет строку с текстом), либо под ней
// остаётся хвост в 1-2 строки.
//
// Поэтому проверяются два свойства, и оба неочевидны:
//   - карточка не обрезана снизу (это ловит баг с обрезанной свежей карточкой);
//   - хвост короче одной карточки с зазором, то есть окно набито максимально
//     плотно и лишней пустоты снизу не осталось.
func assertNewestCardFullyAtBottom(t *testing.T, m Model, last int) {
	t.Helper()
	row := wallRowOf(t, m, last)
	if row < 0 {
		t.Fatal("последняя карточка не нарисована — свежее сообщение не видно")
	}
	block := len(renderCard(m.cards[last], m.width, true))
	end := row + block - 1
	if end > wallScrollHeight-1 {
		t.Fatalf("карточка занимает строки %d..%d, а стена кончается на %d — карточка обрезана снизу",
			row, end, wallScrollHeight-1)
	}
	if tail := wallScrollHeight - 1 - end; tail >= wallGap+block {
		t.Fatalf("под последней карточкой пустоты на %d строк, а поместилась бы ещё карточка (%d строк) — окно набито не плотно",
			tail, wallGap+block)
	}
}

// Стена длиннее экрана: с каждой стороны ровно столько, сколько влезло, и
// последняя карточка у низа. Проверяется на реальной отрисовке, а не на
// арифметике scrollTop: «у нижнего края» — это про картинку на экране.
func TestWallScrollKeepsCursorVisibleOnEveryArrowStep(t *testing.T) {
	m := scrollModel(t)
	m = m.moveCursor(-1)
	for range scrollCardsCount + 3 {
		if got := wallRowOf(t, m, m.cursor); got < 0 {
			t.Fatalf("курсор %d вне окна (scrollTop %d) после шага по стрелкам", m.cursor, m.scrollTop)
		}
		m = m.moveCursor(-1)
	}
	if m.cursor != 0 {
		t.Fatalf("после зажатия вверх курсор = %d, ждали 0", m.cursor)
	}
	if m.scrollTop != 0 {
		t.Fatalf("после зажатия вверх scrollTop = %d, ждали 0", m.scrollTop)
	}
	if got := wallRowOf(t, m, 0); got < 0 {
		t.Fatalf("первая карточка не нарисована при scrollTop = 0, строка %d", got)
	}
}

// PgUp/PgDn двигают окно примерно на экран карточек (не на фиксированное
// число карточек — карточки разной высоты), зажимаются по границам потока, и
// курсор после сдвига остаётся на экране.
func TestPageKeysScrollByAboutOneScreenAndKeepCursorVisible(t *testing.T) {
	m := scrollModel(t)
	start := m.scrollTop

	m = m.pageScroll(-1)
	if m.scrollTop >= start {
		t.Fatalf("PgUp не поднял окно: scrollTop %d -> %d", start, m.scrollTop)
	}
	if moved := start - m.scrollTop; moved <= 1 || moved >= scrollCardsCount {
		t.Fatalf("PgUp сдвинул окно на %d карточек, ждали примерно на экран", moved)
	}
	if got := wallRowOf(t, m, m.cursor); got < 0 {
		t.Fatalf("после PgUp курсор %d вне окна (scrollTop %d)", m.cursor, m.scrollTop)
	}

	down := m.pageScroll(1)
	if down.scrollTop <= m.scrollTop {
		t.Fatalf("PgDown не опустил окно: scrollTop %d -> %d", m.scrollTop, down.scrollTop)
	}
	if got := wallRowOf(t, down, down.cursor); got < 0 {
		t.Fatalf("после PgDown курсор %d вне окна (scrollTop %d)", down.cursor, down.scrollTop)
	}
	// Спуск и подъём на страницу — обратные друг другу операции: стена обязана
	// вернуться туда же, а не оставить окно на половине сдвига.
	if back := down.pageScroll(-1); back.scrollTop != m.scrollTop {
		t.Fatalf("обратный PgUp вернул scrollTop %d, ждали %d", back.scrollTop, m.scrollTop)
	}
}

// Границы окна: выше самой старой карточки и ниже самой новой прокрутить нельзя,
// сколько PgUp/PgDown ни нажимай.
func TestPageKeysClampAtStreamEdges(t *testing.T) {
	m := scrollModel(t)
	for range scrollCardsCount + 3 {
		m = m.pageScroll(-1)
	}
	if m.scrollTop != 0 {
		t.Fatalf("после PgUp до упора scrollTop = %d, ждали 0", m.scrollTop)
	}
	if m.cursor != 0 {
		t.Fatalf("после PgUp до упора курсор = %d, ждали 0", m.cursor)
	}
	for range scrollCardsCount + 3 {
		m = m.pageScroll(1)
	}
	if m.cursor != len(m.cards)-1 {
		t.Fatalf("после PgDown до упора курсор = %d, ждали %d", m.cursor, len(m.cards)-1)
	}
	// Окно заполнено настолько, насколько есть чем: подтянуть ещё одну карточку
	// сверху уже некуда (либо упёрлись в самую старую, либо она не влезла бы).
	// Прежде здесь ждали scrollTop == len(m.cards)-1 (экран почти пустой, видна
	// только последняя карточка) — это и была добиваемая сейчас ошибка, а не
	// желаемое поведение.
	if m.scrollTop != 0 && cardTotalRows(wallCardHeight, m.cards, m.scrollTop-1, m.cursor, m.wallColumnWidth()) <= wallScrollHeight {
		t.Fatalf("после PgDown до упора экран не заполнен целиком: scrollTop = %d, выше есть ещё карточка, которая влезла бы", m.scrollTop)
	}
}

// PgUp/PgDown в Update, а не только в модели: иначе тесты выше проверяли бы
// функцию, до которой нажатия клавиш не доходят.
// PgUp/PgDown в Update, а не только в модели: иначе тесты выше проверяли бы
// функцию, до которой нажатия клавиш не доходят.
func TestPageKeysReachModel(t *testing.T) {
	m := scrollModel(t)
	start := m.scrollTop

	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyPgUp})
	up := next.(Model)
	if up.scrollTop >= start {
		t.Fatalf("PgUp в Update не поднял окно: %d -> %d", start, up.scrollTop)
	}
	next, _ = up.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
	m = next.(Model)
	if m.scrollTop <= up.scrollTop {
		t.Fatalf("PgDown в Update не опустил окно обратно: %d -> %d", up.scrollTop, m.scrollTop)
	}
	if m.scrollTop != start {
		t.Fatalf("страница вниз и обратно вернула scrollTop %d, ждали %d", m.scrollTop, start)
	}
}

// Повторный приход wallLoadedMsg не сбрасывает ни курсор, ни окно прокрутки.
// Стена уже загружена, человек по ней листает, и каждый новый снимок возвращал бы
// его на самое начало потока. Сам механизм живых апдейтов — следующая задача, но
// условие заложено здесь, чтобы его не пришлось переделывать.
func TestRepeatedLoadKeepsCursorAndWindow(t *testing.T) {
	m := scrollModel(t)
	m = m.pageScroll(-1)
	m = m.pageScroll(-1)
	cursor, scrollTop := m.cursor, m.scrollTop

	grown := append(slices.Clone(m.cards), wallScrollCardsFrom("Свеж", 5)...)
	next, _ := m.Update(wallLoadedMsg{cards: grown})
	m = next.(Model)

	if m.cursor != cursor {
		t.Fatalf("повторный приход сдвинул курсор на %d, ждали %d", m.cursor, cursor)
	}
	if m.scrollTop != scrollTop {
		t.Fatalf("повторный приход сдвинул окно на %d, ждали %d", m.scrollTop, scrollTop)
	}
	if got := wallRowOf(t, m, m.cursor); got < 0 {
		t.Fatalf("после повторного прихода курсор %d вне окна (scrollTop %d)", m.cursor, m.scrollTop)
	}
	// Пришедшие карточки на месте, а не потеряны.
	if len(m.cards) != len(grown) {
		t.Fatalf("после повторного прихода на стене %d карточек, ждали %d", len(m.cards), len(grown))
	}
}

// Мышь у стены нужна только ради того, чтобы терминал не подменял колесо
// десятком нажатий ↑/↓: без явного MouseMode терминалы, не умеющие отдавать мышь
// приложению, делают именно это, и стена уезжает через весь поток. Вторая
// половина того же требования — что само колесо ничего не делает.
func TestMouseEventsMoveNothing(t *testing.T) {
	m := scrollModel(t)
	// Модель не в самом низу: на интересном окне заметнее, что ничего не сдвинулось.
	m = m.pageScroll(-1)

	for name, event := range map[string]tea.MouseMsg{
		"wheel":   tea.MouseWheelMsg{X: 10, Y: 5},
		"click":   tea.MouseClickMsg{X: 10, Y: 5},
		"release": tea.MouseReleaseMsg{X: 10, Y: 5},
		"motion":  tea.MouseMotionMsg{X: 10, Y: 5},
	} {
		next, cmd := m.Update(event)
		after := next.(Model)
		if after.cursor != m.cursor || after.scrollTop != m.scrollTop {
			t.Fatalf("%s сдвинул стену: курсор %d -> %d, scrollTop %d -> %d",
				name, m.cursor, after.cursor, m.scrollTop, after.scrollTop)
		}
		if cmd != nil {
			t.Fatalf("%s вернул команду %T, ждали nil", name, cmd)
		}
	}
}

// Мышь захвачена явно: без MouseModeCellMotion терминал не отдаёт события
// приложению и подменяет колесо нажатиями стрелок (см. предыдущий тест).
func TestViewCapturesMouse(t *testing.T) {
	if got := scrollModel(t).View().MouseMode; got != tea.MouseModeCellMotion {
		t.Fatalf("MouseMode = %v, ждали tea.MouseModeCellMotion", got)
	}
}

// Уменьшение терминала не оставляет курсор за нижним краем: окно обязано
// пересчитаться под новый размер, иначе на экране не осталось бы ни одной
// выбранной карточки.
func TestResizeKeepsCursorVisible(t *testing.T) {
	m := scrollModel(t)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 14})
	m = next.(Model)

	if got := wallRowOf(t, m, m.cursor); got < 0 {
		t.Fatalf("после уменьшения терминала курсор %d вне окна (scrollTop %d)", m.cursor, m.scrollTop)
	}
	assertExactlyOneSelected(t, m)
}
