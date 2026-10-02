package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Settings — пользовательские настройки приложения (TOML,
// <UserConfigDir()>/telecli/settings.toml, отдельно от секретов и
// keybindings.toml — разные файлы, разная семантика). Задел на будущее:
// сюда добавляются новые поля по мере появления новых настраиваемых опций,
// не создавая для каждой новый файл.
type Settings struct {
	AlignOwnRight bool     `toml:"-"` // выставляется в LoadSettings, не напрямую из TOML (см. ниже)
	Theme         string   `toml:"theme"`
	Log           LogFiles `toml:"log"`
	// WallFilter — состояние фильтра источников стены (tgwall). Живёт здесь, а не
	// в keybindings.toml: клавиши — это то, чем стена РЕАГИрует, а что человек
	// ОТОБРАЛ показывать — это его настройка, и два разных вопроса не должны
	// лежать в одном файле.
	WallFilter WallFilter `toml:"wall_filter"`
}

// WallFilter — что показывает стена: какие типы источников, какие папки и
// заглушённые ли чаты. Значение по умолчанию (DefaultWallFilter) — «видно всё»:
// стена до появления фильтра показывала ровно это, и первый запуск не должен
// молча сузить то, что человек уже привык видеть.
//
// Папки хранятся по ID, а не по названию: название папки человек переименовывает
// руками в самом Telegram, и фильтр по названию после этого тихо слетел бы с
// реальности.
type WallFilter struct {
	ShowChannels bool    `toml:"show_channels"`
	ShowChats    bool    `toml:"show_chats"`
	ShowPersonal bool    `toml:"show_personal"`
	ShowMuted    bool    `toml:"show_muted"`
	Folders      []int32 `toml:"folders"`
}

// DefaultWallFilter — «Все» включено (все три типа), папки не отмечены,
// «Приглушённые» выключено. Три из четырёх значений тут несут смысл только
// вместе с защитой от пустого состояния (стена не может остаться вообще без
// типов, см. internal/tgwall/filter.go): включение всех трёх — единственное
// состояние, из которого нельзя выйти, не получив пустую стену.
func DefaultWallFilter() WallFilter {
	return WallFilter{ShowChannels: true, ShowChats: true, ShowPersonal: true}
}

// LogFiles — настройки журнала событий: где лежит файл и сколько строк в нём
// живёт. Ограничение именно в строках, а не в мегабайтах: журнал читают глазами в
// окне приложения, и 1000 строк — это несколько экранов истории, которые человек
// способен пролистать. Лимит в байтах означал бы файл, который либо ничего не
// показывает, либо молча вырезает начало по средине сообщения.
//
// Человек описал поведение прямо: 1001-я строка вытесняет первую, 2-я становится
// 1-й, и так по кругу — то есть кольцо, а не усечение с конца.
type LogFiles struct {
	Path     string `toml:"path"`
	MaxLines int    `toml:"max_lines"`
	// Level — с какого уровня показывать события в окне журнала: info, warning или
	// error. Уровни накопительные: error включает warning, warning включает info.
	// Переключать его в самом окне человек решил не надо — уровень живёт здесь, а
	// окно настроек (ctrl+o) будет писать его и сохранять.
	Level string `toml:"level"`
}

func DefaultSettings() Settings {
	return Settings{
		AlignOwnRight: true,
		Theme:         "neon",
		Log:           DefaultLogFiles(),
		WallFilter:    DefaultWallFilter(),
	}
}

// DefaultLogFiles — путь журнала по умолчанию и предел в строках. Путь считается от
// каталога настроек, а не от рабочего каталога: журнал принадлежит пользователю, и
// искать его в том месте, откуда запустили telecli, нельзя.
func DefaultLogFiles() LogFiles {
	return LogFiles{Path: defaultLogPath(), MaxLines: defaultLogMaxLines, Level: "info"}
}

const defaultLogMaxLines = 1000

// defaultLogPath — <UserConfigDir()>/telecli/tgcli.log. При недоступном каталоге
// настроек возвращается пустая строка: журнал тогда просто не пишется, а приложение
// работает дальше — молча падать из-за журнала нельзя.
func defaultLogPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "telecli", "tgcli.log")
}

// settingsPathOverride — отдельный override от fallbackPathOverride (секреты) и
// keybindingsPathOverride (keybindings.toml): у трёх файлов разные жизненные
// циклы и чувствительность, переиспользовать нельзя.
var settingsPathOverride string

