package tui

import (
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// editorFinishedMsg — результат редактирования черновика во внешнем редакторе.
// path — временный файл с черновиком; err — ошибка запуска редактора (или nil).
type editorFinishedMsg struct {
	path string
	err  error
}

// writeDraftTempFile создаёт временный файл с текущим текстом черновика и
// возвращает его путь. Файл остаётся на диске — его читает/удаляет обработчик
// editorFinishedMsg по закрытии редактора.
func writeDraftTempFile(content string) (string, error) {
	f, err := os.CreateTemp("", "telecli-draft-*.txt")
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// readDraftTempFile читает отредактированный черновик. Одной финальной новой
// строки (которую редакторы добавляют при сохранении) не считаем — обрезается.
func readDraftTempFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\n"), nil
}

// resolveEditorCommand возвращает команду редактора (бинарь + аргументы) по
// приоритету: явная настройка (settings.toml, editor=) → переменная окружения
// $EDITOR → фолбэк "vi". Разбор на бинарь+аргументы — strings.Fields (см.
// комментарий в openEditorCmd про "code --wait" и т.п. — та же логика для
// любого источника, не только $EDITOR).
func resolveEditorCommand(settingsEditor string) []string {
	source := settingsEditor
	if source == "" {
		source = os.Getenv("EDITOR")
	}
	parts := strings.Fields(source)
	if len(parts) == 0 {
		parts = []string{"vi"}
	}
	return parts
}

// openEditorCmd открывает редактор (см. resolveEditorCommand — настройка →
// $EDITOR → "vi"; стандартная POSIX-конвенция, доступна и на macOS, и на
// Linux без дополнительной установки) на временном файле с текущим черновиком.
// tea.ExecProcess приостанавливает Program на время работы редактора —
// параллельных апдейтов модели в это время не бывает, поэтому по возврату
// режим гарантированно всё ещё modeInsert, доп. проверка не нужна.
func (m Model) openEditorCmd() tea.Cmd {
	path, err := writeDraftTempFile(m.composeInput.Value())
	if err != nil {
		return func() tea.Msg { return editorFinishedMsg{err: err} }
	}
	// Команду редактора обычно задают с аргументами (например, "code --wait",
	// "subl -n -w") — exec.Command не разбивает строку сам (передал бы "code
	// --wait" как имя одного несуществующего бинаря), поэтому делим по
	// пробелам вручную и дописываем путь к временному файлу последним
	// аргументом.
	parts := resolveEditorCommand(m.settings.Editor)
	args := append(append([]string{}, parts[1:]...), path)
	c := exec.Command(parts[0], args...)
	return tea.ExecProcess(c, func(err error) tea.Msg {
		return editorFinishedMsg{path: path, err: err}
	})
}
