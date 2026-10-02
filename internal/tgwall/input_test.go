package tgwall

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"telecli/internal/auth"
	"telecli/internal/config"
)

// Левая граница поля ввода красится по ТЕКУЩЕЙ цели ввода, а не зашитым цветом:
// человек должен видеть, КУДА уйдёт набранный текст, не вычисляя это в уме и не
// полагаясь на строку подсказок (в широком режиме там вообще нет названия цели).
//
// Раньше цвет был зашит в PalettePersonal независимо от цели, то есть граница
// всегда показывала «личный диалог» независимо от того, что человек печатает в
// канал или группу. На глаз это почти всегда совпадало (курсор стены обычно стоит
// там, куда печатают), и ошибка была видна только в двух случаях — см. второй тест.

// inputZoneBorderSGR — SGR-пролог, которым нарисована левая граница ┃ в блоке
// ввода на указанной строке экрана. Возвращается пролог, а не цвет: он уже несёт
// и цвет текста, и фон панели, а проверке нужен ровно он (см. foregroundSGR).
func inputZoneBorderSGR(t *testing.T, line string) string {
	t.Helper()
	start := strings.Index(line, "┃")
	if start < 0 {
		t.Fatalf("в строке блока ввода нет левой границы: %q", ansi.Strip(line))
	}
	prologueStart := strings.LastIndex(line[:start], "\x1b[")
	if prologueStart < 0 {
		t.Fatalf("граница нарисована вообще без оформления: %q", line)
	}
	end := strings.Index(line[prologueStart:], "m")
	if end < 0 {
		t.Fatalf("SGR-пролог границы не закрыт: %q", line)
	}
	return line[prologueStart : prologueStart+end+1]
}

// inputZoneLines — строки блока ввода на экране. Блок растёт вверх от разделителя и
// всегда прижат к нему снизу, поэтому берутся все строки от разделителя над полем
// до следующего разделителя (того, что над строкой статуса).
//
// Разделитель опознаётся по СОДЕРЖИМОМУ (сплошная линейка ─), а не по номеру
// строки: высота блока зависит от содержимого поля и высоты стены, жёсткий номер
// был бы привязан к вёрстке, а проверка про цвет от вёрстки не зависит.
func inputZoneLines(t *testing.T, m Model) []string {
	t.Helper()
	lines := splitLines(m.renderScreen())
	separators := make([]int, 0, 2)
	for index, line := range lines {
		if isWallSeparator(line) {
			separators = append(separators, index)
		}
	}
	if len(separators) < 2 {
		t.Fatalf("блок ввода не нашден между разделителями (разделителей %d, строк на экране %d)",
			len(separators), len(lines))
	}
	return lines[separators[0]+1 : separators[1]]
}

// isWallSeparator — разделитель зон экрана (см. renderSeparator): линейка во всю
// ширину минус боковые поля, поэтому в обрезанном виде остаётся только знак линейки
// и пробелы по краям.
func isWallSeparator(line string) bool {
	plain := strings.TrimSpace(ansi.Strip(line))
	if plain == "" {
		return false
	}
	return strings.Trim(plain, "─") == ""
}