func SetSettingsPathForTest(path string) {
	settingsPathOverride = path
}

func SettingsPathForTest() string {
	return settingsPathOverride
}

func settingsPath() (string, error) {
	if settingsPathOverride != "" {
		return settingsPathOverride, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine user config dir: %w", err)
	}
	return filepath.Join(configDir, "telecli", "settings.toml"), nil
}

// settingsFile — приватный тип ТОЛЬКО для чтения settings.toml. Булево поле
// нельзя мержить по правилу "пусто = не задано": zero-value
// булева — false, неотличимо от "явно выключено в файле", поэтому парсинг идёт
// через *bool, а наружу отдаётся обычный bool (см. LoadSettings).
type settingsFile struct {
	AlignOwnRight *bool         `toml:"align_own_right"`
	Theme         string        `toml:"theme"`
	Log           logFiles      `toml:"log"`
	WallFilter    wallFilterRaw `toml:"wall_filter"`
}

// wallFilterRaw — приватный тип секции [wall_filter] файла настроек: те же поля,
// что в WallFilter, но с типами, годными для слияния. Четыре флага — *bool по
// той же причине, что и AlignOwnRight: умолчание тут «включено» (ShowMuted —
// «выключено»), а у обычного bool zero-value не отличить от «явно выключено в
// файле», и фильтр молча слезал бы с того, что человек настроил. Признак «секции
// в файле нет» — nil у самой структуры.
type wallFilterRaw struct {
	ShowChannels *bool   `toml:"show_channels"`
	ShowChats    *bool   `toml:"show_chats"`
	ShowPersonal *bool   `toml:"show_personal"`
	ShowMuted    *bool   `toml:"show_muted"`
	Folders      []int32 `toml:"folders"`
}

// logFiles — приватный тип файла настроек: те же поля, что в LogFiles, но с типами,
// годными для слияния (пустое = не задано).
type logFiles struct {
	Path     string `toml:"path"`
	MaxLines int    `toml:"max_lines"`
	Level    string `toml:"level"`
}

// LoadSettings читает settings.toml и мержит непустые поля поверх дефолтов.
// Тот же контракт, что у LoadKeyBindings: нет файла — дефолты без ошибки (файл
// не создаётся автоматически); ошибка чтения/парсинга — явная, не деградация
// молча. Новое поле добавляется тем же паттерном: if fileSettings.X != "" {
// settings.X = fileSettings.X } — не через reflection.
func LoadSettings() (Settings, error) {
	settings := DefaultSettings()

	path, err := settingsPath()
	if err != nil {
		return settings, err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return settings, nil
	}
	if err != nil {
		return settings, fmt.Errorf("failed to read settings config: %w", err)
	}

	var fileSettings settingsFile
	if err := toml.Unmarshal(data, &fileSettings); err != nil {
		return settings, fmt.Errorf("failed to parse settings config: %w", err)
	}

	if fileSettings.AlignOwnRight != nil {
		settings.AlignOwnRight = *fileSettings.AlignOwnRight
	}
	if fileSettings.Theme != "" {
		settings.Theme = fileSettings.Theme
	}
	if fileSettings.Log.Path != "" {
		settings.Log.Path = fileSettings.Log.Path
	}
	// Ноль и отрицательное — «не задано»: в файле может стоять 0, и это должно
	// означать дефолт, а не пустой журнал.
	if fileSettings.Log.MaxLines > 0 {
		settings.Log.MaxLines = fileSettings.Log.MaxLines
	}
	if fileSettings.Log.Level != "" {
		settings.Log.Level = strings.ToLower(fileSettings.Log.Level)
	}
	mergeWallFilter(&settings.WallFilter, fileSettings.WallFilter)
	return settings, nil
}

// mergeWallFilter — непустые поля секции [wall_filter] перекрывают дефолт. Секции в
// файле нет — не трогаем ничего; пустой список папок («ни одна не отмечена»)
// совпадает со значением по умолчанию, поэтому по нему отдельного признака «задано
// явно» не нужно.
func mergeWallFilter(filter *WallFilter, raw wallFilterRaw) {
	if raw.ShowChannels != nil {
		filter.ShowChannels = *raw.ShowChannels
	}
	if raw.ShowChats != nil {
		filter.ShowChats = *raw.ShowChats
	}
	if raw.ShowPersonal != nil {
		filter.ShowPersonal = *raw.ShowPersonal
	}
	if raw.ShowMuted != nil {
		filter.ShowMuted = *raw.ShowMuted
	}
	if len(raw.Folders) > 0 {
		filter.Folders = append([]int32(nil), raw.Folders...)
	}
}

