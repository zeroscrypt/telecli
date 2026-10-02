package tgwall

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"telecli/internal/auth"
)

// Галочки прочтения у своих сообщений (задача 0169, часть A) и маркер курсора
// слева от сообщения (часть B) — два независимых сигнала одного блока
// сообщения. Оба рисуются по карточке переписки, оба проверяются здесь на
// настоящей отрисовке, а не на вычислении: иначе проверка была бы про
// собственное намерение кода, а не про то, что человек увидит.

// zoomCheckCases — четыре состояния, различать которые обязана галочка:
// прочитано/не прочитано, своё/входящее.
type zoomCheckCase struct {
	name      string
	message   auth.Message
	readOutID int64
	want      string
}

func zoomCheckCases() []zoomCheckCase {
	return []zoomCheckCase{
		{
			name:      "своё прочитанное",
			message:   auth.Message{ID: 10, Text: "ок", Date: 1304, IsOutgoing: true},
			readOutID: 10,
			want:      zoomReadCheckRead,
		},
		{
			name:      "своё непрочитанное",
			message:   auth.Message{ID: 10, Text: "ок", Date: 1304, IsOutgoing: true},
			readOutID: 5,
			want:      zoomReadCheckSent,
		},
		{
			name:      "входящее",
			message:   auth.Message{ID: 10, Text: "ок", Date: 1304},
			readOutID: 10,
			want:      "",
		},
		{
			// lastReadOutbox == 0 — чата нет в снимке (или TDLib его не знает).
			// Трактуется как «не прочитано»: вслепую «прочитано» не рисуется.
			name:      "своё без снимка прочтения",
			message:   auth.Message{ID: 10, Text: "ок", Date: 1304, IsOutgoing: true},
			readOutID: 0,
			want:      zoomReadCheckSent,
		},
		{
			// У сообщения нет даже собственного id (такое бывает в тестовых
			// фикстурах — см. те же поля MessageID/ChatID на card, где нулевой
			// id значит «неизвестно»). Сравнение 0 <= 0 без проверки на ноль
			// дало бы «прочитано» вслепую.
			name:      "своё без id и без снимка прочтения",
			message:   auth.Message{Text: "ок", Date: 1304, IsOutgoing: true},
			readOutID: 0,
			want:      zoomReadCheckSent,
		},
	}
}

// Галочка стоит СРАЗУ справа от времени, у самого правого края строки, и её
// вид выводится из прочтения СОБЕСЕДНИКОМ (id сообщения не выше
// LastReadOutboxMessageID). У входящего галочек нет вовсе — это признак своего
// сообщения, и входящему прочтение чужого неотносимо.
func TestZoomReadCheckFollowsPeerReadState(t *testing.T) {
	const width = 50
	inner := width - 2*cardMarginH
	for _, testCase := range zoomCheckCases() {
		t.Run(testCase.name, func(t *testing.T) {
			item := zoomCard(testCase.message, testCase.readOutID)
			lines := renderZoomMessage(item, width, false)
			plain := ansi.Strip(lines[len(lines)-1])
			time := auth.FormatMessageTime(testCase.message.Date)

			// Ширина строки проверяется у ВСЕХ состояний, включая входящее: строка
			// времени обязана занимать ровно зону при любом состоянии прочтения, и
			// забытая галочка в расчёте добивки уехала бы именно сюда — в
			// недостающие ячейки, а не в лишние.
			if got := cellWidth(plain); got != width {
				t.Fatalf("строка шириной %d, ждали %d:\n%q", got, width, plain)
			}
			if testCase.want == "" {
				if strings.Contains(plain, zoomReadCheckSent) {
					t.Fatalf("у входящего появилась галочка прочтения: %q", plain)
				}
				return
			}
			if !strings.Contains(plain, testCase.want) {
				t.Fatalf("строка времени %q не несёт галочку %q", plain, testCase.want)
			}
			// Галочка — справа от времени, и хвост «время + зазор + галочка»
			// прижат к ПРАВОМУ краю зоны: ровно там, где раньше заканчивалось
			// одно время. Это и есть требование «как в оригинальном Telegram».
			wantEdge := cardMarginH + inner
			if got := rightEdgeOf(t, plain, testCase.want); got != wantEdge {
				t.Fatalf("правый край галочки %d, ждали %d:\n%q", got, wantEdge, plain)
			}
			// Зазор между временем и галочкой — ровно одна ячейка, и время
			// осталось ПЕРЕД ней, а не после (иначе строка выглядела бы
			// наоборот прочитанной).
			gapColumn := columnOf(t, plain, testCase.want)
			timeEnd := rightEdgeOf(t, plain, time)
			if gapColumn-timeEnd != cardSegmentGapWidth {
				t.Fatalf("зазор между временем и галочкой = %d, ждали %d:\n%q",
					gapColumn-timeEnd, cardSegmentGapWidth, plain)
			}
		})
	}
}

