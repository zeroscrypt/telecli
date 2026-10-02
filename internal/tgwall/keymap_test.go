package tgwall

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"telecli/internal/config"
	"telecli/internal/keyinput"
)

// Настраиваемые клавиши стены (задача 0158). Ключевая проверка здесь одна: поведение
// И подсказка берутся из одной и той же конфигурации, поэтому правка
// keybindings.toml меняет обе вещи сразу и не может оставить одну из них врать.

// defaultConfirmKeys — подписи клавиш модалки при дефолтной конфигурации. Нужны
// тестам, которые зовут confirmModalBlock напрямую, без модели.
func defaultConfirmKeys() confirmModalKeys {
	km := keyMap{keys: config.DefaultTgwallKeyBindings()}
	return confirmModalKeys{yes: km.label(keySelect), no: km.label(keyBack)}
}

// keysWith — стена с заданными клавишами, всё остальное как у настоящей: те же
// карточки, тот же терминал, тот же пересчёт раскладки (newTestModel, только вместо
// дефолтной конфигурации — заданная).
func keysWith(t *testing.T, keys config.TgwallKeyBindings, width, height int) Model {
	t.Helper()
	m := loadedModel(t)
	m.keymap = keyMap{keys: keys}
	m.width, m.height = width, height
	m.applyLayout()
	if m.input == nil {
		t.Fatal("поле ввода не создано")
	}
	_ = m.input.Focus()
	return m
}

// newTestModelWithKeys — то же, что keysWith, но с именем, совпадающим с
// newTestModel: так тесты про поведение читаются одинаково.
func newTestModelWithKeys(t *testing.T, width, height int, keys config.TgwallKeyBindings) Model {
	t.Helper()
	return keysWith(t, keys, width, height)
}

// TestDefaultKeysMatchTheHardcodedOnesBeforeConfig — дефолты конфигурации обязаны
// быть теми же клавишами, что были зашиты в Update до появления keybindings.toml:
// задача меняет источник клавиш, а не поведение. Сверяется весь набор, а не одна
// клавиша, — иначе переименование одной из них прошло бы незамеченным.
func TestDefaultKeysMatchTheHardcodedOnesBeforeConfig(t *testing.T) {
	for action, want := range map[keyAction]string{
		keyQuit:          "ctrl+c",
		keyReply:         "ctrl+r",
		keyMoveUp:        "up",
		keyMoveDown:      "down",
		keyPageUp:        "pgup",
		keyPageDown:      "pgdown",
		keyFocusNext:     "tab",
		keySelect:        "enter",
		keyBack:          "esc",
		keyDeleteMessage: "delete",
	} {
		km := keyMap{keys: config.DefaultTgwallKeyBindings()}
		bindings := km.bindings(action)
		if len(bindings) != 1 || bindings[0] != want {
			t.Fatalf("дефолт действия №%d = %v, ждали [%q]", int(action), bindings, want)
		}
	}
}

// TestPressedComparesConfigurationNotHardcodedKeys — ядро задачи: нажатие
// опознаётся по конфигурации. Смена клавиши в конфигурации обязана менять и то, на
// что стена реагирует, и то, что она печатает в подсказке, — обе вещи сразу.
func TestPressedComparesConfigurationNotHardcodedKeys(t *testing.T) {
	keys := config.DefaultTgwallKeyBindings()
	keys.MoveUp = []string{"k", "up"}
	m := keysWith(t, keys, 100, 30)

	// Зашитая клавиша продолжает работать: дефолт не должен ломать привычки.
	if !m.keymap.pressed(keyMoveUp, tea.KeyPressMsg{Code: tea.KeyUp}) {
		t.Fatal("стрелка вверх перестала двигать курсор после появления конфигурации")
	}
	if !m.keymap.pressed(keyMoveUp, tea.KeyPressMsg{Code: 'k', Text: "k"}) {
		t.Fatal("клавиша из конфигурации (k) не сработала")
	}
	if m.keymap.label(keyMoveUp) != "k" {
		t.Fatalf("подсказка показывает %q, а первой в конфигурации стоит k", m.keymap.label(keyMoveUp))
	}

	// И наоборот: убрали up из конфигурации — он обязан перестать работать, иначе
	// конфигурация была бы декорацией.
	keys.MoveUp = []string{"k"}
	m = keysWith(t, keys, 100, 30)
	if m.keymap.pressed(keyMoveUp, tea.KeyPressMsg{Code: tea.KeyUp}) {
		t.Fatal("стрелка вверх работает, хотя в конфигурации её больше нет")
	}
}

