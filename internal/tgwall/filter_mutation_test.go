package tgwall

import (
	"strings"
	"testing"
)

// Мутационная проверка фильтра источников. Смысл тот же, что у zoom_mutation_test.go
// и keymap_mutation_test.go: убедиться, что проверки из filter_test.go и
// filtermenu_test.go ЛОВЯТ отказ от главных требований задачи 0164. Обычный тест
// проходит и на неправильном коде, если неправильность живёт в ветке, которую этот
// тест не задевает; здесь каждая мутация — проверка ДРУГОГО теста, а не новый тест.
//
// Мутация — замена `from` на `to` в копии модуля, гоняется `go test -run` по
// содержимому копии. Каждая подобрана так, чтобы ломала ровно ОДИН тест: иначе
// «первым упал соседний» и непонятно, поймана ли нужная защита.
var filterMutantCases = []mutantCase{
	{
		// Формула фильтра. Без проверки типа показывается всё, что попало в
		// поток: галка «Каналы» перестаёт что-либо делать.
		name:      "формула: проверка типа источника отброшена",
		file:      "filter.go",
		from:      "\tif !f.allowsType(c.Type) {\n\t\treturn false\n\t}\n",
		to:        "\tif false {\n\t\treturn false\n\t}\n",
		wantFails: "TestFilterAllowsFormula",
	},
	{
		// То же для папок: без проверки отметка папки ничего не ограничивает.
		name:      "формула: проверка папок отброшена",
		file:      "filter.go",
		from:      "\tif !f.allowsFolders(c.ChatID, folders) {\n\t\treturn false\n\t}\n",
		to:        "\tif false {\n\t\treturn false\n\t}\n",
		wantFails: "TestFilterAllowsFormula",
	},
	{
		// «Папки не отмечены — ограничения нет»: без этой ветки пустая карта
		// отметок означала бы «показывать нечего», и стена без фильтра по папкам
		// пустела бы целиком.
		name:      "формула: пустые отметки папок трактуются как «ничего не видно»",
		file:      "filter.go",
		from:      "\tif len(f.folders) == 0 {\n\t\treturn true\n\t}\n",
		to:        "\tif len(f.folders) == 0 {\n\t\treturn false\n\t}\n",
		wantFails: "TestFilterAllowsFormula",
	},
	{
		// Галка «Приглушённые» в обратную сторону: без этой строки заглушённые
		// показывались бы всегда, и галка ничего не делала бы.
		name:      "формула: заглушённые показываются всегда",
		file:      "filter.go",
		from:      "\treturn f.showMuted || !c.Muted\n",
		to:        "\treturn true\n",
		wantFails: "TestFilterAllowsFormula",
	},
	{
		// Мастер-галка «Все»: отметка обязана включать все три типа разом.
		name:      "«Все»: галка включает не все типы",
		file:      "filter.go",
		from:      "func (f wallFilter) withAllTypes(checked bool) wallFilter {\n\tnext := f\n\tnext.showChannels = checked\n\tnext.showChats = checked\n\tnext.showPersonal = checked\n\treturn next\n}",
		to:        "func (f wallFilter) withAllTypes(checked bool) wallFilter {\n\tnext := f\n\tnext.showChannels = checked\n\treturn next\n}",
		wantFails: "TestFilterAllMasterSyncsBothWays",
	},
	{
		// Защита от пустого состояния типов — главное требование задачи: без неё
		// снятие последней галки оставляет стену, показывающую буквально ничего.
		name:      "защита пустого состояния типов отброшена",
		file:      "filter.go",
		from:      "\tif !next.allTypesEmpty() {\n\t\treturn next, !next.same(f)\n\t}\n",
		to:        "\tif true {\n\t\treturn next, !next.same(f)\n\t}\n",
		wantFails: "TestFilterNeverGoesEmptyOnTypes",
	},
	{
		// Живой предпросмотр: без пересборки видимого списка переключение галки
		// меняет состояние, но экран остаётся прежним до перезапуска.
		name:      "предпросмотр: стена не пересобирается после переключения",
		file:      "filtermenu.go",
		from:      "\tm = m.rebuildVisible(false)\n\treturn m, nil\n}",
		to:        "\treturn m, nil\n}",
		wantFails: "TestFilterLivePreviewWithoutApply",
	},
	{
		// Второе место, где собирается видимый список: снимок стены при загрузке.
		// Без пересборки сохранённый фильтр применялся бы только к живому
		// предпросмотру, а первая загрузка показывала бы всё подряд.
		name: "фильтр не применяется к снимку стены",
		file: "model.go",
		// Строки раздвинуты правкой 0163 (синхронизация сведений об источнике
		// перед пересборкой видимого списка, см. комментарий у m.source =
		// msg.cards) — образец сужен до двух реально смежных строк, чтобы не
		// зависеть от текста комментария между ними.
		from:      "\t\tm = m.syncWallSourceInfo()\n\t\tm = m.rebuildVisible(followLatest)",
		to:        "\t\tm = m.syncWallSourceInfo()\n\t\tm.cards = m.source\n\t\tif followLatest {\n\t\t\tm.cursor = clampWallCursor(len(m.cards)-1, len(m.cards))\n\t\t}",
		wantFails: "TestFilterPersistsAcrossRestart",
	},
	{
		// Курсор стены при переключении галки: удержание на том же источнике, а не
		// на прежнем номере. Без него переключение молча уводит человека на чужую
		// карточку — ровно тот баг, что уже находили у живых апдейтов.
		name:      "предпросмотр: курсор уезжает на другой источник",
		file:      "model.go",
		from:      "\t\tif index := indexOfChatCard(m.cards, anchorChatID); index >= 0 {\n\t\t\tm.cursor = index\n\t\t}\n",
		to:        "\t\tif index := indexOfChatCard(m.cards, anchorChatID); index >= 0 && false {\n\t\t\tm.cursor = index\n\t\t}\n",
		wantFails: "TestFilterCursorFollowsSourceWhenListShifts",
	},
	{
		// Живое сообщение в скрытый источник обязано попасть в полный поток: иначе
		// снятие галки показало бы ему старое сообщение.
		name:      "живое сообщение идёт мимо полного потока",
		file:      "live.go",
		from:      "\tm.source = source\n",
		to:        "\tm.cards = source\n",
		wantFails: "TestFilterLiveMessageGoesThroughSource",
	},
	{
		// Персистентность: без сохранения при закрытии выбранное не переживает
		// перезапуск. Подмена на дефолтный фильтр (а не на no-op) — иначе из
		// файла выпал бы импорт config и мутация падала бы на сборке, доказывая
		// ничего.
		name:      "сохраняется не выбранный фильтр, а дефолтный",
		file:      "filtermenu.go",
		from:      "\t_ = config.SaveWallFilter(m.filter.settings())\n",
		to:        "\t_ = config.SaveWallFilter(wallFilterFromSettings(config.DefaultSettings().WallFilter).settings())\n",
		wantFails: "TestFilterPersistsAcrossRestart",
	},
	{
		// Отметка папки, которой больше нет, фильтру ничего не должна значить: с
		// таким обходом стена молча показывала бы «ничего».
		name:      "отметка удалённой папки остаётся ограничением",
		file:      "folders.go",
		from:      "\t\tif f.folders[id] {\n\t\t\tkept[id] = true\n\t\t}\n",
		to:        "\t\tkept[id] = f.folders[id]\n",
		wantFails: "TestFilterKeepsMarksAcrossFolderRename",
	},
	{
		// Состав папок, пришедший после смены списка папок, устарел: применять его
		// нельзя, иначе чаты удалённой папки остались бы в фильтре. Условие
		// остаётся в коде (иначе выпал бы импорт slices и мутация упала бы на
		// сборке), но перестаёт срабатывать.
		name:      "устаревший состав папок применяется",
		file:      "folders.go",
		from:      "\tif !slices.Equal(msg.builtFor, folderIDs(m.folders)) {\n",
		to:        "\tif !slices.Equal(msg.builtFor, folderIDs(m.folders)) && false {\n",
		wantFails: "TestFilterStaleFolderChatsIgnored",
	},
	{
		// Открытое меню обязано перехватывать ввод раньше стены, иначе Enter
		// отправлял бы сообщение вместо переключения галки.
		name:      "меню не перехватывает ввод стены",
		file:      "model.go",
		from:      "\t\tif m.filterMenu != nil {\n\t\t\treturn m.updateFilterMenu(msg)\n\t\t}\n",
		to:        "",
		wantFails: "TestFilterMenuBlocksWallInput",
	},
	{
		// Клавиша открытия читается из конфигурации: с зашитой ctrl+p правка
		// keybindings.toml была бы враньём.
		name:      "клавиша открытия меню зашита в коде",
		file:      "keymap.go",
		from:      "\tcase keyOpenFilter:\n\t\tfields = [][]string{k.keys.Filter}",
		to:        "\tcase keyOpenFilter:\n\t\tfields = [][]string{{\"ctrl+p\"}}",
		wantFails: "TestFilterMenuOpensOnConfiguredKey",
	},
}