// Курсор стены на карточке каждого типа — граница поля ввода соответствующего цвета.
// Это основной случай: человек нажал на карточку и печатает в неё.
func TestInputBorderTakesTheAccentOfTheCardUnderCursor(t *testing.T) {
	for _, test := range []struct {
		name   string
		chat   auth.Chat
		accent color.Color
	}{
		{name: "канал", chat: auth.Chat{ID: 1, Title: "Канал", Kind: auth.ChatChannel}, accent: PaletteChannel},
		{name: "чат", chat: auth.Chat{ID: 2, Title: "Группа", Kind: auth.ChatGroup}, accent: PaletteChat},
		{name: "личный", chat: auth.Chat{ID: 3, Title: "Андрей", Kind: auth.ChatPrivate}, accent: PalettePersonal},
	} {
		t.Run(test.name, func(t *testing.T) {
			m := newTestModel(t, 100, 30)
			next, _ := m.Update(wallLoadedMsg{
				cards: wallCards([]wallMessage{{Chat: test.chat, Message: auth.Message{ID: 10, Text: "текст", Date: 1}}}),
				chats: []auth.Chat{test.chat},
			})
			m = next.(Model)
			if got := m.cards[m.cursor].ChatID; got != test.chat.ID {
				t.Fatalf("подготовка: под курсором chat %d, а ждали %d", got, test.chat.ID)
			}

			zone := inputZoneLines(t, m)
			if len(zone) == 0 {
				t.Fatal("блок ввода пуст")
			}
			// Подсказки и ответ рисуются приглушённым цветом, а граница у всех строк
			// блока одна и та же, поэтому берём первую строку: она не зависит от
			// содержимого поля и от наличия ответа.
			if got := inputZoneBorderSGR(t, zone[0]); got != foregroundSGR(test.accent, PaletteBackgroundPanel) {
				t.Fatalf("граница поля ввода = %q, а цель ввода (карточка %s) требует %q",
					got, test.name, foregroundSGR(test.accent, PaletteBackgroundPanel))
			}
		})
	}
}

// Палитра по типу источника ЗАФИКСИРОВАНА (задача 0166, 2026-09-30): канал синий,
// чат тёплый оранжевый, личный зелёный. Значения выписаны числами, а не взяты из
// констант палитры, — намеренно.
//
// Если бы проверка сверялась с PaletteChat/PalettePersonal, она прошла бы на ЛЮБОЙ
// смене цвета: и константа, и ожидание поменялись бы вместе, и правка палитры прошла
// бы незамеченной. А смена палитры здесь — не рефакторинг, а решение человека: цвета
// выбраны им самим и должны остаться именно такими, пока он не скажет иначе.
// Поэтому значения зашиты в проверку как есть, с датой и основанием.
//
// Отдельно проверяется, что цвет идёт ОТ ТИПА (card.accent), а не от того, какой
// константой нарисован этот конкретный экран, и что все три цвета разные: два типа
// с одним цветом сделали бы различение на экране невозможным.
func TestCardAccentFollowsTheSourceType(t *testing.T) {
	seen := make(map[color.Color]cardType, 3)
	for _, test := range []struct {
		kind   cardType
		accent color.Color
		want   string
	}{
		{kind: cardChannel, accent: PaletteChannel, want: "#5c9cf5"},
		{kind: cardChat, accent: PaletteChat, want: "#e99b79"},
		{kind: cardPersonal, accent: PalettePersonal, want: "#1a7c33"},
	} {
		if got := (card{Type: test.kind}).accent(); got != test.accent {
			t.Fatalf("акцент типа %v = %v, а ждали %v", test.kind, got, test.accent)
		}
		if got := hexOf(test.accent); got != test.want {
			t.Fatalf("цвет типа %v = %s, а человек зафиксировал %s (задача 0166, 2026-09-30)",
				test.kind, got, test.want)
		}
		if previous, ok := seen[test.accent]; ok {
			t.Fatalf("типы %v и %v получили один цвет %v — по типу их не отличить на экране",
				previous, test.kind, test.accent)
		}
		seen[test.accent] = test.kind
	}
	if len(seen) != 3 {
		t.Fatalf("у типов источников %d разных цветов, а ждали 3", len(seen))
	}
}