// TestUpdateRespondsToConfiguredKeys — то же самое, но через настоящий Update: не
// «pressed вернул true», а стена реально поехала по настроенной клавише и не
// поехала по зашитой.
func TestUpdateRespondsToConfiguredKeys(t *testing.T) {
	keys := config.DefaultTgwallKeyBindings()
	keys.MoveDown = []string{"n"}
	keys.Reply = []string{"ctrl+y"}
	m := newTestModelWithKeys(t, 100, 30, keys)
	// Курсор с конца списка вниз не сдвинется (там зажим), поэтому для проверки
	// сдвига он ставится в начало.
	m = m.moveCursor(-len(testCards()))

	start := m.cursor
	if start == len(testCards())-1 {
		t.Fatal("тест сломан: курсор не ушёл с последней карточки")
	}
	m = updateWith(t, m, tea.KeyPressMsg{Code: 'n', Text: "n"})
	if m.cursor != start+1 {
		t.Fatalf("настроенная клавиша n не сдвинула курсор (было %d, стало %d)", start, m.cursor)
	}
	m = updateWith(t, m, tea.KeyPressMsg{Code: tea.KeyDown})
	if m.cursor != start+1 {
		t.Fatalf("стрелка вниз сработала, хотя в конфигурации её заменили: курсор %d", m.cursor)
	}
	if m.replyTarget != nil {
		t.Fatal("зашитый Ctrl+R сработал, хотя в конфигурации его заменили")
	}
	m = updateWith(t, m, tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	if m.replyTarget == nil {
		t.Fatal("настроенный Ctrl+Y не открыл ответ")
	}
}

// TestQuitRespondsToConfiguredKey — выход тоже из конфигурации, а не зашитый
// Ctrl+C: иначе правка quit в файле была бы враньём.
func TestQuitRespondsToConfiguredKey(t *testing.T) {
	keys := config.DefaultTgwallKeyBindings()
	keys.Quit = []string{"ctrl+q"}
	m := newTestModelWithKeys(t, 100, 30, keys)

	next, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("настроенный Ctrl+Q не закрыл программу")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("команда = %T, ждали tea.QuitMsg", cmd())
	}
	// И зашитый Ctrl+C больше не выход: он идёт в поле текстом.
	next, cmd = next.(Model).Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("зашитый Ctrl+C всё ещё закрывает программу")
	}
	if got := next.(Model).input.Value(); got != "" {
		t.Fatalf("Ctrl+C не должен печататься в поле, а в поле %q", got)
	}

	// Дефолтный Ctrl+C закрывает программу — тот самый случай, ради которого он
	// был зашит (в raw-режиме терминала SIGINT не доходит до процесса иначе).
	def := newTestModel(t, 100, 30)
	_, cmd = def.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("дефолтный Ctrl+C не закрывает программу")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("команда = %T, ждали tea.QuitMsg", cmd())
	}
}

// TestDeleteRespondsToConfiguredKey — удаление открывает вопрос по настроенной
// клавише: Delete у стены был единственным действием с отдельной подписью в модалке,
// и он тоже не должен остаться зашитым.
func TestDeleteRespondsToConfiguredKey(t *testing.T) {
	keys := config.DefaultTgwallKeyBindings()
	keys.DeleteMessage = []string{"ctrl+x"}
	m := newTestModelWithKeys(t, 100, 30, keys)

	m = updateWith(t, m, tea.KeyPressMsg{Code: 'x', Mod: tea.ModCtrl})
	if !m.showConfirm {
		t.Fatal("настроенный Ctrl+X не открыл подтверждение удаления")
	}
	// Клавиша удаления в конфигурации не зашита: без неё удаление не открывается.
	plain := newTestModelWithKeys(t, 100, 30, keys)
	plain = updateWith(t, plain, tea.KeyPressMsg{Code: tea.KeyDelete})
	if plain.showConfirm {
		t.Fatal("зашитый Delete открыл подтверждение, хотя в конфигурации его заменили")
	}
	// Enter модалки — тоже из конфигурации (keySelect).
	m = updateWith(t, m, tea.KeyPressMsg{Code: 'j', Mod: tea.ModCtrl})
	if !m.showConfirm {
		t.Fatal("настроенный Ctrl+J закрыл модалку, хотя на Enter назначено keySelect")
	}
}

