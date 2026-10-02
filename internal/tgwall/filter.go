package tgwall

import (
	"image/color"
	"slices"

	"telecli/internal/auth"
	"telecli/internal/config"
)

// Фильтр источников стены. Что человек выбрал показывать: какие типы
// источников, какие папки и заглушённые ли чаты.
//
// Формула одна и живёт в одном месте (allows), а не россыпью условий по
// отрисовке и подсчётам: три независимых правила, соединённых «И», легко
// разъехались бы по двум-трём местам, и стена показывала бы не то, что человек
// отметил, а то, что осталось от прежней правки.

// wallFilter — состояние фильтра. Значение (не указатель): переключение галки
// переписывает его целиком и возвращает новое, и у копии в меню не может
// оказаться половина применённого состояния.
type wallFilter struct {
	showChannels bool
	showChats    bool
	showPersonal bool
	// showMuted — показывать ли заглушённые чаты. Обратный смысл: в меню это
	// галка «Приглушённые», и отсутствие галки означает «скрывать», то есть
	// ЗДЕСЬ true = показывать. Так оно и по умолчанию: до фильтра стена
	// показывала заглушённые вместе со всеми.
	showMuted bool
	// folders — отмеченные папки по ID. Отсутствие ключа и «папка отмечена
	// на false» — одно и то же: в меню галку можно снять, и тогда хранить её
	// отдельной записью незачем. Пустая карта (папки не отмечены) означает «без
	// ограничения по папкам» — это разные вещи, и различать их приходится
	// именно по пустоте карты.
	folders folderMarks
}

// folderMarks — отметки папок по ID. Собственный тип, а не map[int32]bool
// впрямую: на голом типе-отображении нельзя объявить метод, а копия карты здесь
// нужна каждый раз при переключении (см. toggleRow).
type folderMarks map[int32]bool

// copy — независимая копия отметок. Переключение галки обязано переписывать
// карту целиком, а не править на месте: на месте правка изменила бы и прошлый
// фильтр, и тот, что человек уже закрыл меню и вернулся к стене.
func (m folderMarks) copy() folderMarks {
	result := make(folderMarks, len(m)+1)
	for id, marked := range m {
		result[id] = marked
	}
	return result
}

// wallFilterFromSettings — фильтр стены из настроек. Значение по умолчанию
// (config.DefaultWallFilter) означает «видно всё».
func wallFilterFromSettings(settings config.WallFilter) wallFilter {
	filter := wallFilter{
		showChannels: settings.ShowChannels,
		showChats:    settings.ShowChats,
		showPersonal: settings.ShowPersonal,
		showMuted:    settings.ShowMuted,
	}
	// Карта остаётся nil, когда папок не отмечено, и это безопасно: allows карту
	// только читает, а переключение папки идёт через copy(), которая всегда
	// возвращает готовую карту, — писать в nil-карту никто не лезет.
	if len(settings.Folders) > 0 {
		filter.folders = make(folderMarks, len(settings.Folders))
		for _, id := range settings.Folders {
			filter.folders[id] = true
		}
	}
	return filter
}

// settings — фильтр обратно в форму, в которой он хранится (см. SaveWallFilter).
// Отмеченные папки идут по возрастанию ID, а не в случайном порядке обхода
// карты: map в Go обходится в неопределённом порядке, и файл настроек не должен
// переписываться целиком при каждом переключении галки.
func (f wallFilter) settings() config.WallFilter {
	result := config.WallFilter{
		ShowChannels: f.showChannels,
		ShowChats:    f.showChats,
		ShowPersonal: f.showPersonal,
		ShowMuted:    f.showMuted,
	}
	ids := f.markedFolders()
	if len(ids) == 0 {
		return result
	}
	result.Folders = ids
	return result
}

// allTypes — «Все» отмечено: включены все три типа. Именно отсюда берётся и
// обратная сторона — какое состояние считать «всё включено» после ручной
// правки одной из трёх галок.
func (f wallFilter) allTypes() bool {
	return f.showChannels && f.showChats && f.showPersonal
}

