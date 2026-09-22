package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestParseVersionValid(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want [3]int
	}{
		{"v1.2.3", [3]int{1, 2, 3}},
		{"1.2.3", [3]int{1, 2, 3}},
		{"v10.20.30", [3]int{10, 20, 30}},
	} {
		got, ok := ParseVersion(tc.in)
		if !ok {
			t.Errorf("ParseVersion(%q): ok=false, want true", tc.in)
			continue
		}
		if got != tc.want {
			t.Errorf("ParseVersion(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestParseVersionInvalid(t *testing.T) {
	for _, in := range []string{"v1.2", "v1.2.x", "", "v1", "v1.2.3.4", "abc", "v1..2"} {
		if _, ok := ParseVersion(in); ok {
			t.Errorf("ParseVersion(%q): ok=true, want false", in)
		}
	}
}

func TestIsNewerTrue(t *testing.T) {
	for _, tc := range []struct{ current, latest string }{
		{"v1.2.3", "v1.2.4"},
		{"v1.2.3", "v1.3.0"},
		{"v1.2.3", "v2.0.0"},
		{"v1.2.9", "v1.3.0"},
	} {
		if !IsNewer(tc.current, tc.latest) {
			t.Errorf("IsNewer(%q, %q) = false, want true", tc.current, tc.latest)
		}
	}
}

func TestIsNewerFalseEqualOrOlder(t *testing.T) {
	for _, tc := range []struct{ current, latest string }{
		{"v1.2.3", "v1.2.3"},
		{"v1.2.3", "v1.2.2"},
		{"v1.2.3", "v1.2.0"},
		{"v2.0.0", "v1.9.9"},
	} {
		if IsNewer(tc.current, tc.latest) {
			t.Errorf("IsNewer(%q, %q) = true, want false", tc.current, tc.latest)
		}
	}
}

func TestIsNewerDevNeverOutdated(t *testing.T) {
	if IsNewer("dev", "v99.0.0") {
		t.Fatal("IsNewer(dev, v99.0.0) = true, want false (dev build is never outdated)")
	}
	// Любая непарсящаяся версия тоже не считается устаревшей.
	if IsNewer("dev", "not-a-version") {
		t.Fatal("IsNewer(dev, not-a-version) = true, want false")
	}
	if IsNewer("v1.2.3", "not-a-version") {
		t.Fatal("IsNewer(v1.2.3, not-a-version) = true, want false")
	}
}

func TestCheckLatestSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"tag_name": "v1.2.3", "html_url": "https://example.com"}`))
	}))
	defer srv.Close()

	SetAPIURLForTest(srv.URL)
	defer SetAPIURLForTest("")

	rel, err := CheckLatest(context.Background(), srv.Client(), 5*time.Second)
	if err != nil {
		t.Fatalf("CheckLatest returned error: %v", err)
	}
	if rel.TagName != "v1.2.3" || rel.HTMLURL != "https://example.com" {
		t.Errorf("unexpected release: %+v", rel)
	}
}

func TestCheckLatestNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	SetAPIURLForTest(srv.URL)
	defer SetAPIURLForTest("")

	if _, err := CheckLatest(context.Background(), srv.Client(), 5*time.Second); err == nil {
		t.Fatal("expected error on 404, got nil")
	}
}

func TestCheckLatestInvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("не json"))
	}))
	defer srv.Close()

	SetAPIURLForTest(srv.URL)
	defer SetAPIURLForTest("")

	if _, err := CheckLatest(context.Background(), srv.Client(), 5*time.Second); err == nil {
		t.Fatal("expected error on invalid JSON, got nil")
	}
}

func TestCheckLatestTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
	}))
	defer srv.Close()

	SetAPIURLForTest(srv.URL)
	defer SetAPIURLForTest("")

	_, err := CheckLatest(context.Background(), srv.Client(), 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected error on timeout, got nil")
	}
	if !strings.Contains(err.Error(), "deadline exceeded") && !strings.Contains(err.Error(), "context deadline") {
		t.Errorf("expected deadline-exceeded error, got: %v", err)
	}
}
