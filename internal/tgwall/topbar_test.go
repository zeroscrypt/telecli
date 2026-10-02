package tgwall

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"telecli/internal/auth"
)

// Верхняя строка экрана: одна, во всю ширину, без полей и без левой границы.
// Слева — подпись выбранных источников фильтра, справа — название открытой
// переписки, встающее по левому краю своей панели.

// Экран собран сверху вниз: верхняя строка, стена, строка временных сообщений и
// только потом прежний хвост (разделитель, блок ввода, разделитель, нижняя
// строка). Порядок и количество строк, а не «текст где-то на экране»: строка
// сообщений по виду не отличается от пустой строки стены, и переставленный
// верхний бар поймать по картинке нельзя.
func TestScreenFrameIsTopBarWallNoticeAndTail(t *testing.T) {
	m := newTestModel(t, 100, 30)
	lines := splitLines(m.renderScreen())

	zone := wallZoneLines(t, m, lines)
	if len(zone) != m.wallHeight() {
		t.Fatalf("зона стены — %d строк, ждали %d (окно под неё)", len(zone), m.wallHeight())
	}
	frame := wallTopBarRows + len(zone) + wallNoticeRows
	if frame != countWallLines(t, m) {
		t.Fatalf("над разделителем %d строк, ждали %d: строка фильтра, стена, строка сообщений",
			countWallLines(t, m), frame)
	}
	// Хвост не сдвинулся: сразу за строкой сообщений идёт разделитель…
	if !strings.Contains(ansi.Strip(lines[frame]), "─") {
		t.Fatalf("строка %d — не разделитель: %q", frame, ansi.Strip(lines[frame]))
	}
	// …а последней строкой экрана остаётся нижняя. Если бы строка фильтра не была
	// вычтена из высоты стены, экран стал бы выше терминала, и renderScreen
	// обрезал бы низ — вместе со спиннером.
	if last := ansi.Strip(lines[len(lines)-1]); !strings.Contains(last, testAppName) {
		t.Fatalf("последняя строка экрана — не нижняя: %q", last)
	}
}

// Строка занимает ровно всю ширину терминала, от края до края: полей карточки по
// бокам нет, вертикальной линии слева нет, заливка — основной фон, а не панельный.
// Иначе строка выглядела бы панелью, которой не является, и обрезала бы текст
// прежде, чем он реально кончился.
func TestTopBarSpansFullWidthWithoutBorderOrMargins(t *testing.T) {
	for _, width := range []int{20, 40, 100} {
		bar := newTestModel(t, width, 30).renderTopBar(width)
		if got := cellWidth(bar); got != width {
			t.Fatalf("строка шириной %d на терминале %d: %q", got, width, ansi.Strip(bar))
		}
		if plain := ansi.Strip(bar); strings.ContainsRune(plain, '┃') {
			t.Fatalf("в строке осталась вертикальная линия: %q", plain)
		}
		// Подпись начинается с самой первой ячейки: бокового поля быть не должно.
		if plain := ansi.Strip(bar); !strings.HasPrefix(plain, filterLabelAll) {
			t.Fatalf("строка начинается с %q, ждали подпись фильтра с нулевой колонки", plain)
		}
		// Фон основной, а не панельный: от самой стены полоса не отделяется.
		if strings.Contains(bar, backgroundSGR(t, PaletteBackgroundPanel)) {
			t.Fatalf("строка залита панельным фоном вместо основного: %q", bar)
		}
	}
}

