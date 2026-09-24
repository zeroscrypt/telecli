package tui

import (
	"errors"
	"os"
	"os/exec"
	"time"
)

// voiceFileReadinessPoll и voiceFileReadinessBudget — параметры ожидания
// готовности файла на диске перед запуском плеера (см. waitForVoiceFileReady).
// Переменные, не константы — тесты подменяют на миллисекундные значения.
var voiceFileReadinessPoll = 100 * time.Millisecond
var voiceFileReadinessBudget = 3 * time.Second

// waitForVoiceFileReady — дополнительная best-effort проверка локальной копии:
// перед запуском плеера os.Stat должен увидеть ожидаемый размер. Исходники
// TDLib 1.8.67 уже гарантируют, что is_downloading_completed означает полностью
// доступную копию и нормальный downloader закрывает файл до этого состояния,
// поэтому проверка нужна только как защита от повреждённого или удалённого кэша.
// expectedSize <= 0 или истёкший бюджет не блокируют воспроизведение.
func waitForVoiceFileReady(path string, expectedSize int64) {
	if expectedSize <= 0 {
		return
	}
	deadline := time.Now().Add(voiceFileReadinessBudget)
	for {
		if info, err := os.Stat(path); err == nil && info.Size() >= expectedSize {
			return
		}
		if !time.Now().Before(deadline) {
			return
		}
		time.Sleep(voiceFileReadinessPoll)
	}
}

// resolvePlayer определяет, какой внешний плеер запустить для аудиофайла
// голосового: afplay (macOS, всегда есть), ffplay (fallback Linux), затем mpv.
// Порядок осознанный: ffplay идёт первым среди Linux-плееров — он чаще
// установлен как зависимость ffmpeg. resolvePlayer вынесена с параметром goos
// для юнит-теста (goos подставить любую без трассы до runtime.GOOS).
var findPlayer = exec.LookPath

func resolvePlayer(goos string) (name string, args []string, err error) {
	switch goos {
	case "darwin":
		return "afplay", nil, nil
	default:
		if p, err := findPlayer("ffplay"); err == nil {
			return p, []string{"-nodisp", "-autoexit", "-loglevel", "quiet"}, nil
		}
		if p, err := findPlayer("mpv"); err == nil {
			return p, nil, nil
		}
		return "", nil, errors.New("плеер не найден: нужен afplay (macOS) или ffplay/mpv (Linux)")
	}
}
