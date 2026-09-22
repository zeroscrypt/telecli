package auth

// Folder — папка чатов Telegram (подмножество полей chatFolderInfo, нужное TUI).
type Folder struct {
	ID   int32
	Name string
}

// ParseChatFoldersUpdate разбирает updateChatFolders в список Folder. Если
// @type не совпадает или chat_folders отсутствует/битое — ok == false, без
// паники (тот же контракт, что у ParseNewMessageUpdate из задачи 0008).
func ParseChatFoldersUpdate(update map[string]interface{}) (folders []Folder, ok bool) {
	if update["@type"] != "updateChatFolders" {
		return nil, false
	}
	rawFolders, listOk := update["chat_folders"].([]interface{})
	if !listOk {
		return nil, false
	}
	result := make([]Folder, 0, len(rawFolders))
	for _, raw := range rawFolders {
		folderMap, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		idFloat, idOk := folderMap["id"].(float64)
		if !idOk {
			continue
		}
		name := ""
		if nameMap, ok := folderMap["name"].(map[string]interface{}); ok {
			if textMap, ok := nameMap["text"].(map[string]interface{}); ok {
				name, _ = textMap["text"].(string)
			}
		}
		result = append(result, Folder{ID: int32(idFloat), Name: name})
	}
	return result, true
}
