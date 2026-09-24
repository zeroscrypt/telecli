package auth

import (
	"context"
	"fmt"
	"time"
)

var fileDownloadPollInterval = 200 * time.Millisecond

// WaitForFileDownload запрашивает скачивание файла fileID (downloadFile) и
// блокируется, пока у него не появится готовый локальный путь (local.path +
// is_downloading_completed=true). Асинхронный downloadFile возвращает текущее
// состояние сразу после запуска загрузки, поэтому дальнейшую готовность
// проверяем офлайн-методом getFile: updateFile — общий push-канал с
// переполнением, и параллельные ожидания конкурировали бы за отдельные события.
func WaitForFileDownload(ctx context.Context, client TDClientInterface, fileID int32) (string, error) {
	resp, err := client.Send(ctx, map[string]interface{}{
		"@type":       "downloadFile",
		"file_id":     fileID,
		"priority":    1,
		"offset":      0,
		"limit":       0,
		"synchronous": false,
	})
	if err != nil {
		return "", fmt.Errorf("downloadFile failed: %w", err)
	}
	if resp["@type"] != "file" {
		return "", fmt.Errorf("unexpected response type: %v", resp["@type"])
	}
	if path, ok := downloadedPath(resp); ok {
		return path, nil
	}

	ticker := time.NewTicker(fileDownloadPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			resp, err := client.Send(ctx, map[string]interface{}{
				"@type":   "getFile",
				"file_id": fileID,
			})
			if err != nil {
				return "", fmt.Errorf("getFile failed: %w", err)
			}
			if resp["@type"] != "file" {
				return "", fmt.Errorf("unexpected response type: %v", resp["@type"])
			}
			if path, ok := downloadedPath(resp); ok {
				return path, nil
			}
		case <-ctx.Done():
			return "", fmt.Errorf("не дождались готового файла: %w", ctx.Err())
		}
	}
}

// downloadedPath возвращает локальный путь из файла, если скачивание
// завершено и путь непустой. Схема (сверена с td_api.h): file.local =
// localFile, у него поля path / is_downloading_completed.
func downloadedPath(fileObj map[string]interface{}) (string, bool) {
	local, ok := fileObj["local"].(map[string]interface{})
	if !ok {
		return "", false
	}
	completed, _ := local["is_downloading_completed"].(bool)
	path, _ := local["path"].(string)
	if !completed || path == "" {
		return "", false
	}
	return path, true
}