// TestConfirmModalHintFollowsConfiguredKeys — модалка показывает те же клавиши, на
// которые сама реагирует. Правка keybindings.toml обязана менять и то, и это за
// один раз.
func TestConfirmModalHintFollowsConfiguredKeys(t *testing.T) {
	keys := config.DefaultTgwallKeyBindings()
	keys.Select = []string{"ctrl+y"}
	keys.Back = []string{"ctrl+x"}
	m := keysWith(t, keys, 100, 30)

	// Подписи берутся из keymap'а, а не пишутся здесь руками: префикс ctrl
	// сокращён (см. wallDisplayKey), и зашитое в проверке «ctrl+y» разошлось бы
	// с тем, что на самом деле на экране.
	hint := m.confirmModalKeys().hint()
	if want := m.keymap.label(keySelect) + " — да"; !strings.Contains(hint, want) {
		t.Fatalf("подсказка модалки = %q, ждали %q", hint, want)
	}
	if want := m.keymap.label(keyBack) + " — нет"; !strings.Contains(hint, want) {
		t.Fatalf("подсказка модалки = %q, ждали %q", hint, want)
	}
	if strings.Contains(hint, "enter") || strings.Contains(hint, "esc") {
		t.Fatalf("подсказка модалки показывает зашитые клавиши: %q", hint)
	}
}

// TestHintLineFollowsConfiguredKeys — строка подсказок собирается из конфигурации:
// и перенос строки берётся у keymap'а самого виджета, а названия действий — из
// keymap стены.
func TestHintLineFollowsConfiguredKeys(t *testing.T) {
	keys := config.DefaultTgwallKeyBindings()
	keys.Select = []string{"ctrl+y"}
	keys.MoveUp = []string{"k"}
	m := keysWith(t, keys, 100, 30)

	want := "ctrl+p фильтр · ctrl+j строка · ctrl+y отправить · k лента"
	if got := m.hintLine(); got != want {
		t.Fatalf("подсказка = %q, ждали %q", got, want)
	}

	// Перенос строки — клавиша самого виджета, и подсказка идёт за ним, а не
	// остаётся с зашитым ctrl+j.
	input := newInput()
	input.KeyMap.InsertNewline.SetKeys("ctrl+w")
	changed := m
	changed.input = &input
	if got := changed.hintLine(); !strings.Contains(got, "ctrl+w строка · ") {
		t.Fatalf("подсказка = %q, ждали перенос строки по keymap виджета (ctrl+w)", got)
	}

	// Фильтр открывает keyOpenFilter, и его клавиша тоже берётся из конфигурации,
	// а не зашивается в текст подсказки.
	filterKeys := config.DefaultTgwallKeyBindings()
	filterKeys.Filter = []string{"ctrl+g"}
	withFilter := keysWith(t, filterKeys, 100, 30)
	if got := withFilter.hintLine(); !strings.HasPrefix(got, "ctrl+g фильтр · ") {
		t.Fatalf("подсказка = %q, ждали ctrl+g фильтр по keymap стены", got)
	}
}

