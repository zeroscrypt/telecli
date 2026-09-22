package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Settings — пользовательские настройки приложения (TOML,
// <UserConfigDir()>/telecli/settings.toml, отдельно от секретов и
// keybindings.toml — разные файлы, разная семантика). Задел на будущее:
// сюда добавляются новые поля по мере появления новых настраиваемых опций,
// не создавая для каждой новый файл.
type Settings struct {
	Editor        string `toml:"editor"` // пусто = не задано явно, см. DefaultSettings/приоритет ниже
	AlignOwnRight bool   `toml:"-"`      // выставляется в LoadSettings, не напрямую из TOML (см. ниже)
}

func DefaultSettings() Settings {
	return Settings{Editor: "", AlignOwnRight: true}
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
// нельзя мержить по правилу "пусто = не задано" (как Editor string): zero-value
// булева — false, неотличимо от "явно выключено в файле", поэтому парсинг идёт
// через *bool, а наружу отдаётся обычный bool (см. LoadSettings).
type settingsFile struct {
	Editor        string `toml:"editor"`
	AlignOwnRight *bool  `toml:"align_own_right"`
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

	if fileSettings.Editor != "" {
		settings.Editor = fileSettings.Editor
	}
	if fileSettings.AlignOwnRight != nil {
		settings.AlignOwnRight = *fileSettings.AlignOwnRight
	}
	return settings, nil
}
