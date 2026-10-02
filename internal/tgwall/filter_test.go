package tgwall

import (
	"reflect"
	"testing"

	"telecli/internal/auth"
	"telecli/internal/config"
)

// Фильтр источников стены: формула «показывать ⟺», синхронизация мастер-галки
// «Все» и защита от пустого состояния типов. Здесь проверяется сама формула, без
// модели стены и без TUI: она живёт в чистых функциях, и табличный тест на них
// проверяет ровно то, что человек согласовал, — по одной строке на каждое
// сочетание типов/папок/заглушённости.

// filterTestCard — карточка стены нужного вида. Собирается через wallCard из чата
// и сообщения, а не литералом card{}: у wallCard и у wallFilter должно быть одно
// и то же понимание того, какой чат какой карточкой стал (иначе тест проверял бы
// подмену на стороне фикстуры, а не фильтр).
func filterTestCard(chatID int64, kind auth.ChatKind, muted bool) card {
	return wallCard(
		auth.Chat{ID: chatID, Title: "источник", Kind: kind, Muted: muted},
		auth.Message{ID: chatID * 10, Text: "текст", Date: chatID},
	)
}

// filterTestFolders — карта «чат → папки» из плоского списка пар.
func filterTestFolders(pairs ...any) chatFolderIDs {
	ids := make(chatFolderIDs)
	for index := 0; index+1 < len(pairs); index += 2 {
		chatID, ok := pairs[index].(int64)
		if !ok {
			panic("тестовая фикстура: id чата должен быть int64")
		}
		folderID, ok := pairs[index+1].(int32)
		if !ok {
			panic("тестовая фикстура: id папки должен быть int32")
		}
		ids.add(chatID, folderID)
	}
	return ids
}

// allTypesFilter — фильтр «видно всё»: три типа и заглушённые включены, папки не
// отмечены. Это состояние по умолчанию (config.DefaultWallFilter), и именно с
// него начинается каждая табличная проверка формулы.
func allTypesFilter() wallFilter {
	return wallFilter{showChannels: true, showChats: true, showPersonal: true, showMuted: true}
}

