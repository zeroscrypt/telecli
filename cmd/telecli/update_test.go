package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"telecli/internal/update"
)

// Проверки `telecli update`. Сама команда (runUpdate) печатает в stdout/stderr и
// заменяет бинарник на диске — её не проверяем. Проверяется всё, что от неё
// отделили: planUpdate (решение «качать или нет») и upToDateNotice.
//
// Адрес GitHub подменяется на httptest тем же update.SetAPIURLForTest, что и в
// internal/update: реальный api.github.com в тестах стучаться не должен.

// releaseServer — сервер, отдающий релиз с заданными тегом и ассетами.
func releaseServer(t *testing.T, tag string, assetNames ...string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		release := update.Release{TagName: tag, HTMLURL: "https://example.com/releases/" + tag}
		for _, name := range assetNames {
			release.Assets = append(release.Assets, update.Asset{
				Name:               name,
				BrowserDownloadURL: "https://example.com/download/" + name,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(release); err != nil {
			t.Errorf("encode release: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	update.SetAPIURLForTest(srv.URL)
	t.Cleanup(func() { update.SetAPIURLForTest("") })
	return srv
}

// assetNameHere — имя ассета для машины, на которой идут тесты. Подставляется
// тем же AssetNameForPlatform, что и в planUpdate: проверка «ассет найден»
// бессмысленна, если в тесте подсовывать имя, которого в релизе нет и не
// должно быть.
func assetNameHere(t *testing.T) string {
	t.Helper()
	name, ok := update.AssetNameForPlatform()
	if !ok {
		t.Skipf("платформа без готовых бинарников, проверить поиск ассета не на чем")
	}
	return name
}

// План «качать»: релиз новее текущей версии, ассет этой платформы в релизе есть.
// Проверяются ВСЕ поля плана, а не только UpToDate: пустой AssetURL прошёл бы
// проверку «обновление найдено» и упал бы позже, уже при скачивании.
func TestPlanUpdateDownloadsWhenNewerReleaseExists(t *testing.T) {
	name := assetNameHere(t)
	releaseServer(t, "v9.9.9", name)

	plan, err := planUpdate(context.Background(), http.DefaultClient, "v1.0.0")
	if err != nil {
		t.Fatalf("planUpdate вернул ошибку: %v", err)
	}
	if plan.UpToDate {
		t.Fatal("при релизе v9.9.9 против v1.0.0 сказано, что обновляться не нужно")
	}
	if plan.Version != "v9.9.9" {
		t.Errorf("plan.Version = %q, ждали v9.9.9", plan.Version)
	}
	if plan.AssetURL != "https://example.com/download/"+name {
		t.Errorf("plan.AssetURL = %q, ждали ссылку на ассет %q", plan.AssetURL, name)
	}
	if plan.ReleaseURL == "" {
		t.Error("plan.ReleaseURL пуст — в ошибках нечем будет предложить скачать вручную")
	}
}

// Релиз новее, но ассета под эту платформу нет: это не повод качать чужой
// бинарник. Ошибка обязана называть именно ИСКОМОЕ имя ассета (то, которое
// planUpdate искал, а не то постороннее, что лежит в релизе) и вести на
// страницу релиза — иначе человек остаётся с одной строкой в терминале и не
// понимает, чего именно не хватило.
func TestPlanUpdateFailsWhenAssetForPlatformMissing(t *testing.T) {
	name := assetNameHere(t)
	releaseServer(t, "v9.9.9", "telecli-somewhere-else")

	_, err := planUpdate(context.Background(), http.DefaultClient, "v1.0.0")
	if err == nil {
		t.Fatal("ассета для этой платформы нет, а ошибки не вернулось")
	}
	if !strings.Contains(err.Error(), name) {
		t.Errorf("в ошибке нет имени искомого ассета %q: %v", name, err)
	}
	if !strings.Contains(err.Error(), "https://example.com/releases/v9.9.9") {
		t.Errorf("в ошибке нет ссылки на релиз: %v", err)
	}
}

// Релиз той же или меньшей версии — обновляться нечего. Это НЕ ошибка: команда
// обязана закончиться успешно и сказать об этом в stdout, иначе скрипт,
// вызывающий telecli update, получал бы ненулевой код на штатной ситуации.
func TestPlanUpdateReportsUpToDateWithoutError(t *testing.T) {
	name := assetNameHere(t)
	releaseServer(t, "v1.0.0", name)

	plan, err := planUpdate(context.Background(), http.DefaultClient, "v1.0.0")
	if err != nil {
		t.Fatalf("planUpdate вернул ошибку на равных версиях: %v", err)
	}
	if !plan.UpToDate {
		t.Fatal("версии равны, а сказано, что есть обновление")
	}
	if plan.AssetURL != "" {
		t.Errorf("при UpToDate AssetURL = %q, ждали пусто — качать нечего", plan.AssetURL)
	}
	if plan.Version != "v1.0.0" {
		t.Errorf("plan.Version = %q, ждали текущую версию v1.0.0 для сообщения", plan.Version)
	}
}

// Старая установленная версия при новом релизе — обновление есть.
func TestPlanUpdateDetectsOlderInstalledVersion(t *testing.T) {
	name := assetNameHere(t)
	releaseServer(t, "v1.0.1", name)

	plan, err := planUpdate(context.Background(), http.DefaultClient, "v1.0.0")
	if err != nil {
		t.Fatalf("planUpdate вернул ошибку: %v", err)
	}
	if plan.UpToDate {
		t.Fatal("v1.0.0 при релизе v1.0.1 — обновление есть, а сказано что нечего")
	}
}

// Сбой сети/неожиданный статус от GitHub — обычная возвращаемая ошибка с
// указанием на шаг, который сорвался. Молча проглоченная ошибка здесь читалась
// бы как «у вас последняя версия».
func TestPlanUpdateReportsNetworkFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	update.SetAPIURLForTest(srv.URL)
	defer update.SetAPIURLForTest("")

	_, err := planUpdate(context.Background(), http.DefaultClient, "v1.0.0")
	if err == nil {
		t.Fatal("GitHub ответил 500, а planUpdate вернул nil")
	}
	if !strings.Contains(err.Error(), "проверка обновлений") {
		t.Errorf("в ошибке не назван сорвавшийся шаг: %v", err)
	}
}

// dev-сборка никогда не считается устаревшей — это заложено в
// update.IsNewer, и planUpdate обязан это уважать, а не обходить.
func TestPlanUpdateTreatsDevBuildAsUpToDate(t *testing.T) {
	name := assetNameHere(t)
	releaseServer(t, "v99.0.0", name)

	plan, err := planUpdate(context.Background(), http.DefaultClient, "dev")
	if err != nil {
		t.Fatalf("planUpdate вернул ошибку: %v", err)
	}
	if !plan.UpToDate {
		t.Fatal("dev-сборка объявлена устаревшей — update.IsNewer так не работает")
	}
	if plan.AssetURL != "" {
		t.Errorf("dev-сборка получила ссылку на скачивание: %q", plan.AssetURL)
	}
}

// Сообщение для dev-сборки обязано отличаться от «у вас последняя версия»:
// «dev» — не версия, и называть её последней значило бы утверждать то, чего
// никто не проверял.
func TestUpToDateNoticeDiffersForDevBuild(t *testing.T) {
	devNotice := upToDateNotice("dev")
	if !strings.Contains(devNotice, "dev") {
		t.Errorf("сообщение для dev не упоминает, что это dev-сборка: %q", devNotice)
	}
	if strings.Contains(devNotice, "последняя версия") {
		t.Errorf("dev-сборка названа последней версией: %q", devNotice)
	}

	released := upToDateNotice("v1.2.3")
	if !strings.Contains(released, "v1.2.3") {
		t.Errorf("сообщение не содержит текущей версии: %q", released)
	}
	if !strings.Contains(released, "последняя версия") {
		t.Errorf("сообщение для релиза не говорит, что он последний: %q", released)
	}
	if released == devNotice {
		t.Error("сообщения для dev и для релиза совпали — одно из них врёт")
	}
}

// Путь к заменяемому файлу обязан быть РАЗРЕШЁН: os.Executable() может вернуть
// путь через симлинк, а InstallBinary переименовывает временный файл прямо в
// переданный путь. Без разрешения symlink-раскладка (/usr/local/bin/telecli ->
// /opt/telecli/bin/telecli, как её делают brew и most-дистрибутивы) заменялась
// бы самим симлинком, и настоящий файл остался бы старой версии навсегда.
// Провяется на живом бинарнике тестового процесса: он точно существует, и путь
// к нему проходит через настоящую файловую систему.
func TestExecutablePathResolvesSymlinks(t *testing.T) {
	path, err := executablePath()
	if err != nil {
		t.Fatalf("executablePath вернул ошибку: %v", err)
	}
	if path == "" {
		t.Fatal("executablePath вернул пустой путь")
	}
	if !filepath.IsAbs(path) {
		t.Errorf("путь %q не абсолютный", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("путь из executablePath не разрешается: %v", err)
	}
	if resolved != path {
		t.Errorf("путь из executablePath ещё содержит симлинки: %q (ожидалось %q)", path, resolved)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("путь из executablePath не указывает на существующий файл: %v", err)
	}
}
