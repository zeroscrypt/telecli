package tgwall

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"telecli/internal/config"
	"telecli/internal/keyinput"
)

// keyAction — действие стены. Список клавиш для каждого действия живёт в
// конфигурации (keybindings.toml, секция [tgwall]), здесь только названия
// действий: ни одна клавиша в коде не зашита, иначе правка keybindings.toml
// меняла бы поведение, а подсказки продолжали бы показывать старое.
type keyAction int

const (
	keyQuit keyAction = iota
	keyReply
	keyMoveUp
	keyMoveDown
	keyPageUp
	keyPageDown
	keyFocusNext
	keyFocusPrev
	keySelect
	keyToggle
	keyBack
	keyDeleteMessage
	keyOpenFilter
	keyOpenPanel1
	keyOpenPanel2
)

// keyMap — раскладка клавиш стены поверх конфигурации. Устроена так же, как
// раскладка ленты tgcli (internal/tgclitui/keymap.go), но это отдельный тип, а не
// переиспользование того: набор действий у стены свой, и совпадение названий полей
// между интерфейсами не делает их одним и тем же.
type keyMap struct {
	keys config.TgwallKeyBindings
}

// bindings — клавиши действия из конфигурации, в нижнем регистре и без пустых.
func (k keyMap) bindings(action keyAction) []string {
	var fields [][]string
	switch action {
	case keyQuit:
		fields = [][]string{k.keys.Quit}
	case keyReply:
		fields = [][]string{k.keys.Reply}
	case keyMoveUp:
		fields = [][]string{k.keys.MoveUp}
	case keyMoveDown:
		fields = [][]string{k.keys.MoveDown}
	case keyPageUp:
		fields = [][]string{k.keys.PageUp}
	case keyPageDown:
		fields = [][]string{k.keys.PageDown}
	case keyFocusNext:
		fields = [][]string{k.keys.FocusNext}
	case keyFocusPrev:
		fields = [][]string{k.keys.FocusPrev}
	case keySelect:
		fields = [][]string{k.keys.Select}
	case keyToggle:
		fields = [][]string{k.keys.Toggle}
	case keyBack:
		fields = [][]string{k.keys.Back}
	case keyDeleteMessage:
		fields = [][]string{k.keys.DeleteMessage}
	case keyOpenFilter:
		fields = [][]string{k.keys.Filter}
	case keyOpenPanel1:
		fields = [][]string{k.keys.OpenPanel1}
	case keyOpenPanel2:
		fields = [][]string{k.keys.OpenPanel2}
	}
	bindings := make([]string, 0, len(fields))
	for _, field := range fields {
		for _, binding := range field {
			trimmed := strings.ToLower(strings.TrimSpace(binding))
			// Пустые пропускаем, повторяющиеся убираем: у действия бывает
			// несколько вариантов клавиши, и в подсказке это должна быть одна
			// клавиша, а не «u, u».
			if trimmed == "" || slices.Contains(bindings, trimmed) {
				continue
			}
			bindings = append(bindings, trimmed)
		}
	}
	return bindings
}

// pressed — нажата ли клавиша этого действия. Сверяется и вторая раскладка той же
// физической клавиши: человек набирает текст на русской, и хоткей должен работать
// там же (см. keyinput.KeyPeer). Клавиши с Ctrl и служебные имена вроде «up» от
// раскладки не зависят: peer для них пустой.
func (k keyMap) pressed(action keyAction, msg tea.KeyMsg) bool {
	name := keyinput.KeyName(msg)
	if name == "" {
		return false
	}
	bindings := k.bindings(action)
	for _, binding := range bindings {
		if strings.EqualFold(name, binding) {
			return true
		}
	}
	peer := keyinput.KeyPeer(name)
	return peer != "" && slices.Contains(bindings, peer)
}

// label — как показать клавишу в подсказке: первый указанный в конфигурации
// вариант, приведённый к читаемому виду.
func (k keyMap) label(action keyAction) string {
	bindings := k.bindings(action)
	if len(bindings) == 0 {
		return ""
	}
	return keyinput.DisplayName(bindings[0])
}
