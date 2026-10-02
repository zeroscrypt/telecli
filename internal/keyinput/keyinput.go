// Package keyinput — общий слой над именами клавиш для всех интерфейсов
// проекта: имя нажатой клавиши в том же виде, в каком их пишут в
// keybindings.toml, отображение имени для подсказок, сосед по раскладке и разбор
// имени обратно в нажатие.
//
// Отдельным пакетом, а не частью одного из интерфейсов: правило одно на всех, и
// пока таблица раскладки и разбор имён жили внутри tgclitui, стена tgwall не могла
// ими воспользоваться (задача 0158). Две копии правила разошлись бы при первой же
// правке — а расхождение здесь молчаливое: хоткей перестаёт срабатывать на
// русской раскладке, и это замечает человек, а не сборка.
package keyinput

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// KeyName — имя нажатой клавиши в том же виде, в каком их пишут в
// конфигурации: «ctrl+r», «up», «esc», «delete», «/». Пустая строка означает
// «такой клавиши интерфейс не знает».
func KeyName(msg tea.KeyMsg) string {
	key := msg.Key()
	if name, ok := fixedKeyNames[key.Code]; ok {
		// alt+esc — это отдельное нажатие, которым терминал начинает
		// последовательность, а не сочетание клавиш: назвать его «alt+esc»
		// значило бы сделать неразличимыми два разных события, и отмена
		// перестала бы отличаться от alt+что-нибудь-остальное.
		if name == "esc" && key.Mod.Contains(tea.ModAlt) {
			return name
		}
		return modifiersPrefix(key.Mod) + name
	}
	// Буква или цифра с ctrl — это сама буква в Code плюс модификатор,
	// отдельного диапазона кодов, как в v1, теперь нет: сравниваем по символу и
	// по наличию Ctrl.
	//
	// Цифры добавлены в ту же проверку, а не отдельной веткой: Ctrl+1 и Ctrl+2
	// открывают панели переписки стены, и без них key.Name возвращал для них
	// пустую строку — хоткей не срабатывал нигде, ни в стене, ни в ленте, и
	// добавление полей в keybindings.toml выглядело бы как работающая
	// настройка, которая не работает. Причина молчаливая: у Ctrl+буквы есть
	// Text, у Ctrl+цифры он пустой, и «исправить» это можно было бы только
	// подгонкой теста, а не правкой здесь.
	//
	// Shift ЗДЕСЬ сознательно не учитывается, в отличие от служебных клавиш
	// выше. Терминал без расширений присылает «J» как Text = "J" с
	// ModShift, и с добавлением префикса привязка «j» перестала бы срабатывать
	// на заглавной «J» — а в ленте tgcli таких привязок большинство
	// (j, k, i, q, h, d, t, p, r, 1, 2, 3). Менять это молча нельзя: решение о
	// том, должны ли «J» и «j» быть разными клавишами, за человеком.
	if key.Mod.Contains(tea.ModCtrl) && (isASCIILetter(key.Code) || isASCIIDigit(key.Code)) {
		letter := string(unicode.ToLower(key.Code))
		if key.Mod.Contains(tea.ModAlt) {
			return "alt+ctrl+" + letter
		}
		return "ctrl+" + letter
	}
	// Печатный символ: в v2 это не отдельный тип, а непустой Text. Берём первый
	// рун, как и в v1, где брался единственный символ из Runes.
	if runes := []rune(key.Text); len(runes) == 1 {
		return string(unicode.ToLower(runes[0]))
	}
	return ""
}

// modifiersPrefix — префикс модификаторов в порядке ctrl, alt, shift, meta.
//
// Порядок взят не по вкусу, а из документации bubbletea: Key.Keystroke печатает
// модификаторы именно в таком порядке и прямо обещает, что «ctrl+shift+alt+a»
// встретится, а «shift+ctrl+alt+a» — нет. Своим порядком мы бы заставили
// человека переучивать написание после того, как он прочитал документацию
// библиотеки.
func modifiersPrefix(mod tea.KeyMod) string {
	var prefix string
	if mod.Contains(tea.ModCtrl) {
		prefix += "ctrl+"
	}
	if mod.Contains(tea.ModAlt) {
		prefix += "alt+"
	}
	if mod.Contains(tea.ModShift) {
		prefix += "shift+"
	}
	if mod.Contains(tea.ModMeta) {
		prefix += "meta+"
	}
	return prefix
}

func isASCIILetter(code rune) bool {
	return (code >= 'a' && code <= 'z') || (code >= 'A' && code <= 'Z')
}

