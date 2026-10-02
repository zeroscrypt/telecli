package tgwall

import "testing"

// Геометрия панелей переписки проверяется ПО ЗНАЧЕНИЯМ, а не по экрану: сумма
// ширин панелей, колонки стены и зазоров — это ровно та арифметика, по которой
// десять мест в коде меряют окно скролла и рисуют рамки, и ошибка на ячейку в ней
// проявляется на экране не сразу, а как «текст не влез» уже в разобраться сложном
// месте. Поэтому здесь сравниваются числа, а счёт по константам проекта
// выполняется отдельно — иначе проверка была бы самоисполняющейся и ловила бы
// ровно ту же формулу, что и проверяемый код.

// TestWallPanelWidthsSplitRemainderToFirstPanel — ядро раскладки: правая зона
// делится пополам, и НЕЧЕТНЫЙ остаток забирает ПЕРВАЯ панель.
//
// Почему именно так (правило человека, 2026-10-01: «если остаётся 81, первой
// достаётся 41, второй 40»): при отдаче остатка второй панели распределение
// ячеек было бы тем же самым, но рамка второй панели прыгала бы на ячейку при
// каждом ресайзе, потому что лишняя ячейка переезжала бы с панели на панель.
// Первой достаётся остаток ещё и потому, что панель 1 — та, что у самой ленты:
// её рамка прижата к колонке стены, и «прыгать» должна панель, которую человек
// видит реже, а не та, что стоит под рукой.
//
// Ожидаемые числа зашиты вручную, а не пересчитаны той же формулой, что и в
// коде: пересчёт поймал бы ровно ту же ошибку, которую проверяем.
func TestWallPanelWidthsSplitRemainderToFirstPanel(t *testing.T) {
	for _, test := range []struct {
		width  int
		first  int
		second int
	}{
		// Зона = ширина терминала минус колонка стены (30), минус зазор у каждой панели
		// (по одному на панель) и минус поле справа от крайней панели (2). Остаток
		// нечётный отдаётся ПЕРВОЙ панели — по прямому указанию человека.
		{width: 111, first: 39, second: 38}, // зона 77
		{width: 112, first: 39, second: 39}, // зона 78, делится ровно
		{width: 113, first: 40, second: 39}, // зона 79, лишняя ячейка — первой
		{width: 141, first: 54, second: 53}, // зона 107
		{width: 200, first: 83, second: 83}, // зона 166, делится ровно
	} {
		m := newTestModel(t, test.width, 30)
		got := m.wallPanelWidths()
		if len(got) != 2 {
			t.Fatalf("ширина %d: панелей переписки %d (%v), ждали 2", test.width, len(got), got)
		}
		if got[0] != test.first || got[1] != test.second {
			t.Fatalf("ширина %d: панели %d и %d, ждали %d и %d (зона %d)",
				test.width, got[0], got[1], test.first, test.second, m.wallChatZoneWidth())
		}
		// Первая панель никогда не уже второй: иначе после округления вниз
		// раскладка выглядела бы «веером», и остаток уезжал бы не в ту сторону.
		if got[0] < got[1] {
			t.Fatalf("ширина %d: первая панель %d уже второй %d", test.width, got[0], got[1])
		}
	}
}

// TestTwoPanelModeIsStrictlyAboveThreshold — порог трёхколоночного режима строго
// «больше 110», а не «начиная с 110» (формулировка человека, 2026-10-01).
// Ровно одна ячейка разницы на границе, поэтому и проверяются обе стороны порога:
// сдвиг на «>=» тихо уехал бы на соседнюю ширину терминала.
func TestTwoPanelModeIsStrictlyAboveThreshold(t *testing.T) {
	if newTestModel(t, 110, 30).wallTwoPanelMode() {
		t.Fatal("на ширине 110 трёхколоночный режим уже включился, ждали выключенным")
	}
	if !newTestModel(t, 111, 30).wallTwoPanelMode() {
		t.Fatal("на ширине 111 трёхколоночный режим не включился, ждали включённым")
	}
	// Порог — константа проекта, а не следствие прочей геометрии: иначе смена
	// wallZoomThreshold незаметно утащила бы за собой и число панелей.
	if wallTwoPanelThreshold != 110 {
		t.Fatalf("wallTwoPanelThreshold = %d, ждали 110", wallTwoPanelThreshold)
	}
}

