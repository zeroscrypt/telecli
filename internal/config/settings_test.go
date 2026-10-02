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

	writeSettingsFile(t, "align_own_right = false")

	settings, err := LoadSettings()
	require.NoError(t, err)

	// Заданное поле переопределяет дефолт; незаданные (в т.ч. будущие)
	// поля остаются на дефолтных значениях — структура ожидания строится от
	// DefaultSettings, и при добавлении нового поля этот тест автоматически
	// покрыл бы и его (аналогично partial override в keybindings_test.go).
	want := DefaultSettings()
	want.AlignOwnRight = false
	require.Equal(t, want, settings)
}

func TestLoadSettingsThemeOverride(t *testing.T) {
	_, cleanup := setupSettingsTest(t)
	defer cleanup()

	writeSettingsFile(t, "theme = \"yellow\"")

	settings, err := LoadSettings()
	require.NoError(t, err)

	want := DefaultSettings()
	want.Theme = "yellow"
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

	writeSettingsFile(t, "align_own_right = [\nnot valid toml")

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

// Фильтр стены в settings.toml. Отдельный набор проверок от остальных настроек
// по одной причине: у него четыре булева поля, три из которых по умолчанию
// включены. Именно поэтому чтение идёт через *bool (см. wallFilterRaw) — и
// именно на этом проверки ниже и ломаются, если кто-то «упростит» разбор до
// обычного bool.

// TestLoadSettingsWallFilterDefaults — без секции [wall_filter] фильтр остаётся
// умолчанием, то есть стена показывает всё, что показывала до появления фильтра.
func TestLoadSettingsWallFilterDefaults(t *testing.T) {
	_, cleanup := setupSettingsTest(t)
	defer cleanup()

	settings, err := LoadSettings()
	require.NoError(t, err)
	require.Equal(t, DefaultWallFilter(), settings.WallFilter)
	require.True(t, settings.WallFilter.ShowChannels)
	require.True(t, settings.WallFilter.ShowChats)
	require.True(t, settings.WallFilter.ShowPersonal)
	require.False(t, settings.WallFilter.ShowMuted)
	require.Empty(t, settings.WallFilter.Folders)
}

// TestLoadSettingsWallFilterSection — заданная секция перекрывает дефолт целиком, а
// незаданные внутри неё поля остаются дефолтными.
func TestLoadSettingsWallFilterSection(t *testing.T) {
	_, cleanup := setupSettingsTest(t)
	defer cleanup()

	writeSettingsFile(t, `
[wall_filter]
show_channels = false
show_muted = true
folders = [7, 3]
`)

	settings, err := LoadSettings()
	require.NoError(t, err)
	require.False(t, settings.WallFilter.ShowChannels)
	require.True(t, settings.WallFilter.ShowMuted)
	// Не заданные внутри секции поля остаются дефолтными.
	require.True(t, settings.WallFilter.ShowChats)
	require.True(t, settings.WallFilter.ShowPersonal)
	// Порядок в файле сохраняется как есть: при записи его упорядочивает сам
	// фильтр стены, а чтение чужих рук сортировкой не занимается.
	require.Equal(t, []int32{7, 3}, settings.WallFilter.Folders)
}

// TestLoadSettingsWallFilterExplicitFalse — главная тонкость этого поля: у
// show_channels умолчание «включено», а у обычного bool zero-value не отличить от
// «явно выключено в файле». Файл пишет false, и фильтр обязан это уважать, а не
// молча подставить дефолт.
func TestLoadSettingsWallFilterExplicitFalse(t *testing.T) {
	_, cleanup := setupSettingsTest(t)
	defer cleanup()

	writeSettingsFile(t, `
[wall_filter]
show_channels = false
show_chats = false
show_personal = false
show_muted = false
`)

	settings, err := LoadSettings()
	require.NoError(t, err)
	require.Equal(t, WallFilter{}, settings.WallFilter)
}

// TestSaveWallFilterRoundTrip — сохранённый фильтр читается обратно тем же
// значением. Без этого персистентность была бы фикцией: записать можно одно, а
// прочитать — другое.
func TestSaveWallFilterRoundTrip(t *testing.T) {
	_, cleanup := setupSettingsTest(t)
	defer cleanup()

	want := WallFilter{ShowChannels: true, ShowChats: false, ShowPersonal: true, ShowMuted: true, Folders: []int32{3, 7}}
	require.NoError(t, SaveWallFilter(want))

	settings, err := LoadSettings()
	require.NoError(t, err)
	require.Equal(t, want, settings.WallFilter)
}

// TestSaveWallFilterKeepsOtherSettings — сохранение фильтра не затирает остальное
// settings.toml: файл читается заново и мержится, а не пересобирается с нуля.
func TestSaveWallFilterKeepsOtherSettings(t *testing.T) {
	_, cleanup := setupSettingsTest(t)
	defer cleanup()

	writeSettingsFile(t, `
theme = "yellow"
align_own_right = false

[log]
max_lines = 42
level = "warning"
`)

	require.NoError(t, SaveWallFilter(WallFilter{ShowChannels: true, ShowChats: true, ShowPersonal: true, Folders: []int32{5}}))

	settings, err := LoadSettings()
	require.NoError(t, err)
	require.Equal(t, "yellow", settings.Theme)
	require.False(t, settings.AlignOwnRight)
	require.Equal(t, 42, settings.Log.MaxLines)
	require.Equal(t, "warning", settings.Log.Level)
	require.Equal(t, []int32{5}, settings.WallFilter.Folders)
}

// TestSaveWallFilterPreservesUnknownFields — поле, о котором эта версия программы
// не знает, обязано пережить перезапись: пользователь дописал его руками, и молча
// стереть его — значит потерять его настройку.
func TestSaveWallFilterPreservesUnknownFields(t *testing.T) {
	_, cleanup := setupSettingsTest(t)
	defer cleanup()

	// Ключи в кавычках: TOML не пускает не-ASCII в голых ключах.
	writeSettingsFile(t, "theme = \"yellow\"\n\n[\"будущее\"]\n\"значение\" = 7\n")

	require.NoError(t, SaveWallFilter(DefaultWallFilter()))

	path, err := settingsPath()
	require.NoError(t, err)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(data), "[\"будущее\"]")
	require.Contains(t, string(data), "\"значение\" = 7")
	require.Contains(t, string(data), `theme = "yellow"`)
}

// TestSaveWallFilterWritesPrivateFile — файл настроек создаётся с правами 0600, как
// и конфигурация с секретами: каталог принадлежит пользователю, и оставлять в нём
// общедоступный файл неправильно (см. saveToFallback).
func TestSaveWallFilterWritesPrivateFile(t *testing.T) {
	_, cleanup := setupSettingsTest(t)
	defer cleanup()

	require.NoError(t, SaveWallFilter(DefaultWallFilter()))

	path, err := settingsPath()
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

// TestSaveWallFilterFailsOnUnwritablePath — ошибка записи возвращается, а не
// проглатывается: вызывающая сторона решает по ней, что делать (стена, где ошибку
// показать негде, молчит осознанно, но молчание выбирается там, а не здесь).
func TestSaveWallFilterFailsOnUnwritablePath(t *testing.T) {
	tmpDir := t.TempDir()
	// Каталог на месте файла: запись в него обязана упасть.
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "settings.toml"), 0700))
	SetSettingsPathForTest(filepath.Join(tmpDir, "settings.toml"))
	defer SetSettingsPathForTest("")

	require.Error(t, SaveWallFilter(DefaultWallFilter()))
}
