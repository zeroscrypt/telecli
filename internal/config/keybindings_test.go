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

	writeKeyBindingsFile(t, "quit = [\"x\"]")

	keys, err := LoadKeyBindings()
	require.NoError(t, err)

	defaults := DefaultKeyBindings()
	require.Equal(t, []string{"x"}, keys.Quit)
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

func writeKeyBindingsFile(t *testing.T, content string) {
	t.Helper()
	path, err := keybindingsPath()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
}