// TestHintsShowFullCtrlPrefix — в подписях стены префикс «ctrl» показан полностью,
// без сокращения (задача 0170).
//
// Проверка идёт по ГОТОВОЙ строке подсказок, а не по вызову удалённой функции
// сокращения: после отмены функции проверять нечего, а «ctr» может появиться на
// экране только через keyinput.DisplayName — и вот его-то и надо ловить здесь.
// Регрессия реальна: сокращение «ctr» жило в коде стены и держалось одним тестом,
// который ждал именно «ctr», — то есть его существование и было тем, что этот тест
// охранял.
func TestHintsShowFullCtrlPrefix(t *testing.T) {
	m := keysWith(t, config.DefaultTgwallKeyBindings(), 100, 30)
	hint := m.hintLine()
	if !strings.Contains(hint, "ctrl+p фильтр") {
		t.Fatalf("подсказка = %q, ждали сочетание с полным префиксом «ctrl+p»", hint)
	}
	// Ищем «ctr+», а не просто «ctr»: слово «строка» в той же строке подсказок
	// содержит «ctr» как подстроку (с-т-р), и наивная проверка ругалась бы на
	// совершенно правильный текст. Префикс без «+» — это либо не сокращение вовсе,
	// либо его обрывок.
	if strings.Contains(hint, "ctr+") {
		t.Fatalf("подсказка = %q, а в ней снова появилось сокращение «ctr»", hint)
	}

	// Перенос строки живёт на ctrl+j и подписан тем же префиксом, что и остальные
	// сочетания строки: разные формы одного и того же в одной строке читались бы
	// как два разных языка подписей.
	if !strings.Contains(hint, "ctrl+j строка") {
		t.Fatalf("подсказка = %q, ждали перенос строки как «ctrl+j»", hint)
	}

	// Подписи без Ctrl сокращение не затрагивало и не должно было затрагивать:
	// служебные имена остаются в своём виде.
	for _, want := range []string{"↵ отправить", "↑ лента"} {
		if !strings.Contains(hint, want) {
			t.Fatalf("подсказка = %q, ждали подсказку %q на месте", hint, want)
		}
	}

	// Поведение сверяется с полным именем из конфигурации: отмена сокращения —
	// правка отображения, и нажатия от неё зависеть не могут.
	km := keyMap{keys: config.DefaultTgwallKeyBindings()}
	if !km.pressed(keyOpenFilter, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}) {
		t.Fatal("ctrl+p не сработало после отмены сокращения подписи")
	}
}

// TestHintLineEnterWordSaysReplyWhenReplyIsChosen — при выбранной цели ответа
// (Ctrl+R) подсказка обещает «ответить», а не «отправить»: слово «отправить»
// ничего не говорит о том, ЧТО отправится, и проверять это человеку придётся по
// строке ответа, а не по строке подсказок.
//
// Слово обязано быть «ответить» и при ПУСТОМ поле: цель выбрал человек, и
// проверка идёт по всем состояниям, где цель есть, — иначе подсказка мигала бы
// между «ответить» и «чат» в зависимости от того, напечатано что-то или нет.
func TestHintLineEnterWordSaysReplyWhenReplyIsChosen(t *testing.T) {
	narrow := newTestModel(t, 60, 30)
	// Переписка — с ЗАГРУЖЕННОЙ историей: Ctrl+R отвечает на сообщение под
	// курсором, а у пустой переписки сообщений под курсором нет, и цель просто не
	// выбралась бы (проверка тут про подсказку, а не про то, что цель бывает).
	// Модель с клиентом: без него загрузка истории не приходит вовсе.
	withClient := zoomModelWith(t, newZoomClient(), 60, 30)
	withChat, cmd := withClient.openWallCursorZoom()
	opened := runZoomLoad(t, withChat, cmd)
	for name, m := range map[string]Model{"узкий без чата": narrow, "узкий с чатом": opened} {
		replied := m.startReply()
		if replied.replyTarget == nil {
			t.Fatalf("%s: подготовка — Ctrl+R не выбрал цель ответа", name)
		}
		// Поле пустое: «ответить» всё равно обязано быть, цель выбрана.
		if strings.TrimSpace(replied.input.Value()) != "" {
			t.Fatalf("%s: подготовка — поле непустое", name)
		}
		if got := replied.hintLine(); !strings.Contains(got, "↵ ответить") {
			t.Errorf("%s: подсказка = %q, ждали «enter ответить»", name, got)
		}
		// И с набранным текстом — слово то же, цель не должна влиять на него.
		typed := replied
		typed.input.SetValue("спасибо")
		if got := typed.hintLine(); !strings.Contains(got, "↵ ответить") {
			t.Errorf("%s с текстом: подсказка = %q, ждали «enter ответить»", name, got)
		}
	}

	// Снятие цели (Esc) возвращает обычное слово: подсказка не должна продолжать
	// обещать ответ на сообщение, которого больше не отправляют.
	cleared := narrow.startReply().cancelWallFocus()
	if cleared.replyTarget != nil {
		t.Fatalf("подготовка: Esc не снял цель ответа")
	}
	if got := cleared.hintLine(); strings.Contains(got, "ответить") {
		t.Errorf("после Esc подсказка = %q, а обещает «ответить» при снятой цели", got)
	}
}

