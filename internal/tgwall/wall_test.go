package tgwall

import (
	"image/color"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/auth"
)

func TestCardTitleByType(t *testing.T) {
	tests := []struct {
		name string
		card card
		want string
	}{
		{
			name: "канал без подписи автора",
			card: card{Type: cardChannel, Name: "Новости DevOps"},
			want: "Новости DevOps",
		},
		{
			name: "канал с подписью автора",
			card: card{Type: cardChannel, Name: "Новости DevOps", AuthorSignature: "Дмитрий"},
			want: "Новости DevOps — Дмитрий",
		},
		{
			name: "чат",
			card: card{Type: cardChat, Name: "Соседи по подъезду", Author: "Марина"},
			want: "Соседи по подъезду — Марина",
		},
		{
			name: "личное",
			card: card{Type: cardPersonal, Name: "Андрей", Author: "Андрей"},
			want: "Андрей",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := test.card.title(); got != test.want {
				t.Fatalf("заголовок = %q, ждали %q", got, test.want)
			}
		})
	}
}

func TestTruncateVisibleKeepsWidthAndEllipsis(t *testing.T) {
	tests := []struct {
		text  string
		width int
		want  string
	}{
		{text: "Новости DevOps", width: 20, want: "Новости DevOps"},
		{text: "Новости DevOps", width: 28, want: "Новости DevOps"},
		{text: "Соседи по подъезду — Марина", width: 10, want: "Соседи по…"},
		{text: "Купи хлеба", width: 3, want: "Ку…"},
		{text: "Купи хлеба", width: 1, want: "…"},
		{text: "Купи хлеба", width: 0, want: ""},
	}
	for _, test := range tests {
		got := truncateVisible(test.text, test.width)
		if got != test.want {
			t.Fatalf("truncateVisible(%q, %d) = %q, ждали %q", test.text, test.width, got, test.want)
		}
		if width := lipgloss.Width(got); width > test.width {
			t.Fatalf("обрезка шире запрошенной: %d > %d (%q)", width, test.width, got)
		}
	}
}

// Обрезка многоточием проверяется не только на строке заголовка: текст сообщения
// карточки обрезается по своей, гибкой ширине, и на узком окне он обязан влезть в
// колонку вместе с остальными сегментами строки.
func TestCardLineNeverExceedsWidth(t *testing.T) {
	item := card{
		Type: cardChannel,
		Name: "Новости DevOps",
		Time: "13:04",
		Text: strings.Repeat("очень длинный текст сообщения ", 6),
		Tag:  "#канал",
	}
	for _, width := range []int{120, 100, 80, 62, 60, 40, 20, 4, 1} {
		lines := renderCard(item, width, false)
		for index, line := range lines {
			if got := lipgloss.Width(line); got != width {
				t.Fatalf("ширина %d: строка %d имеет ширину %d:\n%q", width, index, got, line)
			}
		}
	}
}

// Карточка стены — всегда ровно две строки экрана, и выбранная в том числе:
// разворачивать больше нечего, читать целиком нужно на экране одного чата
// (задача 0155). Раньше выбранная разворачивалась на весь текст, и глазом было
// видно, где курсор; теперь единственный признак выбора — фон карточки, и
// проверено именно это: текст у выбранной и невыбранной совпадать ДОЛЖЕН — на
// ОБЕИХ строках.
func TestSelectedCardIsTwoLinesTooAndDiffersOnlyByBackground(t *testing.T) {
	item := testCards()[2] // длинный текст Андрея
	plain := renderCard(item, 100, false)
	selected := renderCard(item, 100, true)
	if len(plain) != 2 {
		t.Fatalf("невыбранная карточка — %d строк, ждали 2", len(plain))
	}
	if len(selected) != 2 {
		t.Fatalf("выбранная карточка — %d строк, ждали 2", len(selected))
	}
	for index := range plain {
		if got, want := ansi.Strip(selected[index]), ansi.Strip(plain[index]); got != want {
			t.Fatalf("строка %d выбранной карточки отличается от невыбранной не только фоном:\nвыбрана: %q\nобычная: %q", index, got, want)
		}
		// Фон при этом разный — иначе «выбрана ровно одна карточка» было бы нечем
		// показать вовсе. Проверяется на каждой строке: выделение, красящее одну
		// строку из двух, читалось бы как «карточка началась», а не «выбрана».
		if !strings.Contains(selected[index], backgroundSGR(t, PaletteBackgroundSelected)) {
			t.Fatalf("выбранная карточка, строка %d, не использует PaletteBackgroundSelected", index)
		}
	}
}

// Прямая проверка инварианта «карточка стены — две строки», а не через
// сценарии: раньше он проверялся только там, где карточку разворачивали, и
// многострочное сообщение в выбранной карточке тихо ломало сетку стены (текст
// печатался в несколько строк, карточки съезжали, следующая рисовалась поверх
// хвоста предыдущей). Ширина окна — от почти не помещающейся карточки до
// широкой: чем уже окно, тем короче колонка текста и тем вернее ломается всё,
// что зависит от высоты карточки.
func TestRenderCardIsAlwaysExactlyTwoLines(t *testing.T) {
	cards := append(testCards(),
		// Отчёт бота: настоящие переносы строк внутри одного сообщения — тот самый
		// случай, из-за которого карточка когда-то становилась многострочной.
		card{Type: cardChat, Name: "PAWTouch Admin Bot", Time: "10:27",
			Text: "Версия X-UI: 3.8.5\nХост: 0104f0783202\nIPv4: 172.18.0.2", Tag: "#чат"},
		// Пустое сообщение: не «нулевая строка», а всё равно обе.
		card{Type: cardPersonal, Name: "Настя", Time: "13:12", Text: "", Tag: "#личное"},
	)
	// Ширина 5 и уже — это «карточка не помещается вовсе» (renderCard честно
	// отдаёт nil), к высоте карточки это отношения не имеет.
	for _, width := range []int{6, 10, 20, 40, 62, 80, 100, 120, 200} {
		for _, item := range cards {
			for _, selected := range []bool{false, true} {
				lines := renderCard(item, width, selected)
				if len(lines) != 2 {
					t.Fatalf("ширина %d, карточка %q (выбрана=%v): %d строк, ждали 2: %q",
						width, item.Name, selected, len(lines), lines)
				}
			}
		}
	}
}