// TestFilterAllowsFormula — по одному утверждению на каждое правило формулы и на
// каждое их сочетание. Порядок строк повторяет саму формулу:
//
//	показывать ⟺ (тип ∈ отмеченных) И (папки не отмечены ИЛИ чат в отмеченной)
//	             И (Приглушённые отмечено ИЛИ чат не заглушён)
func TestFilterAllowsFormula(t *testing.T) {
	channel := filterTestCard(1, auth.ChatChannel, false)
	group := filterTestCard(2, auth.ChatGroup, false)
	personal := filterTestCard(3, auth.ChatPrivate, false)
	mutedChannel := filterTestCard(4, auth.ChatChannel, true)
	mutedPersonal := filterTestCard(5, auth.ChatPrivate, true)
	// Чат 6 состоит в папке 10, чат 7 — в папке 20, чат 8 — в обеих.
	foldered := filterTestCard(6, auth.ChatGroup, false)
	otherFoldered := filterTestCard(7, auth.ChatChannel, false)
	bothFolders := filterTestCard(8, auth.ChatPrivate, false)
	folders := filterTestFolders(int64(6), int32(10), int64(7), int32(20), int64(8), int32(10), int64(8), int32(20))
	// Тот же чат 6 (то есть та же папка 10), но канал — чтобы «тип снят» и «чат в
	// отмеченной папке» относились к одной и той же карточке, а не к разным.
	channelInFolder := filterTestCard(6, auth.ChatChannel, false)

	for _, test := range []struct {
		name    string
		filter  wallFilter
		card    card
		folders chatFolderIDs
		want    bool
	}{
		// Правило типов: снят один тип — его источники скрыты, остальные видны.
		{name: "все типы: видно всё", filter: allTypesFilter(), card: group, want: true},
		{
			name:   "снят канал: канал скрыт",
			filter: wallFilter{showChats: true, showPersonal: true, showMuted: true},
			card:   channel,
			want:   false,
		},
		{
			name:   "снят канал: чат виден",
			filter: wallFilter{showChats: true, showPersonal: true, showMuted: true},
			card:   group,
			want:   true,
		},
		{
			name:   "снят личный: личный скрыт",
			filter: wallFilter{showChannels: true, showChats: true, showMuted: true},
			card:   personal,
			want:   false,
		},
		// Правило папок: отмеченных папок нет — ограничения нет вовсе, то есть
		// видно вообще всё, включая чаты, не состоящие ни в одной папке.
		{name: "папки не отмечены: чат вне папок виден", filter: allTypesFilter(), card: filterTestCard(9, auth.ChatGroup, false), want: true},
		{
			name:    "отмечена папка 10: её чат виден",
			filter:  wallFilter{showChannels: true, showChats: true, showPersonal: true, showMuted: true, folders: folderMarks{10: true}},
			card:    foldered,
			folders: folders,
			want:    true,
		},
		{
			name:    "отмечена папка 10: чат из другой папки скрыт",
			filter:  wallFilter{showChannels: true, showChats: true, showPersonal: true, showMuted: true, folders: folderMarks{10: true}},
			card:    otherFoldered,
			folders: folders,
			want:    false,
		},
		{
			name:    "отмечены обе папки: чат из любой виден",
			filter:  wallFilter{showChannels: true, showChats: true, showPersonal: true, showMuted: true, folders: folderMarks{10: true, 20: true}},
			card:    otherFoldered,
			folders: folders,
			want:    true,
		},
		{
			// Внутри «папок» обычное «ИЛИ»: принадлежность ЛЮБОЙ из отмеченных.
			name:    "отмечены обе папки: чат из первой виден",
			filter:  wallFilter{showChannels: true, showChats: true, showPersonal: true, showMuted: true, folders: folderMarks{10: true, 20: true}},
			card:    bothFolders,
			folders: folders,
			want:    true,
		},
		{
			name:    "отмечена несуществующая папка: всё скрыто",
			filter:  wallFilter{showChannels: true, showChats: true, showPersonal: true, showMuted: true, folders: folderMarks{99: true}},
			card:    foldered,
			folders: folders,
			want:    false,
		},
		{
			// Отсутствие чата в карте и «не входит ни в одну папку» — одно и то же.
			name:    "отмечена папка: состав неизвестен — скрыт",
			filter:  wallFilter{showChannels: true, showChats: true, showPersonal: true, showMuted: true, folders: folderMarks{10: true}},
			card:    foldered,
			folders: nil,
			want:    false,
		},
		// Правило заглушённых: галка снята — заглушённые скрыты.
		{
			name:   "Приглушённые снято: заглушённый канал скрыт",
			filter: wallFilter{showChannels: true, showChats: true, showPersonal: true},
			card:   mutedChannel,
			want:   false,
		},
		{
			name:   "Приглушённые снято: незаглушённый канал виден",
			filter: wallFilter{showChannels: true, showChats: true, showPersonal: true},
			card:   channel,
			want:   true,
		},
		{
			name:   "Приглушённые отмечено: заглушённое личное видно",
			filter: allTypesFilter(),
			card:   mutedPersonal,
			want:   true,
		},
		// Три правила через «И»: каждое по отдельности пропускает, вместе — нет.
		{
			// Канал снят, но чат состоит в отмеченной папке — тип всё равно
			// запрещает показ.
			name:    "тип снят И чат в отмеченной папке: всё равно скрыт",
			filter:  wallFilter{showChats: true, showPersonal: true, showMuted: true, folders: folderMarks{10: true}},
			card:    channelInFolder,
			folders: folders,
			want:    false,
		},
		{
			// Тот же чат, но типы на месте: видно. Разница с предыдущей строкой —
			// ровно в правиле типов.
			name:    "тот же чат при всех типах: видно",
			filter:  allTypesFilter(),
			card:    channelInFolder,
			folders: folders,
			want:    true,
		},
		{
			// Второе правило снято не бывает «частично»: папки не отмечены —
			// ограничения нет. Значит «тип снят, но чат в отмеченной папке»
			// скрыт, а «папки не отмечены» ограничивать не может.
			name:    "тип снят И папки не отмечены: всё равно скрыт",
			filter:  wallFilter{showChats: true, showPersonal: true, showMuted: true},
			card:    channelInFolder,
			folders: folders,
			want:    false,
		},
		{
			name:    "все три правила выполнены: видно",
			filter:  wallFilter{showChannels: true, showChats: true, showPersonal: true, showMuted: true, folders: folderMarks{10: true}},
			card:    foldered,
			folders: folders,
			want:    true,
		},
		{
			// Тот же набор, но чат заглушён, а «Приглушённые» включено: видно.
			name:    "в папке, заглушён, но Приглушённые отмечено: видно",
			filter:  allTypesFilter(),
			card:    filterTestCard(6, auth.ChatGroup, true),
			folders: folders,
			want:    true,
		},
		{
			// Заглушён, вне папок, «Приглушённые» включено: видно — вне папок
			// ограничения нет.
			name:    "вне папок, заглушён, Приглушённые отмечено: видно",
			filter:  allTypesFilter(),
			card:    mutedChannel,
			folders: folders,
			want:    true,
		},
		{
			name:    "заглушён И Приглушённые снято: скрыт",
			filter:  wallFilter{showChannels: true, showChats: true, showPersonal: true, folders: folderMarks{10: true}},
			card:    filterTestCard(6, auth.ChatGroup, true),
			folders: folders,
			want:    false,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := test.filter.allows(test.card, test.folders); got != test.want {
				t.Fatalf("allows = %v, ждали %v", got, test.want)
			}
		})
	}
}