// zoomCard сам по себе кладёт на карточку ГОТОВЫЙ признак прочтения, а не id и
// не ссылку на чат: блок сообщения рисуется по карточке и никаких других
// данных о чате не имеет. Проверяется напрямую по конструктору, чтобы ошибка
// «поле заполняется, но не тем» не спряталась за отрисовкой.
func TestZoomCardStoresReadStateFromSnapshot(t *testing.T) {
	for _, testCase := range zoomCheckCases() {
		t.Run(testCase.name, func(t *testing.T) {
			item := zoomCard(testCase.message, testCase.readOutID)
			want := testCase.message.IsOutgoing && testCase.readOutID > 0 &&
				testCase.message.ID <= testCase.readOutID
			if item.ReadByPeer != want {
				t.Fatalf("ReadByPeer = %v, ждали %v (id %d, lastReadOutbox %d)",
					item.ReadByPeer, want, testCase.message.ID, testCase.readOutID)
			}
		})
	}
}

// Поле LastReadOutboxMessageID живёт в снимке ЧАТОВ, а не в самой переписке:
// переписка приходит отдельным запросом и о прочтении ничего не знает. Проверяется
// поэтому через настоящий путь модели — чат в снимке, история загружена, —
// чтобы связка «снимок → карточка переписки → экран» не могла разойтись ни на
// одном из трёх шагов.
func TestZoomReadCheckReadsChatSnapshot(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		chat  auth.Chat
		want  string
		isOut bool
	}{
		{"прочитано", auth.Chat{ID: 1, Kind: auth.ChatPrivate, LastReadOutboxMessageID: 99}, zoomReadCheckRead, true},
		{"не прочитано", auth.Chat{ID: 1, Kind: auth.ChatPrivate, LastReadOutboxMessageID: 1}, zoomReadCheckSent, true},
		{"ноль в снимке", auth.Chat{ID: 1, Kind: auth.ChatPrivate}, zoomReadCheckSent, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			m := zoomModel(t, newZoomClient(), 100, 30)
			m.chatsByID = map[int64]auth.Chat{testCase.chat.ID: testCase.chat}
			m.source = []card{{ChatID: testCase.chat.ID, Type: cardPersonal, Name: testCase.chat.Title}}
			m.zoom = &wallZoom{chatID: testCase.chat.ID, title: testCase.chat.Title}
			m.zoom.messages = []auth.Message{
				{ID: 50, Text: "моё сообщение", Date: 1304, IsOutgoing: testCase.isOut},
			}
			m.zoom.cursor = 0

			cards := m.zoomCardsOf(m.zoom, m.zoom.messages)
			if len(cards) != 1 {
				t.Fatalf("карточек переписки %d, ждали 1", len(cards))
			}
			plain := ansi.Strip(strings.Join(renderZoomMessage(cards[0], m.zoomListWidth(), false), "\n"))
			if !strings.Contains(plain, testCase.want) {
				t.Fatalf("строка переписки %q не несёт галочку %q", plain, testCase.want)
			}
		})
	}
}