// TestFilterMutationsAreCaught — каждая мутация из filterMutantCases обязана быть
// поймана указанным тестом. Проверки вывода и требования к нему — как у
// TestKeymapMutationsAreCaught, поэтому и помощники те же (общие).
func TestFilterMutationsAreCaught(t *testing.T) {
	for _, mutation := range filterMutantCases {
		t.Run(mutation.name, func(t *testing.T) {
			source := t.TempDir()
			if err := copyGoModule(t, source); err != nil {
				t.Fatal(err)
			}
			if err := applyMutation(t, source, mutation); err != nil {
				t.Fatal(err)
			}
			output, err := runGoTest(t, source, mutation.wantFails)
			if err == nil {
				t.Fatalf("мутация НЕ поймана: %s прошёл на изменённом коде\n%s", mutation.wantFails, tail(output))
			}
			if !strings.Contains(string(output), "--- FAIL") {
				t.Fatalf("падение выглядит не как провал теста, а как ошибка сборки —\n"+
					"такая мутация ничего не доказывает:\n%s", tail(output))
			}
			// Падать должен ИМЕННО тот тест: если упал соседний, нужная защита может
			// быть и не покрыта, просто сломалось что-то по соседству.
			failed := firstFailure(output)
			if !strings.Contains(failed, mutation.wantFails) {
				t.Fatalf("мутация сломала другой тест (%s), а ждали %s:\n%s",
					failed, mutation.wantFails, tail(output))
			}
			t.Logf("поймана: %s", failed)
		})
	}
}