// TestFilterAllMasterSyncsBothWays — мастер-галка «Все» в обе стороны.
//
// Вперёд: отметили «Все» — отметились все три типа.
// Назад: сняли одну из трёх вручную — «Все» пересчиталось и снялось.
func TestFilterAllMasterSyncsBothWays(t *testing.T) {
	t.Run("отметили «Все» — включились все три типа", func(t *testing.T) {
		filter := wallFilter{}
		next, changed := filter.toggleRow(filterRow{kind: filterRowAll, checked: false})
		if !changed {
			t.Fatal("переключение «Все» должно было изменить фильтр")
		}
		if !next.allTypes() {
			t.Fatalf("после отметки «Все» ждали все три типа, получили %+v", next)
		}
	})

	t.Run("сняли одну из трёх — «Все» пересчитано", func(t *testing.T) {
		filter := allTypesFilter()
		next, changed := filter.toggleRow(filterRow{kind: filterRowChats, checked: true})
		if !changed {
			t.Fatal("снятие галки чата должно было изменить фильтр")
		}
		if next.allTypes() {
			t.Fatal("после снятия одного типа «Все» обязано пересчитаться в снятое")
		}
		if next.showChannels != true || next.showPersonal != true {
			t.Fatalf("снятие одного типа обязано оставить остальные включёнными: %+v", next)
		}
		if next.showChats {
			t.Fatal("галка чата должна быть снята")
		}
	})

	t.Run("сняли две из трёх — «Все» всё ещё снято", func(t *testing.T) {
		filter := wallFilter{showChannels: true, showPersonal: true, showMuted: true}
		next, _ := filter.toggleRow(filterRow{kind: filterRowPersonal, checked: true})
		if next.allTypes() {
			t.Fatal("после снятия двух типов «Все» обязано быть снято")
		}
	})
}