// allows — показывать ли этот источник. Единственная точка, где записан сам
// фильтр:
//
//	показывать чат ⟺
//	    (тип чата ∈ отмеченным из {Каналы, Чаты, Личные})
//	  И (папки не отмечены ВООБЩЕ ИЛИ чат состоит хотя бы в одной из отмеченных)
//	  И (Приглушённые отмечено ИЛИ чат не заглушён)
//
// Три правила независимы, внутри «типов» и внутри «папок» — обычное «ИЛИ»
// (принадлежность любому из отмеченных).
func (f wallFilter) allows(c card, folders chatFolderIDs) bool {
	if !f.allowsType(c.Type) {
		return false
	}
	if !f.allowsFolders(c.ChatID, folders) {
		return false
	}
	return f.showMuted || !c.Muted
}

// allowsType — отмечен ли этот тип источника среди трёх. Тип берётся из
// cardType, а не из auth.ChatKind: у карточки он уже есть, и лишнее поле с тем
// же смыслом разошлось бы с ним при первой же правке перевода (см. cardTypeOf).
func (f wallFilter) allowsType(kind cardType) bool {
	switch kind {
	case cardChannel:
		return f.showChannels
	case cardChat:
		return f.showChats
	case cardPersonal:
		return f.showPersonal
	default:
		return false
	}
}

// allowsFolders — входит ли чат в отмеченные папки. Отмеченных папок нет —
// ограничения нет, и это НЕ то же самое, что «чат не входит ни в одну папку»:
// первый случай показывает всё, второй — ничего. Именно поэтому проверка на
// пустоту здесь первая, а не «чат состоит в отмеченной папке» с пустым списком.
func (f wallFilter) allowsFolders(chatID int64, folders chatFolderIDs) bool {
	if len(f.folders) == 0 {
		return true
	}
	for id := range f.folders {
		if folders.inFolder(chatID, id) {
			return true
		}
	}
	return false
}

// toggleRow — переключение строки меню фильтра. Возвращает НОВЫЙ фильтр и
// признак, что состояние действительно изменилось.
//
// Новое состояние берётся ИЗ СТРОКИ, а не из текущего значения поля: строка
// несёт то, что человек видит на экране, а «переключить» значит «сделать наоборот
// показанному». Иначе галка переключалась бы «не туда» при любом рассинхроне
// строк и фильтра.
func (f wallFilter) toggleRow(row filterRow) (wallFilter, bool) {
	checked := !row.checked
	switch row.kind {
	case filterRowAll:
		return f.toggleAll(checked)
	case filterRowChannels:
		return f.toggleType(func(next *wallFilter) { next.showChannels = checked })
	case filterRowChats:
		return f.toggleType(func(next *wallFilter) { next.showChats = checked })
	case filterRowPersonal:
		return f.toggleType(func(next *wallFilter) { next.showPersonal = checked })
	case filterRowFolder:
		next := f
		next.folders = f.folders.copy()
		if checked {
			next.folders[row.folderID] = true
		} else {
			delete(next.folders, row.folderID)
		}
		return next, true
	case filterRowMuted:
		next := f
		next.showMuted = checked
		return next, true
	default:
		return f, false // заголовки разделов не переключаются
	}
}

// toggleAll — мастер-галка «Все»: отметка включает все три типа разом, снятие
// снимает все три разом. Папки и «Приглушённые» она не трогает — они независимы.
func (f wallFilter) toggleAll(checked bool) (wallFilter, bool) {
	next := f.withAllTypes(checked)
	return f.guardEmptyTypes(next)
}

// toggleType — переключение одного из трёх типов. «Все» после этого
// пересчитывается само (filterMenuRows собирает строку из allTypes), отдельного
// шага «пересчитать мастер-галку» здесь нет и быть не должно: он был бы вторым
// местом, где живёт правило «Все отмечена, когда отмечены все три».
func (f wallFilter) toggleType(apply func(*wallFilter)) (wallFilter, bool) {
	next := f
	apply(&next)
	return f.guardEmptyTypes(next)
}

// guardEmptyTypes — защита от пустого состояния типов, единственное место, где
// она живёт. Снятие, после которого не отмечено ВСЁ ТРИ типа, не применяется как
// «ничего не показывать», а откатывается к «Все = включены все три».
//
// Следствие, о котором стоит помнить: снять «Все» нельзя НИКОГДА. Строка «Все»
// отмечена ровно когда включены все три типа, а снятие именно в этом состоянии и
// даёт пустой набор — то есть единственное состояние, из которого снятие «Все»
// начинается, это ровно то, которое защита запрещает. Проверяется тестом
// TestFilterNeverGoesEmptyOnTypes, чтобы это не выглядело багом, а было
// зафиксированным свойством.
//
// На папки и «Приглушённые» правило НЕ распространяется — там «ничего не
// отмечено» осмысленно (нет ограничения по папкам / скрывать заглушённые), и
// запрещать его было бы запретом на два нормальных состояния.
func (f wallFilter) guardEmptyTypes(next wallFilter) (wallFilter, bool) {
	if !next.allTypesEmpty() {
		return next, !next.same(f)
	}
	restored := f.withAllTypes(true)
	return restored, !restored.same(f)
}