// Подпись слева собирается из строк меню фильтра, а не из отдельного перечисления:
// те же строки, тот же порядок, то же состояние. «Все» заменяет собою три типа
// (иначе подпись дублировала бы саму себя), а «Приглушённые» — не источник, а
// правило показа, и в подписи его нет.
func TestTopBarSourceLabelReportsSelectedFilterRows(t *testing.T) {
	folders := []auth.Folder{{ID: 10, Name: "Личные"}, {ID: 20, Name: "боты"}, {ID: 30, Name: "крипта"}}
	// Фильтры собраны литералами, а не переключением строк меню: подпись обязана
	// читать СОСТОЯНИЕ фильтра, и проверять её на состояниях, которых ещё нет ни в
	// настройках, ни в живом меню, честнее, чем прокладывать путь через toggleRow.
	full := allTypesFilter()
	filter := func(channels, chats, personal bool, marked ...int32) wallFilter {
		next := wallFilter{showChannels: channels, showChats: chats, showPersonal: personal, showMuted: true}
		if len(marked) > 0 {
			next.folders = make(map[int32]bool, len(marked))
			for _, id := range marked {
				next.folders[id] = true
			}
		}
		return next
	}

	tests := []struct {
		name    string
		filter  wallFilter
		folders []auth.Folder
		want    string
	}{
		{name: "все три типа: одной строкой «Все»", filter: full, want: filterLabelAll},
		{name: "без папок: «Все»", filter: full, folders: folders, want: filterLabelAll},
		{
			name:    "папки отмечены, типы все: «Все» с папками следом",
			filter:  filter(true, true, true, 10, 20),
			folders: folders,
			want: filterLabelAll + topBarSourceSeparator + "Личные" +
				topBarSourceSeparator + "боты",
		},
		{
			name:    "снят один тип: два остальных по порядку строк меню",
			filter:  filter(true, false, true),
			folders: folders,
			want:    filterLabelChannels + topBarSourceSeparator + filterLabelPersonal,
		},
		{
			name:    "остался один тип: его собственное имя",
			filter:  filter(true, false, false),
			folders: folders,
			want:    filterLabelChannels,
		},
		{
			name:    "папки: именами, в порядке TDLib, а не отмеченных",
			filter:  filter(true, true, true, 30, 10),
			folders: folders,
			want:    filterLabelAll + topBarSourceSeparator + "Личные" + topBarSourceSeparator + "крипта",
		},
		{
			name:    "тип и папки: и то и другое подряд",
			filter:  filter(true, false, true, 20),
			folders: folders,
			want: filterLabelChannels + topBarSourceSeparator + filterLabelPersonal +
				topBarSourceSeparator + "боты",
		},
		{
			name:    "из типов ничего не выбрано, папки не отмечены: подпись пуста",
			filter:  filter(false, false, false),
			folders: folders,
			want:    "",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := newTestModel(t, 100, 30)
			m.filter = test.filter
			m.folders = test.folders
			if got := m.wallSourceLabel(); got != test.want {
				t.Fatalf("подпись источника = %q, ждали %q", got, test.want)
			}
		})
	}
}

// Название открытой переписки стоит в верхней строке ровно тогда, когда панель
// нарисована справа (широкий терминал), и начинается с левого края этой панели —
// колонки стены плюс зазор. В компактном режиме переписка занимает весь экран и
// называет себя шапкой, поэтому в строке остаётся только подпись фильтра.
func TestTopBarTitleStandsOverPanelOnlyInWideMode(t *testing.T) {
	wide := newTestModel(t, 100, 30)
	wide.zoom = &wallZoom{title: "Рабочий чат"}
	title, start := wide.topBarTitle()
	if title != "Рабочий чат" || start != topBarTitleStart {
		t.Fatalf("широкий терминал: подпись справа = %q с колонки %d, ждали %q с колонки %d",
			title, start, "Рабочий чат", topBarTitleStart)
	}
	// Название начинается с той же колонки, что и панель: та и другая приходят от
	// одних и тех же двух чисел, разъехаться они не могут.
	if got, want := topBarTitleStart, wallPanelColumnWidth+wallPanelGapWidth; got != want {
		t.Fatalf("колонка названия = %d, ждали %d (колонка стены + зазор)", got, want)
	}
	row := ansi.Strip(wide.renderTopBar(100))
	if !strings.HasPrefix(row, filterLabelAll) {
		t.Fatalf("подпись фильтра на месте не оказалась: %q", row)
	}
	// Отсчёт от колонки названия — ПО ЯЧЕЙКАМ, а не по байтам: в строке есть
	// кириллица, и срез по байтам встал бы на середину названия.
	if got := strings.TrimSpace(ansi.TruncateLeft(row, topBarTitleStart, "")); got != "Рабочий чат" {
		t.Fatalf("с колонки %d строка = %q, ждали %q", topBarTitleStart, got, "Рабочий чат")
	}

	narrow := newTestModel(t, 60, 30)
	narrow.zoom = &wallZoom{title: "Рабочий чат"}
	if title, start := narrow.topBarTitle(); title != "" || start != 0 {
		t.Fatalf("узкий терминал: подпись справа = %q с колонки %d, ждали пустую", title, start)
	}
	if got := strings.TrimSpace(ansi.Strip(narrow.renderTopBar(60))); got != filterLabelAll {
		t.Fatalf("в узком режиме строка = %q, ждали только подпись фильтра", got)
	}
}