// TestFilterNeverGoesEmptyOnTypes — защита от пустого состояния типов: снятие,
// после которого не отмечено ВСЁ ТРИ типа, не применяется. Оба пути к нему
// проверяются отдельно, потому что это РАЗНЫЕ действия, и защита в одном из них
// ничего не говорит о втором.
func TestFilterNeverGoesEmptyOnTypes(t *testing.T) {
	t.Run("снятие последней из трёх вручную откатывается к «видно всё»", func(t *testing.T) {
		// Остался включён один тип — «Каналы».
		filter := wallFilter{showChannels: true, showMuted: true}
		next, changed := filter.toggleRow(filterRow{kind: filterRowChannels, checked: true})
		if !changed {
			t.Fatal("откат к «Все = включены все три» — это изменение состояния")
		}
		if !next.allTypes() {
			t.Fatalf("после отката обязаны быть включены все три типа, получили %+v", next)
		}
		// Откат не должен трогать независимые от типов части фильтра.
		if !next.showMuted || len(next.folders) != 0 {
			t.Fatalf("откат обязан оставить папки и «Приглушённые» как были: %+v", next)
		}
	})

	t.Run("снятие «Все» при всех трёх включённых откатывается", func(t *testing.T) {
		filter := allTypesFilter()
		next, changed := filter.toggleRow(filterRow{kind: filterRowAll, checked: true})
		if changed {
			t.Fatal("снятие «Все» не должно оставлять стену вовсе пустой")
		}
		if !next.allTypes() {
			t.Fatalf("состояние должно остаться прежним (все три типа), получили %+v", next)
		}
	})

	t.Run("снятие «Все» при неполном наборе типов снимает все разом", func(t *testing.T) {
		// «Все» отмечена ровно когда включены все три типа, поэтому строка
		// «снять Все» может прийти только из этого состояния — и оно же
		// единственное, где снятие оставило бы пустой набор типов. Значит
		// «Все» нельзя снять НИКОГДА: это прямое следствие защиты от пустого
		// состояния, и проверяется здесь именно как следствие, а не как
		// «должно работать».
		//
		// Снять все три типа по одной всё можно — см. следующую подпроверку
		// второго пути, до последней галки.
		filter := wallFilter{showChannels: true, showChats: true, showPersonal: true, showMuted: true}
		next, changed := filter.toggleRow(filterRow{kind: filterRowAll, checked: true})
		if changed {
			t.Fatal("снятие «Все» при всех трёх типах не должно применяться")
		}
		if !next.allTypes() {
			t.Fatalf("состояние должно остаться прежним, получили %+v", next)
		}
	})

	t.Run("снятие двух типов вручную применяется", func(t *testing.T) {
		// Обратная сторона защиты: пока хоть один тип есть, снимать можно — иначе
		// защита превратилась бы в запрет убирать галки.
		filter := allTypesFilter()
		after, changed := filter.toggleRow(filterRow{kind: filterRowChannels, checked: true})
		if !changed {
			t.Fatal("снятие первого типа обязано применяться")
		}
		after, changed = after.toggleRow(filterRow{kind: filterRowChats, checked: true})
		if !changed {
			t.Fatal("снятие второго типа обязало бы применяться")
		}
		if !after.showPersonal {
			t.Fatalf("после снятия двух типов остаться должен третий: %+v", after)
		}
	})

	t.Run("защита не распространяется на папки", func(t *testing.T) {
		// Папки: снятие последней отмеченной осмысленно — это «без ограничения по
		// папкам», и пустая стена тут не следует.
		filter := wallFilter{showChannels: true, showChats: true, showPersonal: true, folders: folderMarks{10: true}}
		next, changed := filter.toggleRow(filterRow{kind: filterRowFolder, folderID: 10, checked: true})
		if !changed {
			t.Fatal("снятие последней отмеченной папки должно применяться")
		}
		if len(next.folders) != 0 {
			t.Fatalf("отметок папок после снятия быть не должно: %+v", next.folders)
		}
	})

	t.Run("защита не распространяется на «Приглушённые»", func(t *testing.T) {
		filter := allTypesFilter()
		next, changed := filter.toggleRow(filterRow{kind: filterRowMuted, checked: true})
		if !changed {
			t.Fatal("снятие «Приглушённые» должно применяться")
		}
		if next.showMuted {
			t.Fatal("«Приглушённые» должны быть сняты")
		}
	})

	t.Run("переключение заголовка раздела ничего не меняет", func(t *testing.T) {
		filter := allTypesFilter()
		if _, changed := filter.toggleRow(filterRow{kind: filterRowFoldersHeader}); changed {
			t.Fatal("заголовок раздела не переключается")
		}
		if _, changed := filter.toggleRow(filterRow{kind: filterRowExtraHeader}); changed {
			t.Fatal("заголовок раздела не переключается")
		}
	})
}

