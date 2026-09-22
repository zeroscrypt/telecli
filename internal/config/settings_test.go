package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func setupSettingsTest(t *testing.T) (string, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	settingsFile := filepath.Join(tmpDir, "settings.toml")
	SetSettingsPathForTest(settingsFile)

	cleanup := func() {
		SetSettingsPathForTest("")
	}

	return tmpDir, cleanup
}

func TestLoadSettingsNoFileReturnsDefaults(t *testing.T) {
	_, cleanup := setupSettingsTest(t)
	defer cleanup()

	settings, err := LoadSettings()
	require.NoError(t, err)
	require.Equal(t, DefaultSettings(), settings)
}

func TestLoadSettingsPartialOverride(t *testing.T) {
	_, cleanup := setupSettingsTest(t)
	defer cleanup()

	writeSettingsFile(t, "editor = \"nano\"")

	settings, err := LoadSettings()
	require.NoError(t, err)

	// Заданный editor переопределяет дефолт; незаданные (в т.ч. будущие)
	// поля остаются на дефолтных значениях — структура ожидания строится от
	// DefaultSettings, и при добавлении нового поля этот тест автоматически
	// покрыл бы и его (аналогично partial override в keybindings_test.go).
	want := DefaultSettings()
	want.Editor = "nano"
	require.Equal(t, want, settings)
}

func TestLoadSettingsAlignOwnRightExplicitFalse(t *testing.T) {
	_, cleanup := setupSettingsTest(t)
	defer cleanup()

	writeSettingsFile(t, "align_own_right = false")

	settings, err := LoadSettings()
	require.NoError(t, err)

	want := DefaultSettings()
	want.AlignOwnRight = false
	require.Equal(t, want, settings)
}

func TestLoadSettingsInvalidTOMLReturnsError(t *testing.T) {
	_, cleanup := setupSettingsTest(t)
	defer cleanup()

	writeSettingsFile(t, "editor = [\nnot valid toml")

	_, err := LoadSettings()
	require.Error(t, err)
}

func TestLoadSettingsUnreadableFileReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	dir := filepath.Join(tmpDir, "settings.toml")
	require.NoError(t, os.MkdirAll(dir, 0700))
	SetSettingsPathForTest(dir)
	defer SetSettingsPathForTest("")

	_, err := LoadSettings()
	require.Error(t, err)
}

func writeSettingsFile(t *testing.T, content string) {
	t.Helper()
	path, err := settingsPath()
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
}