// withAllTypes — все три типа поставлены в одно состояние разом. Отдельный
// метод, потому что «Все» и защита от пустого состояния обязаны считать одно и то
// же: три места с собственным перебором типов разъехались бы при правке.
func (f wallFilter) withAllTypes(checked bool) wallFilter {
	next := f
	next.showChannels = checked
	next.showChats = checked
	next.showPersonal = checked
	return next
}

// same — совпадают ли два фильтра. Отдельный метод, а не сравнение в месте
// вызова: тип содержит карту, и == на нём не компилируется, а разбирать каждое
// поле руками значило бы забыть про новое поле при его добавлении.
func (f wallFilter) same(other wallFilter) bool {
	if f.showChannels != other.showChannels || f.showChats != other.showChats ||
		f.showPersonal != other.showPersonal || f.showMuted != other.showMuted {
		return false
	}
	if len(f.folders) != len(other.folders) {
		return false
	}
	for id, marked := range f.folders {
		if other.folders[id] != marked {
			return false
		}
	}
	return true
}

// allTypesEmpty — не отмечено ни одного типа. Служебная проверка для защиты от
// пустого состояния, не часть allows: «показывать вообще ничего» — не то же
// самое, что «фильтр ничего не разрешает» (во втором случае стена может быть
// пустой из-за папок или заглушённых, и это нормально).
func (f wallFilter) allTypesEmpty() bool {
	return !f.showChannels && !f.showChats && !f.showPersonal
}

