// Package mediaplayer — выбор внешнего плеера для аудиофайлов Telegram.
//
// Правило одно на оба TUI проекта: раньше оно было продублировано в
// internal/tui/player.go и internal/tgclitui/media.go, и правки одной копии
// разъезжались со второй. Здесь только выбор команды; запуск, остановка и
// ожидание остаются на стороне вызывающего.
package mediaplayer

import "errors"

// ErrNotFound — ни один подходящий плеер не найден в системе.
var ErrNotFound = errors.New("плеер не найден: нужен afplay (macOS) или ffplay/mpv (Linux)")

// Resolve возвращает команду внешнего плеера для текущей ОС.
//
// На macOS это afplay: он есть в системе всегда и не требует ничего ставить. На
// остальных платформах сначала ffplay (чаще встречается как зависимость ffmpeg),
// затем mpv. lookPath передаётся параметром, чтобы тест подставлял поиск и не
// зависел от того, что установлено на машине.
func Resolve(goos string, lookPath func(string) (string, error)) (name string, args []string, err error) {
	if goos == "darwin" {
		return "afplay", nil, nil
	}
	if path, err := lookPath("ffplay"); err == nil {
		return path, []string{"-nodisp", "-autoexit", "-loglevel", "quiet"}, nil
	}
	if path, err := lookPath("mpv"); err == nil {
		return path, nil, nil
	}
	return "", nil, ErrNotFound
}
