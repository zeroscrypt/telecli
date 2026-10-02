package tgwall

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Строка временных системных сообщений (задача 0168): одна строка между стеной и
// блоком ввода, занятая всегда, и текст в ней живёт ровно до своего таймера.
//
// Таймер здесь не ждётся: в модель приходит то же сообщение, что прислал бы
// tea.Tick (ровно так же проверяется спиннер в statusbar_test.go), и ждать пять
// секунд ради проверки обработчика незачем.

// noticeLine — строка сообщений на экране. Берётся по геометрии каркаса (два
// бара вверху, зона стены, строка), а не поиском по тексту: иначе проверка «строка
// на месте» зависела бы от того, что в ней написано, и молча проходила бы на
// пустой строке.
func noticeLine(t *testing.T, m Model) string {
	t.Helper()
	lines := splitLines(m.renderScreen())
	index := wallTopBarRows + m.wallHeight()
	if index >= len(lines) {
		t.Fatalf("строка сообщений (%d) за пределами экрана из %d строк:\n%s",
			index, len(lines), strings.Join(lines, "\n"))
	}
	return lines[index]
}

// expireSystemNotice — таймер гашения приходит в модель тем же путём, что и в
// живой программе: через Update, сообщением с версией.
func expireSystemNotice(t *testing.T, m Model, id int) Model {
	t.Helper()
	next, _ := m.Update(systemNoticeExpiredMsg{id: id})
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("Update вернул %T, ждали Model", next)
	}
	return updated
}

func TestSystemNoticeIsVisibleRightAfterShowing(t *testing.T) {
	m := newTestModel(t, 100, 30)
	m, cmd := m.ShowSystemNotice("Отправлено")
	// Команда — обязательна: без неё сообщение висело бы на экране навсегда.
	if cmd == nil {
		t.Fatal("метод не вернул команду таймера")
	}

	plain := ansi.Strip(noticeLine(t, m))
	if got := strings.TrimSpace(plain); got != "Отправлено" {
		t.Fatalf("строка сообщений = %q, ждали %q", got, "Отправлено")
	}
	// По центру строки, а не по левому краю стены.
	text := "Отправлено"
	if got, want := strings.Index(plain, text), (m.width-cellWidth(text))/2; got != want {
		t.Fatalf("текст начинается с %d-й ячейки, ждали %d (по центру): %q", got, want, plain)
	}
}

func TestSystemNoticeGoesAwayOnItsOwnTimer(t *testing.T) {
	m := newTestModel(t, 100, 30)
	m, _ = m.ShowSystemNotice("Отправлено")

	m = expireSystemNotice(t, m, m.notice.id)
	if m.notice != nil {
		t.Fatalf("сообщение %q пережило свой таймер", m.notice.text)
	}
	// Гаснет текст, а не строка: место под неё остаётся, иначе экран прыгал бы.
	if got := strings.TrimSpace(ansi.Strip(noticeLine(t, m))); got != "" {
		t.Fatalf("строка сообщений после гашения = %q, ждали пустую", got)
	}
}

// Устаревший таймер не гасит более новое сообщение: тот же класс ошибки, что уже
// ловили на загрузке переписки (устаревший ответ не применяется). Сообщение,
// сменившееся за пять секунд, должно дожить своих пяти.
func TestStaleNoticeTimerKeepsTheNewerNotice(t *testing.T) {
	m := newTestModel(t, 100, 30)
	m, _ = m.ShowSystemNotice("Первое")
	first := m.notice.id
	m, _ = m.ShowSystemNotice("Второе")
	second := m.notice.id
	if first == second {
		t.Fatalf("второе сообщение получило ту же версию %d: таймер от первого погасил бы и его", first)
	}

	// Таймер от ПЕРВОГО приходит, когда на экране уже ВТОРОЕ.
	m = expireSystemNotice(t, m, first)
	if m.notice == nil || m.notice.text != "Второе" {
		t.Fatalf("устаревший таймер погасил свежее сообщение: %+v", m.notice)
	}
	if got := strings.TrimSpace(ansi.Strip(noticeLine(t, m))); got != "Второе" {
		t.Fatalf("строка сообщений = %q, ждали %q", got, "Второе")
	}

	// А таймер от второго — уже его.
	m = expireSystemNotice(t, m, second)
	if m.notice != nil {
		t.Fatalf("сообщение %q пережило свой таймер", m.notice.text)
	}
}

// Строка сообщений занята всегда, даже когда сообщения нет: экран не должен
// прыгать на строку в момент его появления и исчезновения.
func TestEmptyNoticeRowIsWallBackgroundNotPanel(t *testing.T) {
	m := newTestModel(t, 100, 30)
	row := noticeLine(t, m)

	if got := cellWidth(row); got != m.width {
		t.Fatalf("строка сообщений без сообщения — %d ячеек, ждали %d", got, m.width)
	}
	// Фон основной: строка часть потока стены, а не отдельная панель. Панельный
	// фон превратил бы её в ещё один блок, и разделителя над ней нет.
	if !strings.Contains(row, backgroundSGR(t, PaletteBackgroundMain)) {
		t.Fatalf("строка сообщений нарисована не фоном стены: %q", row)
	}
	if strings.Contains(row, backgroundSGR(t, PaletteBackgroundPanel)) {
		t.Fatalf("строка сообщений нарисована панельным фоном — она выглядит отдельной панелью: %q", row)
	}
}

// «Одно-два слова» это ожидаемый ввод, а не гарантия, которую код обязан
// проверять: длинный текст обрезается и остаётся ровно одной строкой. Без
// обрезки липглосс перенёс бы его на вторую строку, и расчёт высоты стены
// разошёлся бы с отрисовкой.
func TestSystemNoticeTruncatesLongText(t *testing.T) {
	m := newTestModel(t, 60, 30)
	m, _ = m.ShowSystemNotice(strings.Repeat("длинное сообщение ", 5))

	plain := ansi.Strip(noticeLine(t, m))
	if got := cellWidth(plain); got != m.width {
		t.Fatalf("строка сообщений — %d ячеек, ждали %d:\n%q", got, m.width, plain)
	}
	if !strings.Contains(plain, ellipsis) {
		t.Fatalf("длинный текст не обрезан многоточием %q:\n%q", ellipsis, plain)
	}
	if strings.Contains(plain, "сообщение сообщение") {
		t.Fatalf("в строке больше одного сообщения — она не помещается в одну строку:\n%q", plain)
	}
}

// Поле не налезает на новые строки каркаса: с длинным значением на маленьком
// терминале предел роста поля (applyLayout) обязан учитывать и бары, и строку
// сообщений. Иначе экран становится выше терминала, и нижняя строка со спиннером
// уезжает за обрезку renderScreen.
func TestLongInputDoesNotEatTheScreenFrame(t *testing.T) {
	const width, height = 60, 20
	m := newTestModel(t, width, height)
	// Много переносов: поле растёт вверх, пока упрётся в предел.
	for range 2 * height {
		next, _ := m.Update(tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
		m = next.(Model)
	}
	if m.inputRows <= inputMinHeight {
		t.Fatalf("поле выросло всего на %d строк — тест не дождался роста", m.inputRows)
	}

	lines := splitLines(m.renderScreen())
	if len(lines) != height {
		t.Fatalf("строк на экране %d, ждали %d — каркас не уместился в терминал", len(lines), height)
	}
	if last := ansi.Strip(lines[len(lines)-1]); !strings.Contains(last, testAppName) {
		t.Fatalf("последняя строка экрана — не нижняя, поле съело каркас:\n%s", strings.Join(lines, "\n"))
	}
}