// columnOf — номер колонки, на которой в строке начинается подстрока. Сравниваются
// именно колонки, а не байтовые смещения: в строке карточки есть кириллица и
// символы «●»/«…», и байтовый индекс ничего бы не значил.
func columnOf(t *testing.T, plain, sub string) int {
	t.Helper()
	index := strings.Index(plain, sub)
	if index < 0 {
		t.Fatalf("в строке нет подстроки %q: %q", sub, plain)
	}
	return cellWidth(plain[:index])
}

// rightEdgeOf — колонка сразу за правым краем подстроки, то есть номер клетки
// последнего символа плюс один. Именно эта величина, а не колонка начала
// подстроки, означает «прижато к правому краю»: тег сам по себе выровнен по
// правому краю внутри своей колонки, поэтому текст разной длины начинается в
// разных колонках, а заканчивается всегда на одной.
func rightEdgeOf(t *testing.T, plain, sub string) int {
	t.Helper()
	index := strings.Index(plain, sub)
	if index < 0 {
		t.Fatalf("в строке нет подстроки %q: %q", sub, plain)
	}
	// Срез — по байтовой длине подстроки (len), а не по её видимой ширине:
	// «●» и «…» — многобайтовые, но одноклеточные символы.
	return cellWidth(plain[:index+len(sub)])
}

// Строка 1 карточки несёт заголовок, сведения об источнике и (маркер) блока
// тега, и больше ничего; строка 2 несёт текст сообщения с ВРЕМЕНЕМ в самом
// конце. Проверяется по колонкам, а не по подстрокам целиком, потому что
// именно колонки и есть контракт вёрстки.
//
// Контракт после задачи 0163 (время ушло со строки 1 в конец строки 2):
//   - времени на строке 1 нет вовсе;
//   - время стоит у правого края строки 2 у ЛЮБОЙ карточки, независимо от
//     длины текста, потому что это зарезервированный суффикс;
//   - маркер тега по-прежнему прижат к правому краю карточки.
func TestCardSecondLineHoldsTimeAtRightEdge(t *testing.T) {
	const width = 100
	// UnreadCount не ноль у обеих: маркер справа от заголовка нужен для проверки
	// его выравнивания, а на пустом непрочитанном его теперь нет вовсе
	// (точка-заглушка убрана, 2026-09-30).
	items := []card{
		{Type: cardChannel, Name: "Дом", Time: "13:04", Text: "привет", Tag: "#канал", UnreadCount: 3},
		{Type: cardChannel, Name: "Очень длинное название канала, которое не влезает в колонку", Time: "09:41", Text: "пока", Tag: "#канал", UnreadCount: 3},
	}
	wantRightEdge := width - cardMarginH
	for _, item := range items {
		lines := renderCard(item, width, false)
		headline, text := ansi.Strip(lines[0]), ansi.Strip(lines[1])

		// Времени на строке заголовка больше нет — там оно и было бы не к
		// месту, занимая место сведений об источнике.
		if strings.Contains(headline, item.Time) {
			t.Fatalf("карточка %q: время %q всё ещё на строке заголовка: %q", item.Name, item.Time, headline)
		}
		if !strings.Contains(text, item.Time) {
			t.Fatalf("карточка %q: время %q пропало со строки текста: %q", item.Name, item.Time, text)
		}
		// Время прижато к правому краю строки текста: за ним только поля
		// карточки, и оно заканчивается там же, где заканчивается маркер тега.
		if got := rightEdgeOf(t, text, item.Time); got != wantRightEdge {
			t.Fatalf("карточка %q: правый край времени = %d, ждали %d (правый край карточки)",
				item.Name, got, wantRightEdge)
		}
		if got := rightEdgeOf(t, headline, "[3]"); got != wantRightEdge {
			t.Fatalf("карточка %q: правый край маркера = %d, ждали %d (правый край карточки)",
				item.Name, got, wantRightEdge)
		}
	}
}

// Длинный текст и время не влезают в ширину вместе: обрезается ТЕКСТ, а время
// остаётся целым и видимым в конце строки. По прямому указанию человека
// (2026-09-30): «если текст вместе со временем не влезает в ширину карточки —
// обрезать ТЕКСТ короче, а не переносить время на свою строку».
func TestCardLongTextTruncatesButTimeNeverDoes(t *testing.T) {
	const width = 100
	item := card{Type: cardChannel, Name: "Дом", Time: "09:41",
		Text: strings.Repeat("очень длинный текст сообщения ", 5), Tag: "#канал"}
	lines := renderCard(item, width, false)
	headline, text := ansi.Strip(lines[0]), ansi.Strip(lines[1])

	if strings.Contains(headline, item.Time) {
		t.Fatalf("время %q на строке заголовка, а не в конце текста: %q", item.Time, headline)
	}
	if !strings.Contains(text, item.Time) {
		t.Fatalf("время %q пропало или обрезалось: %q", item.Time, text)
	}
	if got := rightEdgeOf(t, text, item.Time); got != width-cardMarginH {
		t.Fatalf("правый край времени = %d, ждали %d", got, width-cardMarginH)
	}
	// Текст обрезан по ширине, и об этом говорит многоточие обрезки — тот же
	// знак, что у collapsedText.
	if !strings.Contains(text, ellipsis) {
		t.Fatalf("длинный текст не обрезан многоточием: %q", text)
	}
	// Строка карточки занимает ровно ширину терминала: боковые поля, граница и
	// тело. Это общий инвариант отрисовки, и время в конце строки его не
	// нарушает.
	if got := cellWidth(text); got != width {
		t.Fatalf("строка текста шириной %d, ждали %d (вместе с полями карточки)", got, width)
	}
}

