package update

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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

func TestAssetNameForDarwinArm64(t *testing.T) {
	name, ok := assetNameFor("darwin", "arm64")
	if !ok {
		t.Fatal("assetNameFor(darwin, arm64) = false, want true")
	}
	if name != "telecli-darwin-arm64" {
		t.Errorf("assetNameFor(darwin, arm64) = %q, want %q", name, "telecli-darwin-arm64")
	}
}

func TestAssetNameForLinuxAmd64(t *testing.T) {
	name, ok := assetNameFor("linux", "amd64")
	if !ok {
		t.Fatal("assetNameFor(linux, amd64) = false, want true")
	}
	if name != "telecli-linux-amd64" {
		t.Errorf("assetNameFor(linux, amd64) = %q, want %q", name, "telecli-linux-amd64")
	}
}

func TestAssetNameForUnsupportedPlatform(t *testing.T) {
	for _, tc := range []struct{ goos, goarch string }{
		{"windows", "amd64"},
		{"darwin", "amd64"},
		{"linux", "arm64"},
		{"freebsd", "amd64"},
	} {
		_, ok := assetNameFor(tc.goos, tc.goarch)
		if ok {
			t.Errorf("assetNameFor(%q, %q) = true, want false", tc.goos, tc.goarch)
		}
	}
}

func TestFindAssetFound(t *testing.T) {
	rel := Release{
		Assets: []Asset{
			{Name: "telecli-darwin-arm64", BrowserDownloadURL: "https://example.com/asset1"},
			{Name: "telecli-linux-amd64", BrowserDownloadURL: "https://example.com/asset2"},
		},
	}
	asset, ok := FindAsset(rel, "telecli-linux-amd64")
	if !ok {
		t.Fatal("FindAsset(rel, telecli-linux-amd64) = false, want true")
	}
	if asset.BrowserDownloadURL != "https://example.com/asset2" {
		t.Errorf("FindAsset returned wrong asset: %+v", asset)
	}
}

func TestFindAssetNotFound(t *testing.T) {
	rel := Release{
		Assets: []Asset{
			{Name: "telecli-darwin-arm64", BrowserDownloadURL: "https://example.com/asset1"},
		},
	}
	_, ok := FindAsset(rel, "telecli-linux-amd64")
	if ok {
		t.Fatal("FindAsset(rel, telecli-linux-amd64) = true, want false (asset not in list)")
	}
}

func TestDownloadBinarySuccess(t *testing.T) {
	fakeBinary := bytes.Repeat([]byte("x"), 2<<20) // 2 MiB > minBinarySize
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(fakeBinary)
	}))
	defer srv.Close()

	data, err := DownloadBinary(context.Background(), srv.Client(), srv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("DownloadBinary returned error: %v", err)
	}
	if len(data) != len(fakeBinary) {
		t.Errorf("DownloadBinary returned %d bytes, want %d", len(data), len(fakeBinary))
	}
}

func TestDownloadBinaryNon200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := DownloadBinary(context.Background(), srv.Client(), srv.URL, 5*time.Second)
	if err == nil {
		t.Fatal("expected error on 404, got nil")
	}
	if !strings.Contains(err.Error(), "неожиданный статус ответа: 404") {
		t.Errorf("expected status error, got: %v", err)
	}
}

func TestDownloadBinaryTooSmall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("<html>error</html>"))
	}))
	defer srv.Close()

	_, err := DownloadBinary(context.Background(), srv.Client(), srv.URL, 5*time.Second)
	if err == nil {
		t.Fatal("expected error on too small file, got nil")
	}
	if !strings.Contains(err.Error(), "подозрительно маленький") {
		t.Errorf("expected size error, got: %v", err)
	}
}

func TestDownloadBinaryRedirect(t *testing.T) {
	fakeBinary := bytes.Repeat([]byte("x"), 2<<20)
	assetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		w.Write(fakeBinary)
	}))
	defer assetSrv.Close()

	redirectSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, assetSrv.URL, http.StatusFound)
	}))
	defer redirectSrv.Close()

	data, err := DownloadBinary(context.Background(), redirectSrv.Client(), redirectSrv.URL, 5*time.Second)
	if err != nil {
		t.Fatalf("DownloadBinary with redirect returned error: %v", err)
	}
	if len(data) != len(fakeBinary) {
		t.Errorf("DownloadBinary with redirect returned %d bytes, want %d", len(data), len(fakeBinary))
	}
}

func TestInstallBinarySuccess(t *testing.T) {
	tmpDir := t.TempDir()
	execPath := filepath.Join(tmpDir, "telecli")

	// Создаём фейковый "старый" бинарник
	oldData := bytes.Repeat([]byte("old"), 2<<20)
	if err := os.WriteFile(execPath, oldData, 0o755); err != nil {
		t.Fatalf("setup: write old binary: %v", err)
	}

	// Новые данные для установки
	newData := bytes.Repeat([]byte("new"), 2<<20)

	err := InstallBinary(newData, execPath)
	if err != nil {
		t.Fatalf("InstallBinary returned error: %v", err)
	}

	// Проверяем, что файл заменён
	installed, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if !bytes.Equal(installed, newData) {
		t.Error("installed binary content does not match new data")
	}

	// Проверяем права 0755
	info, err := os.Stat(execPath)
	if err != nil {
		t.Fatalf("stat installed binary: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("installed binary permissions = %o, want 0755", info.Mode().Perm())
	}

	// Проверяем, что временных файлов не осталось
	entries, err := os.ReadDir(tmpDir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".telecli-update-") {
			t.Errorf("leftover temp file: %s", e.Name())
		}
	}
}

func TestInstallBinaryNoWritePermission(t *testing.T) {
	tmpDir := t.TempDir()
	execPath := filepath.Join(tmpDir, "telecli")

	// Создаём фейковый "старый" бинарник
	oldData := bytes.Repeat([]byte("old"), 2<<20)
	if err := os.WriteFile(execPath, oldData, 0o755); err != nil {
		t.Fatalf("setup: write old binary: %v", err)
	}

	// Убираем права на запись в директорию
	if err := os.Chmod(tmpDir, 0o500); err != nil {
		t.Fatalf("chmod dir: %v", err)
	}
	// ВАЖНО: восстанавливаем права перед cleanup, иначе t.TempDir() не удалится
	t.Cleanup(func() {
		os.Chmod(tmpDir, 0o700)
	})

	newData := bytes.Repeat([]byte("new"), 2<<20)
	err := InstallBinary(newData, execPath)
	if err == nil {
		t.Fatal("expected error on no write permission, got nil")
	}
	if !strings.Contains(err.Error(), "нет доступа на запись") {
		t.Errorf("expected write permission error, got: %v", err)
	}

	// Проверяем, что оригинальный файл НЕ изменился
	installed, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("read original binary after failed install: %v", err)
	}
	if !bytes.Equal(installed, oldData) {
		t.Error("original binary was modified despite error")
	}
}

func TestInstallBinaryCreatesExecutable(t *testing.T) {
	tmpDir := t.TempDir()
	execPath := filepath.Join(tmpDir, "telecli")

	newData := bytes.Repeat([]byte("new"), 2<<20)
	err := InstallBinary(newData, execPath)
	if err != nil {
		t.Fatalf("InstallBinary returned error: %v", err)
	}

	// Проверяем, что файл создан и исполнимый
	info, err := os.Stat(execPath)
	if err != nil {
		t.Fatalf("stat new binary: %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Errorf("new binary permissions = %o, want 0755", info.Mode().Perm())
	}
}
