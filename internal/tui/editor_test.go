package tui

import (
	"reflect"
	"testing"
)

// resolveEditorCommand — чистый приоритет источника редактора: явная настройка
// (settings.toml, editor=) важнее $EDITOR, $EDITOR важнее фолбэка "vi".
func TestResolveEditorCommandPrefersSettings(t *testing.T) {
	// $EDITOR задан и в настройках задан — побеждает настройка.
	t.Setenv("EDITOR", "vi")
	got := resolveEditorCommand("nano")
	want := []string{"nano"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveEditorCommand(nano) = %v, want %v", got, want)
	}
}

func TestResolveEditorCommandFallsBackToEnv(t *testing.T) {
	// settings.Editor пуст, $EDITOR задан — побеждает $EDITOR.
	t.Setenv("EDITOR", "vim")
	got := resolveEditorCommand("")
	want := []string{"vim"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveEditorCommand(\"\") = %v, want %v", got, want)
	}
}

func TestResolveEditorCommandFallsBackToVi(t *testing.T) {
	// Оба источника пусты — фолбэк "vi" (POSIX-конвенция). t.Setenv("", ...)
	// невозможен, поэтому выставляем пустую переменную через Setenv("EDITOR", "").
	t.Setenv("EDITOR", "")
	got := resolveEditorCommand("")
	want := []string{"vi"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("resolveEditorCommand(\"\") with empty EDITOR = %v, want %v", got, want)
	}
}

// Команда редактора с аргументами (например, "code --wait", "subl -n -w")
// разбирается на бинарь + аргументы для exec.Command — из любого источника.
func TestResolveEditorCommandSplitsArgs(t *testing.T) {
	t.Setenv("EDITOR", "")
	for _, tc := range []struct {
		settings string
		want     []string
	}{
		{settings: "code --wait", want: []string{"code", "--wait"}},
		{settings: "  nano  ", want: []string{"nano"}},
	} {
		got := resolveEditorCommand(tc.settings)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("resolveEditorCommand(%q) = %v, want %v", tc.settings, got, tc.want)
		}
	}
}