// isASCIIDigit — цифра верхней строки клавиатуры. Отдельной функцией, а не
// расширением isASCIILetter: буквы участвуют в соседе по раскладке (KeyPeer,
// см. ниже), а цифры — нет, на «ц» соседом является «w», но соседом «1» по
// раскладке назвать нечего, и таблица раскладки о цифрах не знает.
func isASCIIDigit(code rune) bool {
	return code >= '0' && code <= '9'
}

// fixedKeyNames — клавиши, у которых в bubbletea нет вычисляемого имени. Ключ
// таблицы — Code из v2, то есть руна, а не тип из v1. Имена взяты из таблицы
// имён самого терминального слоя (ultraviolet keyTypeString), поэтому «delete»
// в конфигурации означает ровно ту клавишу, которую bubbletea зовёт KeyDelete.
var fixedKeyNames = map[rune]string{
	tea.KeyPgUp:      "pgup",
	tea.KeyPgDown:    "pgdown",
	tea.KeyUp:        "up",
	tea.KeyDown:      "down",
	tea.KeyLeft:      "left",
	tea.KeyRight:     "right",
	tea.KeyEnter:     "enter",
	tea.KeyEsc:       "esc",
	tea.KeyTab:       "tab",
	tea.KeySpace:     "space",
	tea.KeyBackspace: "backsp",
	tea.KeyDelete:    "delete",
}

// displayNames — только отображение: какая клавиша нарисована как «↑», а какая
// как «↵». Поведение отсюда не берётся, поэтому таблица не может разойтись с
// конфигурацией.
//
// enter показан символом ↵ (U+21B5), а не словом: слово занимало в подсказке
// пять ячеек и делало её самой длинной частью, при том что значок читается так
// же (решение человека, 2026-10-02). Взят U+21B5, а не U+23CE «⏎»: последний
// есть не во всех шрифтах, и в терминале без него остаётся пустое место.
var displayNames = map[string]string{
	"up":     "↑",
	"down":   "↓",
	"left":   "←",
	"right":  "→",
	"enter":  "↵",
	"esc":    "esc",
	"tab":    "tab",
	"space":  "space",
	"backsp": "backspace",
}

// DisplayName — как показать клавишу в подсказке. Всё, чего нет в таблице,
// показывается строчными: конфигурация пишется строчными (ctrl+p), и подсказки со
// справкой читаются как продолжение того же текста. Заглавных букв в названиях
// клавиш нет — по прямому требованию человека.
func DisplayName(name string) string {
	if displayed, ok := displayNames[name]; ok {
		return displayed
	}
	return strings.ToLower(name)
}

// MsgForName — сообщение терминала по имени клавиши из конфигурации. Нужно
// тестам: иначе проверки поведения расходились бы с keybindings.toml из-за
// зашитых букв. Имена, которые терминал не различает (alt, shift, пробел), не
// разбираются — false, кроме shift над служебными клавишами: такой приход
// терминалу различать нечего (см. ниже).
func MsgForName(name string) (tea.KeyMsg, bool) {
	name = strings.ToLower(strings.TrimSpace(name))
	// Shift разбирается ТОЛЬКО над служебными клавишами, и отдельной веткой
	// раньше остальных: shift+tab терминал присылает отдельным нажатием уже
	// десятилетиями (ultraviolet разбирает последовательность CSI Z в
	// «tab + shift»), тогда как shift над буквой в обычном терминале от
	// shift+буквы неотличим и приходит просто как другой символ.
	//
	// Ветка стоит ДО разбора ctrl+буквы и не трогает её: «ctrl+r» обязан
	// остаться ctrl+r, а «alt+x» — по-прежнему неразбираемым, потому что
	// терминал не отличает alt+x от «x».
	if head, rest, found := strings.Cut(name, "+"); found && head == "shift" {
		for code, key := range fixedKeyNames {
			if key == rest {
				return tea.KeyPressMsg{Code: code, Mod: tea.ModShift}, true
			}
		}
	}
	for code, key := range fixedKeyNames {
		if key == name {
			return tea.KeyPressMsg{Code: code}, true
		}
	}
	if prefix, letter, found := strings.Cut(name, "+"); found && prefix == "ctrl" {
		if runes := []rune(letter); len(runes) == 1 {
			if code := unicode.ToLower(runes[0]); isASCIILetter(code) {
				return tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl}, true
			}
		}
	}
	if runes := []rune(name); len(runes) == 1 {
		return tea.KeyPressMsg{Code: runes[0], Text: name}, true
	}
	return nil, false
}
