package auth

import (
	"context"
	"errors"
	"fmt"
)

// WaitForFileDownload запрашивает скачивание файла fileID (downloadFile) и
// блокируется, пока у него не появится готовый локальный путь (local.path +
// is_downloading_completed=true). Свежеотправленным голосовым TDLib обычно
// отдаёт файл уже скачанным в самом ответе downloadFile — тогда возвращаем
// сразу, не трогая канал. Иначе ждём updateFile по этому file.id. Чужие
// файлы игнорируем. Ответ downloadFile не "file" — ошибка.
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
		return "", err
	}
	if resp["@type"] != "file" {
		return "", fmt.Errorf("unexpected response type: %v", resp["@type"])
	}
	if path, ok := downloadedPath(resp); ok {
		return path, nil
	}

	ch := client.FileUpdates()
	for {
		select {
		case upd, ok := <-ch:
			if !ok {
				return "", errors.New("tdlib: file-updates channel closed before download finished")
			}
			if upd["@type"] != "updateFile" {
				continue
			}
			fileObj, ok := upd["file"].(map[string]interface{})
			if !ok {
				continue
			}
			id, ok := fileObj["id"].(float64)
			if !ok || int32(id) != fileID {
				continue // апдейт про другой файл — не наш, ждём дальше
			}
			if path, ok := downloadedPath(fileObj); ok {
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
