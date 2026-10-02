package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func setupKeyBindingsTest(t *testing.T) (string, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	keyFile := filepath.Join(tmpDir, "keybindings.toml")
	SetKeyBindingsPathForTest(keyFile)

	cleanup := func() {
		SetKeyBindingsPathForTest("")
	}

	return tmpDir, cleanup
}

func TestLoadKeyBindingsNoFileReturnsDefaults(t *testing.T) {
	_, cleanup := setupKeyBindingsTest(t)
	defer cleanup()

	keys, err := LoadKeyBindings()
	require.NoError(t, err)
	require.Equal(t, DefaultKeyBindings(), keys)
}

func TestLoadKeyBindingsPartialOverride(t *testing.T) {
	_, cleanup := setupKeyBindingsTest(t)
	defer cleanup()

	writeKeyBindingsFile(t, "quit = [\"x\"]\nplay_voice = [\"v\"]\npreview_photo = [\"f\"]")

	keys, err := LoadKeyBindings()
	require.NoError(t, err)

	defaults := DefaultKeyBindings()
	require.Equal(t, []string{"x"}, keys.Quit)
	require.Equal(t, []string{"v"}, keys.PlayVoice)
	require.Equal(t, []string{"f"}, keys.PreviewPhoto)
	require.Equal(t, defaults.MoveUp, keys.MoveUp)
	require.Equal(t, defaults.MoveDown, keys.MoveDown)
	require.Equal(t, defaults.FocusNext, keys.FocusNext)
	require.Equal(t, defaults.FocusLeft, keys.FocusLeft)
	require.Equal(t, defaults.FocusRight, keys.FocusRight)
	require.Equal(t, defaults.FocusPane1, keys.FocusPane1)
	require.Equal(t, defaults.FocusPane2, keys.FocusPane2)
	require.Equal(t, defaults.FocusPane3, keys.FocusPane3)
	require.Equal(t, defaults.Select, keys.Select)
	require.Equal(t, defaults.Back, keys.Back)
	require.Equal(t, defaults.EnterInsert, keys.EnterInsert)
	require.Equal(t, defaults.EnterCommand, keys.EnterCommand)
	require.Equal(t, defaults.SendFile, keys.SendFile)
	require.Equal(t, defaults.Search, keys.Search)
	require.Equal(t, defaults.About, keys.About)
}

func TestLoadKeyBindingsEmptyFieldFallsBackToDefault(t *testing.T) {
	_, cleanup := setupKeyBindingsTest(t)
	defer cleanup()

	writeKeyBindingsFile(t, "move_up = []\nquit = [\"x\"]")

	keys, err := LoadKeyBindings()
	require.NoError(t, err)

	defaults := DefaultKeyBindings()
	require.Equal(t, []string{"x"}, keys.Quit)
	require.Equal(t, defaults.MoveUp, keys.MoveUp)
}

func TestLoadKeyBindingsFullOverride(t *testing.T) {
	_, cleanup := setupKeyBindingsTest(t)
	defer cleanup()

	writeKeyBindingsFile(t, `
move_up = ["a"]
move_down = ["s"]
focus_next = ["n"]
focus_left = ["e"]
focus_right = ["t"]
focus_pane_1 = ["1"]
focus_pane_2 = ["2"]
focus_pane_3 = ["3"]
select = ["e"]
back = ["b"]
enter_insert = ["i"]
enter_command = ["c"]
quit = ["q"]
send_file = ["ctrl+d"]
search = ["/"]
show_help = ["h"]
delete_chat = ["x"]
about = ["a"]
play_voice = ["v"]
preview_photo = ["f"]
reply = ["z"]
delete_message = ["ctrl+y"]
new_line = ["ctrl+n"]
`)

	keys, err := LoadKeyBindings()
	require.NoError(t, err)

	want := KeyBindings{
		MoveUp:       []string{"a"},
		MoveDown:     []string{"s"},
		FocusNext:    []string{"n"},
		FocusLeft:    []string{"e"},
		FocusRight:   []string{"t"},
		FocusPane1:   []string{"1"},
		FocusPane2:   []string{"2"},
		FocusPane3:   []string{"3"},
		Select:       []string{"e"},
		Back:         []string{"b"},
		EnterInsert:  []string{"i"},
		EnterCommand: []string{"c"},
		Quit:         []string{"q"},
		SendFile:     []string{"ctrl+d"},
		Search:       []string{"/"},
		ShowHelp:     []string{"h"},
		DeleteChat:   []string{"x"},
		About:        []string{"a"},
		PlayVoice:    []string{"v"},
		PreviewPhoto: []string{"f"},
		Reply:        []string{"z"},
		// Поле, которого раньше не было, читается так же, как остальные.
		DeleteMessage: []string{"ctrl+y"},
		NewLine:       []string{"ctrl+n"},
	}
	require.Equal(t, want, keys)
}

func TestLoadKeyBindingsInvalidTOMLReturnsError(t *testing.T) {
	_, cleanup := setupKeyBindingsTest(t)
	defer cleanup()

	writeKeyBindingsFile(t, "move_up = [\nnot valid toml")

	_, err := LoadKeyBindings()
	require.Error(t, err)
}

func TestLoadKeyBindingsUnreadableFileReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, "keybindings.toml")
	require.NoError(t, os.MkdirAll(dir, 0700))
	SetKeyBindingsPathForTest(dir)
	defer SetKeyBindingsPathForTest("")

	_, err := LoadKeyBindings()
	require.Error(t, err)
}

// Секция [tgwall] — третий набор клавиш проекта (задача 0158). Проверяется и то,
// что он читается, и то, что остальные секции файла на него не влияют: один
// keybindings.toml на три интерфейса, и смешивание наборов было бы тихой поломкой
// (лента и стена выглядят похоже, а ведут себя по-разному).

