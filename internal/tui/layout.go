package tui

import (
	tea "github.com/charmbracelet/bubbletea"
)

// cyrillicToLatin — раскладка ЙЦУКЕН → QWERTY по физическому положению
// клавиш верхнего/среднего/нижнего ряда букв. Нужна, чтобы горячие клавиши
// Normal-режима работали независимо от активной раскладки ОС — та же
// проблема, которую в vim решает :lmap. НЕ применяется к набору текста в
// Insert/Command — см. translateLayout и его использование в Update.
var cyrillicToLatin = map[rune]rune{
	'й': 'q', 'ц': 'w', 'у': 'e', 'к': 'r', 'е': 't', 'н': 'y', 'г': 'u', 'ш': 'i', 'щ': 'o', 'з': 'p',
	'ф': 'a', 'ы': 's', 'в': 'd', 'а': 'f', 'п': 'g', 'р': 'h', 'о': 'j', 'л': 'k', 'д': 'l', 'ж': ';', 'э': '\'',
	'я': 'z', 'ч': 'x', 'с': 'c', 'м': 'v', 'и': 'b', 'т': 'n', 'ь': 'm', 'б': ',', 'ю': '.',
	'Й': 'Q', 'Ц': 'W', 'У': 'E', 'К': 'R', 'Е': 'T', 'Н': 'Y', 'Г': 'U', 'Ш': 'I', 'Щ': 'O', 'З': 'P',
	'Ф': 'A', 'Ы': 'S', 'В': 'D', 'А': 'F', 'П': 'G', 'Р': 'H', 'О': 'J', 'Л': 'K', 'Д': 'L', 'Ж': ':', 'Э': '"',
	'Я': 'Z', 'Ч': 'X', 'С': 'C', 'М': 'V', 'И': 'B', 'Т': 'N', 'Ь': 'M', 'Б': '<', 'Ю': '>',
}

// translateLayout транслитерирует одну руну из ЙЦУКЕН в QWERTY, если она есть
// в таблице; любые другие tea.KeyMsg (спецклавиши, уже латинские руны,
// многосимвольный ввод) возвращает как есть, без изменений.
func translateLayout(msg tea.KeyMsg) tea.KeyMsg {
	if msg.Type != tea.KeyRunes || len(msg.Runes) != 1 {
		return msg
	}
	latin, ok := cyrillicToLatin[msg.Runes[0]]
	if !ok {
		return msg
	}
	translated := msg
	translated.Runes = []rune{latin}
	return translated
}
