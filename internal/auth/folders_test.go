package auth

import (
	"reflect"
	"testing"
)

func TestParseChatFoldersUpdateValid(t *testing.T) {
	update := map[string]interface{}{
		"@type": "updateChatFolders",
		"chat_folders": []interface{}{
			map[string]interface{}{
				"id": 1.0,
				"name": map[string]interface{}{
					"text": map[string]interface{}{"text": "Работа"},
				},
			},
			// Папка с пустым именем не должна ронять парсинг.
			map[string]interface{}{
				"id": 2.0,
				"name": map[string]interface{}{
					"text": map[string]interface{}{"text": ""},
				},
			},
		},
	}

	folders, ok := ParseChatFoldersUpdate(update)
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := []Folder{{ID: 1, Name: "Работа"}, {ID: 2, Name: ""}}
	if !reflect.DeepEqual(folders, want) {
		t.Errorf("unexpected folders: got %#v, want %#v", folders, want)
	}
}

func TestParseChatFoldersUpdateWrongType(t *testing.T) {
	update := map[string]interface{}{
		"@type":        "updateNewMessage",
		"chat_folders": []interface{}{},
	}
	folders, ok := ParseChatFoldersUpdate(update)
	if ok {
		t.Fatal("expected ok=false")
	}
	if folders != nil {
		t.Errorf("expected nil folders, got %#v", folders)
	}
}

func TestParseChatFoldersUpdateMissingList(t *testing.T) {
	update := map[string]interface{}{"@type": "updateChatFolders"}
	folders, ok := ParseChatFoldersUpdate(update)
	if ok {
		t.Fatal("expected ok=false for missing chat_folders")
	}
	if folders != nil {
		t.Errorf("expected nil folders, got %#v", folders)
	}
}

func TestParseChatFoldersUpdateBadListType(t *testing.T) {
	update := map[string]interface{}{
		"@type":        "updateChatFolders",
		"chat_folders": "not a list",
	}
	folders, ok := ParseChatFoldersUpdate(update)
	if ok {
		t.Fatal("expected ok=false for non-list chat_folders")
	}
	if folders != nil {
		t.Errorf("expected nil folders, got %#v", folders)
	}
}

func TestParseChatFoldersUpdateSkipsMalformedEntries(t *testing.T) {
	update := map[string]interface{}{
		"@type": "updateChatFolders",
		"chat_folders": []interface{}{
			map[string]interface{}{
				"id": 1.0,
				"name": map[string]interface{}{
					"text": map[string]interface{}{"text": "Первая"},
				},
			},
			// Битый элемент: вовсе не объект — не должен ронять парсинг остальных.
			"not a map",
			// Битый элемент: нет поля id — тоже просто пропускается.
			map[string]interface{}{
				"name": map[string]interface{}{
					"text": map[string]interface{}{"text": "без id"},
				},
			},
			map[string]interface{}{
				"id": 4.0,
				"name": map[string]interface{}{
					"text": map[string]interface{}{"text": "Четвёртая"},
				},
			},
		},
	}

	folders, ok := ParseChatFoldersUpdate(update)
	if !ok {
		t.Fatal("expected ok=true, malformed entries should be skipped, not fatal")
	}
	want := []Folder{{ID: 1, Name: "Первая"}, {ID: 4, Name: "Четвёртая"}}
	if !reflect.DeepEqual(folders, want) {
		t.Errorf("unexpected folders: got %#v, want %#v", folders, want)
	}
}