// hexOf — цвет в виде #rrggbb строчными, как его записывают в палитре: регистр
// в записи значения не различает цвет, и сравнение с ним было бы проверкой
// оформления, а не палитры. Нужна проверке палитры для сверки с зафиксированными
// значениями.
func hexOf(c color.Color) string {
	r, g, b, _ := c.RGBA()
	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// Переписка в фокусе важнее видимого курсора стены: в широком режиме она открыта
// по тому источнику, на котором курсор стоял, когда её открыли, и с тех пор курсор
// по стене ушёл. Текст уходит в ПЕРЕПИСКУ (так же, как его отправляет
// submitInput), и граница обязана показывать именно её, а не то, что человек
// видит выделенным в списке.
//
// Именно этот случай стоил зашитого цвета: в широком режиме переписка личного
// диалога с курсором на канале — обычное состояние, и граница показывала бы
// «личный» именно тогда, когда человек печатает в канал. Обратный порядок тоже
// возможен (переписка канала при курсоре на личном), поэтому проверяются оба.
func TestInputBorderFollowsTheFocusedConversationNotTheWallCursor(t *testing.T) {
	m := zoomModelWith(t, newZoomClient(), 100, 30)
	if !m.wallPanelMode() {
		t.Fatal("подготовка: нужен широкий режим (переписка рядом со стеной)")
	}
	opened := runZoomLoad(t, m, m.wallZoomLoadedCmd(m.zoom.panel, m.zoom.chatID, m.zoom.loadID))
	// Переписка открыта по личному диалогу (последняя карточка фикстуры), фокус на
	// ней — обычное состояние широкого режима.
	if opened.cards[opened.cursor].Type != cardPersonal {
		t.Fatalf("подготовка: под курсором стены не личный диалог, а %v", opened.cards[opened.cursor].Type)
	}
	zoomChat := opened.zoom.chatID

	// Курсор стены уводим на карточку КАНАЛА, оставляя переписку личного диалога
	// открытой. Это не выдуманное состояние, а два независимых куска состояния
	// модели: открытая переписка и видимый курсор стены, которые стена держит
	// одинаковыми по syncWallZoomToCursor — но не по каждому событию. Например,
	// переключение галки фильтра пересобирает список и уводит курсор на соседний
	// источник (toggleFilterMenuRow), а открытую переписку не трогает вовсе.
	//
	// Состояние ставится прямо здесь, а не через живые события, намеренно: путь
	// через фильтр протащил бы через себя ещё и сам оверлей меню, и проверка
	// границы тогда зависела бы от вёрстки меню, а не от правила выбора цели.
	opened = opened.moveCursor(-len(opened.cards)) // на первую карточку — канал
	if opened.cards[opened.cursor].Type != cardChannel {
		t.Fatalf("подготовка: под курсором стены %v, а ждали канал", opened.cards[opened.cursor].Type)
	}
	focused, _ := pressZoomKey(t, opened, keyTab())
	if !focused.zoomHasFocus() {
		t.Fatal("подготовка: фокус должен быть на переписке")
	}
	if focused.zoom.chatID != zoomChat {
		t.Fatalf("подготовка: переписка сменилась на чат %d, а ждали прежний %d", focused.zoom.chatID, zoomChat)
	}

	zone := inputZoneLines(t, focused)
	if got := inputZoneBorderSGR(t, zone[0]); got != foregroundSGR(PalettePersonal, PaletteBackgroundPanel) {
		t.Fatalf("граница поля = %q, а текст уходит в переписку личного диалога: ждали %q",
			got, foregroundSGR(PalettePersonal, PaletteBackgroundPanel))
	}
	if got := inputZoneBorderSGR(t, zone[0]); got == foregroundSGR(PaletteChannel, PaletteBackgroundPanel) {
		t.Fatal("граница покрашена по карточке под курсором стены, а не по цели ввода")
	}
}

// Пустая стена — откат, а не паника: цвета цели нет, а экран обязан рисоваться.
func TestInputBorderOnEmptyWallFallsBackWithoutPanic(t *testing.T) {
	m := newTestModel(t, 100, 30)
	m.cards = nil
	m.applyLayout()

	zone := inputZoneLines(t, m)
	if len(zone) == 0 {
		t.Fatal("блок ввода пуст на пустой стене")
	}
	if got := inputZoneBorderSGR(t, zone[0]); got != foregroundSGR(PalettePersonal, PaletteBackgroundPanel) {
		t.Fatalf("на пустой стене граница = %q, ждали безопасный откат %q",
			got, foregroundSGR(PalettePersonal, PaletteBackgroundPanel))
	}
}

// Цель ввода и её акцент — одно и то же решение в трёх местах стены: отправка
// (submitInput), ответ (startReply) и теперь цвет границы. Расхождение означало бы,
// что граница обещает отправку не туда, куда текст уйдёт на самом деле, — самый
// дорогой вид расхождения, потому что человек узнаёт о нём уже после отправки.
func TestInputTargetAccentAgreesWithWhereTheTextGoes(t *testing.T) {
	m := zoomModelWith(t, newZoomClient(), 100, 30)
	opened := runZoomLoad(t, m, m.wallZoomLoadedCmd(m.zoom.panel, m.zoom.chatID, m.zoom.loadID))

	// Без переписки в фокусе цель — карточка под курсором стены.
	wallTarget := opened.cards[clampWallCursor(opened.cursor, len(opened.cards))]
	if got, want := opened.inputTargetAccent(), wallTarget.accent(); got != want {
		t.Fatalf("цель ввода — карточка %d (цвет %v), а граница бы красилась в %v", wallTarget.ChatID, want, got)
	}

	// С перепиской в фокусе цель — источник переписки, даже если карточка под
	// курсором другая.
	focused, _ := pressZoomKey(t, opened, keyTab())
	if !focused.zoomHasFocus() {
		t.Fatal("подготовка: фокус должен быть на переписке")
	}
	zoomIndex := indexOfChatCard(focused.source, focused.zoom.chatID)
	if zoomIndex < 0 {
		t.Fatalf("источник переписки %d не найден в полном потоке стены", focused.zoom.chatID)
	}
	if got, want := focused.inputTargetAccent(), focused.source[zoomIndex].accent(); got != want {
		t.Fatalf("цель ввода — переписка %d (цвет %v), а граница бы красилась в %v",
			focused.zoom.chatID, want, got)
	}

	// Тот же случай, когда источник переписки СКРЫТ фильтром: в видимом списке
	// карточек его нет, и взятый оттуда цвет был бы чужим (либо никаким) — то
	// есть граница обещала бы отправку не туда, куда текст уйдёт на самом деле.
	hidden := auth.Chat{ID: 4242, Title: "Скрытый фильтром", Kind: auth.ChatChannel}
	hiddenSource := []card{{ChatID: hidden.ID, Type: cardChannel, Name: hidden.Title}}
	focused.source = append(append([]card(nil), focused.source...), hiddenSource...)
	focused.cards = nil
	focused.zoom = &wallZoom{chatID: hidden.ID, title: hidden.Title, panel: focused.zoom.panel}
	if got, want := focused.inputTargetAccent(), PaletteChannel; got != want {
		t.Fatalf("источник переписки скрыт фильтром, а граница покрашена в %v, ждали %v "+
			"(цвет взят не из полного потока стены)", got, want)
	}
}

// Источник открытой переписки ищется в ПОЛНОМ потоке стены (m.source), а не среди
// видимых карточек (m.cards). Разница видна ровно тогда, когда карточка источника
// скрыта фильтром: человек открыл переписку канала, снял галку «Каналы», и канал
// исчез со стены — а печатать он продолжает в него же, и граница обязана остаться
// прежней.
//
// Среди видимых карточек источника в этом состоянии нет, и поиск даёт -1: откат
// PalettePersonal совпал бы с личным диалогом, но с КАНАЛОМ — нет, так что проверка
// различима. Именно поэтому источник переписки берётся из полного потока: иначе
// граница молча меняла бы цвет вслед за фильтром, а текст продолжал бы уходить туда
// же.
func TestInputTargetAccentSurvivesTheSourceBeingFilteredOut(t *testing.T) {
	m := zoomModelWith(t, newZoomClient(), 100, 30)
	if !m.wallPanelMode() {
		t.Fatal("подготовка: нужен широкий режим")
	}
	opened := runZoomLoad(t, m, m.wallZoomLoadedCmd(m.zoom.panel, m.zoom.chatID, m.zoom.loadID))
	// Переписка открыта по ЛИЧНОМУ диалогу, а снимаем галку «Каналы» — так
	// расхождение заметно по цвету, а не только по индексу.
	if opened.cards[opened.cursor].Type != cardPersonal {
		t.Fatalf("подготовка: под курсором стены не личный диалог, а %v", opened.cards[opened.cursor].Type)
	}
	zoomChat := opened.zoom.chatID

	// Снимаем галку типа каналов и пересобираем видимый список, как это делает
	// toggleFilterMenuRow: полный поток остаётся, из видимого список собирается заново.
	opened.filter.showChannels = false
	opened = opened.rebuildVisible(false)
	for _, item := range opened.cards {
		if item.ChatID == 1 {
			t.Fatal("подготовка: канал должен был уйти со стены вместе со снятой галкой")
		}
	}
	if indexOfChatCard(opened.source, 1) < 0 {
		t.Fatal("подготовка: канал обязан остаться в полном потоке стены")
	}

	focused, _ := pressZoomKey(t, opened, keyTab())
	if !focused.zoomHasFocus() {
		t.Fatal("подготовка: фокус должен быть на переписке")
	}
	if focused.zoom.chatID != zoomChat {
		t.Fatalf("подготовка: переписка сменилась на чат %d, а ждали %d", focused.zoom.chatID, zoomChat)
	}
	if got, want := focused.inputTargetAccent(), PalettePersonal; got != want {
		t.Fatalf("цвет границы = %v, а переписка открыта по личному диалогу: ждали %v", got, want)
	}
}

// Поле ввода на самом деле красится переданным цветом, а не зашитым: проверка выше
// идёт через inputTargetAccent, и без этого renderInputZone мог бы принимать
// параметр и не использовать его — тест про цель ввода прошёл бы, а граница так и
// осталась бы одноцветной.
func TestRenderInputZoneUsesTheAccentItIsGiven(t *testing.T) {
	const width = 20
	for _, accent := range []color.Color{PaletteChannel, PaletteChat, PalettePersonal} {
		lines := renderInputZone(newInput(), width, 1, "", []hintPair{{key: "ctrl+p", word: "фильтр"}}, accent)
		if len(lines) == 0 {
			t.Fatal("блок ввода пуст")
		}
		if got := inputZoneBorderSGR(t, lines[0]); got != foregroundSGR(accent, PaletteBackgroundPanel) {
			t.Fatalf("граница нарисована как %q, а передан цвет %v (%q)",
				got, accent, foregroundSGR(accent, PaletteBackgroundPanel))
		}
	}
}

// Обрезка строки подсказок идёт ПО ЦЕЛЫМ ПАРАМ, а не по символам: обрезок вида
// «ctrl+p фильтр · ctrl+j строк…» читается как несуществующее действие «строк».
// Проверка идёт по всем ширинам от 0 до размера самой строки: правило обязано
// работать на каждой из них, а не на одной удачно выбранной.
func TestHintTruncationCutsOnPairBoundary(t *testing.T) {
	full := newTestModel(t, 100, 30).hintLine()
	if cellWidth(full) == 0 {
		t.Fatal("подсказка пуста — проверять обрезку нечего")
	}
	all := strings.Split(full, wallHintSeparator)
	// С нулевой ширины начинать нельзя: там нечего рисовать, и пустая строка —
	// правильный ответ, а не пропущенная подсказка (то же у truncateVisible).
	for width := 1; width <= cellWidth(full)+2; width++ {
		got := truncateHint(full, width)
		if cellWidth(got) > width {
			t.Fatalf("ширина %d: обрезок %q шириной %d", width, got, cellWidth(got))
		}
		trimmed, cut := strings.TrimSuffix(got, ellipsis), strings.Contains(got, ellipsis)
		if !cut {
			// Без многоточия обрезки быть не должно: строка либо помещается целиком,
			// либо помечается многоточием — молчаливый обрезок хуже обоих.
			if got != full {
				t.Fatalf("ширина %d: строка обрезана без многоточия: %q", width, got)
			}
			continue
		}
		// Каждый кусок обрезка — ЦЕЛАЯ пара исходной строки, а не её начало. Сравнение
		// точное, а не по вхождению: «ctrl+j строк» входит в исходную строку как
		// начало пары, и проверка «есть ли она внутри» такой обрывок пропустила бы.
		//
		// Само по себе многоточие без пар — законный ответ на слишком узкую строку,
		// поэтому пустой остаток тут не ошибка (ошибкой будет пустая строка, это
		// отдельная проверка ниже).
		var kept []string
		if trimmed != "" {
			kept = strings.Split(trimmed, wallHintSeparator)
		}
		for _, pair := range kept {
			if !slices.Contains(all, pair) {
				t.Fatalf("ширина %d: %q — не целая пара подсказки %q", width, pair, full)
			}
		}
		// Число оставшихся пар — МАКСИМАЛЬНО возможное: если следующая пара влезает
		// вместе с многоточием, она обязана быть в обрезке, иначе подсказка молча
		// теряет целую подпись там, где для неё есть место.
		if fits, next := hintPairsFitWidth(all, width); fits {
			if len(kept) != next {
				t.Fatalf("ширина %d: влезало пар %d, а оставлено %d: %q", width, next, len(kept), got)
			}
		} else if len(kept) != 0 {
			t.Fatalf("ширина %d: не помещалась ни одна пара, а обрезок %q", width, got)
		}
	}
}

// hintPairsFitWidth — сколько пар подсказки помещается в ширину вместе с
// многоточием, и влезает ли хоть одна. Считается НЕЗАВИСИМО от truncateHint, и
// именно поэтому проверка выше ловит отказ от обрезки по границе пар: обе стороны
// обязаны сойтись на каждой ширине.
func hintPairsFitWidth(pairs []string, width int) (fits bool, kept int) {
	used := 0
	for index, pair := range pairs {
		price := cellWidth(pair)
		if index > 0 {
			price += cellWidth(wallHintSeparator)
		}
		if used+price+cellWidth(ellipsis) > width {
			return index > 0, index
		}
		used += price
	}
	return true, len(pairs)
}

// Многоточие обрезки подсказок — то же самое, что и везде в проекте, и подсказка
// при обрезке не превращается в пустую строку на узком терминале: пустая подсказка
// молчит там, где человеку нужнее всего подсказка.
func TestHintTruncationKeepsTheSharedEllipsisAndNeverGoesSilent(t *testing.T) {
	full := newTestModel(t, 100, 30).hintLine()
	for width := 1; width <= cellWidth(full); width++ {
		got := truncateHint(full, width)
		if cellWidth(full) > width && got == "" {
			t.Fatalf("ширина %d: подсказка исчезла совсем, ждали хоть что-то с многоточием", width)
		}
		if cellWidth(full) <= width && got != full {
			t.Fatalf("ширина %d: строка обрезана, хотя помещалась целиком: %q", width, got)
		}
		if strings.Contains(got, "...") {
			t.Fatalf("ширина %d: в обрезке многоточие из трёх точек вместо общего %q: %q", width, ellipsis, got)
		}
	}
}

// Обрезанная подсказка рисуется в блоке ввода без разрыва ширины: строка блока
// остаётся ровно по ширине поля при ЛЮБОЙ ширине терминала, включая ту, где
// подсказка обрезана сильнее всего.
func TestTruncatedHintKeepsTheInputZoneRowWidth(t *testing.T) {
	m := newTestModel(t, 100, 30)
	zoneWidth := func(width int) int { return width + 2*cardMarginH + cardBorderWidth }
	for width := 1; width <= 80; width++ {
		lines := renderInputZone(*m.input, width, 1, "", m.hintPairs(), PalettePersonal)
		if len(lines) == 0 {
			t.Fatalf("ширина %d: блок ввода пуст", width)
		}
		if got := cellWidth(lines[0]); got != zoneWidth(width) {
			t.Fatalf("ширина %d: строка подсказок шириной %d, ждали %d", width, got, zoneWidth(width))
		}
	}
}

// hintCellSGR — SGR-пролог, которым нарисована ячейка с данным текстом в строке
// подсказок. Ищет текст и возвращает оформление, действующее на нём: именно оно
// и есть «цвет подсказки», а не то, чем окрашены остальные её части.
func hintCellSGR(t *testing.T, line, text string) string {
	t.Helper()
	start := strings.Index(line, text)
	if start < 0 {
		t.Fatalf("в строке подсказок нет %q: %q", text, ansi.Strip(line))
	}
	prologueStart := strings.LastIndex(line[:start], "\x1b[")
	if prologueStart < 0 {
		t.Fatalf("%q нарисовано вообще без оформления: %q", text, line)
	}
	end := strings.Index(line[prologueStart:], "m")
	if end < 0 {
		t.Fatalf("у %q не найден конец SGR: %q", text, line)
	}
	return line[prologueStart : prologueStart+end+1]
}

// TestHintKeysAndWordsHaveTheirOwnColors — сочетания клавиш и слова к ним в строке
// подсказок нарисованы РАЗНЫМИ цветами: сочетание #888888 (PaletteTextMuted), слово
// #666666 (PaletteHintWord, по прямому слову человека 2026-10-01).
//
// Проверка идёт по каждой паре отдельно и по обоим её цветам. Одного «цвета
// отличного от фона» мало: при одном цвете на сочетание и на слово подсказка
// выглядит как ровный серый список, и глаз не находит в ней главного — что
// нажимать.
func TestHintKeysAndWordsHaveTheirOwnColors(t *testing.T) {
	m := newTestModel(t, 100, 30)
	line := renderInputZone(*m.input, 100, 1, "", m.hintPairs(), PalettePersonal)[0]

	keySGR := foregroundSGR(PaletteHintKey, PaletteBackgroundPanel)
	wordSGR := foregroundSGR(PaletteHintWord, PaletteBackgroundPanel)
	if keySGR == wordSGR {
		t.Fatal("подготовка: цвета сочетания и слова совпали — проверять нечего")
	}
	for _, pair := range m.hintPairs() {
		if got := hintCellSGR(t, line, pair.key); got != keySGR {
			t.Errorf("сочетание %q нарисовано как %s, ждали %s (#999999)", pair.key, got, keySGR)
		}
		if got := hintCellSGR(t, line, pair.word); got != wordSGR {
			t.Errorf("слово %q нарисовано как %s, ждали %s (#666666)", pair.word, got, wordSGR)
		}
	}
	// Слова не темнее сочетаний: иначе строка читалась бы наоборот — главным
	// показалось бы описание, а не клавиша.
	if PaletteHintWord == PaletteTextFaint {
		t.Fatal("слово подсказки осталось приглушённым #555555 вместо #666666")
	}
}

// TestReplyRowIsNotFainterThanTheHint — строка ответа (↩ Название — «текст»)
// нарисована PaletteTextMuted (#888888), а не приглушённым #555555: по прямому
// слову человека (2026-10-01). Ответ должен читаться так же ярко, как сочетания
// клавиш под ним, иначе его можно отправить, не заметив, на что.
func TestReplyRowIsNotFainterThanTheHint(t *testing.T) {
	m := newTestModel(t, 100, 30)
	target := m.cards[0]
	lines := renderInputZone(*m.input, 100, 1, replyLabel(target), m.hintPairs(), PalettePersonal)
	if len(lines) < 2 {
		t.Fatalf("блок ввода = %d строк, ждали строку ответа над полем", len(lines))
	}
	reply := lines[1]
	if !strings.Contains(ansi.Strip(reply), wallReplyMarker) {
		t.Fatalf("строка ответа без знака %q: %q", wallReplyMarker, ansi.Strip(reply))
	}
	want := foregroundSGR(PaletteTextMuted, PaletteBackgroundPanel)
	if got := inputZoneBorderSGR(t, reply); got == want {
		// Граница красится акцентом источника, а не цветом текста: проверять её
		// тут нечего, и совпадение сразу отмечено, чтобы не принять за успех.
		t.Fatal("подготовка: строка ответа проверяется по границе, а она красится акцентом")
	}
	// Ищем оформление по САМОМУ знаку ответа: он рисуется в той же строке и
	// несёт ровно тот стиль, который проверяется.
	start := strings.Index(reply, wallReplyMarker)
	prologue := strings.LastIndex(reply[:start], "\x1b[")
	if prologue < 0 {
		t.Fatalf("строка ответа нарисована без оформления: %q", reply)
	}
	if end := strings.Index(reply[prologue:], "m"); end >= 0 {
		got := reply[prologue : prologue+end+1]
		if got != want {
			t.Fatalf("строка ответа нарисована как %s, ждали %s (#888888)", got, want)
		}
	}
	if PaletteTextMuted == PaletteTextFaint {
		t.Fatal("подготовка: PaletteTextMuted и PaletteTextFaint совпали — проверять нечего")
	}
}

// Клавиша Toggle — настроенное отдельное действие, и дефолт у неё один: пробел.
// Отдельным полем, а не переиспользованием Select: в стене Enter — отправка
// текста, и навязать ему ещё и переключение галки значило бы, что переназначив
// отправку, человек невольно переназначил ещё и меню.
func TestToggleIsItsOwnConfiguredAction(t *testing.T) {
	keys := config.DefaultTgwallKeyBindings()
	if len(keys.Toggle) != 1 || keys.Toggle[0] != "space" {
		t.Fatalf("дефолт Toggle = %v, а ждали [space]", keys.Toggle)
	}
	// Select на месте и не изменился: Enter в стене по-прежнему отправка.
	if len(keys.Select) != 1 || keys.Select[0] != "enter" {
		t.Fatalf("дефолт Select = %v, а ждали [enter]", keys.Select)
	}
	km := keyMap{keys: keys}
	if got := km.label(keyToggle); got != "space" {
		t.Fatalf("подпись keyToggle = %q, а ждали space", got)
	}
	if !km.pressed(keyToggle, tea.KeyPressMsg{Code: tea.KeySpace}) {
		t.Fatal("пробел не опознан как keyToggle")
	}
	if km.pressed(keySelect, tea.KeyPressMsg{Code: tea.KeySpace}) {
		t.Fatal("пробел опознан как keySelect — это разные действия, и Enter в стене остаётся отправкой")
	}
}

// Подсказка про круг фокуса появляется ТОЛЬКО когда фокусировать есть чем.
//
// Правило человека (2026-10-02): подсказка обещает клавиши, которые работают, и
// ни одной больше. В узком режиме панели переписки нет, Tab молчит, и обещание
// про круг фокуса было бы враньём — хуже, чем отсутствие упоминания.
func TestPanelsHintAppearsOnlyWhenFocusIsPossible(t *testing.T) {
	// Узкий терминал: панелей нет вовсе.
	narrow := newTestModel(t, 60, 30)
	if narrow.focusable() {
		t.Fatalf("на узком терминале фокусировать некуда, а focusable=%v", narrow.focusable())
	}
	if got := narrow.hintLine(); strings.Contains(got, "tab") {
		t.Fatalf("узкий режим обещает Tab, который там не делает ничего: %q", got)
	}

	// Широкий с открытой перепиской: круг фокуса есть, и подсказка его обещает.
	wide := zoomModel(t, newZoomClient(), 120, 30)
	wide = loadWallPanelHistories(t, wide)
	if !wide.focusable() {
		t.Fatal("в двухпанельном режиме с открытой перепиской фокусировать есть чем")
	}
	if got := wide.hintLine(); !strings.Contains(got, "tab "+wallHintPanels) {
		t.Fatalf("в двухпанельном режиме подсказка молчит про круг фокуса: %q", got)
	}
}