// TestWallPanelsCountByTerminalWidth — сколько панелей переписки есть на
// терминале: ноль в узком режиме (переписка занимает весь ввод, отдельной панели
// нет), одна в обычном широком и две в трёхколоночном. Проверяются обе границы
// каждого режима — 65/66 и 110/111, — потому что число панелей по ним и
// переключается, а опечатка в одном знаке меняла бы раскладку только на одной
// ширине терминала.
func TestWallPanelsCountByTerminalWidth(t *testing.T) {
	for _, test := range []struct {
		width int
		want  int
	}{
		{width: 60, want: 0},  // узкий: переписка во весь экран
		{width: 65, want: 0},  // ровно порог узкого режима
		{width: 66, want: 1},  // широкий обычный: одна панель на всю правую зону
		{width: 100, want: 1}, // обычный широкий
		{width: 110, want: 1}, // ещё одна панель не помещается
		{width: 111, want: 2}, // трёхколоночный
		{width: 240, want: 2}, // широкий трёхколоночный
	} {
		m := newTestModel(t, test.width, 30)
		if got := m.wallChatPanels(); got != test.want {
			t.Fatalf("ширина %d: панелей переписки %d, ждали %d", test.width, got, test.want)
		}
		if got, want := len(m.wallPanelWidths()), test.want; got != want {
			t.Fatalf("ширина %d: ширин панелей %d, а wallChatPanels() = %d", test.width, got, test.want)
		}
	}
}

// TestWallPanelWidthsFillTerminalExactly — панели, колонка стены и зазоры между
// ними в сумме занимают ширину терминала ровно, без остатка и без наезда.
//
// Считается по всему диапазону ширин, а не по нескольким «красивым» числам:
// сумма проходит на 112 и слетает на 113, если забыт один зазор или взят не тот
// знак в wallPanelStart. Отдельно проверяется, что ширина панели не нулевая и не
// отрицательная — панель нулевой ширины не рисует рамку и роняет деление на
// свою ширину уже в отрисовке.
//
// Узкие ширины (65 и меньше) из суммы исключены не по недосмотру: там
// трёхколоночной раскладки нет вовсе, колонка стены занимает всю ширину
// терминала, и складывать её с панелями бессмысленно. Вместо этого проверяется
// ровно то, что на этих ширинах верно: панелей нет, а колонка — во всю ширину.
//
// ТЕСТ СЕЙЧАС ПАДАЕТ, и это дефект panels.go, а не ошибка проверки: при двух
// панелях между ними тоже рисуется зазор (renderWallZone склеивает ВСЕ колонки
// через wallPanelGapWidth, а wallPanelStart прибавляет его же при переходе к
// панели 2), но wallChatZoneWidth вычитает из ширины терминала зазор только
// один. Из-за этого зона шире, чем есть места, и панель 2 уезжает на ячейку за
// правый край терминала. Проверено мутацией: с вычитанием второго зазора этот
// тест проходит, и тогда падает TestWallPanelWidthsSplitRemainderToFirstPanel —
// то есть таблица ширин и требование «сумма сходится» сейчас несовместимы между
// собой, и какое из них правильное — решать автору panels.go (см. отчёт).
func TestWallPanelWidthsFillTerminalExactly(t *testing.T) {
	for width := 65; width <= 240; width++ {
		m := newTestModel(t, width, 30)
		widths := m.wallPanelWidths()
		panels := m.wallChatPanels()

		if panels == 0 {
			// Узкий режим: раскладки с колонкой стены и панелями ещё нет.
			if len(widths) != 0 {
				t.Fatalf("ширина %d: при %d панелях переписки ширин %d", width, panels, len(widths))
			}
			if got := m.wallColumnWidth(); got != width {
				t.Fatalf("ширина %d: колонка стены %d, а без панелей она во всю ширину", width, got)
			}
			continue
		}

		if len(widths) != panels {
			t.Fatalf("ширина %d: wallChatPanels() = %d, а ширин %d", width, panels, len(widths))
		}
		for index, panel := range widths {
			if panel <= 0 {
				t.Fatalf("ширина %d: панель №%d шириной %d — рисовать нечего", width, index+1, panel)
			}
		}

		// Зазоров ровно по числу панелей: один справа от колонки стены и по
		// одному между соседними панелями. Плюс сама колонка и поле справа от
		// крайней панели (wallPanelInsetRight) — иначе рамка последней панели
		// упиралась бы в край терминала.
		total := wallPanelColumnWidth + wallPanelGapWidth*panels + wallPanelInsetRight
		for _, panel := range widths {
			total += panel
		}
		if total != width {
			// Сообщение называет ВИДИМОЕ последствие расхождения, а не только
			// числа: лишняя ячейка — это панель, уехавшая за правый край
			// терминала, то есть на экране рамка второй панели обрезается.
			t.Fatalf("ширина %d: колонка %d + зазоры %d + панели %v = %d, лишних/недостающих ячеек %d",
				width, wallPanelColumnWidth, wallPanelGapWidth*panels, widths, total, total-width)
		}
	}
}

