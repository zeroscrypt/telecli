package mediaplayer

import (
	"errors"
	"testing"
)

func TestResolvePicksTheSamePlayerEverywhere(t *testing.T) {
	present := func(names ...string) func(string) (string, error) {
		set := make(map[string]bool, len(names))
		for _, name := range names {
			set[name] = true
		}
		return func(name string) (string, error) {
			if set[name] {
				return "/usr/bin/" + name, nil
			}
			return "", errors.New("not found: " + name)
		}
	}

	for _, test := range []struct {
		name      string
		goos      string
		installed []string
		wantName  string
		wantArgs  []string
		wantErr   bool
	}{
		{
			name: "macOS всегда afplay, без поиска", goos: "darwin",
			wantName: "afplay",
		},
		{
			name: "Linux с ffplay берёт его", goos: "linux",
			installed: []string{"ffplay", "mpv"},
			wantName:  "/usr/bin/ffplay",
			wantArgs:  []string{"-nodisp", "-autoexit", "-loglevel", "quiet"},
		},
		{
			name: "Linux без ffplay берёт mpv", goos: "linux",
			installed: []string{"mpv"},
			wantName:  "/usr/bin/mpv",
		},
		{
			name: "Linux без плееров — понятная ошибка", goos: "linux",
			wantErr: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			name, args, err := Resolve(test.goos, present(test.installed...))
			if test.wantErr {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("err = %v, want ErrNotFound", err)
				}
				if name != "" {
					t.Fatalf("name = %q, want empty on error", name)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve failed: %v", err)
			}
			if name != test.wantName {
				t.Fatalf("name = %q, want %q", name, test.wantName)
			}
			if len(args) != len(test.wantArgs) {
				t.Fatalf("args = %q, want %q", args, test.wantArgs)
			}
			for index := range args {
				if args[index] != test.wantArgs[index] {
					t.Fatalf("args = %q, want %q", args, test.wantArgs)
				}
			}
		})
	}
}