// TestHintLineUpWordFollowsFocus — «↑» листает то, что в фокусе, и подсказка
// обещает ровно это. В широком режиме фокус переключается Tab: на стене —
// «лента», на переписке — «чат». Проверка на обоих состояниях обязательна: одна
// подпись на оба состояния была бы прямо врёт там, где человек в неё поверил.
func TestHintLineUpWordFollowsFocus(t *testing.T) {
	narrow := newTestModel(t, 60, 30)
	if got := narrow.hintLine(); !strings.Contains(got, "↑ лента") {
		t.Fatalf("узкий режим без чата: подсказка = %q, ждали «↑ лента»", got)
	}

	opened, _ := narrow.openWallCursorZoom()
	if got := opened.hintLine(); !strings.Contains(got, "↑ чат") {
		t.Fatalf("узкий режим с открытым чатом: подсказка = %q, ждали «↑ чат»", got)
	}

	wide := newTestModel(t, 100, 30)
	wide.zoom = &wallZoom{title: "Рабочий чат"}
	if got := wide.hintLine(); !strings.Contains(got, "↑ лента") {
		t.Fatalf("широкий режим, фокус на стене: подсказка = %q, ждали «↑ лента»", got)
	}
	focused, _ := wide.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	next := focused.(Model)
	if !next.zoomHasFocus() {
		t.Fatal("Tab не перевёл фокус на переписку — проверка не о том")
	}
	if got := next.hintLine(); !strings.Contains(got, "↑ чат") {
		t.Fatalf("широкий режим, фокус на переписке: подсказка = %q, ждали «↑ чат»", got)
	}
}

// TestHintLineEnterWordFollowsState — Enter обещает ровно то, что сделает:
// отправку, когда есть что отправлять, и открытие чата в узком режиме с пустым
// полем. Пустое поле — это и есть «чат», а не «отправить»: обещать отправку
// пустого сообщения, которого не будет, нельзя.
func TestHintLineEnterWordFollowsState(t *testing.T) {
	narrow := newTestModel(t, 60, 30)
	if got := narrow.hintLine(); !strings.Contains(got, "↵ чат") {
		t.Fatalf("узкий режим, пустое поле: подсказка = %q, ждали «enter чат»", got)
	}

	opened, _ := narrow.openWallCursorZoom()
	if got := opened.hintLine(); !strings.Contains(got, "↵ отправить") {
		t.Fatalf("узкий режим, чат открыт: подсказка = %q, ждали «enter отправить»", got)
	}

	typed := narrow
	typed.input.SetValue("привет")
	if got := typed.hintLine(); !strings.Contains(got, "↵ отправить") {
		t.Fatalf("узкий режим, есть текст: подсказка = %q, ждали «enter отправить»", got)
	}

	// Из одних пробелов отправлять нечего: submitInput точно так же отбрасывает
	// их через TrimSpace, и подсказка про «чат» не должна появляться из-за
	// пробела, который уйдёт в никуда.
	blank := narrow
	blank.input.SetValue("   ")
	if got := blank.hintLine(); !strings.Contains(got, "↵ чат") {
		t.Fatalf("узкий режим, одни пробелы: подсказка = %q, ждали «enter чат»", got)
	}

	wide := newTestModel(t, 100, 30)
	if got := wide.hintLine(); !strings.Contains(got, "↵ отправить") {
		t.Fatalf("широкий режим: подсказка = %q, ждали «enter отправить»", got)
	}
}

// TestHintLinePromisesNothingThatDoesNotExist — в подсказке нет «/ поиск», хотя
// прежний зашитый текст её обещал: действия поиска у стены нет, и обещать клавишу,
// которая ничего не делает, нельзя. Случай, когда её добавят настоящим действием,
// ломает эту проверку — и это правильно: тогда подсказку придётся пересобрать.
func TestHintLinePromisesNothingThatDoesNotExist(t *testing.T) {
	m := newTestModel(t, 100, 30)
	if hint := m.hintLine(); strings.Contains(hint, "поиск") {
		t.Fatalf("подсказка обещает поиск, которого у стены нет: %q", hint)
	}
}

