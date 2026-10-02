package config

import (
	"errors"
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
	PlayVoice    []string `toml:"play_voice"`
	PreviewPhoto []string `toml:"preview_photo"`
	// Действия, которые понадобились tgcli: в старом tui их не было, поэтому и
	// полей для них не было. Читаются так же, как остальные, — непустое поле
	// файла перекрывает дефолт.
	Reply         []string `toml:"reply"`
	DeleteMessage []string `toml:"delete_message"`
	NewLine       []string `toml:"new_line"`
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
		PlayVoice:    []string{"p"},
		PreviewPhoto: []string{"p"},
		Reply:        []string{"r"},
		// ctrl+d, а не d: в общем keybindings.toml d занят удалением чата, и
		// переопределять его значило бы сломать старый интерфейс.
		DeleteMessage: []string{"ctrl+d"},
		NewLine:       []string{"ctrl+j"},
	}
}

// TgcliKeyBindings — раскладка клавиш нового интерфейса (tgcli). Отдельная от
// KeyBindings не по названию, а по смыслу: у старого интерфейса есть режимы, где
// буква означает действие, а в новом поле ввода всегда активно, поэтому буква
// должна печататься. Отсюда и клавиши по умолчанию с модификаторами: Ctrl+H на
// справку вместо h, который иначе невозможно набрать в сообщении.
//
// Читается из той же keybindings.toml, секция [tgcli]: одно место для клавиш, но
// без вынужденной связи между двумя интерфейсами.
type TgcliKeyBindings struct {
	MoveUp     []string `toml:"move_up"`
	MoveDown   []string `toml:"move_down"`
	FocusNext  []string `toml:"focus_next"`
	FocusLeft  []string `toml:"focus_left"`
	FocusRight []string `toml:"focus_right"`
	Select     []string `toml:"select"`
	Back       []string `toml:"back"`
	Search     []string `toml:"search"`
	SendFile   []string `toml:"send_file"`
	Quit       []string `toml:"quit"`
	// Info — один экран «Информация»: логотип, версия и список горячих клавиш.
	// Раньше это были два отдельных экрана (show_help и about), человек попросил
	// слить их в один, поэтому и поле одно.
	Info          []string `toml:"info"`
	Reply         []string `toml:"reply"`
	DeleteMessage []string `toml:"delete_message"`
	Media         []string `toml:"media"`
	// Source — переключение источника сообщений по кругу (все, личные, чаты,
	// каналы). Именно ctrl+s, а не ctrl+i: Ctrl+I и Tab — один и тот же байт
	// 0x09, и на ctrl+i хоткей не сработал бы (см. Info выше — тот же довод).
	Source []string `toml:"source"`
	// ShowMuted — чекбокс «приглушённые» в строке источника. Именно ctrl+x:
	// ctrl+u — это DeleteBeforeCursor у textarea (удалить всё слева от курсора),
	// и человек настаивает, чтобы в поле работало как в терминале (решение
	// человека). Из всех 26 букв ctrl+x и ctrl+y — единственные, не занятые ни
	// терминалом, ни keymap виджета, ни другим действием tgcli; ctrl+y выбран не
	// был, потому что в readline это yank, и мышечная память человека связывает
	// его с правкой текста, а не с фильтром.
	//
	// Смысл чекбокса — показывать ли приглушённые чаты, поэтому выключен он по
	// умолчанию: приглушённые чаты скрыты сразу при старте (задача 0124).
	ShowMuted []string `toml:"show_muted"`
	NewLine   []string `toml:"new_line"`
	PageUp    []string `toml:"page_up"`
	PageDown  []string `toml:"page_down"`
	ShowLog   []string `toml:"show_log"`
}

