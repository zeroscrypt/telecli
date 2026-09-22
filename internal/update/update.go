package update

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Release — минимальный набор полей ответа GitHub API
// GET /repos/{owner}/{repo}/releases/latest, которые нужны telecli.
type Release struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
}

// apiURLOverride — для тестов, тот же паттерн, что SetKeyBindingsPathForTest/
// SetSettingsPathForTest в internal/config: подменяемый URL вместо реального
// api.github.com, чтобы тесты не делали настоящих сетевых запросов.
var apiURLOverride string

func SetAPIURLForTest(url string) {
	apiURLOverride = url
}

func apiURL() string {
	if apiURLOverride != "" {
		return apiURLOverride
	}
	return "https://api.github.com/repos/zeroscrypt/telecli/releases/latest"
}

// CheckLatest запрашивает последний релиз telecli на GitHub. Любая ошибка
// (сеть, таймаут, неожиданный статус, битый JSON) — обычная возвращаемая
// ошибка, вызывающий код (internal/tui) решает, как её показать (обычно —
// молча игнорировать при фоновой проверке, показать статус при явном
// :update — см. п.3 ниже).
func CheckLatest(ctx context.Context, client *http.Client, timeout time.Duration) (Release, error) {
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, apiURL(), nil)
	if err != nil {
		return Release{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := client.Do(req)
	if err != nil {
		return Release{}, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Release{}, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return Release{}, fmt.Errorf("decode response: %w", err)
	}
	return rel, nil
}

// ParseVersion разбирает "vX.Y.Z" (или "X.Y.Z") в [X,Y,Z]. Другие форматы
// (пре-релизы вида "v1.2.3-rc1", неполные версии) не поддерживаются — этого
// достаточно для простого семвера, которым помечаются релизы telecli; ok=false
// для всего, что не парсится.
func ParseVersion(v string) (parts [3]int, ok bool) {
	v = strings.TrimPrefix(v, "v")
	segs := strings.Split(v, ".")
	if len(segs) != 3 {
		return parts, false
	}
	for i, s := range segs {
		n, err := strconv.Atoi(s)
		if err != nil {
			return [3]int{}, false
		}
		parts[i] = n
	}
	return parts, true
}

// IsNewer — true, если latest строго новее current. "dev" (версия сборки без
// -ldflags, дефолт для локальной разработки) НИКОГДА не считается устаревшей
// — дев-сборка не привязана к номеру релиза, сравнивать нечего. Любая версия,
// не прошедшая ParseVersion (включая саму "dev" на месте latest — такого не
// бывает, но на всякий случай), тоже даёт false — при сомнении не показываем
// "обновление доступно".
func IsNewer(current, latest string) bool {
	if current == "dev" {
		return false
	}
	cur, ok1 := ParseVersion(current)
	lat, ok2 := ParseVersion(latest)
	if !ok1 || !ok2 {
		return false
	}
	if lat[0] != cur[0] {
		return lat[0] > cur[0]
	}
	if lat[1] != cur[1] {
		return lat[1] > cur[1]
	}
	return lat[2] > cur[2]
}