// TestKeyMapWorksOnTheRussianLayout — хоткей-буква должна работать на раскладке, на
// которой человек набирает текст. Терминал раскладку не сообщает, поэтому буква
// опознаётся и по русскому соседу (см. keyinput.KeyPeer).
//
// Проверяется в обе стороны и по-настоящему: клавиша, записанная в конфигурации
// латиницей, срабатывает и по русскому нажатию, и наоборот. С дефолтами стены это
// буквенной клавиши не касается (там только ctrl+буква и служебные имена), поэтому
// ради проверки берётся нестандартная конфигурация — человек вправе так настроить.
func TestKeyMapWorksOnTheRussianLayout(t *testing.T) {
	keys := config.DefaultTgwallKeyBindings()
	keys.MoveUp = []string{"w"}
	latin := keysWith(t, keys, 100, 30)
	if !latin.keymap.pressed(keyMoveUp, tea.KeyPressMsg{Code: 'ц', Text: "ц"}) {
		t.Fatal("русское нажатие не сработало на латинскую букву в конфигурации")
	}

	keys.MoveUp = []string{"ц"}
	cyrillic := keysWith(t, keys, 100, 30)
	if !cyrillic.keymap.pressed(keyMoveUp, tea.KeyPressMsg{Code: 'w', Text: "w"}) {
		t.Fatal("латинское нажатие не сработало на русскую букву в конфигурации")
	}
	// Соседи по таблице именно такие: «w» стоит на клавише «ц» (см. keyinput).
	if peer := keyinput.KeyPeer("w"); peer != "ц" {
		t.Fatalf("сосед w по таблице = %q, ждали ц", peer)
	}
	if peer := keyinput.KeyPeer("ц"); peer != "w" {
		t.Fatalf("сосед ц по таблице = %q, ждали w", peer)
	}
}

// TestCtrlLetterDoesNotDependOnTheLayout — клавиша с Ctrl от раскладки не зависит:
// терминал шлёт управляющий байт по физической клавише, а не символ, поэтому
// Ctrl+R это тот же байт на любой раскладке, и сосед по таблице к нему не
// применяется (у «ctrl+r» соседа нет). Проверяется и то, что хоткей вообще жив, и
// то, что русская буква сама по себе его не подменяет.
func TestCtrlLetterDoesNotDependOnTheLayout(t *testing.T) {
	m := newTestModel(t, 100, 30)
	if !m.keymap.pressed(keyReply, tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl}) {
		t.Fatal("Ctrl+R не сработало")
	}
	if keyinput.KeyPeer("ctrl+r") != "" {
		t.Fatal("у клавиши с Ctrl не должно быть соседа по раскладке")
	}
	if m.keymap.pressed(keyReply, tea.KeyPressMsg{Code: 'з', Text: "з"}) {
		t.Fatal("русская буква без Ctrl сработала как ctrl+r")
	}
}

// TestNamedKeysAreRecognisedByTheirConfigNames — служебные клавиши стены обязаны
// опознаваться по именам из конфигурации, а не по зашитым кодам bubbletea. Имена
// те же, что у самого терминального слоя: «delete» здесь — именно KeyDelete
// (клавиша ⌦), а не Backspace.
func TestNamedKeysAreRecognisedByTheirConfigNames(t *testing.T) {
	for name, want := range map[string]rune{
		"up":     tea.KeyUp,
		"down":   tea.KeyDown,
		"pgup":   tea.KeyPgUp,
		"pgdown": tea.KeyPgDown,
		"esc":    tea.KeyEscape,
		"enter":  tea.KeyEnter,
		"tab":    tea.KeyTab,
		"delete": tea.KeyDelete,
	} {
		msg, ok := keyinput.MsgForName(name)
		if !ok {
			t.Fatalf("имя клавиши %q не понимается", name)
		}
		if msg.Key().Code != want {
			t.Fatalf("имя %q разбирается в код %d, ждали %d", name, msg.Key().Code, want)
		}
		// И обратно: имя нажатой клавиши совпадает с тем, что написано в файле.
		if got := keyinput.KeyName(msg); got != name {
			t.Fatalf("нажатие %q названо %q", name, got)
		}
	}
}

// updateWith — Update с проверкой типа возврата: тесты клавиш не должны молча
// работать на другой модели.
func updateWith(t *testing.T, m Model, msg tea.Msg) Model {
	t.Helper()
	next, _ := m.Update(msg)
	updated, ok := next.(Model)
	if !ok {
		t.Fatalf("Update вернул %T, а не Model", next)
	}
	return updated
}
