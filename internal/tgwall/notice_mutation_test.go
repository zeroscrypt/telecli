package tgwall

import (
	"strings"
	"testing"
)

// Мутационная проверка обязательных требований каркаса экрана.
//
// Файл существует ради одной цели: убедиться, что проверки из topbar_test.go и
// notice_test.go ЛОВЯТ отказ от каждого из них (строки каркаса учтены в высоте,
// верхняя строка нарисована ровно одна, строка сообщений занята всегда, текст в
// ней центрирован и обрезан, версия сообщения сверяется на гашении). Обычный
// тест проходит и на неправильном коде, если неправильность живёт в ветке,
// которую этот тест не задевает; здесь каждая мутация — это проверка ДРУГОГО
// теста.
//
// Содержание самой верхней строки (подпись фильтра, название чата, обрезка по
// ширине) — не здесь, а в topbar_mutation_test.go: это требования topbar.go, а
// в этом файле про каркас.
//
// Механика (копия модуля в t.TempDir, прогон go test по содержимому, требование
// падения ИМЕННО нужного теста) — та же, что в statusbar_mutation_test.go,
// хелперы переиспользуются оттуда и из zoom_mutation_test.go, свои дубли не
// заводились.

// noticeMutants — по несколько мутаций на каждое обязательное требование.
var noticeMutants = []mutantCase{
	{
		// Требование «wallHeight вычитает ОБЕ строки каркаса». Без верхней строки
		// стена занимает на строку больше, чем есть на экране, и нижняя строка со
		// спиннером уезжает за обрезку.
		name:      "высота стены забыла верхнюю строку",
		file:      "model.go",
		from:      "m.height-wallReservedRows-wallTopBarRows-wallNoticeRows-m.inputRows",
		to:        "m.height-wallReservedRows-wallNoticeRows-m.inputRows",
		wantFails: "TestScreenFrameIsTopBarWallNoticeAndTail",
	},
	{
		// То же про строку сообщений: без неё в формуле экран выше терминала
		// ровно на одну строку.
		name:      "высота стены забыла строку сообщений",
		file:      "model.go",
		from:      "m.height-wallReservedRows-wallTopBarRows-wallNoticeRows-m.inputRows",
		to:        "m.height-wallReservedRows-wallTopBarRows-m.inputRows",
		wantFails: "TestScreenFrameIsTopBarWallNoticeAndTail",
	},
	{
		// Предел роста поля — вторая формула той же арифметики: если он не знает
		// про строки каркаса, длинное значение поля съедает каркас, и нижняя строка
		// пропадает с экрана.
		name:      "предел роста поля забыл новые строки каркаса",
		file:      "model.go",
		from:      "m.height-wallReservedRows-wallTopBarRows-wallNoticeRows-m.replyRows()-inputMinHeight",
		to:        "m.height-wallReservedRows-m.replyRows()-inputMinHeight",
		wantFails: "TestLongInputDoesNotEatTheScreenFrame",
	},
	{
		// Верхняя строка должна быть нарисована: без неё каркас поехал бы на
		// строку, а стена и строка сообщений остались бы на месте — то есть
		// «где-то на экране есть строка фильтра» такой тест бы пропустил.
		name:      "верхняя строка не рисуется вовсе",
		file:      "model.go",
		from:      "lines = append(lines, m.renderTopBar(m.width))",
		to:        "_ = m.renderTopBar",
		wantFails: "TestScreenFrameIsTopBarWallNoticeAndTail",
	},
	{
		// Верхняя строка одна: двумя строками стена съехала бы на лишний ряд, а
		// проверка «на экране есть подпись фильтра» осталась бы довольна.
		name:      "верхняя строка занимает две строки вместо одной",
		file:      "model.go",
		from:      "wallTopBarRows = 1",
		to:        "wallTopBarRows = 2",
		wantFails: "TestScreenFrameIsTopBarWallNoticeAndTail",
	},
	{
		// Требование «строка сообщений занята всегда». Без неё экран прыгал бы на
		// строку в момент появления и исчезновения сообщения.
		name:      "пустая строка сообщений не рисуется вовсе",
		file:      "notice.go",
		from:      "return renderedFill(width, background)",
		to:        "return \"\"",
		wantFails: "TestEmptyNoticeRowIsWallBackgroundNotPanel",
	},
	{
		// Требование «фон строки сообщений — фон стены, а не панели»: иначе она
		// выглядит отдельной панелью, которой быть не должно.
		name:      "строка сообщений нарисована панельным фоном",
		file:      "notice.go",
		from:      "background := PaletteBackgroundMain",
		to:        "background := PaletteBackgroundPanel",
		wantFails: "TestEmptyNoticeRowIsWallBackgroundNotPanel",
	},
	{
		// Требование «текст по центру».
		name:      "текст сообщения прижат к левому краю",
		file:      "notice.go",
		from:      "Align(lipgloss.Center)",
		to:        "Align(lipgloss.Left)",
		wantFails: "TestSystemNoticeIsVisibleRightAfterShowing",
	},
	{
		// Требование «длинный текст обрезается, строка остаётся одной»: без
		// обрезки липглосс переносил бы сообщение на вторую строку, и расчёт
		// высоты стены разошёлся бы с отрисовкой.
		name:      "длинный текст сообщения не обрезается по ширине",
		file:      "notice.go",
		from:      "Render(truncateVisible(notice.text, width))",
		to:        "Render(notice.text)",
		wantFails: "TestSystemNoticeTruncatesLongText",
	},
	{
		// Требование «таймер гасит только свою версию»: без сверки сообщение,
		// сменившееся за пять секунд, гасло бы вместе со старым.
		name:      "гашение не сверяет версию сообщения",
		file:      "model.go",
		from:      "if m.notice != nil && m.notice.id == msg.id {",
		to:        "if m.notice != nil {",
		wantFails: "TestStaleNoticeTimerKeepsTheNewerNotice",
	},
	{
		// Требование «версия растёт на каждый вызов». Счётчик, начатый с
		// единицы, выдаёт двум подряд показанным сообщениям один номер — и
		// таймер от первого гасит второе.
		name:      "счётчик версий сообщения не растёт",
		file:      "notice.go",
		from:      "m.noticeID++",
		to:        "m.noticeID = 1",
		wantFails: "TestStaleNoticeTimerKeepsTheNewerNotice",
	},
	{
		// Требование «метод возвращает таймер»: без него сообщение не гаснет
		// никогда, и никакой код его уже не снимет.
		name:      "показ сообщения не заводит таймер гашения",
		file:      "notice.go",
		from:      "return m, systemNoticeClearCmd(m.noticeID)",
		to:        "return m, nil",
		wantFails: "TestSystemNoticeIsVisibleRightAfterShowing",
	},
}

func TestNoticeMutationsAreCaught(t *testing.T) {
	for _, mutation := range noticeMutants {
		t.Run(mutation.name, func(t *testing.T) {
			// Свежая копия на КАЖДУЮ мутацию: три из них правят один и тот же
			// фрагмент формулы высоты, и на общем дереве вторая не нашла бы свой
			// образец уже изменённым первой.
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
			// Падать должен ИМЕННО тот тест: если упал соседний, защита может быть
			// и не покрыта, просто сломалось что-то по соседству.
			failed := firstFailure(output)
			if !strings.Contains(failed, mutation.wantFails) {
				t.Fatalf("мутация сломала другой тест (%s), а ждали %s:\n%s",
					failed, mutation.wantFails, tail(output))
			}
			t.Logf("поймана: %s", failed)
		})
	}
}