// Маркер курсора (часть B задачи 0169): у ВЫДЕЛЕННОГО сообщения слева, в
// боковом поле, на первой строке блока появляется треугольник акцентным цветом
// ТИПА ОТКРЫТОГО чата. Проверяются все три типа источника — цвет обязан быть
// разным, иначе «акцент типа чата» на экране ничего не значил бы.
func TestZoomSelectionMarkerUsesOpenChatTypeAccent(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		kind  auth.ChatKind
		typ   cardType
		wantC string
	}{
		{"канал", auth.ChatChannel, cardChannel, "5c9cf5"},
		{"чат", auth.ChatGroup, cardChat, "9d7cd8"},
		{"личное", auth.ChatPrivate, cardPersonal, "fab283"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			m := zoomModel(t, newZoomClient(), 100, 30)
			chat := auth.Chat{ID: 1, Title: "Источник", Kind: testCase.kind}
			m.chatsByID = map[int64]auth.Chat{chat.ID: chat}
			m.source = []card{{ChatID: chat.ID, Type: testCase.typ, Name: chat.Title}}
			m.zoom = &wallZoom{chatID: chat.ID, title: chat.Title}
			m.zoom.messages = []auth.Message{{ID: 5, Text: "строка", Date: 1304}}

			cards := m.zoomCardsOf(m.zoom, m.zoom.messages)
			lines := renderZoomMessage(cards[0], m.zoomListWidth(), true)
			first := ansi.Strip(lines[0])
			if !strings.Contains(first, zoomSelectionMarker) {
				t.Fatalf("на первой строке выделенного сообщения нет маркера %q: %q", zoomSelectionMarker, first)
			}
			// У ВХОДЯЩЕГО маркер — в левом поле, вплотную к тексту, а не где-то
			// по центру строки.
			if columnOf(t, first, zoomSelectionMarker) != 0 {
				t.Fatalf("маркер не в левом поле: %q", first)
			}
			// Цвет маркера — акцент типа открытого чата. Проверяется по SGR
			// именно цвета типа: палитра стены задана значениями в palette.go,
			// и сравнение идёт с ними, а не с зашитыми здесь копиями того же
			// числа — иначе правка палитры молча оставила бы тест зелёным.
			wantSGR := foregroundSGR(card{Type: testCase.typ}.accent(), PaletteBackgroundMain)
			if !strings.Contains(lines[0], wantSGR+zoomSelectionMarker) {
				t.Fatalf("маркер нарисован не акцентом типа чата (%s):\n%q", wantSGR, lines[0])
			}
			// И только у ВЫДЕЛЕННОГО: у соседнего сообщения на том же месте
			// пустой margin, иначе маркер означал бы не «здесь курсор», а
			// «здесь какое-то сообщение».
			other := renderZoomMessage(cards[0], m.zoomListWidth(), false)
			if strings.Contains(ansi.Strip(strings.Join(other, "\n")), zoomSelectionMarker) {
				t.Fatalf("маркер появился у невыделенного сообщения: %q", other)
			}
		})
	}
}