// TestFilterRowToggleMatchesVisibleState — переключение берёт состояние ИЗ строки
// (то есть из того, что человек видит) и переписывает его на противоположное. Если
// бы toggleRow опиралась на своё собственное поле, а не на строку, рассинхрон
// строк и фильтра означал бы, что галка на экране переключается «не туда».
func TestFilterRowToggleMatchesVisibleState(t *testing.T) {
	t.Run("папка отмечена в фильтре, но строка показывает снятую", func(t *testing.T) {
		filter := allTypesFilter()
		filter.folders = folderMarks{10: true}
		// Строка говорит «снято» — переключение обязано её отметить, а не снять
		// (как было бы, если бы toggleRow смотрела на filter.folders[10]).
		next, changed := filter.toggleRow(filterRow{kind: filterRowFolder, folderID: 10, checked: false})
		if !changed || !next.folders[10] {
			t.Fatalf("переключение должно отметить папку 10, получили %+v", next.folders)
		}
	})

	t.Run("тип снят в фильтре, а строка показывает отмеченный — строка побеждает", func(t *testing.T) {
		// Обратная сторона того же правила: если фильтр молча потерял галку, а на
		// экране она видна, то нажатие её НЕ включает обратно, а подтверждает то,
		// что показано, — то есть после нажатия строка и фильтр совпадают.
		filter := wallFilter{showChannels: true, showPersonal: true, showMuted: true}
		next, changed := filter.toggleRow(filterRow{kind: filterRowChats, checked: true})
		if changed {
			t.Fatalf("фильтр уже сходится с показанной строкой, переключать нечего: %+v", next)
		}
		if next.showChats {
			t.Fatalf("нажатие не должно было включать снятую галку: %+v", next)
		}
	})
}

// TestFilterToggleDoesNotMutateOriginal — переключение возвращает новый фильтр и
// не трогает прежний: иначе прошлый фильтр, с которым человек уже закрыл меню и
// вернулся к стене, изменился бы вместе с текущим.
func TestFilterToggleDoesNotMutateOriginal(t *testing.T) {
	filter := wallFilter{showChannels: true, showChats: true, showPersonal: true, folders: folderMarks{10: true}}
	_, _ = filter.toggleRow(filterRow{kind: filterRowFolder, folderID: 10, checked: true})
	_, _ = filter.toggleRow(filterRow{kind: filterRowFolder, folderID: 20, checked: false})
	_, _ = filter.toggleRow(filterRow{kind: filterRowChats, checked: true})

	if !filter.showChats {
		t.Fatal("прежний фильтр не должен терять тип")
	}
	if !filter.folders[10] {
		t.Fatal("прежний фильтр не должен терять отметку папки")
	}
	if filter.folders[20] {
		t.Fatal("прежний фильтр не должен получать новых отметок")
	}
}

// TestFilterFromSettingsAndBack — круг «настройки → фильтр → настройки».
// Персистентность без этого шага была бы фикцией: сохранить можно было бы что-то
// одно, а прочитать — совсем другое.
func TestFilterFromSettingsAndBack(t *testing.T) {
	settings := config.WallFilter{
		ShowChannels: true,
		ShowChats:    false,
		ShowPersonal: true,
		ShowMuted:    true,
		Folders:      []int32{7, 3},
	}
	filter := wallFilterFromSettings(settings)

	if filter.allTypes() {
		t.Fatal("настройка снятого типа не должна читаться как «все три включены»")
	}
	if !filter.folders[7] || !filter.folders[3] {
		t.Fatalf("отмеченные папки должны читаться: %+v", filter.folders)
	}
	if filter.folders[5] {
		t.Fatal("неотмеченная папка не должна считаться отмеченной")
	}

	// Обратный шаг: тот же набор значений, но папки по возрастанию ID — порядок
	// обхода map в Go не задан, и файл настроек не должен переписываться целиком
	// при каждом переключении галки.
	got := filter.settings()
	want := config.WallFilter{ShowChannels: true, ShowPersonal: true, ShowMuted: true, Folders: []int32{3, 7}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("settings() = %+v, ждали %+v", got, want)
	}
}

// TestFilterDefaultSettingsShowEverything — умолчание «видно всё» обязано быть
// именно таким, каким было поведение стены ДО появления фильтра, иначе первый же
// запуск молча сузил бы то, к чему человек привык.
func TestFilterDefaultSettingsShowEverything(t *testing.T) {
	filter := wallFilterFromSettings(config.DefaultSettings().WallFilter)
	if !filter.allTypes() {
		t.Fatal("по умолчанию должны быть включены все три типа")
	}
	if filter.showMuted {
		t.Fatal("«Приглушённые» по умолчанию выключено (стена показывает все чаты)")
	}
	if len(filter.folders) != 0 {
		t.Fatal("по умолчанию папки не отмечены")
	}
}

