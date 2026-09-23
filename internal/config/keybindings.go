package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// KeyBindings — конфигурируемые из TOML клавиши для действий в Normal-режиме.
// Пустые поля файла трактуются как «использовать дефолт», см. LoadKeyBindings.
type KeyBindings struct {
	MoveUp       []string `toml:"move_up"`
	MoveDown     []string `toml:"move_down"`
	FocusNext    []string `toml:"focus_next"`
	FocusLeft    []string `toml:"focus_left"`
	FocusRight   []string `toml:"focus_right"`
	FocusPane1   []string `toml:"focus_pane_1"`
	FocusPane2   []string `toml:"focus_pane_2"`
	FocusPane3   []string `toml:"focus_pane_3"`
	Select       []string `toml:"select"`
	Back         []string `toml:"back"`
	EnterInsert  []string `toml:"enter_insert"`
	EnterCommand []string `toml:"enter_command"`
	Quit         []string `toml:"quit"`
	SendFile     []string `toml:"send_file"`
	Search       []string `toml:"search"`
	ShowHelp     []string `toml:"show_help"`
	DeleteChat   []string `toml:"delete_chat"`
	About        []string `toml:"about"`
}

func DefaultKeyBindings() KeyBindings {
	return KeyBindings{
		MoveUp:       []string{"k", "up"},
		MoveDown:     []string{"j", "down"},
		FocusNext:    []string{"tab"},
		FocusLeft:    []string{"left"},
		FocusRight:   []string{"right"},
		FocusPane1:   []string{"1"},
		FocusPane2:   []string{"2"},
		FocusPane3:   []string{"3"},
		Select:       []string{"enter"},
		Back:         []string{"esc"},
		EnterInsert:  []string{"i"},
		EnterCommand: []string{":"},
		Quit:         []string{"q", "ctrl+c"},
		SendFile:     []string{"ctrl+f"},
		Search:       []string{"/"},
		ShowHelp:     []string{"h"},
		DeleteChat:   []string{"d"},
		About:        []string{"t"},
	}
}

// keybindingsPathOverride — отдельный override от fallbackPathOverride: он про
// секреты (config.toml), а этот путь про keybindings.toml, их жизненные циклы
// и чувствительность разные, переиспользовать нельзя.
var keybindingsPathOverride string

func SetKeyBindingsPathForTest(path string) {
	keybindingsPathOverride = path
}

func KeyBindingsPathForTest() string {
	return keybindingsPathOverride
}

func keybindingsPath() (string, error) {
	if keybindingsPathOverride != "" {
		return keybindingsPathOverride, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine user config dir: %w", err)
	}
	return filepath.Join(configDir, "telecli", "keybindings.toml"), nil
}

// LoadKeyBindings читает keybindings.toml и мержит непустые поля поверх
// дефолтов. Нет файла — дефолты без ошибки (файл не создаётся автоматически).
// Ошибка чтения/парсинга — явная, не деградация молча. Валидация имён клавиш
// не выполняется: нерабочий биндинг — просто нерабочий биндинг.
func LoadKeyBindings() (KeyBindings, error) {
	keys := DefaultKeyBindings()

	path, err := keybindingsPath()
	if err != nil {
		return keys, err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return keys, nil
	}
	if err != nil {
		return keys, fmt.Errorf("failed to read keybindings config: %w", err)
	}

	var fileKeys KeyBindings
	if err := toml.Unmarshal(data, &fileKeys); err != nil {
		return keys, fmt.Errorf("failed to parse keybindings config: %w", err)
	}

	if len(fileKeys.MoveUp) > 0 {
		keys.MoveUp = fileKeys.MoveUp
	}
	if len(fileKeys.MoveDown) > 0 {
		keys.MoveDown = fileKeys.MoveDown
	}
	if len(fileKeys.FocusNext) > 0 {
		keys.FocusNext = fileKeys.FocusNext
	}
	if len(fileKeys.FocusLeft) > 0 {
		keys.FocusLeft = fileKeys.FocusLeft
	}
	if len(fileKeys.FocusRight) > 0 {
		keys.FocusRight = fileKeys.FocusRight
	}
	if len(fileKeys.FocusPane1) > 0 {
		keys.FocusPane1 = fileKeys.FocusPane1
	}
	if len(fileKeys.FocusPane2) > 0 {
		keys.FocusPane2 = fileKeys.FocusPane2
	}
	if len(fileKeys.FocusPane3) > 0 {
		keys.FocusPane3 = fileKeys.FocusPane3
	}
	if len(fileKeys.Select) > 0 {
		keys.Select = fileKeys.Select
	}
	if len(fileKeys.Back) > 0 {
		keys.Back = fileKeys.Back
	}
	if len(fileKeys.EnterInsert) > 0 {
		keys.EnterInsert = fileKeys.EnterInsert
	}
	if len(fileKeys.EnterCommand) > 0 {
		keys.EnterCommand = fileKeys.EnterCommand
	}
	if len(fileKeys.Quit) > 0 {
		keys.Quit = fileKeys.Quit
	}
	if len(fileKeys.SendFile) > 0 {
		keys.SendFile = fileKeys.SendFile
	}
	if len(fileKeys.Search) > 0 {
		keys.Search = fileKeys.Search
	}
	if len(fileKeys.ShowHelp) > 0 {
		keys.ShowHelp = fileKeys.ShowHelp
	}
	if len(fileKeys.DeleteChat) > 0 {
		keys.DeleteChat = fileKeys.DeleteChat
	}
	if len(fileKeys.About) > 0 {
		keys.About = fileKeys.About
	}
	return keys, nil
}