func DefaultTgcliKeyBindings() TgcliKeyBindings {
	return TgcliKeyBindings{
		MoveUp:     []string{"up"},
		MoveDown:   []string{"down"},
		FocusNext:  []string{"tab"},
		FocusLeft:  []string{"left"},
		FocusRight: []string{"right"},
		Select:     []string{"enter"},
		Back:       []string{"esc"},
		Search:     []string{"/"},
		SendFile:   []string{"ctrl+f"},
		Quit:       []string{"ctrl+c"},
		// Справка и «о программе» — один экран «Информация», и одна клавиша.
		// Именно ctrl+o, а не ctrl+i: в bubbletea Ctrl+I и Tab — один и тот же
		// байт 0x09, названный «tab», поэтому на ctrl+i хоткей не сработал бы
		// (решение человека — см. задачу 0120).
		Info: []string{"ctrl+o"},
		// Ни одного хоткея без модификатора: поле ввода всегда активно, поэтому
		// одиночная буква не может быть и действием, и текстом — иначе слово не
		// набрать (решение человека). Поэтому и «p», и «r» — с Ctrl.
		Reply:         []string{"ctrl+r"},
		DeleteMessage: []string{"ctrl+d"},
		// Звук и превью уехали с ctrl+p на ctrl+g: панель чатов человек попросил
		// открывать и закрывать именно ctrl+p, а одна клавиша не может делать два
		// разных дела. ctrl+g свободен и в том же смысле (Ctrl плюс буква), поэтому
		// правило «никакого хоткея без модификатора» не нарушается.
		Media:   []string{"ctrl+g"},
		NewLine: []string{"ctrl+j"},
		// Источник и фильтр приглушённых — по одной букве с Ctrl, как и все
		// остальные: правило «никакого хоткея без модификатора» одно на весь
		// интерфейс. ctrl+s не попадает в коллизию с Tab (ctrl+i) или Enter
		// (ctrl+m), из-за которой Info уехал с ctrl+i на ctrl+o.
		Source: []string{"ctrl+s"},
		// ctrl+u вернулось полю: это DeleteBeforeCursor у textarea, «удалить всё
		// слева от курсора» — как в терминале, и человек настаивал именно на этом.
		ShowMuted: []string{"ctrl+x"},
		ShowLog:   []string{"ctrl+l"},
		// Листание на экран: у ленты, у списка чатов и у истории открытого чата.
		// Человек попросил pgup/pgdown, и это единственные клавиши интерфейса,
		// которых не было вовсе.
		PageUp:   []string{"pgup"},
		PageDown: []string{"pgdown"},
	}
}

// TgwallKeyBindings — раскладка клавиш стены (tgwall). Отдельная от двух
// предыдущих наборов, а не переиспользование одного из них: у стены свой набор
// действий (курсор по стене и по переписке, удаление карточки, переключение фокуса
// список/переписка), и даже совпадающие по названию поля означают там другое, чем в
// tgcli. Механическое совпадение имён не повод связать интерфейсы одной структурой.
//
// Читается из того же keybindings.toml, секция [tgwall]: одно место для клавиш
// проекта, но без вынужденной связи между тремя интерфейсами.
//
// Валидация имён клавиш не выполняется (как и у двух других наборов): нерабочий
// биндинг — просто нерабочий биндинг. Одна и та же клавиша, назначенная двум
// действиям, не запрещается: сработает первое действие в фиксированном порядке
// проверок стены (выход → ответ → …), и этот порядок задокументирован в
// internal/tgwall/model.go.
type TgwallKeyBindings struct {
	MoveUp    []string `toml:"move_up"`
	MoveDown  []string `toml:"move_down"`
	PageUp    []string `toml:"page_up"`
	PageDown  []string `toml:"page_down"`
	FocusNext []string `toml:"focus_next"`
	// FocusPrev — круг фокуса в обратную сторону. Отдельное поле, а не
	// «FocusNext плюс направление»: направление задаётся клавишей, а не
	// настройкой, и по умолчанию человек хочет ходить по кругу в обе стороны.
	FocusPrev     []string `toml:"focus_prev"`
	Select        []string `toml:"select"`
	Back          []string `toml:"back"`
	DeleteMessage []string `toml:"delete_message"`
	Reply         []string `toml:"reply"`
	// Filter открывает меню фильтра источников. Отдельного действия «закрыть
	// его» в конфигурации нет намеренно: то же нажатие и открывает оверлей, и
	// закрывает его, поэтому назначить ему вторую клавишу «на закрытие» было бы
	// двумя полями для одного и того же действия.
	Filter []string `toml:"filter"`
	// Toggle переключает галку под курсором в меню фильтра. Отдельное поле, а не
	// переиспользование Select: в меню это не «отправить текст», а переключение
	// строки списка, и на Enter та же клавиша означает в стене совершенно другое
	// действие. Одно поле на два разных действия означало бы, что переназначив
	// отправку, человек невольно переназначил и переключение в меню (и наоборот).
	Toggle []string `toml:"toggle"`
	Quit   []string `toml:"quit"`
	// OpenPanel1 и OpenPanel2 открывают источник под курсором стены в панели
	// переписки 1 или 2 (см. нумерацию панелей в internal/tgwall/panels.go).
	//
	// Отдельные поля, а не одно с номером: номер панели в TOML не выразить, а
	// значит пришлось бы либо разбирать «panel_1» вручную, либо заводить массив
	// структур — и тогда одна опечатка в файле тихо убирала бы второе действие
	// вместо понятной ошибки.
	//
	// Смысл есть только в трёхколоночном режиме (ширина терминала больше 110);
	// в обычном широком режиме панель одна, и обе клавиши открывают её — как и
	// Enter, только явным выбором панели.
	OpenPanel1 []string `toml:"open_panel_1"`
	OpenPanel2 []string `toml:"open_panel_2"`
}