// Маркер стоит вплотную слева от ТЕКСТА при обеих сторонах выравнивания: у
// входящего сообщение прижато влево и маркер попадает в левое поле, у своего —
// прижато вправо, и маркер переезжает внутрь строки, вплотную к тексту.
//
// Раньше он стоял у левого края панели при обеих сторонах, то есть у своего
// сообщения оказывался через всю ширину экрана от текста, к которому
// относится, и перескакивал туда-сюда при каждом движении курсора (человек
// заметил вживую, 2026-10-01).
func TestZoomSelectionMarkerStandsNextToTextOnBothSides(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		outgoing bool
	}{
		{"входящее", false},
		{"своё", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			item := zoomCard(auth.Message{ID: 5, Text: "строка", Date: 1304, IsOutgoing: testCase.outgoing}, 0)
			item.SelectionAccent = PaletteChat
			first := ansi.Strip(renderZoomMessage(item, 50, true)[0])
			marker := columnOf(t, first, zoomSelectionMarker)
			text := columnOf(t, first, "строка")
			// Знак и текст разделяет ровно одна ячейка зазора, и ни больше: без
			// зазора «▸» читался бы частью слова, а с двумя — оторванным от
			// сообщения знаком, висящим в поле.
			if gap := text - marker - cellWidth(zoomSelectionMarker); gap != 1 {
				t.Fatalf("между маркером и текстом %d ячеек, ждали 1 (строка %q)", gap, first)
			}
			// У своего сообщения маркер НЕ у левого края панели: это ровно то,
			// что было не так.
			if testCase.outgoing && marker < cardMarginH {
				t.Fatalf("маркер своего сообщения стоит у левого края панели (колонка %d): %q", marker, first)
			}
		})
	}
}

// Маркер занимает ровно боковое поле карточки. Это не совпадение, а условие
// сходимости строки: у входящего маркер стоит в этом поле, а у своего встаёт на
// его место перед текстом, и обе строки собираются по одной формуле с двумя
// полями по краям зоны. Расхождение на ячейку уводило бы строку за край
// панели (или оставляло бы дыру у правого поля).
func TestZoomMarkerBlockMatchesCardMargin(t *testing.T) {
	if got, want := zoomMarkerBlock(), cardMarginH; got != want {
		t.Fatalf("маркер занимает %d ячеек, а боковое поле карточки — %d: строка перестанет сходиться по ширине", got, want)
	}
}

// Место под маркер зарезервировано у ВСЕХ сообщений, выделенного и
// невыделенного: иначе текст прыгал бы на две ячейки при каждом шаге курсора
// (тот же приём, что у невидимого контура панели, задача 0167).
func TestZoomSelectionMarkerKeepsTextColumn(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		outgoing bool
	}{
		{"входящее", false},
		{"своё", true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			item := zoomCard(auth.Message{ID: 5, Text: "строка", Date: 1304, IsOutgoing: testCase.outgoing}, 0)
			item.SelectionAccent = PaletteChat
			const width = 50
			selected := ansi.Strip(renderZoomMessage(item, width, true)[0])
			plain := ansi.Strip(renderZoomMessage(item, width, false)[0])
			if columnOf(t, selected, "строка") != columnOf(t, plain, "строка") {
				t.Fatalf("текст своего/чужого сообщения сдвинулся при выделении: %q против %q", selected, plain)
			}
		})
	}
}

// Маркер — ровно на ПЕРВОЙ строке блока и больше нигде. Первая строка блока —
// цитата ответа, если она есть, иначе первая строка текста: у сообщения с
// цитатой текст начинается со ВТОРОЙ строки, и маркер на второй, а не на
// первой, читался бы как относящийся к тексту, а не к сообщению.
func TestZoomSelectionMarkerOnFirstLineOnly(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		quote string
	}{
		{"без цитаты", ""},
		{"с цитатой ответа", "го в субботу"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			item := zoomCard(auth.Message{ID: 5, Text: "первая строка подлиннее\nвторая строка", Date: 1304}, 0)
			item.ReplyQuote = testCase.quote
			item.SelectionAccent = PaletteChat
			lines := renderZoomMessage(item, 40, true)
			if len(lines) < 3 {
				t.Fatalf("блок из цитаты и двух абзацев занял %d строк, ждали минимум 3:\n%q",
					len(lines), lines)
			}
			if !strings.Contains(ansi.Strip(lines[0]), zoomSelectionMarker) {
				t.Fatalf("на первой строке блока нет маркера: %q", ansi.Strip(lines[0]))
			}
			for index, line := range lines[1:] {
				if plain := ansi.Strip(line); strings.Contains(plain, zoomSelectionMarker) {
					t.Fatalf("маркер продублирован на строке %d блока: %q", index+1, plain)
				}
			}
		})
	}
}