// Шапка переписки и её строка живут только в компактном режиме. В широком название
// ушло в верхнюю строку, и панель отдаёт шапке ноль строк — значит список
// сообщений занимает всю высоту панели и на сообщение становится на одну строку
// больше.
func TestZoomHeaderOnlyTakesRowInCompactMode(t *testing.T) {
	wide := newTestModel(t, 100, 30)
	wide.zoom = &wallZoom{title: "Рабочий чат"}
	if got := wide.zoomHeaderRows(); got != 0 {
		t.Fatalf("в широком режиме шапка заняла %d строк, ждали 0 (название в верхней строке)", got)
	}
	if got, want := wide.wallZoomListHeight(), wide.wallContentHeight(); got != want {
		t.Fatalf("список переписки — %d строк, ждали %d (вся панель)", got, want)
	}

	narrow := newTestModel(t, 60, 30)
	narrow.zoom = &wallZoom{title: "Рабочий чат"}
	if got := narrow.zoomHeaderRows(); got != wallZoomHeaderRows {
		t.Fatalf("в узком режиме шапка заняла %d строк, ждали %d", got, wallZoomHeaderRows)
	}
	if got, want := narrow.wallZoomListHeight(), narrow.wallContentHeight()-wallZoomHeaderRows; got != want {
		t.Fatalf("список переписки — %d строк, ждали %d (панель минус шапка)", got, want)
	}
}