func DefaultTgwallKeyBindings() TgwallKeyBindings {
	return TgwallKeyBindings{
		MoveUp:    []string{"up"},
		MoveDown:  []string{"down"},
		PageUp:    []string{"pgup"},
		PageDown:  []string{"pgdown"},
		FocusNext: []string{"tab"},
		// FocusPrev — shift+tab, а не alt+tab: терминал присылает shift+tab
		// отдельным нажатием без всяких расширений клавиатуры (последовательность
		// CSI Z), тогда как alt+tab в большинстве терминалов до программы не
		// доходит вовсе. Обратного хода по кругу фокуса иначе нет никакого.
		FocusPrev: []string{"shift+tab"},
		Select:    []string{"enter"},
		Back:      []string{"esc"},
		// Delete, а не ctrl+d: у стены поле ввода всегда активно, но отдельного
		// символа удаления у неё не было — та же клавиша, что и до появления
		// конфигурации (задача 0157), и переименовывать её вместе с разделом
		// конфигурации значило бы поменять поведение по умолчанию.
		DeleteMessage: []string{"delete"},
		// Reply — ctrl+r, а не голая «r»: как и в tgcli, правило «никакого хоткея
		// без модификатора» одно на оба интерфейса, иначе слово в поле не набрать.
		Reply: []string{"ctrl+r"},
		// Filter — ctrl+p, как у большинства терминальных программ: тот же
		// «быстрый переход к списку», только список здесь один — фильтр.
		Filter: []string{"ctrl+p"},
		// Toggle — space, а не enter: по велению человека (2026-09-30, задача
		// 0166). В меню списка галка переключается пробелом — как в файловых
		// менеджерах и в большинстве терминальных программ, — а Enter в этом
		// меню больше ничего не делает: отправка текста и открытие источника
		// живут в стене и под Enter остаются.
		Toggle: []string{"space"},
		Quit:   []string{"ctrl+c"},
		// Номера панелей как у человека: панель 0 — левая лента со списком
		// источников, 1 — панель чатов у самой ленты, 2 — крайняя справа
		// (2026-10-01). Дефолт — ctrl+1 и ctrl+2, буквенные хоткеи для «второй
		// панели» заняты быть не могут по той же причине, что и ctrl+ занят: у
		// стены любая буква без модификатора уходит в текст сообщения.
		OpenPanel1: []string{"ctrl+1"},
		OpenPanel2: []string{"ctrl+2"},
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

	var fileKeys keybindingsFile
	if err := toml.Unmarshal(data, &fileKeys); err != nil {
		return keys, fmt.Errorf("failed to parse keybindings config: %w", err)
	}
	return mergeKeyBindings(keys, fileKeys.KeyBindings), nil
}

// keybindingsFile — содержимое keybindings.toml: клавиши старого интерфейса в
// верхнем уровне (как всегда) и отдельные секции [tgcli] и [tgwall] новых.
type keybindingsFile struct {
	KeyBindings
	Tgcli  TgcliKeyBindings  `toml:"tgcli"`
	Tgwall TgwallKeyBindings `toml:"tgwall"`
}

// mergeKeyBindings — непустые поля файла перекрывают дефолты.
func mergeKeyBindings(keys, fileKeys KeyBindings) KeyBindings {

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
	if len(fileKeys.PlayVoice) > 0 {
		keys.PlayVoice = fileKeys.PlayVoice
	}
	if len(fileKeys.PreviewPhoto) > 0 {
		keys.PreviewPhoto = fileKeys.PreviewPhoto
	}
	if len(fileKeys.Reply) > 0 {
		keys.Reply = fileKeys.Reply
	}
	if len(fileKeys.DeleteMessage) > 0 {
		keys.DeleteMessage = fileKeys.DeleteMessage
	}
	if len(fileKeys.NewLine) > 0 {
		keys.NewLine = fileKeys.NewLine
	}
	return keys
}

// LoadTgcliKeyBindings читает секцию [tgcli] того же keybindings.toml поверх
// tgcli-дефолтов. Нет файла или нет секции — дефолты без ошибки.
func LoadTgcliKeyBindings() (TgcliKeyBindings, error) {
	keys := DefaultTgcliKeyBindings()
	path, err := keybindingsPath()
	if err != nil {
		return keys, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return keys, nil
	}
	if err != nil {
		return keys, fmt.Errorf("failed to read keybindings config: %w", err)
	}
	var fileKeys keybindingsFile
	if err := toml.Unmarshal(data, &fileKeys); err != nil {
		return keys, fmt.Errorf("failed to parse keybindings config: %w", err)
	}
	return mergeTgcliKeyBindings(keys, fileKeys.Tgcli), nil
}

// mergeTgcliKeyBindings — непустые поля секции [tgcli] перекрывают дефолты.
func mergeTgcliKeyBindings(keys, fileKeys TgcliKeyBindings) TgcliKeyBindings {
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
	if len(fileKeys.Select) > 0 {
		keys.Select = fileKeys.Select
	}
	if len(fileKeys.Back) > 0 {
		keys.Back = fileKeys.Back
	}
	if len(fileKeys.Search) > 0 {
		keys.Search = fileKeys.Search
	}
	if len(fileKeys.SendFile) > 0 {
		keys.SendFile = fileKeys.SendFile
	}
	if len(fileKeys.Info) > 0 {
		keys.Info = fileKeys.Info
	}
	if len(fileKeys.Quit) > 0 {
		keys.Quit = fileKeys.Quit
	}
	if len(fileKeys.Reply) > 0 {
		keys.Reply = fileKeys.Reply
	}
	if len(fileKeys.DeleteMessage) > 0 {
		keys.DeleteMessage = fileKeys.DeleteMessage
	}
	if len(fileKeys.Media) > 0 {
		keys.Media = fileKeys.Media
	}
	if len(fileKeys.Source) > 0 {
		keys.Source = fileKeys.Source
	}
	if len(fileKeys.ShowMuted) > 0 {
		keys.ShowMuted = fileKeys.ShowMuted
	}
	if len(fileKeys.NewLine) > 0 {
		keys.NewLine = fileKeys.NewLine
	}
	if len(fileKeys.PageUp) > 0 {
		keys.PageUp = fileKeys.PageUp
	}
	if len(fileKeys.PageDown) > 0 {
		keys.PageDown = fileKeys.PageDown
	}
	if len(fileKeys.ShowLog) > 0 {
		keys.ShowLog = fileKeys.ShowLog
	}
	return keys
}

// LoadTgwallKeyBindings читает секцию [tgwall] того же keybindings.toml поверх
// tgwall-дефолтов. Нет файла или нет секции — дефолты без ошибки.
func LoadTgwallKeyBindings() (TgwallKeyBindings, error) {
	keys := DefaultTgwallKeyBindings()
	path, err := keybindingsPath()
	if err != nil {
		return keys, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return keys, nil
	}
	if err != nil {
		return keys, fmt.Errorf("failed to read keybindings config: %w", err)
	}
	var fileKeys keybindingsFile
	if err := toml.Unmarshal(data, &fileKeys); err != nil {
		return keys, fmt.Errorf("failed to parse keybindings config: %w", err)
	}
	return mergeTgwallKeyBindings(keys, fileKeys.Tgwall), nil
}

// mergeTgwallKeyBindings — непустые поля секции [tgwall] перекрывают дефолты.
func mergeTgwallKeyBindings(keys, fileKeys TgwallKeyBindings) TgwallKeyBindings {
	if len(fileKeys.MoveUp) > 0 {
		keys.MoveUp = fileKeys.MoveUp
	}
	if len(fileKeys.MoveDown) > 0 {
		keys.MoveDown = fileKeys.MoveDown
	}
	if len(fileKeys.PageUp) > 0 {
		keys.PageUp = fileKeys.PageUp
	}
	if len(fileKeys.PageDown) > 0 {
		keys.PageDown = fileKeys.PageDown
	}
	if len(fileKeys.FocusNext) > 0 {
		keys.FocusNext = fileKeys.FocusNext
	}
	if len(fileKeys.FocusPrev) > 0 {
		keys.FocusPrev = fileKeys.FocusPrev
	}
	if len(fileKeys.Select) > 0 {
		keys.Select = fileKeys.Select
	}
	if len(fileKeys.Back) > 0 {
		keys.Back = fileKeys.Back
	}
	if len(fileKeys.DeleteMessage) > 0 {
		keys.DeleteMessage = fileKeys.DeleteMessage
	}
	if len(fileKeys.Reply) > 0 {
		keys.Reply = fileKeys.Reply
	}
	if len(fileKeys.Filter) > 0 {
		keys.Filter = fileKeys.Filter
	}
	if len(fileKeys.Toggle) > 0 {
		keys.Toggle = fileKeys.Toggle
	}
	if len(fileKeys.Quit) > 0 {
		keys.Quit = fileKeys.Quit
	}
	if len(fileKeys.OpenPanel1) > 0 {
		keys.OpenPanel1 = fileKeys.OpenPanel1
	}
	if len(fileKeys.OpenPanel2) > 0 {
		keys.OpenPanel2 = fileKeys.OpenPanel2
	}
	return keys
}