// Высота блока сообщения не зависит от выделения — иначе окно прокрутки
// считало бы по одной картине, а рисовало по другой (тот же инвариант, что у
// заливки фона выделения). Маркер занимает уже зарезервированную ячейку поля и
// ни одной новой строки не добавляет.
func TestZoomSelectionMarkerDoesNotChangeBlockHeight(t *testing.T) {
	item := zoomCard(auth.Message{ID: 5, Text: "строка подлиннее\nвторая", Date: 1304}, 0)
	item.SelectionAccent = PaletteChannel
	const width = 50
	selected := renderZoomMessage(item, width, true)
	if got, want := zoomCardHeight(item, width, true), len(selected); got != want {
		t.Fatalf("мера высоты выделенного %d разошлась с отрисовкой %d", got, want)
	}
	if selected, plain := zoomCardHeight(item, width, true), zoomCardHeight(item, width, false); selected != plain {
		t.Fatalf("маркер изменил высоту блока: %d против %d", selected, plain)
	}
	for index, line := range selected {
		if got := cellWidth(ansi.Strip(line)); got != width {
			t.Fatalf("строка %d выделенного сообщения шириной %d, ждали %d: %q", index, got, width, ansi.Strip(line))
		}
	}
}

// Маркер берётся по карточке ИСТОЧНИКА, а не по карточке под курсором стены:
// открытая переписка может показывать источник, скрытый фильтром, и у
// видимого списка карточек его тогда просто нет. Проверяется именно этот случай —
// при взятии из видимого списка цвет был бы не задан вовсе.
func TestZoomSelectionAccentComesFromFullSourceNotVisibleList(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 100, 30)
	chat := auth.Chat{ID: 7, Title: "Скрытый фильтром", Kind: auth.ChatChannel}
	m.source = []card{{ChatID: chat.ID, Type: cardChannel, Name: chat.Title}}
	m.cards = nil // источник скрыт фильтром: в видимом списке его нет
	m.zoom = &wallZoom{chatID: chat.ID, title: chat.Title}
	m.zoom.messages = []auth.Message{{ID: 5, Text: "строка", Date: 1304}}

	cards := m.zoomCardsOf(m.zoom, m.zoom.messages)
	wantSGR := foregroundSGR(PaletteChannel, PaletteBackgroundMain)
	if !strings.Contains(renderZoomMessage(cards[0], 50, true)[0], wantSGR+zoomSelectionMarker) {
		t.Fatal("маркер выделенного сообщения не окрашен акцентом типа чата, скрытого фильтром")
	}
}

// У чата, которого нет в потоке стены, цвета нет — и подставлять вместо него
// чужой цвет хуже, чем никакого: маркер был бы заведомо неверным тоном ровно у
// того источника, о котором модель ничего не знает.
func TestZoomSelectionAccentAbsentWhenSourceUnknown(t *testing.T) {
	m := zoomModel(t, newZoomClient(), 100, 30)
	m.source = nil
	m.zoom = &wallZoom{chatID: 404, title: "Неизвестный"}
	if accent, ok := m.zoomSelectionAccent(m.zoom); ok {
		t.Fatalf("у неизвестного источника взят цвет %v", accent)
	}
	if accent, ok := (Model{}).zoomSelectionAccent(nil); ok {
		t.Fatalf("при m.zoom == nil взят цвет %v", accent)
	}
}

