package keyinput

import (
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// Пакет общий для двух интерфейсов (tgcli и tgwall), поэтому проверяется сам по
// себе, а не только через чужой интерфейс: иначе первый же интерфейс, который
// перестанет им пользоваться, оставил бы правило без проверки.

// TestKeyNameMatchesConfigNames — имя нажатой клавиши совпадает с тем, что
// пишется в keybindings.toml. Это и есть контракт пакета: несовпадение здесь
// означало бы, что ни одна настроенная клавиша не срабатывает.
func TestKeyNameMatchesConfigNames(t *testing.T) {
	for _, test := range []struct {
		name string
		msg  tea.KeyMsg
		want string
	}{
		{name: "up", msg: tea.KeyPressMsg{Code: tea.KeyUp}, want: "up"},
		{name: "down", msg: tea.KeyPressMsg{Code: tea.KeyDown}, want: "down"},
		{name: "pgup", msg: tea.KeyPressMsg{Code: tea.KeyPgUp}, want: "pgup"},
		{name: "pgdown", msg: tea.KeyPressMsg{Code: tea.KeyPgDown}, want: "pgdown"},
		{name: "esc", msg: tea.KeyPressMsg{Code: tea.KeyEscape}, want: "esc"},
		{name: "enter", msg: tea.KeyPressMsg{Code: tea.KeyEnter}, want: "enter"},
		{name: "tab", msg: tea.KeyPressMsg{Code: tea.KeyTab}, want: "tab"},
		{name: "space", msg: tea.KeyPressMsg{Code: tea.KeySpace}, want: "space"},
		{name: "backsp", msg: tea.KeyPressMsg{Code: tea.KeyBackspace}, want: "backsp"},
		{name: "delete", msg: tea.KeyPressMsg{Code: tea.KeyDelete}, want: "delete"},
		{name: "ctrl+r", msg: tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}, want: "ctrl+r"},
		// Заглавная буква с Ctrl — тот же биндинг: конфигурация пишется строчными.
		{name: "ctrl+r", msg: tea.KeyPressMsg{Code: 'R', Mod: tea.ModCtrl}, want: "ctrl+r"},
		{name: "ctrl+/", msg: tea.KeyPressMsg{Code: '/', Mod: tea.ModCtrl}, want: ""},
		{name: "alt+up", msg: tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModAlt}, want: "alt+up"},
		// Alt с esc не пишется префиксом: по построению «alt+esc» в конфигурации
		// не бывает, иначе отмена перестала бы быть отменой.
		{name: "esc", msg: tea.KeyPressMsg{Code: tea.KeyEscape, Mod: tea.ModAlt}, want: "esc"},
		{name: "alt+ctrl+b", msg: tea.KeyPressMsg{Code: 'b', Mod: tea.ModCtrl | tea.ModAlt}, want: "alt+ctrl+b"},
		{name: "/", msg: tea.KeyPressMsg{Code: '/', Text: "/"}, want: "/"},
		{name: "ц", msg: tea.KeyPressMsg{Code: 'ц', Text: "ц"}, want: "ц"},
		{name: "!", msg: tea.KeyPressMsg{Code: '!', Text: "!"}, want: "!"},
		// Пустое нажатие (перерисовка, служебное сообщение) — не клавиша.
		{name: "", msg: tea.KeyPressMsg{Code: 0}, want: ""},
		// Shift с буквой: имя не меняется, потому что конфигурация пишет букву
		// строчной и без shift (ctrl+j — это не ctrl+J).
		{name: "j", msg: tea.KeyPressMsg{Code: 'j', Text: "J", Mod: tea.ModShift}, want: "j"},
	} {
		if got := KeyName(test.msg); got != test.want {
			t.Fatalf("KeyName(%s) = %q, ждали %q", test.name, got, test.want)
		}
	}
}

// TestFixedKeyNamesAreDistinct — таблица служебных клавиш не должна содержать двух
// кодов под одним именем и двух имён под одним кодом: иначе одна из клавиш стала бы
// ненастраиваемой, а имя в конфигурации — молчаливым мусором.
func TestFixedKeyNamesAreDistinct(t *testing.T) {
	byName := make(map[string]rune, len(fixedKeyNames))
	for code, name := range fixedKeyNames {
		if previous, ok := byName[name]; ok {
			t.Fatalf("имя %q принадлежит сразу двум кодам: %d и %d", name, previous, code)
		}
		byName[name] = code
	}
	if len(byName) != len(fixedKeyNames) {
		t.Fatalf("в таблице %d имён на %d кодов", len(byName), len(fixedKeyNames))
	}
}

