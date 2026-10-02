package tgwall

import (
	"errors"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"telecli/internal/config"
)

// errTestLoad — искусственная ошибка загрузки для проверки состояния «сбой».
var errTestLoad = errors.New("тестовая ошибка загрузки")

// Кадры спиннера — буквально список из макета. Отдельная проверка на сам список:
// пока он совпадал с макетом только потому, что так его скопировали, и любая
// правка кадра тихо испортила бы вид лоадера.
func TestSpinnerFramesAreExactlyMockup(t *testing.T) {
	want := []rune{'⣋', '⣙', '⢻', '⢼', '⣶', '⣴', '⣦', '⣧', '⣏', '⣏'}
	if len(spinnerFrames) != len(want) {
		t.Fatalf("кадров %d, ждали %d", len(spinnerFrames), len(want))
	}
	for index, frame := range want {
		if spinnerFrames[index] != frame {
			t.Fatalf("кадр %d = %q, ждали %q", index, spinnerFrames[index], frame)
		}
	}
}

func TestSpinnerCyclesThroughFramesInOrder(t *testing.T) {
	m := New(nil, nil, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	m.width, m.height = 100, 30
	m.applyLayout()

	// Два полных круга и ещё один кадр — чтобы поймать и порядок, и переход через
	// конец списка.
	got := make([]string, 0, 2*len(spinnerFrames)+1)
	for range 2*len(spinnerFrames) + 1 {
		got = append(got, screenIndicator(t, m))
		next, _ := m.Update(spinnerTickMsg{})
		m = next.(Model)
	}

	want := make([]string, 0, len(got))
	for range 2 {
		for _, frame := range spinnerFrames {
			want = append(want, string(frame))
		}
	}
	want = append(want, string(spinnerFrames[0]))
	for index := range got {
		if got[index] != want[index] {
			t.Fatalf("кадр %d = %q, ждали %q (получено %q)", index, got[index], want[index], got)
		}
	}
}

// Спиннер крутится РОВНО пока грузится хоть что-то видимое. Пока грузится —
// анимированные кадры из макета; когда загружать нечего, на месте индикатора
// пусто: статичный глиф убран вовсе, а не заменён другим символом (указание
// человека, задача 0162), и тики больше не двигают значок. Проверяются обе
// стороны, потому что ошибка в любую из них одна и та же по виду: либо вечно
// крутящийся спиннер на загруженной стене, либо пустота вместо «идёт».
func TestSpinnerIsAnimatedOnlyWhileSomethingLoads(t *testing.T) {
	loading := newTestModel(t, 100, 30)
	loading.loading = true
	if first := screenIndicator(t, loading); first != string(spinnerFrames[0]) {
		t.Fatalf("при загрузке индикатор = %q, ждали первый кадр %q", first, spinnerFrames[0])
	}
	next, _ := loading.Update(spinnerTickMsg{})
	afterTick := next.(Model)
	if got := screenIndicator(t, afterTick); got != string(spinnerFrames[1]) {
		t.Fatalf("после тика при загрузке индикатор = %q, ждали %q", got, spinnerFrames[1])
	}

	blank := strings.Repeat(" ", spinnerColumnWidth)
	idle := newTestModel(t, 100, 30)
	for range 3 {
		if got := screenIndicator(t, idle); got != blank {
			t.Fatalf("без загрузки индикатор = %q, ждали пустую колонку %q", got, blank)
		}
		next, _ := idle.Update(spinnerTickMsg{})
		idle = next.(Model)
	}
}

// Спиннер нижней строки — индикатор ВСЕГО, что грузится сейчас, а не только
// начальной загрузки стены. Пока едет история открытой переписки, стена давно
// загружена (m.loading == false), и раньше индикатор в этот момент молча
// показывал пустоту, как будто ничего не происходит (найдено человеком вживую).
func TestSpinnerAnimatesWhileZoomHistoryLoads(t *testing.T) {
	m := newTestModel(t, 100, 30)
	if m.loading {
		t.Fatal("стена не загружена: проверяется случай «стена готова, грузится только история»")
	}
	opened, _ := m.openWallZoom(4242, "источник")
	loading := opened
	if loading.zoom == nil || !loading.zoom.loading {
		t.Fatal("переписка не открылась в состоянии загрузки — проверяется не то")
	}
	if !loading.isLoading() {
		t.Fatal("история переписки грузится, а isLoading() говорит «ничего не грузится»")
	}
	if got := screenIndicator(t, loading); got != string(spinnerFrames[0]) {
		t.Fatalf("при загрузке истории индикатор = %q, ждали первый кадр %q", got, spinnerFrames[0])
	}
	next, _ := loading.Update(spinnerTickMsg{})
	afterTick := next.(Model)
	if got := screenIndicator(t, afterTick); got != string(spinnerFrames[1]) {
		t.Fatalf("после тика при загрузке истории индикатор = %q, ждали %q", got, spinnerFrames[1])
	}
}

// Когда история пришла, а стена была загружена и раньше, индикатор гаснет:
// обе загрузки кончились — крутиться больше нечему. Без этой проверки спиннер
// мог бы замереть на одном кадре навсегда.
func TestSpinnerStopsWhenZoomHistoryArrives(t *testing.T) {
	opened, _ := newTestModel(t, 100, 30).openWallZoom(4242, "источник")
	// Номер панели в ответе обязателен: applyWallZoomLoaded сверяет ответ со
	// СВОЕЙ панелью, и без номера искал бы переписку в панели ленты, которой
	// переписки не бывает, — и молча проигнорировал бы ответ.
	next, _ := opened.Update(wallZoomLoadedMsg{panel: opened.zoom.panel, chatID: 4242, loadID: opened.zoom.loadID, messages: nil})
	loaded := next.(Model)
	if loaded.zoom.loading {
		t.Fatal("ответ с верным номером попытки не выключил загрузку истории")
	}
	if loaded.isLoading() {
		t.Fatal("стена загружена, история пришла, а isLoading() всё ещё говорит «идёт»")
	}
	blank := strings.Repeat(" ", spinnerColumnWidth)
	if got := screenIndicator(t, loaded); got != blank {
		t.Fatalf("после загрузки истории индикатор = %q, ждали пустую колонку %q", got, blank)
	}
}

// Когда не грузится ничего, индикатор — пустой фон, и ширина нижней строки от
// этого не меняется: лого не должен прыгать на ячейку влево-вправо при каждом
// включении и выключении загрузки. Именно этим плох и был прежний вариант с
// исчезающим знаком, поэтому ширины сравниваются явно, а не на глаз.
func TestSpinnerIdleKeepsStatusBarWidth(t *testing.T) {
	loading := newTestModel(t, 100, 30)
	loading.loading = true
	spinning := statusBarLine(t, loading)
	idle := statusBarLine(t, newTestModel(t, 100, 30))

	if got, want := cellWidth(idle), cellWidth(spinning); got != want {
		t.Fatalf("ширина нижней строки без загрузки = %d, при загрузке = %d — колонка индикатора поехала", got, want)
	}
	// Лого и версия — на тех же местах: общая ширина сама по себе не доказывает
	// этого, сдвиг мог бы уйти в поле между ними.
	if got, want := columnOf(t, idle, testAppName), columnOf(t, spinning, testAppName); got != want {
		t.Fatalf("лого без загрузки на колонке %d, при загрузке на %d", got, want)
	}
	if got, want := columnOf(t, idle, testVersion), columnOf(t, spinning, testVersion); got != want {
		t.Fatalf("версия без загрузки на колонке %d, при загрузке на %d", got, want)
	}
	// И наоборот, при загрузке кадр на месте: иначе сравнение ширин выше прошло
	// бы на двух одинаковых пустых строках и ничего не доказывало.
	if !strings.ContainsRune(spinning, spinnerFrames[0]) {
		t.Fatalf("при загрузке индикатора нет вовсе: %q", spinning)
	}
}

// Без загрузки на месте индикатора не рисуется НИЧЕГО: ни прежнего статичного
// многоточия, ни кадра спиннера, ни любого другого символа-заглушки (указание
// человека: убрать, а не заменить).
func TestSpinnerIdleDrawsNoGlyphAtAll(t *testing.T) {
	idle := statusBarLine(t, newTestModel(t, 100, 30))
	if strings.ContainsRune(idle, '⋯') {
		t.Fatalf("статичный глиф «⋯» снова нарисован без загрузки: %q", idle)
	}
	for _, frame := range spinnerFrames {
		if strings.ContainsRune(idle, frame) {
			t.Fatalf("кадр %q нарисован без загрузки: %q", frame, idle)
		}
	}
	if got := screenIndicator(t, newTestModel(t, 100, 30)); got != strings.Repeat(" ", spinnerColumnWidth) {
		t.Fatalf("колонка индикатора = %q, ждали только пустой фон", got)
	}
}

// Состояние загрузки задаётся приходом результата, а не константой: стена стартует
// с включённой загрузкой (данных ещё нет, программа только запустилась) и
// выключает её ровно тогда, когда пришло wallLoadedMsg.
func TestLoadingStateFollowsWallLoaded(t *testing.T) {
	m := New(nil, nil, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	if !m.loading {
		t.Fatal("при старте стена не в состоянии загрузки: спиннер бы не крутился, а карточек ещё нет")
	}
	if len(m.cards) != 0 {
		t.Fatalf("при старте на стене %d карточек, ждали 0 — данные приезжают позже", len(m.cards))
	}

	next, _ := m.Update(wallLoadedMsg{cards: testCards()})
	loaded := next.(Model)
	if loaded.loading {
		t.Fatal("после wallLoadedMsg стена осталась в состоянии загрузки")
	}
	if len(loaded.cards) == 0 {
		t.Fatal("карточки из wallLoadedMsg не попали на стену")
	}
}

// Сбой загрузки — это тоже конец загрузки: крутящийся спиннер после ошибки врал
// бы вечно. Текст ошибки при этом обязан быть ВИДЕН на экране — молчаливая пустая
// стена без причины неотличима от «у аккаунта нет чатов», и человек зря будет
// думать, что переписки нет.
func TestLoadingErrorStopsSpinnerAndShowsReason(t *testing.T) {
	m := New(nil, nil, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	next, _ := m.Update(wallLoadedErrorMsg{err: errTestLoad})
	failed := next.(Model)
	if failed.loading {
		t.Fatal("после ошибки загрузки спиннер остался крутящимся")
	}
	if !strings.Contains(failed.loadError, errTestLoad.Error()) {
		t.Fatalf("текст ошибки = %q, ждали содержащий %q", failed.loadError, errTestLoad)
	}
	if notice := failed.wallNotice(); !strings.Contains(notice, errTestLoad.Error()) {
		t.Fatalf("на стене показано %q, а человек должен видеть причину сбоя", notice)
	}
	// Размер терминала обязателен: без него renderScreen не рисует ничего, и
	// проверка «видно на экране» прошла бы на пустом выводе.
	failed.width, failed.height = 100, 30
	failed.applyLayout()
	screen := ansi.Strip(failed.renderScreen())
	if !strings.Contains(screen, errTestLoad.Error()) {
		t.Fatalf("причины сбоя нет на экране:\n%s", screen)
	}
}

// Стена без карточек и без ошибки — это «чатов нет», а не тишина. Отдельная строка
// на месте стены, приглушённая: её не с чем спутать, и она не занимает экран.
func TestEmptyWallSaysThereAreNoChats(t *testing.T) {
	m := newTestModel(t, 100, 30)
	m.cards = nil
	// Спиннер на месте стены не нужен: при загрузке о нём говорит нижняя строка.
	m.loading = false
	if notice := m.wallNotice(); notice != "Чатов нет" {
		t.Fatalf("надпись пустой стены = %q, ждали %q", notice, "Чатов нет")
	}
}

// Пока идёт загрузка, надписи на месте стены нет: о том, что идёт, говорит уже
// нижняя строка со спиннером, и два одинаковых сообщения на экране шумят.
func TestLoadingWallHasNoNotice(t *testing.T) {
	m := New(nil, nil, "", config.DefaultTgwallKeyBindings(), config.DefaultSettings(), testAppName, testVersion)
	if notice := m.wallNotice(); notice != "" {
		t.Fatalf("при загрузке показано %q, а о загрузке уже говорит нижняя строка", notice)
	}
}

// Свой ник аккаунта стоит справа от лого (по указанию человека), с «@» — так,
// как его вводили в Telegram. Пустой ник (у аккаунта его нет вовсе) не
// печатается ничем: ни пустого «@», ни заглушки.
func TestStatusBarShowsOwnUsername(t *testing.T) {
	line := ansi.Strip(renderStatusBar(100, 0, false, "zeroscrypt", testAppName, testVersion))
	if !strings.Contains(line, "@zeroscrypt") {
		t.Fatalf("в нижней строке нет @ника: %q", line)
	}
	// Ник стоит после названия: порядок «индикатор, название, ник, …, версия».
	if strings.Index(line, "@zeroscrypt") < strings.Index(line, testAppName) {
		t.Fatalf("ник стоит перед названием, а не справа от лого: %q", line)
	}
}

func TestStatusBarOmitsUsernameWhenEmpty(t *testing.T) {
	for _, username := range []string{"", " "} {
		line := ansi.Strip(renderStatusBar(100, 0, false, strings.TrimSpace(username), testAppName, testVersion))
		if strings.Contains(line, "@") {
			t.Fatalf("ника нет, но в строке есть @: %q", line)
		}
		if !strings.Contains(line, testAppName) {
			t.Fatalf("пропал логотип вместе с ником: %q", line)
		}
	}
}

func TestStatusBarHasLogoAndVersion(t *testing.T) {
	line := ansi.Strip(renderStatusBar(100, 0, true, "", testAppName, testVersion))
	if !strings.Contains(line, testAppName) {
		t.Fatalf("в нижней строке нет названия %q: %q", testAppName, line)
	}
	if !strings.Contains(line, testVersion) {
		t.Fatalf("в нижней строке нет версии %q: %q", testVersion, line)
	}
	// Название слева, версия справа — между ними всё свободное место.
	logo := strings.Index(line, testAppName)
	version := strings.Index(line, testVersion)
	if logo > version {
		t.Fatalf("название не прижато влево, версия не вправо: %q", line)
	}
}

// На совсем узком терминале версия уходит целиком, а не обрезается половиной
// строки: обрезанный «v9.9.9-…» в углу читается как мусор.
//
// Ширина подобрана под длины testAppName/testVersion: названия (7 ячеек) должно
// хватать, а версии (13 ячеек) — нет. На более узком экране обрезался бы уже
// сам логотип, и проверка «версия ушла, название осталось» теряла бы смысл.
func TestStatusBarDropsVersionOnNarrowTerminal(t *testing.T) {
	line := ansi.Strip(renderStatusBar(20, 0, true, "", testAppName, testVersion))
	if strings.Contains(line, testVersion) {
		t.Fatalf("на узком терминале версия не помещается, но нарисована: %q", line)
	}
	if !strings.Contains(line, testAppName) {
		t.Fatalf("на узком терминале пропало название: %q", line)
	}
}

// statusBarLine — нижняя строка текущего экрана без ANSI-последовательностей:
// индикатор, название, ник и версия живут именно в ней.
func statusBarLine(t *testing.T, m Model) string {
	t.Helper()
	lines := strings.Split(ansi.Strip(m.renderScreen()), "\n")
	last := lines[len(lines)-1]
	if strings.TrimSpace(last) == "" {
		t.Fatalf("нижняя строка пустая: %q", last)
	}
	return last
}

// screenIndicator — содержимое колонки индикатора в нижней строке: ровно
// spinnerColumnWidth ячеек, начиная с cardMarginH.
//
// TrimSpace здесь недопустим: он срезал бы и отступ, и пустую колонку, и вернул
// бы первую букву названия — а «индикатор пуст» и есть проверяемое состояние.
func screenIndicator(t *testing.T, m Model) string {
	t.Helper()
	runes := []rune(statusBarLine(t, m))
	if len(runes) < cardMarginH+spinnerColumnWidth {
		t.Fatalf("нижняя строка короче колонки индикатора: %q", string(runes))
	}
	return string(runes[cardMarginH : cardMarginH+spinnerColumnWidth])
}