// Галочки не должно быть и на стене: карточка стены на строке времени несёт
// сведения об источнике, и знак прочтения там был бы третьим смыслом в одном
// месте. Заодно проверяется, что новое поле не просочилось в wallCard.
func TestWallCardHasNoReadTicks(t *testing.T) {
	item := wallCard(
		auth.Chat{ID: 1, Title: "Источник", Kind: auth.ChatPrivate, LastReadOutboxMessageID: 99},
		auth.Message{ID: 10, Text: "моё сообщение", Date: 1304, IsOutgoing: true})
	if item.ReadByPeer {
		t.Fatal("карточка стены заполнила поле прочтения — там галочек нет")
	}
	for _, line := range renderCard(item, 100, false) {
		if plain := ansi.Strip(line); strings.Contains(plain, zoomReadCheckSent) {
			t.Fatalf("галочка прочтения попала на карточку стены: %q", plain)
		}
	}
}

// Регрессия на самом экране: переписка с прочитанным и непрочитанным своим
// сообщением в обоих режимах раскладки. Галочки и маркер считаются по
// настоящей отрисовке зоны, а не по вызову renderZoomMessage напрямую —
// иначе неполадка в раскладке (ширине зоны, обрезке, склейке) осталась бы
// незамеченной.
func TestZoomScreenShowsTicksAndMarkerInBothModes(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		width  int
		tabbed bool
	}{
		{"узкий", 60, false},
		{"широкий", 100, true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			m := zoomModel(t, newZoomClient(), testCase.width, 30)
			m.chatsByID = map[int64]auth.Chat{2: {ID: 2, Title: "Соседи", Kind: auth.ChatGroup, LastReadOutboxMessageID: 21}}
			m.zoom = &wallZoom{chatID: 2, title: "Соседи"}
			m.zoom.messages = []auth.Message{
				{ID: 20, Text: "прочитано", Date: 1300, IsOutgoing: true},
				{ID: 21, Text: "прочитано тоже", Date: 1301, IsOutgoing: true},
				{ID: 22, Text: "ещё не прочитано", Date: 1302, IsOutgoing: true},
				{ID: 23, Text: "входящее", Date: 1303},
			}
			m.zoom.cursor = 2 // непрочитанное своё — под курсором
			m.zoom.scrollTop = 0
			if testCase.tabbed {
				m.zoomFocused = true
			}
			zone := strings.Join(zoneLines(t, m), "\n")
			if !strings.Contains(zone, zoomSelectionMarker) {
				t.Fatalf("маркер курсора не виден в зоне:\n%s", zone)
			}
			// Прочитанных два (id 20 и 21 не выше 21), непрочитанных одно
			// (id 22), входящее без галочек.
			//
			// Считается по СТРОКАМ, а не вхождениями подстроки: «✓✓» содержит
			// две «✓», и подсчёт символов дал бы 5 вместо 1 и ошибку про одну
			// ячейку вёрстки, которой на экране нет. Галочка — конец строки
			// времени, поэтому сверяется суффикс строки, а не где попало.
			//
			// Перед суффиксом сначала снимается необязательная правая граница
			// контура панели (задача 0167, "┃" + пробел после неё): в широком
			// режиме это ПОСЛЕДНИЙ символ строки зоны, и без снятия граница, а
			// не галочка, оказалась бы настоящим суффиксом строки. TrimSuffix —
			// no-op в узком режиме, где контура нет вовсе.
			read, sent := 0, 0
			for _, line := range strings.Split(zone, "\n") {
				trimmed := strings.TrimRight(line, " ")
				trimmed = strings.TrimRight(strings.TrimSuffix(trimmed, "┃"), " ")
				switch {
				case strings.HasSuffix(trimmed, zoomReadCheckRead):
					read++
				case strings.HasSuffix(trimmed, zoomReadCheckSent):
					sent++
				}
			}
			if read != 2 || sent != 1 {
				t.Fatalf("галочек «%s» %d и «%s» %d, ждали 2 и 1:\n%s",
					zoomReadCheckRead, read, zoomReadCheckSent, sent, zone)
			}
		})
	}
}