// SaveWallFilter записывает ТОЛЬКО секцию [wall_filter], оставляя остальное
// settings.toml как есть.
//
// Файл читается в КАРТУ, а не в Settings, и обратно кодируется тоже карта: так
// внутрь попадают и те ключи, которых эта версия программы не знает, — дописанные
// человеком руками настройки переживают перезапись. Разбор в Settings с
// последующим кодированием этой структуры их бы молча стёр (и заодно стёр бы
// align_own_right: у него тег toml:"-" — при РАЗБОРЕ он не нужен, нулевое
// значение bool не отличить от «явно выключено», а вот при записи из структуры
// поле выпало бы из файла).
//
// Отсутствующий файл — не ошибка: он создаётся. Битый файл — явная ошибка, а не
// молчаливая перезапись: человек с неразобранным settings.toml должен узнать об
// этом, а не получить вместо своего файла новый.
//
// Каталог создаётся, а файл пишется с правами 0600 — тем же способом, что и
// конфигурация с секретами (см. saveToFallback): каталог настроек принадлежит
// пользователю, и оставлять в нём общедоступный файл неправильно.
func SaveWallFilter(filter WallFilter) error {
	path, err := settingsPath()
	if err != nil {
		return err
	}

	document := make(map[string]interface{})
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := toml.Unmarshal(data, &document); err != nil {
			return fmt.Errorf("failed to parse settings config: %w", err)
		}
	case !os.IsNotExist(err):
		return fmt.Errorf("failed to read settings config: %w", err)
	}

	document[wallFilterSection] = wallFilterDocument(filter)

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("cannot create settings dir: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("cannot open settings file: %w", err)
	}
	if err := toml.NewEncoder(f).Encode(document); err != nil {
		_ = f.Close()
		return fmt.Errorf("cannot encode settings: %w", err)
	}
	// Ошибка закрытия проверяется явно, а не отбрасывается вместе с defer: часть
	// ошибок записи (в том числе «диск кончился») выходит именно на Close, и
	// проглоченная ошибка означала бы, что стена сообщила «сохранено», а файл
	// остался прежним.
	if err := f.Close(); err != nil {
		return fmt.Errorf("cannot close settings file: %w", err)
	}
	return nil
}

// wallFilterSection — имя секции фильтра в TOML, которым пользуется запись.
// Разбор берёт то же имя из тега у wallFilterRaw (тег обязан быть литералом, и
// связать его с константой нельзя), поэтому совпадение имён проверяет тест
// «сохранённый фильтр читается обратно» — расхождение здесь сразу его роняет.
const wallFilterSection = "wall_filter"

// wallFilterDocument — секция фильтра для записи, в виде карты. Значения
// обычные, а не структура WallFilter: кодировать надо вместе с чужими ключами
// файла, и структура здесь была бы единственным местом, где тип не совпадает с
// тем, что лежит в карте.
//
// Все четыре флаза пишутся явно, а не «только отличившиеся от дефолта»: файл
// должен читаться однозначно, а правило «пустое = дефолт» (см. LoadSettings)
// остаётся в силе для файлов, написанных руками или прошлой версией.
func wallFilterDocument(filter WallFilter) map[string]interface{} {
	return map[string]interface{}{
		"show_channels": filter.ShowChannels,
		"show_chats":    filter.ShowChats,
		"show_personal": filter.ShowPersonal,
		"show_muted":    filter.ShowMuted,
		"folders":       int32sToInt64s(filter.Folders),
	}
}

// int32sToInt64s — id папок в том виде, в каком их пишет кодировщик TOML. Отдельная
// функция ради одного места: []int32 в []interface{} внутри карты не пишется, и
// подмена типа молча уронила бы запись в ошибку кодировщика.
func int32sToInt64s(values []int32) []int64 {
	converted := make([]int64, 0, len(values))
	for _, value := range values {
		converted = append(converted, int64(value))
	}
	return converted
}