// Обе подписи получают столько, сколько занимают, пока влезают в свои полосы. Не
// влезают — делят терминал пропорционально своим ширинам, а каждая всё равно не
// залезает в свою полосу: подпись фильтра не должна наезжать на панель, а название
// чата — вылезать за её правый край.
//
// Проверка по ЗНАЧЕНИЮ topBarWidths, а не по разобранной строке: разбор отрендеренной
// строки не отличил бы «обрезано по своей полосе» от «обрезано пропорционально»,
// если обе подписи попали бы в одну и ту же ячейку.
func TestTopBarWidthsSplitProportionalWhenBothDoNotFit(t *testing.T) {
	tests := []struct {
		name                    string
		width, anchor           int
		sourceWidth, titleWidth int
		wantSource, wantTitle   int
	}{
		// Полосы шире обеих подписей: берут свою естественную ширину, зазор между
		// ними добивается фоном.
		{name: "влезают обе: естественная ширина", width: 100, anchor: 31, sourceWidth: 3, titleWidth: 12,
			wantSource: 3, wantTitle: 12},
		{name: "влезают обе, левая почти во всю колонку: естественная ширина", width: 100, anchor: 31,
			sourceWidth: 20, titleWidth: 12, wantSource: 20, wantTitle: 12},
		// Вместе влезают, но в свои полосы — нет: каждая режется по своей.
		{name: "правая не влезает в полосу: обрезается до края терминала", width: 66, anchor: 31,
			sourceWidth: 3, titleWidth: 60, wantSource: 3, wantTitle: 35},
		{name: "левая не влезает в полосу: обрезается до колонки названия", width: 66, anchor: 31,
			sourceWidth: 40, titleWidth: 6, wantSource: 31, wantTitle: 6},
		// Не влезают обе: сначала делят терминал по своим ширинам (20 на 20 при
		// 40 и равных подписях), потом каждая ужимается до своей полосы.
		{name: "не влезают обе: делятся по ширинам, потом по полосам", width: 40, anchor: 31,
			sourceWidth: 20, titleWidth: 20, wantSource: 20, wantTitle: 9},
		// Пропорция срабатывает и там, где в полосу просилась бы вся строка: при 80
		// ячейках на две подписи по 30 напрашивалось бы по 35, но правая полоса
		// начинается с колонки 31 и заканчивается терминалом.
		{name: "правая делит терминал, но не выходит за свой край", width: 66, anchor: 31,
			sourceWidth: 50, titleWidth: 30, wantSource: 31, wantTitle: 24},
		// Левая пропорция видна отдельно от полосы: 10 и 70 ячеек на 66 делятся как
		// 8 и 58, и в полосу правая всё равно ужимается до 35.
		{name: "левая делит терминал по своей ширине", width: 66, anchor: 31,
			sourceWidth: 10, titleWidth: 70, wantSource: 8, wantTitle: 35},
		// Правой части нет по трём разным причинам — вся строка отдана подписи.
		{name: "названия нет: вся строка отдана подписи", width: 60, anchor: 0, sourceWidth: 3, titleWidth: 0,
			wantSource: 60, wantTitle: 0},
		{name: "колонки названия нет: подпись одна", width: 60, anchor: 0, sourceWidth: 3, titleWidth: 9,
			wantSource: 60, wantTitle: 0},
		{name: "названию негде начинаться: подпись одна", width: 20, anchor: 31, sourceWidth: 5, titleWidth: 5,
			wantSource: 20, wantTitle: 0},
		{name: "терминала нет: обе части нулевые", width: 0, anchor: 31, sourceWidth: 5, titleWidth: 5,
			wantSource: 0, wantTitle: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, title := topBarWidths(test.width, test.anchor, test.sourceWidth, test.titleWidth)
			if source != test.wantSource || title != test.wantTitle {
				t.Fatalf("полосы %d/%d, ждали %d/%d", source, title, test.wantSource, test.wantTitle)
			}
			// Ни одна полоса не выходит за свой край: подпись фильтра — за колонку
			// названия, название — за терминал.
			if source > test.anchor && test.anchor > 0 {
				t.Fatalf("полоса подписи %d залезла за колонку названия %d", source, test.anchor)
			}
			if test.width > 0 && test.titleWidth > 0 && test.anchor > 0 && test.anchor < test.width {
				if title > test.width-test.anchor {
					t.Fatalf("полоса названия %d шире своей полосы %d", title, test.width-test.anchor)
				}
			}
		})
	}
}

// Верхняя строка в трёхколоночном режиме называет ОБЕ панели переписки, и каждое
// название стоит по левому краю СВОЕЙ панели, а не прижато к краю терминала:
// панели стоят одна за другой, и название, прижатое к краю строки, оказалось бы
// над соседней панелью и читалось бы как заголовок чужого чата.
func TestTopBarNamesBothPanelsOverTheirOwnColumns(t *testing.T) {
	const width = 112
	m := newTestModel(t, width, 30)
	m.zoom = &wallZoom{title: "Первый чат"}
	m.zoomPinned = &wallZoom{title: "Второй чат"}

	first, second := m.wallPanelStart(panelChat1), m.wallPanelStart(panelChat2)
	if second <= first {
		t.Fatalf("колонки панелей %d и %d: вторая должна стоять правее первой", first, second)
	}
	parts := m.topBarParts()
	if len(parts) != 2 {
		t.Fatalf("верхняя строка называет %d переписок, ждали обе панели", len(parts))
	}
	// Названия идут в порядке панелей на экране, и каждое со своим якорем.
	if parts[0].text != "Первый чат" || parts[0].anchor != first {
		t.Fatalf("первое название = %q с колонки %d, ждели %q с колонки %d панели 1",
			parts[0].text, parts[0].anchor, "Первый чат", first)
	}
	if parts[1].text != "Второй чат" || parts[1].anchor != second {
		t.Fatalf("второе название = %q с колонки %d, ждали %q с колонки %d панели 2",
			parts[1].text, parts[1].anchor, "Второй чат", second)
	}

	row := ansi.Strip(m.renderTopBar(width))
	// Подпись фильтра осталась на своём месте: добавление второго названия не
	// сдвинуло её с нулевой колонки.
	if !strings.HasPrefix(row, filterLabelAll) {
		t.Fatalf("строка начинается с %q, ждали подпись фильтра с нулевой колонки", row)
	}
	if got := topBarSlice(row, first, second); got != "Первый чат" {
		t.Fatalf("колонки %d..%d строки = %q, ждали %q", first, second, got, "Первый чат")
	}
	if got := topBarSlice(row, second, width); got != "Второй чат" {
		t.Fatalf("колонки %d..%d строки = %q, ждали %q", second, width, got, "Второй чат")
	}
	// Не прижато к правому краю терминала: справа от названия второй панели
	// остаётся поле. Прижатое название стояло бы над краем своей же панели, а
	// читаться могло бы как подпись к чему-то за терминалом.
	if second+cellWidth("Второй чат") >= width {
		t.Fatalf("название второй панели кончается в колонке %d из %d: оно прижато к краю",
			second+cellWidth("Второй чат"), width)
	}
}