// TestEveryLetterOfTheLayoutHasAPeer — у каждой буквы латинских рядов есть сосед по
// раскладке, и он тоже буква: иначе хоткей-буква работала бы только в одной
// раскладке, а это ровно то, ради чего таблица заведена.
//
// Проверяется не «сосед симметричен» (KeyPeer(KeyPeer(x)) == x), а наличие соседа у
// буквы: симметрия ломается на крайней правой клавише нижнего ряда, где латинская «.»
// и русская «/» делят одну ячейку таблицы (см. задачу 0158, отчёт: там же о самой
// этой ячейке), и такая проверка закрепила бы опечатку в таблице как правило.
func TestEveryLetterOfTheLayoutHasAPeer(t *testing.T) {
	for _, row := range russianLayoutRows {
		for _, key := range []rune(row.latin) {
			peer := KeyPeer(string(key))
			if peer == "" {
				t.Fatalf("у клавиши %q нет соседа по раскладке", string(key))
			}
			if got := KeyPeer(peer); got == "" {
				t.Fatalf("сосед %q клавиши %q сам соседа не имеет", peer, string(key))
			}
		}
	}
	// Обратная сторона: у русских букв латинский сосед тоже есть, иначе конфигурация,
	// написанная по-русски, работала бы только в русской раскладке.
	for _, row := range russianLayoutRows {
		for _, key := range []rune(row.cyrillic) {
			if KeyPeer(string(key)) == "" {
				t.Fatalf("у русской клавиши %q нет соседа по раскладке", string(key))
			}
		}
	}
}

// TestKeyPeerFacts — факты о таблице раскладки, на которые опираются оба интерфейса:
// сосед латинской «r» — русская «к» (это и есть ответ на вопрос задачи про Ctrl+R на
// русской раскладке), у «h» — «р», у «w» — «ц». Если таблица раскладки когда-нибудь
// переедет, тест заметит это, а не человек на своей клавиатуре.
//
// Пара «/» ↔ «.» присутствует здесь как ДОКУМЕНТАЦИЯ текущего поведения: в нижнем
// ряду эти две клавиши делят одну ячейку таблицы, и в физической раскладке там стоит
// «/», а не «.». Пока таблица именно такая, тест её фиксирует; изменение этой пары
// будет осознанным решением, а не правкой по ходу (см. отчёт задачи 0158).
func TestKeyPeerFacts(t *testing.T) {
	for _, test := range []struct{ name, want string }{
		{"r", "к"},
		{"h", "р"},
		{"w", "ц"},
		{"/", "."},
		// Служебные имена и цифры соседа не имеют: у них нет второй раскладки.
		{"up", ""},
		{"enter", ""},
		{"delete", ""},
		{"7", ""},
		// Регистр значения не имеет: конфигурация может быть написана заглавными.
		{"R", "к"},
		{"H", "р"},
		// Клавиша с Ctrl от раскладки не зависит: терминал шлёт управляющий байт
		// по физической клавише, а не символ, поэтому соседа у «ctrl+r» нет.
		{"ctrl+r", ""},
	} {
		if got := KeyPeer(test.name); got != test.want {
			t.Fatalf("KeyPeer(%q) = %q, ждали %q", test.name, got, test.want)
		}
	}
}

// TestDisplayNameIsHumanReadable — подсказки и справка показывают клавиши строчными,
// а стрелки — знаками. Заглавных букв в названиях клавиш нет по требованию человека,
// поэтому проверяется не «сейчас так», а «всегда так»: любое новое имя из таблицы
// обязано пройти это правило.
func TestDisplayNameIsHumanReadable(t *testing.T) {
	for _, test := range []struct{ name, want string }{
		{"up", "↑"},
		{"down", "↓"},
		{"left", "←"},
		{"right", "→"},
		{"enter", "↵"},
		{"esc", "esc"},
		{"tab", "tab"},
		{"space", "space"},
		{"backsp", "backspace"},
		{"delete", "delete"},
		{"ctrl+r", "ctrl+r"},
		{"CTRL+R", "ctrl+r"},
		{"/", "/"},
	} {
		if got := DisplayName(test.name); got != test.want {
			t.Fatalf("DisplayName(%q) = %q, ждали %q", test.name, got, test.want)
		}
	}
	// Всё, чего нет в таблице, остаётся строчным: конфигурация пишется строчными.
	for _, name := range []string{"alt+shift+x", "f1", "zzz", ""} {
		got := DisplayName(name)
		if got != strings.ToLower(got) {
			t.Fatalf("DisplayName(%q) = %q, а названия клавиш показываются строчными", name, got)
		}
	}
}

// TestMsgForNameRoundTrip — имя из конфигурации разбирается обратно в нажатие, и
// имя этого нажатия совпадает с исходным. Этим пользуются тесты обоих интерфейсов:
// без обратного разбора проверки поведения расходились бы с keybindings.toml из-за
// зашитых букв.
func TestMsgForNameRoundTrip(t *testing.T) {
	for _, name := range []string{"up", "down", "left", "right", "pgup", "pgdown", "esc", "enter", "tab", "space", "backsp", "delete", "ctrl+r", "ctrl+j", "/", "ц"} {
		msg, ok := MsgForName(name)
		if !ok {
			t.Fatalf("имя клавиши %q не разбирается", name)
		}
		if got := KeyName(msg); got != name {
			t.Fatalf("разбор имени %q дал нажатие, названное %q", name, got)
		}
	}
	// Пробелы и регистр файла значения не имеют: конфигурация пишется по-разному от
	// того, как выглядит нажатие.
	if msg, ok := MsgForName("  CTRL+R "); !ok || KeyName(msg) != "ctrl+r" {
		t.Fatalf("разбор \"  CTRL+R \" = %q, %v", KeyName(msg), ok)
	}
}

