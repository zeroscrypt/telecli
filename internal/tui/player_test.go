package tui

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// restoreFindPlayer возвращает пакетную переменную findPlayer на место после
// каждого теста — без этого подмена протекала бы между тестами.
func restoreFindPlayer(original func(string) (string, error)) {
	findPlayer = original
}

// restoreVoiceReadinessVars возвращает пакетные переменные опроса готовности
// файла (см. waitForVoiceFileReady) на место после каждого теста.
func restoreVoiceReadinessVars(poll, budget time.Duration) {
	voiceFileReadinessPoll = poll
	voiceFileReadinessBudget = budget
}

func TestResolvePlayerDarwinUsesAfplay(t *testing.T) {
	name, args, err := resolvePlayer("darwin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "afplay" {
		t.Errorf("expected afplay, got %q", name)
	}
	if len(args) != 0 {
		t.Errorf("expected no args for afplay, got %v", args)
	}
}

func TestResolvePlayerLinuxFindsFfplay(t *testing.T) {
	orig := findPlayer
	defer restoreFindPlayer(orig)
	findPlayer = func(name string) (string, error) {
		return "/usr/bin/" + name, nil
	}

	name, args, err := resolvePlayer("linux")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "/usr/bin/ffplay" {
		t.Errorf("expected /usr/bin/ffplay, got %q", name)
	}
	wantArgs := []string{"-nodisp", "-autoexit", "-loglevel", "quiet"}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Errorf("expected args %v, got %v", wantArgs, args)
	}
}

func TestResolvePlayerLinuxFallsBackToMpv(t *testing.T) {
	orig := findPlayer
	defer restoreFindPlayer(orig)
	findPlayer = func(name string) (string, error) {
		switch name {
		case "ffplay":
			return "", errors.New("not found")
		default:
			return "/usr/bin/mpv", nil
		}
	}

	name, args, err := resolvePlayer("linux")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if name != "/usr/bin/mpv" {
		t.Errorf("expected /usr/bin/mpv, got %q", name)
	}
	if len(args) != 0 {
		t.Errorf("expected no args for mpv, got %v", args)
	}
}

func TestResolvePlayerLinuxNoPlayerFound(t *testing.T) {
	orig := findPlayer
	defer restoreFindPlayer(orig)
	findPlayer = func(name string) (string, error) {
		return "", errors.New("not found")
	}

	_, _, err := resolvePlayer("linux")
	if err == nil {
		t.Fatal("expected error when no player found")
	}
	if !strings.Contains(err.Error(), "плеер не найден") {
		t.Errorf("expected error about missing player, got: %v", err)
	}
}

// TestWaitForVoiceFileReadyAlreadyFullSize — файл уже достиг ожидаемого размера:
// функция возвращается сразу, без задержки (обычный случай).
func TestWaitForVoiceFileReadyAlreadyFullSize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "voice.ogg")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 1000)), 0o644); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	waitForVoiceFileReady(path, 1000)
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		t.Errorf("expected immediate return for already-full file, took %v", elapsed)
	}
}

// TestWaitForVoiceFileReadyUnknownSizeSkipsWait — ожидаемый размер неизвестен
// (0): wait не имеет основания для сравнения и не блокирует воспроизведение,
// даже если файла на диске ещё нет.
func TestWaitForVoiceFileReadyUnknownSizeSkipsWait(t *testing.T) {
	path := filepath.Join(t.TempDir(), "voice.ogg") // файла нет — неважно

	start := time.Now()
	waitForVoiceFileReady(path, 0)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("expected immediate return for unknown size, took %v", elapsed)
	}
}

// TestWaitForVoiceFileReadyWaitsForGrowth — проверяет защитный сценарий из
// задачи 0042: путь готов, но локальный размер ещё меньше ожидаемого.
func TestWaitForVoiceFileReadyWaitsForGrowth(t *testing.T) {
	origPoll, origBudget := voiceFileReadinessPoll, voiceFileReadinessBudget
	defer restoreVoiceReadinessVars(origPoll, origBudget)
	voiceFileReadinessPoll = 5 * time.Millisecond
	voiceFileReadinessBudget = time.Second

	path := filepath.Join(t.TempDir(), "voice.ogg")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 100)), 0o644); err != nil {
		t.Fatal(err)
	}

	go func() {
		time.Sleep(40 * time.Millisecond)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Error(err)
			return
		}
		if _, err := f.Write([]byte(strings.Repeat("x", 900))); err != nil {
			t.Error(err)
		}
		f.Close()
	}()

	waitForVoiceFileReady(path, 1000)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 1000 {
		t.Errorf("expected file to reach 1000 bytes before return, got %d", info.Size())
	}
}

// TestWaitForVoiceFileReadyTimesOutBestEffort — файл так и не достигает
// ожидаемого размера: по истечении бюджета return происходит всё равно.
func TestWaitForVoiceFileReadyTimesOutBestEffort(t *testing.T) {
	origPoll, origBudget := voiceFileReadinessPoll, voiceFileReadinessBudget
	defer restoreVoiceReadinessVars(origPoll, origBudget)
	voiceFileReadinessPoll = 5 * time.Millisecond
	voiceFileReadinessBudget = 30 * time.Millisecond

	path := filepath.Join(t.TempDir(), "voice.ogg")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 100)), 0o644); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	waitForVoiceFileReady(path, 100000) // до такого размера файл не дорастёт
	elapsed := time.Since(start)
	if elapsed < 25*time.Millisecond || elapsed > time.Second {
		t.Errorf("expected return near budget (30ms), took %v", elapsed)
	}
}