// Заголовок длиннее cardTitleInfoMaxWidth обрезается многоточием, а сведения об
// источнике остаются целыми и не съезжают за жёсткий потолок. Потолок 65
// зафиксирован человеком (2026-09-30) для блока «имя + [то, что стоит справа]»
// и после переезда времени на строку текста стал ограничивать «имя +
// сведения»: у личного диалога это «в сети», у группы и канала — счётчик
// участников.
func TestCardLongTitleTruncatesButInfoNeverDoes(t *testing.T) {
	const width = 200 // достаточно широко, чтобы потолок был именно cardTitleInfoMaxWidth, а не колонка тега
	cases := []struct {
		name string
		item card
		want string
	}{
		{
			name: "группа со счётчиком",
			item: card{Type: cardChat, Name: strings.Repeat("Очень длинное название чата ", 5),
				Time: "09:41", Text: "текст", Tag: "#чат", MemberCount: 1200},
			want: "1,2K",
		},
		{
			name: "личный со статусом",
			item: card{Type: cardPersonal, Name: strings.Repeat("Очень длинное имя собеседника ", 5),
				Time: "09:41", Text: "текст", Tag: "#личное",
				Status: auth.UserStatus{Kind: auth.UserStatusOnline}},
			want: "в сети",
		},
	}
	base := cardMarginH + cardBorderWidth
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			plain := ansi.Strip(renderCard(testCase.item, width, false)[0])
			if !strings.Contains(plain, testCase.want) {
				t.Fatalf("сведения %q пропали при длинном заголовке: %q", testCase.want, plain)
			}
			infoColumn := columnOf(t, plain, testCase.want)
			if end := infoColumn + cellWidth(testCase.want) - base; end > cardTitleInfoMaxWidth {
				t.Fatalf("сведения заканчиваются на смещении %d от начала блока — вышли за потолок %d (cardTitleInfoMaxWidth)",
					end, cardTitleInfoMaxWidth)
			}
			if strings.Contains(plain, testCase.item.Name) {
				t.Fatalf("длинный заголовок не обрезан, хотя должен был: %q", plain)
			}
		})
	}
}

// Личный чат без статуса и группа, чей счётчик ещё не пришёл, показывают ровно
// то же, что показывали до появления сведений: имя само по себе, без лишнего
// зазора после него. Зазор отличается от «имя + сведения» ровно на одну ячейку,
// и именно на неё имя съезжало бы влево у карточек без сведений.
func TestCardWithoutSourceInfoShowsNoStrayGap(t *testing.T) {
	const width = 100
	withInfo := card{Type: cardChat, Name: "Дом", Time: "13:04", Text: "привет",
		Tag: "#чат", MemberCount: 1200}
	withoutInfo := withInfo
	withoutInfo.MemberCount = 0

	got := columnOf(t, ansi.Strip(renderCard(withoutInfo, width, false)[0]), withoutInfo.Name)
	want := columnOf(t, ansi.Strip(renderCard(withInfo, width, false)[0]), withInfo.Name)
	if got != want {
		t.Fatalf("имя без сведений на колонке %d, ждали %d (как у карточки со сведениями)", got, want)
	}
}

// В узкой колонке рядом с панелью переписки (panelColumn) сведения об источнике
// показываются по тому же принципу ширины, что и на обычной стене — не по
// отдельному запрету (человек попросил вернуть их сюда, 2026-09-30; отдельный
// !panelColumn стоял здесь после ревью 0163, но протечка «короткое имя +
// сведения» тогда не была багом раскладки — сведения ПОМЕЩАЛИСЬ рядом с
// коротким именем корректно, просто это противоречило заявленному тогда
// решению «сведений в панели нет никогда»). Короткое имя оставляет место в
// бюджете — сведения видны; длинное имя съедает весь бюджет — fitLine обрезает
// сведения сам, тем же способом, что и везде в проекте.
//
// Ширина — та, по которой колонка стены реально рисуется рядом с панелью
// переписки: содержимое панели минус её контур (задача 0167).
func TestCardTitleInfoShowsInPanelColumnWhenNameLeavesRoom(t *testing.T) {
	short := card{Type: cardChannel, Name: "A", Time: "13:04", Text: "пост", Tag: "#канал", MemberCount: 45_000}
	lines := renderWallCard(short, wallPanelColumnWidth-wallPanelOutlineInset, true, false, true)
	if len(lines) == 0 {
		t.Fatal("карточка не отрисована")
	}
	plain := ansi.Strip(lines[0])
	info := cardTitleInfo(short)
	if info == "" {
		t.Fatal("подготовка: cardTitleInfo пуст для карточки с MemberCount")
	}
	if !strings.Contains(plain, info) {
		t.Fatalf("короткое имя оставляет место, но сведения (%q) не видны в узкой колонке: %q", info, plain)
	}
}