// TestMsgForNameRejectsWhatTerminalCannotTell — имена, по которым терминал не может
// отличить одну клавишу от другой, не разбираются вовсе, а не разбираются во что-то
// неверное: подмена молча проверяла бы несуществующий биндинг.
func TestMsgForNameRejectsWhatTerminalCannotTell(t *testing.T) {
	for _, name := range []string{"alt+x", "shift+a", "ctrl+enter", "ctrl+up", "", "не-клавиша"} {
		if msg, ok := MsgForName(name); ok {
			t.Fatalf("имя %q разобралось в нажатие %q, а разбираться не должно", name, KeyName(msg))
		}
	}
}

// TestIsASCIILetter — служебная проверка, но она решает, попадёт ли буква с Ctrl в
// имя вида «ctrl+буква» или потеряется: кириллица с Ctrl в имя не попадает, и это
// проверено, чтобы правка не изменила поведение незаметно.
func TestIsASCIILetter(t *testing.T) {
	for _, symbol := range []rune{'a', 'z', 'A', 'Z', '0', ' ', 'ц', 'ё', '/'} {
		want := (symbol >= 'a' && symbol <= 'z') || (symbol >= 'A' && symbol <= 'Z')
		if got := isASCIILetter(symbol); got != want {
			t.Fatalf("isASCIILetter(%q) = %v, ждали %v", symbol, got, want)
		}
	}
	// И именованный символ, который не буква, — тоже не буква (сравнение с
	// unicode.IsLetter поймало бы и не-ASCII, а здесь важна именно ASCII-буква,
	// потому что с Ctrl в конфигурации пишут латиницу).
	if isASCIILetter(unicode.ToLower('К')) {
		t.Fatal("кириллическая «к» попала в ASCII-буквы")
	}
}

// Shift над служебной клавишей — отдельное нажатие, а не то же самое без
// shift. Терминал присылает его decades-old последовательностью CSI Z, и
// ultraviolet разбирает её в «tab + shift» без всяких расширений клавиатуры.
//
// Проверяется в обе стороны: сдвинутая клавиша обязана получить своё имя, и
// обычный tab — остаться прежним. Без второй половины проверки слой мог бы
// начать отдавать «shift+tab» на ЛЮБОЕ нажатие tab, и хоткей перестал бы
// срабатывать вовсе.
func TestShiftOnFixedKeyIsItsOwnName(t *testing.T) {
	shiftTab := tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	if got := KeyName(shiftTab); got != "shift+tab" {
		t.Fatalf("shift+tab назван %q, ждали \"shift+tab\"", got)
	}
	if got := KeyName(tea.KeyPressMsg{Code: tea.KeyTab}); got != "tab" {
		t.Fatalf("обычный tab назван %q, ждали \"tab\" — сдвинутая клавиша его перехватила", got)
	}
	// Round trip: имя из конфигурации обязано давать то же нажатие обратно,
	// иначе тесты не смогли бы нажать то, что человек нажимает.
	msg, ok := MsgForName("shift+tab")
	if !ok {
		t.Fatal("имя \"shift+tab\" не разобралось в нажатие")
	}
	if got := KeyName(msg); got != "shift+tab" {
		t.Fatalf("разбор \"shift+tab\" дал нажатие, названное %q", got)
	}
	// Модификаторы идут в порядке из документации bubbletea (Key.Keystroke):
	// ctrl, alt, shift.
	if got := KeyName(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift | tea.ModAlt}); got != "alt+shift+enter" {
		t.Fatalf("alt+shift+enter назван %q", got)
	}
}

// Shift над буквой НЕ различается: в обычном терминале «J» приходит просто
// символом, и различать его от «j» нечем. Проверка зафиксирована намеренно —
// если решение изменится, тест напомнит, что это было сделано осознанно, и
// покажет, сколько привязок ленты tgcli этим затронуто.
func TestShiftOnLetterStaysTheSameName(t *testing.T) {
	// Именно так терминал присылает заглавную букву: Code строчный, Text
	// заглавный, ModShift выставлен (ultraviolet/decoder.go).
	shiftA := tea.KeyPressMsg{Code: 'a', ShiftedCode: 'A', Text: "A", Mod: tea.ModShift}
	if got := KeyName(shiftA); got != "a" {
		t.Fatalf("заглавная A названа %q, ждали \"a\" — привязки ленты перестали бы срабатывать", got)
	}
}
