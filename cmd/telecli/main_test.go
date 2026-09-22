package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withStdin подменяет os.Stdin на время теста и возвращает функцию восстановления.
func withStdin(t *testing.T, f *os.File) {
	t.Helper()
	orig := os.Stdin
	os.Stdin = f
	t.Cleanup(func() {
		os.Stdin = orig
	})
}

func TestReadStdinIfPipedReadsFileContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "msg.txt")
	content := "текст из пайпа\n"
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer f.Close()
	withStdin(t, f)

	got, err := readStdinIfPiped()
	if err != nil {
		t.Fatalf("readStdinIfPiped failed: %v", err)
	}
	if got != "текст из пайпа" {
		t.Errorf("expected content with trailing newline trimmed, got %q", got)
	}
}

func TestReadStdinIfPipedEmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, []byte(""), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer f.Close()
	withStdin(t, f)

	got, err := readStdinIfPiped()
	if err != nil {
		t.Fatalf("readStdinIfPiped failed: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestReadStdinIfPipedCharDeviceReturnsEmpty(t *testing.T) {
	// /dev/null — символьное устройство (то же, что интерактивный терминал по
	// признаку os.ModeCharDevice) — читать нечего, должен вернуться пустой вывод.
	f, err := os.Open("/dev/null")
	if err != nil {
		t.Fatalf("Open /dev/null failed: %v", err)
	}
	defer f.Close()
	withStdin(t, f)

	got, err := readStdinIfPiped()
	if err != nil {
		t.Fatalf("readStdinIfPiped failed: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string for char device, got %q", got)
	}
}

func TestValidateSendInputRequiresAtLeastOneSource(t *testing.T) {
	if err := validateSendInput("", ""); err == nil {
		t.Fatal("expected error when both -m and -f are empty")
	}
	if err := validateSendInput("текст", ""); err != nil {
		t.Errorf("expected nil error for -m only, got %v", err)
	}
}

func TestValidateSendInputMissingFile(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(existing, []byte("x"), 0600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	if err := validateSendInput("подпись", existing); err != nil {
		t.Errorf("expected nil error for existing file, got %v", err)
	}
	if err := validateSendInput("", existing); err != nil {
		t.Errorf("expected nil error for existing file without -m, got %v", err)
	}

	missing := filepath.Join(dir, "nope.txt")
	err := validateSendInput("подпись", missing)
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "файл недоступен") {
		t.Errorf("unexpected error: %v", err)
	}
}