// Длинное имя НЕ вытесняет сведения (в отличие от того, что можно было бы
// подумать) — cardTitleText специально урезает бюджет ИМЕНИ на ширину
// сведений (`maxPrefix-cardSegmentGapWidth-infoWidth`), а не наоборот: сведения
// показываются всегда, пока они не пустые, имени просто достаётся меньше
// места. Тот же принцип, что и в компактном режиме с самой задачи 0163 —
// узкая колонка здесь ничем не отличается, кроме меньшего общего бюджета.
func TestCardTitleInfoAlwaysShowsEvenWithLongNameInPanelColumn(t *testing.T) {
	long := card{Type: cardChannel, Name: strings.Repeat("Очень длинное название канала ", 3),
		Time: "13:04", Text: "пост", Tag: "#канал", MemberCount: 45_000}
	lines := renderWallCard(long, wallPanelColumnWidth-wallPanelOutlineInset, true, false, true)
	if len(lines) == 0 {
		t.Fatal("карточка не отрисована")
	}
	plain := ansi.Strip(lines[0])
	info := cardTitleInfo(long)
	if info == "" {
		t.Fatal("подготовка: cardTitleInfo пуст для карточки с MemberCount")
	}
	if !strings.Contains(plain, info) {
		t.Fatalf("сведения (%q) обязаны остаться видны даже с длинным именем: %q", info, plain)
	}
}

// Текст тега («#канал» и т.п.) показывается только у ВЫБРАННОЙ карточки на
// терминале шириной <=65: на более широком экране это место со временем
// займёт боковая панель чата (задача 0155), и повторять тег на каждой строке
// стало избыточным — по прямому указанию человека (2026-09-30). Счётчик
// непрочитанных при этом виден ВСЕГДА, независимо от выбора и ширины — это
// отдельный сигнал (задача 0153), а не часть тега. Карточка нарочно с
// непрочитанными (не ноль): точки-заглушки для «непрочитанных нет» больше нет
// (2026-09-30, «не нравятся»), и проверять «маркер виден всегда» на пустом
// маркере было бы уже нечем.
func TestCardTagLabelShownOnlyWhenSelectedAndNarrow(t *testing.T) {
	item := card{Type: cardChannel, Name: "Дом", Time: "13:04", Text: "привет", Tag: "#канал", UnreadCount: 3}
	cases := []struct {
		name      string
		width     int
		selected  bool
		wantLabel bool
	}{
		{"не выбрана, узко", 60, false, false},
		{"не выбрана, широко", 100, false, false},
		{"выбрана, ровно 65", 65, true, true},
		{"выбрана, широко (100)", 100, true, false},
		{"выбрана, очень широко (120)", 120, true, false},
		{"выбрана, очень узко (40)", 40, true, true},
	}
	for _, tc := range cases {
		plain := ansi.Strip(renderCard(item, tc.width, tc.selected)[0])
		hasLabel := strings.Contains(plain, "#канал")
		if hasLabel != tc.wantLabel {
			t.Fatalf("%s: текст тега показан=%v, ждали %v: %q", tc.name, hasLabel, tc.wantLabel, plain)
		}
		if !strings.Contains(plain, "[3]") {
			t.Fatalf("%s: маркер непрочитанных пропал вместе с текстом тега: %q", tc.name, plain)
		}
	}
}

// Строка 1 начинается СРАЗУ за границей ┃, без отступа: человек попросил убрать
// лишний отступ слева от имени (2026-09-30) — раньше здесь была ещё одна ячейка
// пробела, симметричная строке 2, теперь её нет ни у одной из строк.
func TestCardFirstLineHasNoIndentAfterBorder(t *testing.T) {
	const width = 100
	item := card{Type: cardPersonal, Name: "Настя", Time: "13:12", Text: "текст", Tag: "#личное"}
	plain := ansi.Strip(renderCard(item, width, false)[0])
	indent := cardMarginH + cardBorderWidth
	if got := columnOf(t, plain, item.title()); got != indent {
		t.Fatalf("заголовок начинается на колонке %d, ждали %d (сразу за границей, без отступа)",
			got, indent)
	}
}

// Строка 2 — текст сообщения СРАЗУ за границей ┃, тем же принципом, что и
// строка 1 (без лишнего отступа, человек попросил его убрать, 2026-09-30):
// раньше здесь была своя ячейка отступа, симметричная строке 1, теперь нет ни
// у одной из строк — обе начинаются с одной и той же колонки.
func TestCardSecondLineHasNoIndentAfterBorder(t *testing.T) {
	const width = 100
	short := card{Type: cardPersonal, Name: "Настя", Time: "13:12", Text: "Купи хлеба", Tag: "#личное"}
	long := card{Type: cardChannel, Name: "Новости DevOps", Time: "13:04",
		Text: strings.Repeat("длинный текст ", 12), Tag: "#канал"}

	indent := cardMarginH + cardBorderWidth
	plain := ansi.Strip(renderCard(short, width, false)[1])
	if got := columnOf(t, plain, "Купи хлеба"); got != indent {
		t.Fatalf("текст начинается на колонке %d, ждали %d (сразу за границей, без отступа): %q",
			got, indent, plain)
	}
	// Короткий текст не добивается до ВРЕМЕНИ — между ними пустые клетки, и
	// это нормально: добрать текст до края было бы враньём, будто сообщение
	// длиннее. Время прижато к правому краю строки (задача 0163), поэтому
	// «дотянулся до края» теперь означает «дотянулся до времени».
	timeColumn := columnOf(t, plain, short.Time)
	textEnd := columnOf(t, plain, "Купи хлеба") + cellWidth("Купи хлеба")
	if textEnd >= timeColumn {
		t.Fatalf("короткий текст кончился на клетке %d, а время начинается на %d — текст добран до края",
			textEnd, timeColumn)
	}
	if got := cellAt(t, plain, timeColumn-1); got != " " {
		t.Fatalf("клетка %d перед временем = %q, ждали пробел", timeColumn-1, got)
	}

	// Длинный текст обрезан по ширине с многоточием последней своей клеткой —
	// то есть перед зазором и временем, а не у правого края карточки: время
	// стоит в конце строки и обрезает текст до себя (задача 0163).
	longPlain := ansi.Strip(renderCard(long, width, false)[1])
	longTimeColumn := columnOf(t, longPlain, long.Time)
	if got := cellAt(t, longPlain, longTimeColumn-cardSegmentGapWidth-1); got != ellipsis {
		t.Fatalf("клетка %d перед зазором и временем = %q, ждали многоточие обрезки: %q",
			longTimeColumn-cardSegmentGapWidth-1, got, longPlain)
	}
}