// TestFilterRowIsSameAfterRebuild — опознание той же строки после пересборки
// списка: иначе переключение одной галки уводило бы курсор меню на другую строку.
// У папок проверяется именно id, а не название: название человек переименовывает
// руками в Telegram.
func TestFilterRowIsSameAfterRebuild(t *testing.T) {
	folders := []auth.Folder{{ID: 10, Name: "Работа"}, {ID: 20, Name: "Дом"}}
	filter := wallFilter{showChannels: true, showChats: true, showPersonal: true, folders: folderMarks{20: true}}

	menu := wallFilterMenu{rows: filterMenuRows(filter, folders)}
	// Встаём на папку 20: после «Все» три типа, затем заголовок «Папки», затем
	// папка 10, затем папка 20.
	menu.cursor = 6

	next := menu.rebuilt(wallFilter{showChannels: true, showChats: true, showPersonal: true}, folders)
	row, ok := next.rowAt()
	if !ok {
		t.Fatal("после пересборки под курсором должна остаться строка")
	}
	if row.kind != filterRowFolder || row.folderID != 20 {
		t.Fatalf("курсор уехал на %+v, ждали папку 20", row)
	}
}

// TestFilterMenuRowsLayout — порядок строк меню по согласованной схеме: «Все», три
// типа, «Папки», папки в порядке от TDLib, «Дополнительно», «Приглушённые».
// Порядок папок НЕ сортируется: это порядок, который задал человек в Telegram.
func TestFilterMenuRowsLayout(t *testing.T) {
	rows := filterMenuRows(allTypesFilter(), []auth.Folder{{ID: 30, Name: "Позднее"}, {ID: 10, Name: "Работа"}})
	want := []struct {
		kind  filterRowKind
		label string
	}{
		{kind: filterRowAll, label: filterLabelAll},
		{kind: filterRowChannels, label: filterLabelChannels},
		{kind: filterRowChats, label: filterLabelChats},
		{kind: filterRowPersonal, label: filterLabelPersonal},
		{kind: filterRowFoldersHeader, label: filterLabelFolders},
		{kind: filterRowFolder, label: "Позднее"},
		{kind: filterRowFolder, label: "Работа"},
		{kind: filterRowExtraHeader, label: filterLabelExtra},
		{kind: filterRowMuted, label: filterLabelMuted},
	}
	if len(rows) != len(want) {
		t.Fatalf("строк %d, ждали %d: %+v", len(rows), len(want), rows)
	}
	for index, expected := range want {
		if rows[index].kind != expected.kind || rows[index].label != expected.label {
			t.Fatalf("строка %d = %+v, ждали %+v", index, rows[index], expected)
		}
	}
	if rows[5].folderID != 30 || rows[6].folderID != 10 {
		t.Fatalf("порядок папок должен остаться порядком от TDLib, а не по алфавиту: %d, %d", rows[5].folderID, rows[6].folderID)
	}
}

// TestFilterMenuRowsWithoutFolders — у человека без единой папки раздел «Папки»
// пропускается целиком: заголовок с пустым списком под ним читался бы как
// оборванный. На логику фильтра это не влияет — без отмеченных папок
// ограничения нет вовсе (см. TestFilterAllowsFormula).
func TestFilterMenuRowsWithoutFolders(t *testing.T) {
	rows := filterMenuRows(allTypesFilter(), nil)
	for _, row := range rows {
		if row.kind == filterRowFoldersHeader || row.kind == filterRowFolder {
			t.Fatalf("без папок раздел «Папки» не должен попадать в меню: %+v", rows)
		}
	}
	if len(rows) != 6 {
		t.Fatalf("строк %d, ждали 6 (без раздела «Папки»): %+v", len(rows), rows)
	}
	if !rows[0].selectable() || rows[0].kind != filterRowAll {
		t.Fatalf("курсор должен вставать на первую выбираемую строку: %+v", rows[0])
	}
	if rows[4].selectable() || rows[4].kind != filterRowExtraHeader {
		t.Fatalf("заголовок «Дополнительно» должен остаться невыбираемым: %+v", rows[4])
	}
}
