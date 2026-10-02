package keyinput

import "strings"

// Русская раскладка ЙЦУКЕН: те же физические клавиши, что и в английской.
// Терминал не сообщает раскладку — приходит только символ, — поэтому хоткей-буква
// без такой таблицы работала бы только в английской. Человек попросил, чтобы
// хоткей работал и на русской, поэтому клавиша, описанная в конфигурации латиницей,
// и её русский сосед считаются одной и той же клавишей.
var russianLayoutRows = []struct{ latin, cyrillic string }{
	// Верхний ряд: 12 клавиш против 12 — «ё» живёт на клавише с обратной кавычкой.
	{"qwertyuiop[]", "йцукенгшщзхъ"},
	{"asdfghjkl;'", "фывапролджэ"},
	{"zxcvbnm,./", "ячсмитьбю."},
	{"`", "ё"},
}

// keyPeers — соответствие «клавиша ↔ её сосед в русской раскладке» в обе стороны.
var keyPeers = buildKeyPeers()

func buildKeyPeers() map[string]string {
	peers := make(map[string]string, 64)
	for _, row := range russianLayoutRows {
		latin, cyrillic := []rune(row.latin), []rune(row.cyrillic)
		if len(latin) != len(cyrillic) {
			panic("строки раскладки разной длины: " + row.latin + " и " + row.cyrillic)
		}
		for index, key := range latin {
			peers[string(key)] = string(cyrillic[index])
			peers[string(cyrillic[index])] = string(key)
		}
	}
	return peers
}

// KeyPeer — вторая раскладка той же клавиши: «h» → «р», «/» → «.». Пустая строка,
// если такой клавиши в раскладке нет (цифры, служебные имена вроде «up», а также
// имена с префиксом модификатора вида «ctrl+r» — Ctrl с раскладкой не связан:
// терминал шлёт управляющий байт по физической клавише, а не символ).
func KeyPeer(name string) string {
	return keyPeers[strings.ToLower(name)]
}