// TestWallPanelStartFollowsColumnAndPreviousPanel — с какой колонки начинается
// каждая панель.
//
// Панель 1 стоит вплотную к колонке стены, то есть начинается сразу за её
// правым краем и за зазором: 30 + 1 = 31. Панель 2 начинается за панелью 1 и за
// СВОИМ зазором — на том месте, где на самом деле рисуется её левая рамка, а не
// на колонке стены: именно поэтому верхняя строка экрана называет чат над той
// колонкой, в которой он нарисован.
//
// Панель ленты начинается с нуля: она и так первая на экране, и «начало» у неё
// — сам левый край терминала, а не сдвиг за колонкой.
func TestWallPanelStartFollowsColumnAndPreviousPanel(t *testing.T) {
	// 120 — заведомо трёхколоночный режим, где есть обе панели переписки.
	m := newTestModel(t, 120, 30)

	if got, want := m.wallPanelStart(panelChat1), wallPanelColumnWidth+wallPanelGapWidth; got != want {
		t.Fatalf("панель 1 с колонки %d, ждали %d (= %d + %d)",
			got, want, wallPanelColumnWidth, wallPanelGapWidth)
	}

	wantSecond := m.wallPanelStart(panelChat1) + m.wallPanelWidth(panelChat1) + wallPanelGapWidth
	if got := m.wallPanelStart(panelChat2); got != wantSecond {
		t.Fatalf("панель 2 с колонки %d, ждали %d (панель 1 шириной %d + зазор %d)",
			got, wantSecond, m.wallPanelWidth(panelChat1), wallPanelGapWidth)
	}

	if got := m.wallPanelStart(panelWall); got != 0 {
		t.Fatalf("панель ленты с колонки %d, ждали 0 (она первая на экране)", got)
	}

	// За последней панелью — поле шириной wallPanelInsetRight, а не вплотную край
	// терминала: рамка панели должна отстоять от края экрана (решение человека,
	// 2026-10-01).
	order := m.wallChatPanelsOrder()
	if len(order) == 0 {
		t.Fatalf("на ширине %d не оказалось ни одной панели переписки", m.width)
	}
	last := order[len(order)-1]
	if right := m.wallPanelStart(last) + m.wallPanelWidth(last) + wallPanelInsetRight; right != m.width {
		t.Fatalf("последняя панель %d заканчивается на колонке %d при ширине %d", last, right, m.width)
	}
}