// Базовый цвет текста сообщения — приглушённый (PaletteTextMuted), а не основной:
// строка 2 вторична по отношению к строке 1, и полноценный белый текст под заголовком
// тянул бы на себя внимание сильнее, чем сам заголовок. Подсветка смысловых
// токенов поверх приглушённого цвета при этом сохраняется (см. highlight_test.go).
func TestCardTextUsesMutedBaseColor(t *testing.T) {
	item := card{Type: cardPersonal, Name: "Настя", Time: "13:12", Text: "просто слова", Tag: "#личное"}
	line := renderCard(item, 100, false)[1]
	if !strings.Contains(line, foregroundSGR(PaletteTextMuted, PaletteBackgroundMain)) {
		t.Fatalf("текст сообщения нарисован не приглушённым PaletteTextMuted: %q", line)
	}
	if strings.Contains(line, foregroundSGR(PaletteText, PaletteBackgroundMain)) {
		t.Fatalf("текст сообщения нарисован основным PaletteText — он должен быть приглушён: %q", line)
	}
}

// У ВЫБРАННОЙ карточки текст сообщения ярче (PaletteText), не приглушённый —
// по мокапу CompactCard.dc.html («фон светлее на обе строки сразу, текст
// ярче»): ревью задачи 0156 (реализация красила текст одинаково независимо от
// selected, сверка с мокапом эту деталь и поймала).
func TestCardTextIsBrighterWhenSelected(t *testing.T) {
	item := card{Type: cardPersonal, Name: "Настя", Time: "13:12", Text: "просто слова", Tag: "#личное"}
	line := renderCard(item, 100, true)[1]
	// Проверяется участок ДО времени: с переездом времени в конец строки
	// текста (задача 0163) время приглушено на этой строке ВСЕГДА, даже у
	// выбранной карточки, и иначе проверка «нет PaletteTextMuted в строке»
	// падала бы на цвете самого времени, а не на цвете текста.
	// Проверяется SGR непосредственно перед самим текстом: с переездом времени в
	// конец строки (задача 0163) время приглушено на этой строке ВСЕГДА, даже у
	// выбранной карточки, и проверка «в строке нет PaletteTextMuted» падала бы на
	// цвете самого времени, а не на цвете текста.
	beforeText := line[:strings.Index(line, "просто слова")]
	if !strings.HasSuffix(beforeText, foregroundSGR(PaletteText, PaletteBackgroundSelected)) {
		t.Fatalf("текст выбранной карточки не PaletteText: %q", line)
	}
	if strings.Contains(beforeText, foregroundSGR(PaletteTextMuted, PaletteBackgroundSelected)) {
		t.Fatalf("текст выбранной карточки остался приглушённым PaletteTextMuted: %q", line)
	}
	// Время на строке текста — подпись к сообщению, а не часть заголовка: на
	// строке заголовка оно было приглушённым у любой карточки, и после переезда
	// правило не изменилось.
	if !strings.Contains(line, foregroundSGR(PaletteTextMuted, PaletteBackgroundSelected)) {
		t.Fatalf("время на строке текста перестало быть приглушённым: %q", line)
	}
}

// Ни разделителя «│» между именем и временем, ни знака «есть продолжение» в
// карточке нет. Закреплено явно, чтобы их не вернули вместе с «улучшением»
// отрисовки: в макете CompactCard.dc.html разделителя нет вовсе, а знак «▶»
// жил ровно под однострочный формат (задачи 0154/0156).
func TestCardHasNoSeparatorAndNoMoreMarker(t *testing.T) {
	items := []card{
		{Type: cardChannel, Name: "Новости DevOps", Time: "13:04", Text: "одна строка", Tag: "#канал"},
		{Type: cardChat, Name: "PAWTouch Admin Bot", Time: "10:27",
			Text: "Версия X-UI: 3.8.5\nХост: 0104f0783202", Tag: "#чат"},
	}
	for _, item := range items {
		for index, line := range renderCard(item, 100, false) {
			plain := ansi.Strip(line)
			if strings.Contains(plain, "│") {
				t.Fatalf("карточка %q, строка %d: остался разделитель «│»: %q", item.Name, index, plain)
			}
			if strings.Contains(plain, "▶") {
				t.Fatalf("карточка %q, строка %d: остался знак «есть продолжение» «▶»: %q", item.Name, index, plain)
			}
		}
	}
}

