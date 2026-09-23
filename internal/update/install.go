package update

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// minBinarySize — нижняя граница разумного размера скачанного бинарника.
// Статический telecli (TDLib вкомпилирована) весит десятки мегабайт — 1 МиБ
// заведомо меньше любого настоящего бинарника, но достаточно, чтобы поймать
// класс ошибок «скачали HTML-страницу с ошибкой вместо файла».
const minBinarySize = 1 << 20 // 1 MiB

// AssetNameForPlatform возвращает имя ассета релиза для текущей платформы
// (см. матрицу сборки release-binaries.yml — сейчас собираются ровно эти две
// комбинации) и false, если готового бинарника для этой платформы нет.
func AssetNameForPlatform() (string, bool) {
	return assetNameFor(runtime.GOOS, runtime.GOARCH)
}

// assetNameFor — вынесено отдельно от AssetNameForPlatform ради тестируемости
// (runtime.GOOS/GOARCH нельзя подменить в тесте, а эту функцию — можно
// вызвать напрямую с любыми значениями).
func assetNameFor(goos, goarch string) (string, bool) {
	switch {
	case goos == "darwin" && goarch == "arm64":
		return "telecli-darwin-arm64", true
	case goos == "linux" && goarch == "amd64":
		return "telecli-linux-amd64", true
	default:
		return "", false
	}
}

// FindAsset ищет ассет по имени файла среди Assets релиза.
func FindAsset(rel Release, name string) (Asset, bool) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}

// DownloadBinary скачивает тело ассета по прямой ссылке (GitHub отдаёт 302 на
// objects.githubusercontent.com — http.Client следует редиректам сам, ничего
// доп. не нужно). Ограничение размера через http.MaxBytesReader — страховка
// от неограниченного роста памяти, если сервер вернёт что-то неожиданно
// большое; лимит с большим запасом над реальным размером бинарника (см.
// release-binaries.yml — бинарники десятки МиБ).
//
// timeout — на весь запрос целиком (соединение + чтение тела), тот же
// контракт, что у CheckLatest: без него зависшее соединение держало бы
// installingUpdate=true в internal/tui вечно, до перезапуска приложения —
// не найдено ревью, найдено оркестратором отдельно, не было явно расписано
// в файле задачи. Отдельный (больший, чем у CheckLatest) таймаут — тело
// ответа тут десятки МиБ, не одна короткая JSON-запись.
func DownloadBinary(ctx context.Context, client *http.Client, url string, timeout time.Duration) ([]byte, error) {
	const maxDownloadSize = 200 << 20 // 200 MiB — щедрый потолок, не реальный ожидаемый размер

	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("скачивание не удалось: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("неожиданный статус ответа: %d", resp.StatusCode)
	}

	data, err := io.ReadAll(http.MaxBytesReader(nil, resp.Body, maxDownloadSize))
	if err != nil {
		return nil, fmt.Errorf("чтение тела ответа: %w", err)
	}
	if len(data) < minBinarySize {
		return nil, fmt.Errorf("скачанный файл подозрительно маленький (%d байт) — похоже, это не бинарник", len(data))
	}
	return data, nil
}

// InstallBinary атомарно заменяет исполняемый файл по пути execPath на
// содержимое data. Ключевое свойство безопасности: execPath НЕ трогается, пока
// не готов полный, проверенный временный файл — любая ошибка на пути
// (нет места на диске, нет прав) оставляет текущий рабочий бинарник
// нетронутым, процесс, который сейчас выполняется из execPath, не ломается.
//
// Временный файл создаётся В ТОЙ ЖЕ директории, что execPath (не os.TempDir())
// — иначе os.Rename может упасть с EXDEV, если /tmp и целевая директория на
// разных файловых системах; rename внутри одной директории атомарен на POSIX.
//
// Почему безопасно заменять файл, который прямо сейчас выполняется тем же
// процессом: на POSIX (macOS, Linux — единственные поддерживаемые здесь
// платформы, см. AssetNameForPlatform) уже запущенный процесс держит открытым
// СТАРЫЙ inode через собственное отображение в память при exec, а не путь на
// диске — os.Rename меняет, на какой inode ссылается имя файла, не трогая уже
// запущенный процесс. Следующий запуск telecli по этому пути возьмёт новый
// файл. Windows сюда не входит (AssetNameForPlatform никогда не вернёт
// вариант для неё) — там семантика другая и этот приём не работает.
func InstallBinary(data []byte, execPath string) error {
	dir := filepath.Dir(execPath)

	tmp, err := os.CreateTemp(dir, ".telecli-update-*")
	if err != nil {
		return fmt.Errorf("нет доступа на запись в %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	// В любом пути выхода после этой точки временный файл должен быть либо
	// переименован в execPath (успех), либо удалён (любая ошибка) — иначе
	// мусор остаётся в директории с бинарником.
	success := false
	defer func() {
		if !success {
			os.Remove(tmpPath)
		}
	}()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("запись временного файла: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("закрытие временного файла: %w", err)
	}
	if err := os.Chmod(tmpPath, 0o755); err != nil {
		return fmt.Errorf("выставление прав на исполнение: %w", err)
	}
	if err := os.Rename(tmpPath, execPath); err != nil {
		return fmt.Errorf("замена исполняемого файла: %w", err)
	}
	success = true
	return nil
}
