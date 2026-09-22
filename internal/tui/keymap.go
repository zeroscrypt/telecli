package tui

import (
	"github.com/charmbracelet/bubbles/key"

	"telecli/internal/config"
)

// KeyMap — биндинги Normal-режима, собранные из конфигурируемых клавиш.
type KeyMap struct {
	MoveUp       key.Binding
	MoveDown     key.Binding
	FocusNext    key.Binding
	FocusLeft    key.Binding
	FocusRight   key.Binding
	FocusPane1   key.Binding
	FocusPane2   key.Binding
	FocusPane3   key.Binding
	Select       key.Binding
	Back         key.Binding
	EnterInsert  key.Binding
	EnterCommand key.Binding
	Quit         key.Binding
	OpenEditor   key.Binding
	SendFile     key.Binding
}

func newKeyMap(cfg config.KeyBindings) KeyMap {
	return KeyMap{
		MoveUp:       key.NewBinding(key.WithKeys(cfg.MoveUp...), key.WithHelp("↑/k", "вверх")),
		MoveDown:     key.NewBinding(key.WithKeys(cfg.MoveDown...), key.WithHelp("↓/j", "вниз")),
		FocusNext:    key.NewBinding(key.WithKeys(cfg.FocusNext...), key.WithHelp("tab", "переключить фокус")),
		FocusLeft:    key.NewBinding(key.WithKeys(cfg.FocusLeft...), key.WithHelp("←", "фокус влево")),
		FocusRight:   key.NewBinding(key.WithKeys(cfg.FocusRight...), key.WithHelp("→", "фокус вправо")),
		FocusPane1:   key.NewBinding(key.WithKeys(cfg.FocusPane1...), key.WithHelp("1", "панель папок")),
		FocusPane2:   key.NewBinding(key.WithKeys(cfg.FocusPane2...), key.WithHelp("2", "панель чатов")),
		FocusPane3:   key.NewBinding(key.WithKeys(cfg.FocusPane3...), key.WithHelp("3", "панель сообщений")),
		Select:       key.NewBinding(key.WithKeys(cfg.Select...), key.WithHelp("enter", "открыть чат")),
		Back:         key.NewBinding(key.WithKeys(cfg.Back...), key.WithHelp("esc", "назад")),
		EnterInsert:  key.NewBinding(key.WithKeys(cfg.EnterInsert...), key.WithHelp("i", "ввод")),
		EnterCommand: key.NewBinding(key.WithKeys(cfg.EnterCommand...), key.WithHelp(":", "команда")),
		Quit:         key.NewBinding(key.WithKeys(cfg.Quit...), key.WithHelp("q", "выход")),
		OpenEditor:   key.NewBinding(key.WithKeys(cfg.OpenEditor...), key.WithHelp("ctrl+e", "редактор")),
		SendFile:     key.NewBinding(key.WithKeys(cfg.SendFile...), key.WithHelp("ctrl+f", "файл")),
	}
}