// markedFolders — отмеченные папки по возрастанию ID. Порядок строк в меню
// задаётся TDLib (см. filterMenuRows), а этот список нужен для сохранения
// настроек, где порядок должен быть устойчивым между запусками.
func (f wallFilter) markedFolders() []int32 {
	ids := make([]int32, 0, len(f.folders))
	for id, marked := range f.folders {
		if marked {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	return ids
}

// chatFolderIDs — в каких папках состоит каждый чат: id чата → набор id папок.
//
// Отдельная карта, а не поле карточки: принадлежность папке приходит отдельным
// запросом (у TDLib нет поля «папки чата» в объекте chat, см. filterFolderChatsCmd),
// и карточка стены про это ничего не знает. Клади её в карточку — значило бы
// расставлять её по всем карточкам отдельно при каждом приходе папок, то есть
// завести ровно то же второе место хранения, только с ручным обходом всех
// карточек вместо пересборки.
type chatFolderIDs map[int64]map[int32]bool

// inFolder — состоит ли чат в папке. Отсутствие чата в карте и отсутствие папки
// у чата дают одно и то же «нет»: карта заполняется ответами на запросы по
// папкам, и чат, в который не спросили, не должен молча считаться
// принадлежащим всем.
func (ids chatFolderIDs) inFolder(chatID int64, folderID int32) bool {
	return ids[chatID][folderID]
}

// add — пометить чат состоящим в папке.
func (ids chatFolderIDs) add(chatID int64, folderID int32) {
	byFolder, ok := ids[chatID]
	if !ok {
		byFolder = make(map[int32]bool)
		ids[chatID] = byFolder
	}
	byFolder[folderID] = true
}

// filterCards — карточки потока, которые показывает текущий фильтр. Единственное
// место, где список стены собирается с учётом фильтра: и снимок при загрузке, и
// живой апдейт, и переключение галки в меню идут через него, поэтому «что
// показывать» не может разъехаться между этими четырьмя путями.
func (m Model) filterCards() []card {
	if len(m.source) == 0 {
		return nil
	}
	// Фильтр по умолчанию ничего не исключает — тогда копию не делаем вовсе:
	// это самый частый случай (человек открывает стену и смотрит всё), а
	// filterCards вызывается ещё и на каждом живом апдейте.
	if m.filter.allTypes() && m.filter.showMuted && len(m.filter.folders) == 0 {
		return m.source
	}
	visible := make([]card, 0, len(m.source))
	for _, item := range m.source {
		if m.filter.allows(item, m.chatFolders) {
			visible = append(visible, item)
		}
	}
	return visible
}

// filterRowKind — что за строка в меню фильтра. Заголовки разделов (Папки,
// Дополнительно) не переключаются, поэтому от них отдельный вид: на них курсор
// не встаёт (см. moveFilterCursor).
type filterRowKind int

const (
	filterRowAll filterRowKind = iota
	filterRowChannels
	filterRowChats
	filterRowPersonal
	filterRowFoldersHeader
	filterRowFolder
	filterRowExtraHeader
	filterRowMuted
)

// filterRow — строка меню фильтра: что нарисовать и что означает переключение.
// checked — состояние ДО переключения, то есть ровно то, что человек видит на
// экране, когда нажимает.
type filterRow struct {
	kind     filterRowKind
	label    string
	folderID int32
	checked  bool
}

// selectable — на строку можно встать курсором. Заголовки разделов нельзя: они
// не несут состояния, и курсор, вставший на «Папки», ничего не переключил бы
// по нажатию — хуже, чем просто перепрыгнуть такую строку.
func (r filterRow) selectable() bool {
	return r.kind != filterRowFoldersHeader && r.kind != filterRowExtraHeader
}

// accent — цвет строки меню по типу источника и признак, что он у строки есть.
//
// Цвет тот же, что у карточки этого типа на стене (см. card.accent), и берётся
// из тех же констант палитры: строка «Каналы» в меню и карточка канала обязаны
// выглядеть одним и тем же, иначе человек сверял бы их как разные сущности.
//
// Тип есть только у трёх строк — «Каналы», «Чаты», «Личные». У «Все», папок и
// «Приглушённых» его нет: это не один источник, а набор или правило, и красить
// их чужим акцентом значило бы приписать им тип, которого у них нет.
func (r filterRow) accent() (color.Color, bool) {
	switch r.kind {
	case filterRowChannels:
		return PaletteChannel, true
	case filterRowChats:
		return PaletteChat, true
	case filterRowPersonal:
		return PalettePersonal, true
	default:
		return nil, false
	}
}

// filterMenuRows — строки меню в согласованном порядке: «Все», три типа,
// разделитель «Папки», сами папки, разделитель «Дополнительно», «Приглушённые».
//
// Порядок папок — как пришёл от TDLib, без сортировки: это порядок, который
// задал сам человек в Telegram, и переставлять его здесь незачем.
//
// У человека без единой папки заголовок «Папки» и пустой список под ним —
// в меню это выглядело бы оборванным разделом, поэтому заголовок вместе с
// разделом и пропускается (на логику фильтра это не влияет: без отмеченных папок
// ограничения нет вовсе).
func filterMenuRows(filter wallFilter, folders []auth.Folder) []filterRow {
	rows := []filterRow{
		{kind: filterRowAll, label: filterLabelAll, checked: filter.allTypes()},
		{kind: filterRowChannels, label: filterLabelChannels, checked: filter.showChannels},
		{kind: filterRowChats, label: filterLabelChats, checked: filter.showChats},
		{kind: filterRowPersonal, label: filterLabelPersonal, checked: filter.showPersonal},
	}
	if len(folders) > 0 {
		rows = append(rows, filterRow{kind: filterRowFoldersHeader, label: filterLabelFolders})
		for _, folder := range folders {
			rows = append(rows, filterRow{
				kind:     filterRowFolder,
				label:    folder.Name,
				folderID: folder.ID,
				checked:  filter.folders[folder.ID],
			})
		}
	}
	return append(rows,
		filterRow{kind: filterRowExtraHeader, label: filterLabelExtra},
		filterRow{kind: filterRowMuted, label: filterLabelMuted, checked: filter.showMuted},
	)
}

// Подписи строк меню. Собраны отдельно от filterMenuRows, потому что на них
// ссылаются и меню, и его тесты — и «Каналы», написанные в двух местах, разъехались
// бы при первой же правке формулировки.
const (
	filterLabelAll      = "Все"
	filterLabelChannels = "Каналы"
	filterLabelChats    = "Чаты"
	filterLabelPersonal = "Личные"
	filterLabelFolders  = "Папки"
	filterLabelExtra    = "Дополнительно"
	filterLabelMuted    = "Приглушённые"
)