// Панель без открытой переписки названия не получает — ни своего, ни чужого: в
// колонке этой панели пусто, а переписки, которую она назвала бы, нет.
func TestTopBarNamesOnlyPanelsWithConversation(t *testing.T) {
	const width = 112
	tests := []struct {
		name   string
		zoom   *wallZoom
		pinned *wallZoom
	}{
		{name: "открыта только первая панель", zoom: &wallZoom{title: "Первый чат"}},
		{name: "открыта только вторая панель", pinned: &wallZoom{title: "Второй чат"}},
		{name: "не открыта ни одна панель"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			m := newTestModel(t, width, 30)
			m.zoom, m.zoomPinned = test.zoom, test.pinned

			row := ansi.Strip(m.renderTopBar(width))
			// Сверяется содержимое КАЖДОЙ колонки панели с тем, что в этой панели
			// открыто: пустой панели полагается пустая колонка, иначе название из
			// соседней колонки заехало бы на неё (и наоборот — чужое название в
			// пустой панели было бы выдумкой строки).
			for _, panel := range []wallPanel{panelChat1, panelChat2} {
				want := ""
				if zoom := m.panelZoom(panel); zoom != nil {
					want = zoom.title
				}
				start, end := m.wallPanelStart(panel), width
				if panel == panelChat1 {
					end = m.wallPanelStart(panelChat2)
				}
				if got := topBarSlice(row, start, end); got != want {
					t.Fatalf("колонки панели %d (%d..%d) строки = %q, ждали %q",
						int(panel), start, end, got, want)
				}
			}
		})
	}
}

// В узком режиме панелей переписки нет вовсе (переписка занимает весь экран и
// называет себя шапкой), поэтому верхняя строка не называет ничего — даже когда
// открыты обе переписки: панелей на экране нет, ставить названиям не над чем.
func TestTopBarNamesNothingWhenThereAreNoPanels(t *testing.T) {
	const width = 60
	m := newTestModel(t, width, 30)
	m.zoom = &wallZoom{title: "Первый чат"}
	m.zoomPinned = &wallZoom{title: "Второй чат"}

	if parts := m.topBarParts(); len(parts) != 0 {
		t.Fatalf("узкий терминал: верхняя строка называет %v, ждали пустого", parts)
	}
	if got := strings.TrimSpace(ansi.Strip(m.renderTopBar(width))); got != filterLabelAll {
		t.Fatalf("строка = %q, ждали только подпись фильтра", got)
	}
}