// Левая граница ┃ стоит на ОБЕИХ строках карточки в любом состоянии. На одной
// строке она читалась бы как «граница есть только у заголовка», а не у
// карточки; на двух строках это единственный вертикальный элемент, связывающий
// их в одну карточку.
//
// Цвет границы — единое правило для всей стены (не только узкой колонки рядом
// с панелью переписки, человек обобщил решение 2026-09-30): акцентный цвет
// источника — только у ВЫДЕЛЕННОЙ карточки. У остальных граница красится в
// цвет ФОНА (невидима, человек попросил убрать «серые полоски», 2026-09-30) —
// не PaletteBorder, как было раньше сразу после первого решения: столбец
// границы остаётся зарезервированным по ширине, просто у невыделенных карточек
// в нём больше ничего не видно.
func TestCardAccentBorderOnBothLines(t *testing.T) {
	for _, item := range testCards() {
		accentBorder := foregroundSGR(item.accent(), PaletteBackgroundSelected) + "┃"
		mutedBorder := foregroundSGR(PaletteBackgroundMain, PaletteBackgroundMain) + "┃"
		column := cardMarginH

		for index, line := range renderCard(item, 100, true) {
			if got := cellAt(t, ansi.Strip(line), column); got != "┃" {
				t.Fatalf("выделена, карточка %q, строка %d: в колонке %d = %q, ждена левая граница", item.Name, index, column, got)
			}
			if !strings.Contains(line, accentBorder) {
				t.Fatalf("выделена, карточка %q, строка %d: граница нарисована не акцентным цветом источника: %q", item.Name, index, line)
			}
		}
		for index, line := range renderCard(item, 100, false) {
			if got := cellAt(t, ansi.Strip(line), column); got != "┃" {
				t.Fatalf("не выделена, карточка %q, строка %d: в колонке %d = %q, ждена левая граница", item.Name, index, column, got)
			}
			if !strings.Contains(line, mutedBorder) {
				t.Fatalf("не выделена, карточка %q, строка %d: граница нарисована не приглушённым цветом: %q", item.Name, index, line)
			}
		}
	}
}

// Знака разворачивания на строках карточки больше нет: разворачивать нечего
// (карточка всегда одной высоты), и знак, переключающийся по выбору, был бы враньём
// о поведении, которого нет. Закреплено явно, чтобы его не вернули вместе с
// «улучшением» отрисовки.
//
// Проверяется началом КАЖДОЙ строки (поля, граница, сразу содержимое — колонки
// стрелки нет) и отсутствием «▼» где-либо.
func TestCardLineHasNoExpandArrow(t *testing.T) {
	item := card{Type: cardChannel, Name: "Новости DevOps", Time: "13:04",
		Text: "первая строка\nвторая строка", Tag: "#канал"}
	for _, selected := range []bool{false, true} {
		for index, line := range renderCard(item, 100, selected) {
			plain := ansi.Strip(line)
			if !strings.HasPrefix(plain, "  ┃") {
				t.Fatalf("выбрана=%v, строка %d: начинается с %q, ждали поля, границу и сразу содержимое (колонки стрелки нет)",
					selected, index, plain[:min(8, len(plain))])
			}
			if strings.Contains(plain, "▼") {
				t.Fatalf("выбрана=%v, строка %d: остался знак разворачивания «▼»: %q", selected, index, plain)
			}
		}
	}
}

// cellAt — видимая клетка строки по колонке. Все символы в тестовых карточках
// одноклеточные (кириллица, «▶», «●»), поэтому руны и есть клетки; lipgloss.Width
// понадобился бы только для символов двойной ширины.
func cellAt(t *testing.T, plain string, column int) string {
	t.Helper()
	runes := []rune(plain)
	if column < 0 || column >= len(runes) {
		t.Fatalf("колонка %d за пределами строки из %d клеток: %q", column, len(runes), plain)
	}
	return string(runes[column])
}

// widthUpToEllipsis — сколько клеток занято непустым текстом от column до
// многоточия обрезки ВКЛЮЧИТЕЛЬНО.
//
// Так измеряется видимая ширина обрезанного имени. «До первого пробела» здесь
// не годится: название вполне может содержать пробелы («Новости DevOps»), и
// такая мера обрезала бы его посередине. Многоточие, наоборот, надёжно: это
// единственный знак, которым вёрстка помечает «здесь имя кончилось».
func widthUpToEllipsis(t *testing.T, plain string, column int) int {
	t.Helper()
	cut := ansi.Cut(plain, column, len([]rune(plain)))
	index := strings.Index(cut, ellipsis)
	if index < 0 {
		t.Fatalf("в строке нет многоточия обрезки, хотя имя длиннее бюджета: %q", plain)
	}
	return cellWidth(cut[:index]) + cellWidth(ellipsis)
}

// Настоящий перенос строки в исходном тексте — граница строки в выводе, а не
// пробел: раньше strings.Fields сразу на всём тексте стирал эту границу, и
// абзацы отчёта бота сливались в один сплошной поток слов (находка человека
// вживую — «вернуть структуру абзацев»).
func TestWrapVisibleKeepsParagraphBreaks(t *testing.T) {
	got := wrapVisible("первый абзац\nвторой абзац", 40)
	want := []string{"первый абзац", "второй абзац"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wrapVisible = %#v, ждали %#v", got, want)
	}
}

// Пустая строка между абзацами (два "\n" подряд) — это тоже строка вывода
// (пустая), а не «ничего»: так сохраняется намеренный отступ между разделами
// исходного сообщения.
func TestWrapVisibleKeepsBlankLineBetweenParagraphs(t *testing.T) {
	got := wrapVisible("раздел один\n\nраздел два", 40)
	want := []string{"раздел один", "", "раздел два"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wrapVisible = %#v, ждали %#v", got, want)
	}
}

// Один длинный абзац по-прежнему переносится по словам внутри себя — перенос
// по абзацам не должен сломать перенос по ширине там, где абзацев вообще нет.
func TestWrapVisibleStillWrapsLongParagraphByWords(t *testing.T) {
	got := wrapVisible("слово слово слово слово", 11)
	want := []string{"слово слово", "слово слово"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wrapVisible = %#v, ждали %#v", got, want)
	}
}