// TestWallPanelListWidthIsPanelWidthMinusOutline — содержимое панели уже самой
// панели ровно на две ячейки: по одной слева и справа занимает контур.
//
// Место под контур зарезервировано ВСЕГДА, а не только у панели в фокусе, и это
// здесь и проверяется: если бы контур вычитался только в фокусе, переключение
// фокуса по Tab двигало бы текст на две ячейки, и раскладка прыгала бы в момент
// нажатия. Считать на панели не в фокусе.
//
// У ленты панели переписки нет вовсе, и ширина её содержимого — ноль, а не
// отрицательные два: отрицательная ширина уехала бы в отрисовку как срез «с
// конца».
func TestWallPanelListWidthIsPanelWidthMinusOutline(t *testing.T) {
	// 100 — обычный широкий режим, 120 — трёхколоночный: обе раскладки.
	for _, width := range []int{66, 100, 111, 120, 200} {
		m := newTestModel(t, width, 30)
		for _, panel := range append(m.wallChatPanelsOrder(), panelWall) {
			listWidth := m.wallPanelListWidth(panel)
			if listWidth < 0 {
				t.Fatalf("ширина %d, панель %d: ширина содержимого %d", width, panel, listWidth)
			}
			if panel == panelWall {
				if listWidth != 0 {
					t.Fatalf("ширина %d: у ленты ширина содержимого %d, ждали 0", width, listWidth)
				}
				continue
			}
			if want := m.wallPanelWidth(panel) - wallPanelOutlineInset; listWidth != want {
				t.Fatalf("ширина %d, панель %d: ширина содержимого %d, ждали %d (панель %d - %d)",
					width, panel, listWidth, want, m.wallPanelWidth(panel), wallPanelOutlineInset)
			}
			if listWidth == 0 {
				t.Fatalf("ширина %d, панель %d: содержимое нулевой ширины", width, panel)
			}
		}
	}
}

// TestWallPanelsDoNotOverlap — соседние панели переписки не наезжают друг на
// друга: между концом первой и началом второй ровно одна ячейка зазора.
//
// Проверяется в трёхколоночном режиме по всему диапазону ширин, потому что
// именно там две панели стоят рядом и именно там ошибка в счёте зазора даёт
// влезающий на одну ячейку лишний текст. В обычном широком режиме второй панели
// нет, и несуществующая панель обязана иметь нулевую ширину, а не «хоть какую» —
// иначе по её колонке что-то нарисуется на месте пустоты.
func TestWallPanelsDoNotOverlap(t *testing.T) {
	for width := 111; width <= 240; width++ {
		m := newTestModel(t, width, 30)

		if got, want := m.wallPanelStart(panelChat2)-m.wallPanelStart(panelChat1),
			m.wallPanelWidth(panelChat1)+wallPanelGapWidth; got != want {
			t.Fatalf("ширина %d: панель 1 шириной %d, а панель 2 начинается на %d ячеек позже (зазор %d)",
				width, m.wallPanelWidth(panelChat1), got, wallPanelGapWidth)
		}
		if got := m.wallPanelStart(panelChat2); got <= m.wallPanelStart(panelChat1) {
			t.Fatalf("ширина %d: панель 2 с колонки %d не правее панели 1 с колонки %d",
				width, got, m.wallPanelStart(panelChat1))
		}
	}

	// Одна панель: второй нет, и она не занимает на экране ничьей ширины.
	one := newTestModel(t, 100, 30)
	if got := one.wallPanelWidth(panelChat2); got != 0 {
		t.Fatalf("в обычном широком режиме ширина второй панели %d, ждали 0", got)
	}
	if got := one.wallPanelWidth(panelWall); got != 0 {
		t.Fatalf("ширина панели ленты как панели переписки %d, ждали 0", got)
	}
}