// Два названия в строке делят терминал по тем же правилам, что подпись фильтра и
// одно название: влезают — берут естественную ширину, не влезают — делят терминал
// пропорционально своим ширинам. Плюс своё, чего у одной подписи не было: полоса
// названия заканчивается колонкой СЛЕДУЮЩЕЙ панели, а не краем терминала, иначе
// длинное название первого чата уехало бы на панель второго.
//
// Числа в таблице посчитаны для геометрии панелей при такой ширине, и она же
// проверяется отдельно: при смене геометрии тест должен падать с понятным
// сообщением, а не с невнятными числами.
func TestTopBarPartsWidthsGiveEachTitleItsOwnPanelStrip(t *testing.T) {
	const width = 112
	m := newTestModel(t, width, 30)
	first, second := m.wallPanelStart(panelChat1), m.wallPanelStart(panelChat2)
	// 112: зона переписки = 112 − 30 − 2 зазора − 2 поля справа = 78, панели по 39.
	// Панель 2 начинается с 31 + 39 + 1 = 71.
	if first != 31 || second != 71 {
		t.Fatalf("колонки панелей при %d — %d и %d, а расчёт полос в таблице ждёт 31 и 71", width, first, second)
	}
	anchors := []int{first, second}

	tests := []struct {
		name        string
		sourceWidth int
		titles      []int
		wantSource  int
		wantTitles  []int
	}{
		{name: "названий нет: вся строка отдана подписи фильтра", sourceWidth: 3, wantSource: width},
		{name: "влезают все: естественные ширины", sourceWidth: 3, titles: []int{10, 10},
			wantSource: 3, wantTitles: []int{10, 10}},
		{name: "название не влезает в свою полосу: обрезается до колонки соседа",
			sourceWidth: 3, titles: []int{50, 10}, wantSource: 3, wantTitles: []int{40, 10}},
		{name: "оба названия не влезают в свои полосы: каждое по своей",
			sourceWidth: 3, titles: []int{50, 50}, wantSource: 3, wantTitles: []int{40, 41}},
		// Не влезает подпись фильтра: терминал делится пропорционально (30 на 130
		// от 112 — это 25), а названия всё равно ужимаются до своих полос.
		{name: "подпись фильтра делит терминал пропорционально", sourceWidth: 30, titles: []int{50, 50},
			wantSource: 25, wantTitles: []int{40, 41}},
		// Оба названия огромные: подпись фильтра остаётся, но ужимается сильнее
		// всех (20 на 420 от 112 — это 5), иначе длинный заголовок чата вытеснил
		// бы из строки то, что на стене настроено.
		{name: "названия шире терминала: подпись фильтра ужимается", sourceWidth: 20,
			titles: []int{200, 200}, wantSource: 5, wantTitles: []int{40, 41}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parts := make([]topBarPart, 0, len(test.titles))
			for index, titleWidth := range test.titles {
				parts = append(parts, topBarPart{text: topBarText(titleWidth), anchor: anchors[index]})
			}
			source, titles := topBarPartsWidths(width, test.sourceWidth, parts)
			if source != test.wantSource {
				t.Fatalf("полоса подписи фильтра = %d, ждали %d", source, test.wantSource)
			}
			if len(titles) != len(test.wantTitles) {
				t.Fatalf("полос названий %v, ждали %v", titles, test.wantTitles)
			}
			for index, want := range test.wantTitles {
				if titles[index] != want {
					t.Fatalf("полоса названия %d = %d, ждали %d", index+1, titles[index], want)
				}
				// Ни одно название не заезжает на колонку соседней панели: его
				// полоса заканчивается там, где начинается следующая.
				band := width - anchors[index]
				if index+1 < len(titles) {
					band = anchors[index+1] - anchors[index]
				}
				if titles[index] > band {
					t.Fatalf("название %d заняло %d ячеек, а его полоса — %d",
						index+1, titles[index], band)
				}
			}
		})
	}
}

