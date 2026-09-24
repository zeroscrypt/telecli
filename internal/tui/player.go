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

// waitForVoiceFileReady ждёт, пока файл на диске перестанет расти и достигнет
// ожидаемого размера (expectedSize). Баг из задачи 0042: TDLib выставляет
// local.is_downloading_completed=true, когда файл ещё дописывается на диск, и
// afplay/ffplay открывают обрезанный файл — голосовое обрывается через ~2с.
// Коротким опросом os.Stat сверяем реальный размер с voice_note.voice.size
// перед запуском плеера. best-effort: expectedSize <= 0 (TDLib не сообщил
// размер) или истёкший бюджет не блокируют воспроизведение — в это время
// задержка перед стартом (макс. voiceFileReadinessBudget) всё равно меньше
// реального времени ожидания скачивания, а в норме файл уже готов на первом
// же замере и функция возвращается сразу.
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