// Слово длиннее строки режется принудительно, а не вылезает за карточку: без
// этого одна длинная ссылка или хеш растянули бы строку и сломали сетку.
//
// После задачи 0154 на стене этим путём уже ничто не ходит (карточка всегда одна
// строка), но поведение осталось в пакете для экрана одного чата (задача 0155) —
// поэтому проверяется напрямую, а не через карточку: иначе тест молча выродился бы
// в проверку того, что вызывающего кода нет.
//
// Проверяется ТОЛЬКО ширина, а не точный набор строк: принудительный разрез
// режет слово по БАЙТАМ и может разорвать многобайтовый символ пополам (найдено
// при написании этого теста, см. REPORT.md, задача 0154). Пока это не исправлено,
// закреплять точный вывод тестом нельзя — тест зафиксировал бы невалидный UTF-8
// как «правильный» результат.
func TestWrapVisibleCutsWordLongerThanWidth(t *testing.T) {
	got := wrapVisible("аб https://example.com/очень/длинный/путь/здесь конец", 10)
	if len(got) < 2 {
		t.Fatalf("длинное слово не было разрезано: %#v", got)
	}
	for index, line := range got {
		if width := cellWidth(line); width > 10 {
			t.Fatalf("строка %d шире 10 ячеек (%d): %q", index, width, line)
		}
		// Найдено при ревью 0154: принудительный разрез резал по БАЙТОВОМУ
		// индексу, совпадающему с числом ячеек, — многобайтовая руна
		// (кириллица в этом же тексте) могла попасть под разрез пополам,
		// и строка превращалась в невалидный UTF-8. До этой задачи баг был
		// невидим (на стене принудительный разрез не проходил вовсе,
		// карточка разворачивалась в несколько строк только целиком), но
		// станет видимым на экране одного чата (задача 0155), где это же
		// принудительное разрезание — основной путь для длинных слов.
		if !utf8.ValidString(line) {
			t.Fatalf("строка %d — невалидный UTF-8 (руна разрезана пополам): %q", index, line)
		}
	}
}

// collapsedText: сообщение из одной строки без обрезки по ширине — как есть,
// без добавленного многоточия (обрезки не было вовсе, ни по абзацам, ни по
// ширине).
func TestCollapsedTextSingleLineFitsAsIs(t *testing.T) {
	if got := collapsedText("короткий текст", 40); got != "короткий текст" {
		t.Fatalf("collapsedText = %q, ждали текст без изменений", got)
	}
}

// collapsedText: у сообщения есть ещё абзацы (настоящий перенос строки) — это
// тоже «не поместилось», и получает «…», даже если первая строка сама по себе
// уместилась бы в ширину без обрезки.
func TestCollapsedTextMarksMoreParagraphsWithEllipsis(t *testing.T) {
	got := collapsedText("первая строка\nвторая строка", 40)
	if got != "первая строка…" {
		t.Fatalf("collapsedText = %q, ждали %q", got, "первая строка…")
	}
}

// collapsedText: первая строка сама длиннее ширины — обрезка по ширине даёт
// многоточие тем же порядком, что и раньше (без абзацев), второй абзац
// дополнительного многоточия не добавляет (оно уже есть от обрезки по ширине).
func TestCollapsedTextWidthTruncationTakesPriority(t *testing.T) {
	got := collapsedText("очень длинная первая строка сообщения\nвторая", 10)
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("collapsedText = %q, ждали окончание на «…»", got)
	}
	if strings.Count(got, "…") != 1 {
		t.Fatalf("collapsedText = %q, ждали ровно одно «…», а не два", got)
	}
}

// backgroundSGR — параметры SGR, которыми renderedFill красит клетку в этот
// фон. Используется как отпечаток цвета: сравнивать строки целиком неудобно, а
// вот найти в них ровно эти параметры — надёжный способ проверить, что фон
// именно тот, что ожидался, а не «какой-то тёмный».
//
// Префикс CSI («\x1b[») намеренно отбрасывается: lipgloss объединяет цвет и
// атрибуты в ОДНОЙ последовательности (например «\x1b[1;38;2;…;48;2;34;34;34m»),
// и фоновые параметры оказываются в её хвосте. Сравнение с префиксом искало бы
// подстроку, которой в реальной строке нет — и тест падал бы на вполне
// корректной отрисовке.
func backgroundSGR(t *testing.T, background color.Color) string {
	t.Helper()
	rendered := renderedFill(1, background)
	const prefix = "\x1b["
	start := strings.Index(rendered, prefix)
	index := strings.IndexByte(rendered, 'm')
	if start < 0 || index < start {
		t.Fatalf("renderedFill не дал SGR-пролог: %q", rendered)
	}
	return rendered[start+len(prefix) : index+1]
}

// Невыделенная карточка заливается тем же фоном, что и весь экран
// (PaletteBackgroundMain), и потому визуально сливается с ним — тип виден по
// цвету левой границы, а не по отдельному «боксу» вокруг каждой строки. Раньше
// все строки красились в один и тот же фон панели, и выделенную было не сразу
// отличить (человек заметил это вживую, не по мокапу — мокап на бумаге выглядел
// иначе). Единственный фон, зарезервированный под выделение, —
// PaletteBackgroundSelected, и невыделенная карточка его не использует.
func TestUnselectedCardUsesScreenBackground(t *testing.T) {
	line := renderCard(testCards()[0], 100, false)[0]
	if column, _ := lineWithoutBackground(line, 0, 0); column >= 0 {
		t.Fatalf("невыделенная карточка: ячейка %d нарисована без фона, а фон должен быть везде: %q", column, line)
	}
	if !strings.Contains(line, backgroundSGR(t, PaletteBackgroundMain)) {
		t.Fatalf("невыделенная карточка не залита фоном экрана (PaletteBackgroundMain): %q", line)
	}
	if strings.Contains(line, backgroundSGR(t, PaletteBackgroundSelected)) {
		t.Fatalf("невыделенная карточка красится в PaletteBackgroundSelected — этот цвет зарезервирован только под выделение")
	}
}