func TestLoadTgwallKeyBindingsNoFileReturnsDefaults(t *testing.T) {
	_, cleanup := setupKeyBindingsTest(t)
	defer cleanup()

	keys, err := LoadTgwallKeyBindings()
	require.NoError(t, err)
	require.Equal(t, DefaultTgwallKeyBindings(), keys)
}

func TestLoadTgwallKeyBindingsPartialOverride(t *testing.T) {
	_, cleanup := setupKeyBindingsTest(t)
	defer cleanup()

	writeKeyBindingsFile(t, "[tgwall]\nmove_down = [\"j\"]\n")

	keys, err := LoadTgwallKeyBindings()
	require.NoError(t, err)

	defaults := DefaultTgwallKeyBindings()
	require.Equal(t, []string{"j"}, keys.MoveDown)
	require.Equal(t, defaults.MoveUp, keys.MoveUp)
	require.Equal(t, defaults.PageUp, keys.PageUp)
	require.Equal(t, defaults.PageDown, keys.PageDown)
	require.Equal(t, defaults.FocusNext, keys.FocusNext)
	require.Equal(t, defaults.Select, keys.Select)
	require.Equal(t, defaults.Back, keys.Back)
	require.Equal(t, defaults.DeleteMessage, keys.DeleteMessage)
	require.Equal(t, defaults.Reply, keys.Reply)
	// Toggle перечислен наравне с остальными: забытое в списке поле молча
	// возвращалось бы к дефолту, и такая опечатка в проверке проходила бы незамеченной.
	require.Equal(t, defaults.Filter, keys.Filter)
	require.Equal(t, defaults.Toggle, keys.Toggle)
	require.Equal(t, defaults.Quit, keys.Quit)
}

func TestLoadTgwallKeyBindingsEmptyFieldFallsBackToDefault(t *testing.T) {
	_, cleanup := setupKeyBindingsTest(t)
	defer cleanup()

	writeKeyBindingsFile(t, "[tgwall]\nselect = []\nquit = [\"ctrl+q\"]\n")

	keys, err := LoadTgwallKeyBindings()
	require.NoError(t, err)

	defaults := DefaultTgwallKeyBindings()
	require.Equal(t, []string{"ctrl+q"}, keys.Quit)
	require.Equal(t, defaults.Select, keys.Select)
	require.Equal(t, defaults.Toggle, keys.Toggle)
}

func TestLoadTgwallKeyBindingsFullOverride(t *testing.T) {
	_, cleanup := setupKeyBindingsTest(t)
	defer cleanup()

	writeKeyBindingsFile(t, `
[tgwall]
move_up = ["k", "up"]
move_down = ["j", "down"]
page_up = ["ctrl+b"]
page_down = ["ctrl+f"]
focus_next = ["tab"]
focus_prev = ["ctrl+p"]
select = ["ctrl+m"]
back = ["ctrl+g"]
delete_message = ["ctrl+d"]
reply = ["r"]
filter = ["ctrl+o"]
toggle = ["ctrl+t"]
quit = ["ctrl+x"]

[tgcli]
move_up = ["q"]
quit = ["ctrl+q"]
`)

	keys, err := LoadTgwallKeyBindings()
	require.NoError(t, err)

	want := TgwallKeyBindings{
		MoveUp:        []string{"k", "up"},
		MoveDown:      []string{"j", "down"},
		PageUp:        []string{"ctrl+b"},
		PageDown:      []string{"ctrl+f"},
		FocusNext:     []string{"tab"},
		FocusPrev:     []string{"ctrl+p"},
		Select:        []string{"ctrl+m"},
		Back:          []string{"ctrl+g"},
		DeleteMessage: []string{"ctrl+d"},
		Reply:         []string{"r"},
		Filter:        []string{"ctrl+o"},
		Toggle:        []string{"ctrl+t"},
		Quit:          []string{"ctrl+x"},
		// Панели переписки в файле не заданы, и их дефолты (ctrl+1 и ctrl+2)
		// обязаны доехать из настроек по умолчанию: пустая секция — это «не задано»,
		// а не «панелей нет».
		OpenPanel1: []string{"ctrl+1"},
		OpenPanel2: []string{"ctrl+2"},
	}
	require.Equal(t, want, keys)

	// Соседняя секция [tgcli] не должна подмешиваться в набор стены: у ленты свои
	// действия и свои клавиши, и наборы не связаны.
	tgcli, err := LoadTgcliKeyBindings()
	require.NoError(t, err)
	require.Equal(t, []string{"q"}, tgcli.MoveUp)
	require.Equal(t, []string{"ctrl+q"}, tgcli.Quit)
	require.Equal(t, []string{"down"}, tgcli.MoveDown)
}

func TestLoadTgwallKeyBindingsInvalidTOMLReturnsError(t *testing.T) {
	_, cleanup := setupKeyBindingsTest(t)
	defer cleanup()

	writeKeyBindingsFile(t, "[tgwall]\nmove_up = [\nnot valid toml")

	_, err := LoadTgwallKeyBindings()
	require.Error(t, err)
}

func TestLoadTgwallKeyBindingsUnreadableFileReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, "keybindings.toml")
	require.NoError(t, os.MkdirAll(dir, 0700))
	SetKeyBindingsPathForTest(dir)
	defer SetKeyBindingsPathForTest("")

	_, err := LoadTgwallKeyBindings()
	require.Error(t, err)
}

func writeKeyBindingsFile(t *testing.T, content string) {
	t.Helper()
	path, err := keybindingsPath()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
}