// Одно название и список из одного названия — одно и то же правило, и полосы
// должны совпадать ячейка в ячейку. Иначе одно и то же название на одном и том же
// терминале обрезалось бы по-разному в зависимости от того, сколько панелей
// нарисовано, а человек ничего бы об этом не узнал.
func TestTopBarPartsWidthsMatchTwoPartRuleForSingleTitle(t *testing.T) {
	for _, test := range []struct {
		width, anchor, sourceWidth, titleWidth int
	}{
		{width: 100, anchor: 31, sourceWidth: 3, titleWidth: 12},
		{width: 100, anchor: 31, sourceWidth: 20, titleWidth: 12},
		{width: 66, anchor: 31, sourceWidth: 3, titleWidth: 60},
		{width: 66, anchor: 31, sourceWidth: 40, titleWidth: 6},
		{width: 40, anchor: 31, sourceWidth: 20, titleWidth: 20},
		{width: 60, anchor: 0, sourceWidth: 3, titleWidth: 9},
		{width: 20, anchor: 31, sourceWidth: 5, titleWidth: 5},
		{width: 0, anchor: 31, sourceWidth: 5, titleWidth: 5},
	} {
		source, title := topBarWidths(test.width, test.anchor, test.sourceWidth, test.titleWidth)
		gotSource, titles := topBarPartsWidths(test.width, test.sourceWidth,
			[]topBarPart{{text: topBarText(test.titleWidth), anchor: test.anchor}})
		if gotSource != source || len(titles) != 1 || titles[0] != title {
			t.Fatalf("ширина %d, якорь %d, подписи %d и %d: полосы %d/%v, а у topBarWidths %d/%d",
				test.width, test.anchor, test.sourceWidth, test.titleWidth, gotSource, titles, source, title)
		}
	}
}

// Два длинных названия на одной строке: каждое обрезается многоточием в своей
// полосе, до колонки соседней панели, а подпись фильтра остаётся. Без обрезки
// первое название уехало бы на панель второго и читалось бы как его заголовок.
func TestTopBarCutsBothTitlesInsideTheirOwnPanels(t *testing.T) {
	const width = 112
	m := newTestModel(t, width, 30)
	m.zoom = &wallZoom{title: topBarText(45)}
	m.zoomPinned = &wallZoom{title: topBarText(45)}

	row := ansi.Strip(m.renderTopBar(width))
	if !strings.HasPrefix(row, filterLabelAll) {
		t.Fatalf("строка начинается с %q, ждали подпись фильтра с нулевой колонки", row)
	}
	for _, panel := range []wallPanel{panelChat1, panelChat2} {
		start, end := m.wallPanelStart(panel), width
		if panel == panelChat1 {
			end = m.wallPanelStart(panelChat2)
		}
		got := topBarSlice(row, start, end)
		if !strings.HasSuffix(got, ellipsis) {
			t.Fatalf("колонки %d..%d строки = %q, ждали обрезку многоточием", start, end, got)
		}
		if cellWidth(got) > end-start {
			t.Fatalf("название панели %d заняло %d ячеек, а её полоса — %d",
				int(panel), cellWidth(got), end-start)
		}
	}
}

// topBarSlice — кусок строки между двумя колонками ПО ЯЧЕЙКАМ, без краёв: в строке
// кириллица, и срез по байтам встал бы на середину названия, а края нужны пустыми
// только для проверки «здесь ничего нет».
func topBarSlice(row string, start, end int) string {
	return strings.TrimSpace(ansi.Truncate(ansi.TruncateLeft(row, start, ""), end-start, ""))
}

// topBarText — подпись заданной ширины в ячейках. Для проверок ширин важна сама
// длина, а не буквы, и «я» в одну ячейку — самый честный способ её задать.
func topBarText(cells int) string {
	return strings.Repeat("я", cells)
}

// Каркас экрана — не часть панельной раскладки широкого режима: и строка фильтра,
// и строка сообщений обязаны быть и на узком терминале, и на широком. Иначе
// сообщение пропадало бы ровно там, где стена помещается рядом с перепиской.
func TestTopBarAndNoticeRowExistOnNarrowAndWideTerminals(t *testing.T) {
	for _, size := range [][2]int{{40, 20}, {60, 30}, {100, 30}, {200, 40}} {
		m, _ := newTestModel(t, size[0], size[1]).ShowSystemNotice("Отправлено")
		lines := splitLines(m.renderScreen())

		for index := range wallTopBarRows {
			if lines[index] != m.renderTopBar(size[0]) {
				t.Fatalf("терминал %dx%d: строка %d — не верхняя строка:\n%q", size[0], size[1], index,
					ansi.Strip(lines[index]))
			}
		}
		if got := strings.TrimSpace(ansi.Strip(noticeLine(t, m))); got != "Отправлено" {
			t.Fatalf("терминал %dx%d: строка сообщений = %q, ждали текст сообщения", size[0], size[1], got)
		}
	}
}