// Выделенная карточка красится в PaletteBackgroundSelected — значение человек
// подобрал по живой проверке в реальном терминале (#333333, светлее и фона
// экрана, и фона панели поля ввода), разница с невыделенной обязана быть
// заметной сразу, без сравнения оттенков.
func TestSelectedCardStandsOutWithSelectedBackground(t *testing.T) {
	line := renderCard(testCards()[0], 100, true)[0]
	want := backgroundSGR(t, PaletteBackgroundSelected)
	if !strings.Contains(line, want) {
		t.Fatalf("выделенная карточка не использует PaletteBackgroundSelected:\nSGR=%q\nстрока=%q", want, line)
	}
}

// Блок тега прижат к правому краю карточки: текст тега, два пробела, цветной
// счётчик у самого правого края (по прямому указанию человека — маркер стоит
// последним, не первым). ПРАВЫЙ КРАЙ МАРКЕРА — это и есть правый край
// карточки, у любой карточки независимо от длины сообщения. Раньше короткое
// сообщение не добивалось пробелами до колонки тега, и тег у него оказывался
// не у правого края, а сразу после текста (человек заметил это вживую).
// UnreadCount не ноль у обеих карточек: на пустом непрочитанном маркера нет
// вовсе (точка-заглушка убрана, 2026-09-30), выравнивать было бы нечего.
func TestCardTagRightEdgeAlignsWithCardRightEdge(t *testing.T) {
	width := 100
	short := renderCard(card{Type: cardPersonal, Name: "Настя", Time: "13:12", Text: "Купи хлеба", Tag: "#личное", UnreadCount: 3}, width, false)[0]
	long := renderCard(card{Type: cardChannel, Name: "Новости DevOps", Time: "13:04", Text: strings.Repeat("длинный текст ", 6), Tag: "#канал", UnreadCount: 3}, width, false)[0]

	rightEdge := func(line string) int {
		plain := ansi.Strip(line)
		index := strings.LastIndex(plain, "[3]")
		if index < 0 {
			t.Fatalf("нет маркера тега в строке: %q", plain)
		}
		return lipgloss.Width(plain[:index+len("[3]")])
	}

	wantEdge := width - cardMarginH
	if got := rightEdge(short); got != wantEdge {
		t.Fatalf("короткое сообщение: правый край точки = %d, ждали %d (правый край карточки)", got, wantEdge)
	}
	if got := rightEdge(long); got != wantEdge {
		t.Fatalf("длинное сообщение: правый край точки = %d, ждали %d (правый край карточки)", got, wantEdge)
	}
}

// В любой момент выбрана ровно одна карточка — та, на которой стоит курсор.
func TestNavigationMovesCursorAndClamps(t *testing.T) {
	m := newTestModel(t, 100, 30)

	// Курсор при старте — на последней карточке: стена отсортирована по дате
	// (старые сверху), и открывать её с самой старой значило бы выделить
	// сообщение, которого на экране нет (см. задачу 0149).
	if m.cursor != len(m.cards)-1 {
		t.Fatalf("при старте выбрана карточка %d, ждали %d", m.cursor, len(m.cards)-1)
	}
	for range len(m.cards) + 3 {
		assertExactlyOneSelected(t, m)
		m = m.moveCursor(1)
	}
	if m.cursor != len(m.cards)-1 {
		t.Fatalf("после зажатия вниз курсор = %d, ждали %d", m.cursor, len(m.cards)-1)
	}
	for range len(m.cards) + 3 {
		assertExactlyOneSelected(t, m)
		m = m.moveCursor(-1)
	}
	if m.cursor != 0 {
		t.Fatalf("после зажатия вверх курсор = %d, ждали 0", m.cursor)
	}
}

// Стрелки двигают курсор по карточкам. Стена открыта с последней карточки
// (задача 0149), поэтому ↓ упирается в конец потока, а каждое ↑ уводит на одну
// карточку выше.
func TestArrowKeysMoveCursor(t *testing.T) {
	m := newTestModel(t, 100, 30)
	last := len(m.cards) - 1

	down, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m = down.(Model)
	if m.cursor != last {
		t.Fatalf("↓ на последней карточке увёл курсор на %d, ждали зажим на %d", m.cursor, last)
	}
	for want := last - 1; ; want-- {
		up, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyUp})
		m = up.(Model)
		if m.cursor != want {
			t.Fatalf("после ↑ курсор = %d, ждали %d", m.cursor, want)
		}
		if want == 0 {
			break
		}
	}
}

// assertExactlyOneSelected — на экране ровно одна выбранная карточка, то есть
// ровно один блок строк, покрашенных в PaletteBackgroundSelected.
//
// Раньше здесь считался знак «▼», но он означал «карточка развёрнута», и после
// задачи 0154 разворачивать нечего: выбранная карточка отличается от остальных
// только фоном. Считать надо именно фон — иначе проверка «ровно одна выбрана»
// незаметно превратилась бы в проверку высоты карточки.
//
// Считаются не строки, а НАЧАЛА карточек: карточка занимает две строки (задача
// 0156), и построчный счётчик дал бы «выбрано 2» на единственной выбранной
// карточке. Начало карточки — покрашенная строка, перед которой либо ничего нет,
// либо строка не покрашена; вторая строка той же карточки перед собой имеет
// покрашенную, поэтому за новую карточку не считается. Так проверка не завязана
// на высоту карточки и переживёт следующий формат стены.
func assertExactlyOneSelected(t *testing.T, m Model) {
	t.Helper()
	selected := backgroundSGR(t, PaletteBackgroundSelected)
	count := 0
	previousSelected := false
	for _, line := range strings.Split(m.renderScreen(), "\n") {
		isSelected := strings.Contains(line, selected)
		if isSelected && !previousSelected {
			count++
		}
		previousSelected = isSelected
	}
	if count != 1 {
		t.Fatalf("выбранных карточек на экране %d, ждали 1 (курсор %d)", count, m.cursor)
	}
}
