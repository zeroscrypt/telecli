package tgwall

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Мутационная проверка обязательных требований задачи 0155.
//
// Файл существует ради одной цели: убедиться, что проверки из zoom_test.go
// ЛОВЯТ отказ от каждого из четырёх обязательных защит. Обычный тест проходит и на
// неправильном коде, если неправильность живёт в ветке, которую этот тест не
// задевает; здесь каждая мутация — это проверка ДРУГОГО теста, а не новый тест.
//
// Мутации применяются к копии файлов в t.TempDir, пакет собирается и
// прогоняется go test -run на СОДЕРЖИМОЕ, а не на мутированные проверки: если
// нужный тест после мутации всё ещё зелёный, защита не покрыта.

// mutantCase — одна мутация: что и на что заменяется в исходнике копии, и какой
// тест обязан после этого упасть.
type mutantCase struct {
	name string
	file string
	from string
	to   string
	// wantFails — имя теста (подстрока для -run), который обязан УПАСТЬ на мутации.
	wantFails string
}

// mutantCases — по несколько мутаций на каждое обязательное требование задачи.
var mutantCases = []mutantCase{
	{
		// (а) Защита от гонки загрузки: сверка источника и номера попытки.
		// Без неё устаревший ответ предыдущего источника применяется поверх
		// текущей переписки.
		name:      "гонка загрузки: сверка чата и номера попытки отброшена",
		file:      "zoom.go",
		from:      "if zoom == nil || zoom.chatID != msg.chatID || zoom.loadID != msg.loadID {",
		to:        "if false {",
		wantFails: "TestWideStaleZoomResponseIsDiscarded",
	},
	{
		// (а, вторая половина) loadID на Model обнуляется при каждом открытии:
		// повторное открытие того же чата начинается с того же номера, и ответ
		// прошлого открытия проходит сверку.
		name:      "гонка загрузки: счётчик попыток не растёт",
		file:      "zoom.go",
		from:      "\tm.zoomLoadID++\n",
		to:        "\tm.zoomLoadID = 1\n",
		wantFails: "TestWideStaleZoomResponseIsDiscarded",
	},
	{
		// (б) Слоёная отмена Esc в узком режиме: если снятие цели ответа
		// пропущено, Esc сразу закрывает переписку, и выбранный ответ теряется
		// вместе с ней.
		name:      "слоёный Esc: ступень снятия цели ответа пропущена",
		file:      "model.go",
		from:      "\tif m.replyTarget != nil {\n\t\tm.replyTarget = nil\n\t\t// applyLayout пересчитывает",
		to:        "\tif false {\n\t\tm.replyTarget = nil\n\t\t// applyLayout пересчитывает",
		wantFails: "TestNarrowEscCancelsReplyThenClosesZoom",
	},
	{
		// (б, широкий режим) Esc при фокусе на переписке обязан вернуть фокус на
		// список, а не закрыть переписку.
		name:      "слоёный Esc: в широком режиме переписка закрывается",
		file:      "model.go",
		from:      "\tif m.zoom != nil && !m.wallPanelMode() {\n\t\treturn m.closeWallZoom()\n\t}",
		to:        "\tif m.zoom != nil {\n\t\treturn m.closeWallZoom()\n\t}",
		wantFails: "TestWideEscReturnsFocusToListWithoutClosingZoom",
	},
	{
		// (в) Адресат отправки при фокусе на переписке: если смотреть только на
		// карточку стены, текст уходит в тот чат, который под курсором стены, а
		// не в открытый.
		name:      "адресат отправки: переписка игнорируется",
		file:      "send.go",
		from:      "\tcase m.zoomHasFocus():\n\t\t// Фокус на переписке",
		to:        "\tcase false:\n\t\t// Фокус на переписке",
		wantFails: "TestSendGoesToZoomChatNotWallCard",
	},
	{
		// Курсор переписки обязан ехать в ПАНЕЛИ В ФОКУСЕ. Возврат m.zoom —
		// это всегда первая панель — означает, что при фокусе на второй
		// стрелки листают первый чат, а человек смотрит на второй.
		name:      "навигация: курсор едет в первой панели, а не в фокусной",
		file:      "zoom.go",
		from:      "func (m *Model) moveWallZoomCursor(delta int) {\n\tzoom := m.focusedZoom()",
		to:        "func (m *Model) moveWallZoomCursor(delta int) {\n\tzoom := m.zoom",
		wantFails: "TestArrowsMoveCursorInFocusedPanelNotFirst",
	},
	{
		// Enter в двойном режиме ОБЯЗАН переключать свободную панель: без
		// переключения обе панели показывали бы одно и то же, и вторая
		// переписка была бы недоступна.
		name:      "двойной режим: Enter не переключает свободную панель",
		file:      "send.go",
		from:      "\t\t\tm.previewPanel = otherChatPanel(m.previewPanel)\n\t\t\treturn m.openWallCursorZoomIn(m.previewPanel)",
		to:        "\t\t\treturn m.openWallCursorZoomIn(m.previewPanel)",
		wantFails: "TestTwoPanelEnterSwapsFreePanelAndOpensCursorChat",
	},
	{
		// Возврат проверки «чат под курсором уже открыт в какой-либо панели»
		// делает Enter мёртвым: свободная панель следует за курсором через
		// syncWallZoomToCursor, поэтому к моменту нажатия чат под курсором в ней
		// уже есть, и проверка срабатывает всегда. Именно так Enter и перестал
		// работать при живом зелёном тесте — тест двигал курсор в обход
		// moveFocused. Теперь он идёт настоящим путём, и мутация ловится.
		name:      "двойной режим: Enter гасится проверкой «чат уже открыт»",
		file:      "send.go",
		from:      "\t\t\tm.previewPanel = otherChatPanel(m.previewPanel)\n\t\t\treturn m.openWallCursorZoomIn(m.previewPanel)",
		to:        "\t\t\tfor _, panel := range m.wallChatPanelsOrder() {\n\t\t\t\tif zoom := m.panelZoom(panel); zoom != nil && zoom.chatID == target.ChatID {\n\t\t\t\t\treturn m, nil\n\t\t\t\t}\n\t\t\t}\n\t\t\tm.previewPanel = otherChatPanel(m.previewPanel)\n\t\t\treturn m.openWallCursorZoomIn(m.previewPanel)",
		wantFails: "TestTwoPanelEnterSwapsFreePanelAndOpensCursorChat",
	},
	{
		// (в, вторая половина) Тот же адресат для цели ответа Ctrl+R: если брать
		// карточку стены, ответ уходит на сообщение, которого человек не выбирал.
		name:      "цель ответа: берётся карточка стены, а не сообщение переписки",
		file:      "send.go",
		from:      "\tif m.zoomHasFocus() {\n\t\tmessage, ok := m.selectedWallZoomMessage()",
		to:        "\tif false {\n\t\tmessage, ok := m.selectedWallZoomMessage()",
		wantFails: "TestReplyTargetComesFromZoomMessage",
	},
	{
		// (г) Скрытие времени и превью у карточки под курсором в широком режиме.
		// Без флага карточка повторяет то, что уже целиком видно справа.
		name:      "широкий режим: карточка под курсором не теряет время и превью",
		file:      "zoom.go",
		from:      "return renderWallCard(c, width, underCursor, panel && underCursor, panel)",
		to:        "return renderWallCard(c, width, underCursor, false, panel)",
		wantFails: "TestWideHidesTimeOnEveryCardAndPreviewUnderCursor",
	},
	{
		// (г, вторая половина) Скрытие не должно затронуть НЕвыделенные карточки:
		// превью у них — единственное, что о чате известно.
		name:      "широкий режим: время и превью скрыты у всех карточек",
		file:      "zoom.go",
		from:      "return renderWallCard(c, width, underCursor, panel && underCursor, panel)",
		to:        "return renderWallCard(c, width, underCursor, panel, panel)",
		wantFails: "TestWideHidesTimeOnEveryCardAndPreviewUnderCursor",
	},
	{
		// Стена в широком режиме рисуется обычным renderWall, а не тем же
		// renderWallCardBlock: флаг «скрыть превью» остался бы свойством
		// тестового хелпера, и на экране карточка под курсором показывала бы
		// ровно то, что и так целиком видно справа.
		name:      "широкий режим: колонка стены рисуется обычной карточкой",
		file:      "zoom.go",
		from:      "left := renderWallWith(m.cards, m.scrollTop, m.cursor, leftWidth, panelHeight, m.wallNotice(), m.renderWallCardBlock)",
		to:        "left := renderWall(m.cards, m.scrollTop, m.cursor, leftWidth, panelHeight, m.wallNotice())",
		wantFails: "TestWideHidesTimeOnEveryCardAndPreviewUnderCursor",
	},
	{
		// Тег в суженной колонке: при вычислении ширины колонки по ширине ЗОНЫ
		// вместо признака панели тег выводился бы в колонке шириной 30, съедал
		// 14 ячеек из 25 и обрезал заголовок каждой карточки до трёх символов.
		name:      "широкая колонка: тег выводится и съедает заголовок",
		file:      "wall.go",
		from:      "\ttagWidth := cardTagWidth\n\tif panelColumn {\n\t\ttagWidth = cardUnreadMarkerMaxWidth\n\t}",
		to:        "\ttagWidth := cardTagWidth",
		wantFails: "TestWideCardTitlesStayReadableInPanelColumn",
	},
	{
		// Живая вставка в переписку: без неё открытый чат не показывает
		// пришедшее сообщение вовсе.
		name:      "живая вставка: сообщение не попадает в переписку",
		file:      "model.go",
		from:      "\t\tm.insertWallZoomMessage(msg.chatID, msg.message)\n",
		to:        "",
		wantFails: "TestLiveMessageLandsInOpenZoom",
	},
	{
		// Живая вставка в переписку чужого чата: без сверки источника сообщение
		// из другого чата попадёт в открытую переписку.
		name:      "живая вставка: чужой чат не сверяется",
		file:      "zoom.go",
		from:      "if m.zoom == nil || chatID == 0 || m.zoom.chatID != chatID {",
		to:        "if m.zoom == nil || chatID == 0 {",
		wantFails: "TestLiveMessageOfOtherChatLeavesZoomUntouched",
	},
	{
		// Автоследование за курсором стены в широком режиме: без него панель
		// показывала бы прежний источник, и переписка была бы не про то, что
		// выбрано.
		name:      "широкий режим: переписка не следует за курсором",
		file:      "zoom.go",
		from:      "func (m Model) syncWallZoomToCursor() (Model, tea.Cmd) {\n\tif !m.wallPanelMode() {",
		to:        "func (m Model) syncWallZoomToCursor() (Model, tea.Cmd) {\n\tif true {",
		wantFails: "TestWideCursorMoveReopensZoomOfNewSource",
	},
	{
		// Ширина ПАНЕЛИ переписки: если мерить окно прокрутки переписки по ширине
		// терминала вместо панели, сообщения переносятся по другой ширине, блоки
		// занимают другое число строк, и в экран влезает другое число сообщений.
		name:      "широкая панель: окно переписки меряется по ширине терминала",
		file:      "zoom.go",
		from:      "\t\treturn m.wallPanelListWidth(panelChat1)\n\t}\n\treturn m.width",
		to:        "\t\treturn m.width\n\t}\n\treturn m.width",
		wantFails: "TestWideZoomScrollWindowMeasuredByPanelWidth",
	},
	{
		// Ширина КОЛОНКИ стены в широком режиме: сузив колонку, карточка рисуется
		// шириной терминала, панель справа считается от ширины терминала же и
		// строка зоны становится шире экрана.
		//
		// Мутируется wallColumnWidth, а не вызов renderWallWith: контур обрезает
		// содержимое панели по своей ширине (fitLine), и лишние ячейки карточки
		// больше не вылезали бы наружу — на экране потерялось бы только то, что
		// стояло у правого края колонки (время карточки, маркер непрочитанных).
		name:      "широкая колонка: карточка рисуется на ширине терминала",
		file:      "zoom.go",
		from:      "\tif m.wallPanelMode() {\n\t\treturn max(0, wallPanelColumnWidth-wallPanelOutlineInset)\n\t}\n\treturn m.width",
		to:        "\treturn m.width",
		wantFails: "TestWidePanelWidthIsExactlyRemainder",
	},
	{
		// Окно переписки при загрузке: без прижимания к низу открытый чат
		// показывал бы начало истории вместо свежего.
		name:      "загрузка переписки: окно не прижато к свежему сообщению",
		file:      "zoom.go",
		from:      "\tzoom.cursor = max(0, len(zoom.messages)-1)\n\tzoom.scrollTop = scrollTopToBottom(",
		to:        "\tzoom.cursor = 0\n\tzoom.scrollTop = scrollTopToBottom(",
		wantFails: "TestNarrowEnterOpensZoomOfCardUnderCursor",
	},
	{
		// Enter с пустым полем в узком режиме — единственный способ открыть
		// переписку на весь экран.
		name:      "узкий режим: Enter с пустым полем не открывает переписку",
		file:      "send.go",
		from:      "\t\tif m.zoom == nil && !m.wallPanelMode() {\n\t\t\treturn m.openWallCursorZoom()\n\t\t}",
		to:        "\t\tif false {\n\t\t\treturn m.openWallCursorZoom()\n\t\t}",
		wantFails: "TestNarrowEnterOpensZoomOfCardUnderCursor",
	},
	{
		// Двусторонняя вёрстка: без признака IsOutgoing все сообщения встали бы
		// влево.
		name:      "выравнивание: признак IsOutgoing не переносится в карточку",
		file:      "zoom.go",
		from:      "\t\tOutgoing:   message.IsOutgoing,",
		to:        "\t\tOutgoing:   false,",
		wantFails: "TestZoomMessagesAlignByOutgoingSide",
	},
	{
		// Часть A задачи 0169: признак прочтения СОБЕСЕДНИКОМ. Без сравнения с
		// LastReadOutboxMessageID каждое исходящее считалось бы прочитанным, и
		// обе галочки выглядели бы одинаково — то есть различать их было бы
		// нечем.
		name:      "галочки: прочтение собеседником не вычисляется",
		file:      "zoom.go",
		from:      "ReadByPeer: message.IsOutgoing && lastReadOutboxMessageID > 0 && message.ID <= lastReadOutboxMessageID,",
		to:        "ReadByPeer: message.IsOutgoing,",
		wantFails: "TestZoomReadCheckFollowsPeerReadState",
	},
	{
		// Нулевое LastReadOutboxMessageID — «чата нет в снимке», а НЕ «всё
		// прочитано». Без проверки на ноль сообщение, у которого нет даже
		// собственного id (такое бывает в тестовых фикстурах, см. поля MessageID
		// и ChatID на card), сравнилось бы с нулём как «прочитано» и было бы
		// помечено двумя галочками вслепую.
		name:      "галочки: нулевой снимок прочтения трактуется как прочтение",
		file:      "zoom.go",
		from:      "message.IsOutgoing && lastReadOutboxMessageID > 0 && message.ID <= lastReadOutboxMessageID,",
		to:        "message.IsOutgoing && message.ID <= lastReadOutboxMessageID,",
		wantFails: "TestZoomReadCheckFollowsPeerReadState",
	},
	{
		// Снимок чатов — единственный источник прочтения, и потерять его
		// означало бы вернуться к нулю, то есть к одной галочке у всех.
		name:      "галочки: снимок чатов не читается",
		file:      "zoom.go",
		from:      "return m.chatsByID[z.chatID].LastReadOutboxMessageID",
		to:        "return 0",
		wantFails: "TestZoomReadCheckReadsChatSnapshot",
	},
	{
		// Галочка — только у СВОЕГО сообщения. Если признак «своё» при вычислении
		// хвоста строки потеряется, у входящего посчитается и зазор, и галочка:
		// на экране это выглядит правдоподобно (входящее с одной галочкой — как
		// обычное дело), а на самом деле строка времени входящего стала уже
		// зоны на ширину галочки с зазором.
		name:      "галочки: у входящего вычисляется галочка",
		file:      "zoom.go",
		from:      "\tif c.Outgoing {\n\t\treadCheckText = zoomReadCheck(c)\n\t\treadCheck = foregroundBackgroundStyle(PaletteTextMuted, PaletteBackgroundMain).Render(readCheckText)\n\t}",
		to:        "\treadCheckText = zoomReadCheck(c)\n\treadCheck = foregroundBackgroundStyle(PaletteTextMuted, PaletteBackgroundMain).Render(readCheckText)",
		wantFails: "TestZoomReadCheckFollowsPeerReadState",
	},
	{
		// Часть B задачи 0169: маркер курсора. Без него выделение нечем и не
		// показать: заливки фона у сообщения больше нет.
		name:      "маркер курсора: не рисуется у выделенного сообщения",
		file:      "zoom.go",
		from:      "\tmarker := renderedFill(markerBlock, background)\n\tif selected {",
		to:        "\tmarker := renderedFill(markerBlock, background)\n\tif false {",
		wantFails: "TestZoomSelectionMarkerUsesOpenChatTypeAccent",
	},
	{
		// Маркер обязан быть акцентом ТИПА ОТКРЫТОГО чата: без сверки по
		// карточке источника цвет не задаётся вовсе и маркер рисуется тоном
		// терминала по умолчанию.
		name:      "маркер курсора: цвет не берётся у типа открытого чата",
		file:      "zoom.go",
		from:      "\t\tmarker = foregroundBackgroundStyle(c.SelectionAccent, background).",
		to:        "\t\tmarker = foregroundBackgroundStyle(PaletteBackgroundMain, background).",
		wantFails: "TestZoomSelectionMarkerUsesOpenChatTypeAccent",
	},
	{
		// Источник открытой переписки может быть скрыт фильтром, и тогда в
		// ВИДИМОМ списке карточек его нет: взятие цвета оттуда оставило бы
		// маркер без цвета ровно у того чата, который человек открыл.
		name:      "маркер курсора: цвет берётся из видимого списка карточек",
		file:      "zoom.go",
		from:      "\tindex := indexOfChatCard(m.source, z.chatID)",
		to:        "\tindex := indexOfChatCard(m.cards, z.chatID)",
		wantFails: "TestZoomSelectionAccentComesFromFullSourceNotVisibleList",
	},
	{
		// То же про границу поля ввода: обе красятся по ТОМУ ЖЕ признаку — по
		// источнику открытой переписки из полного потока стены, — и разошлись бы
		// они только в одном состоянии: когда источник скрыт фильтром и в видимом
		// списке его нет.
		name:      "цвет границы поля берётся из видимого списка карточек",
		file:      "input.go",
		from:      "if index := indexOfChatCard(m.source, m.focusedZoom().chatID); index >= 0 {",
		to:        "if index := indexOfChatCard(m.cards, m.focusedZoom().chatID); index >= 0 {",
		wantFails: "TestInputTargetAccentAgreesWithWhereTheTextGoes",
	},
	{
		// Маркер — только на ПЕРВОЙ строке блока. Если сброса поля не будет, он
		// проставится на каждой строке текста, и блок в сообщении из нескольких
		// абзацев пришёлся бы треугольниками вместо одного.
		name:      "маркер курсора: рисуется на каждой строке блока",
		file:      "zoom.go",
		from:      "\t\tlines = append(lines, alignedLine(firstPrefix, firstLead, rendered,\n\t\t\tmin(bubbleWidth, cellWidth(text))))\n\t\tfirstPrefix, firstLead = restPrefix, restLead",
		to:        "\t\tlines = append(lines, alignedLine(firstPrefix, firstLead, rendered,\n\t\t\tmin(bubbleWidth, cellWidth(text))))",
		wantFails: "TestZoomSelectionMarkerOnFirstLineOnly",
	},
	{
		// Заливка выделения у сообщения убрана (человек попросил, 2026-10-01):
		// возвращённая заливка красит прямоугольник даже под короткое «ба», и
		// сторона выравнивания теряется за пустым цветным полем.
		name: "фон: выделенное сообщение снова заливается",
		file: "zoom.go",
		from: "\tbackground := PaletteBackgroundMain\n\tinner := max(0, width-2*cardMarginH)\n" +
			"\t// markerBlock",
		to: "\tbackground := PaletteBackgroundMain\n\tif selected {\n\t\tbackground = PaletteBackgroundSelected\n\t}\n" +
			"\tinner := max(0, width-2*cardMarginH)\n\t// markerBlock",
		wantFails: "TestZoomMessageUsesScreenBackgroundEvenWhenSelected",
	},
	{
		// У СВОЕГО сообщения маркер стоит вплотную перед текстом, а не у левого
		// края панели: у правого прижатого сообщения левое поле — другой конец
		// экрана, и маркер оказывается через всю ширину от текста, к которому
		// относится.
		name:      "маркер курсора: у своего сообщения остаётся в левом поле",
		file:      "zoom.go",
		from:      "\tfirstPrefix, firstLead := marker, \"\"\n\tif c.Outgoing {\n\t\tfirstPrefix, firstLead = \"\", marker\n\t}",
		to:        "\tfirstPrefix, firstLead := marker, \"\"\n\t_ = c.Outgoing",
		wantFails: "TestZoomSelectionMarkerStandsNextToTextOnBothSides",
	},
	{
		// Место под маркер зарезервировано у всех сообщений. Без резерва у
		// невыделенного текст стоял бы на две ячейки правее и прыгал при каждом
		// шаге курсора.
		name: "маркер курсора: место под него не зарезервировано",
		file: "zoom.go",
		from: "\t\t\treturn prefix + gap + lead + fitLine(rendered, textWidth, background) + margin",
		to: "\t\t\tif selected {\n\t\t\t\treturn prefix + gap + lead + fitLine(rendered, textWidth, background) + margin\n" +
			"\t\t\t}\n\t\t\treturn prefix + gap + fitLine(rendered, textWidth, background) + margin",
		wantFails: "TestZoomSelectionMarkerKeepsTextColumn",
	},
	{
		// Знак и текст разделяет зазор в ячейку: без него «▸» примыкал бы к
		// первой букве и читался частью слова.
		name:      "маркер курсора: знак примыкает к тексту без зазора",
		file:      "zoom.go",
		from:      "\treturn cellWidth(zoomSelectionMarker) + cardSegmentGapWidth",
		to:        "\treturn cellWidth(zoomSelectionMarker)",
		wantFails: "TestZoomSelectionMarkerStandsNextToTextOnBothSides",
	},
	{
		// Время сообщения — своей строкой: без отдельной строки короткое время
		// растягивало бы блок и выравнивание перестало бы значить чьё сообщение.
		name:      "выравнивание: время встаёт в строку с текстом",
		file:      "zoom.go",
		from:      "\tif c.Outgoing {\n\t\treturn append(lines, margin+renderedFill(max(0, inner-tailWidth), PaletteBackgroundMain)+\n\t\t\ttime+readGap+readCheck+margin)\n\t}\n\treturn append(lines, margin+time+renderedFill(max(0, inner-tailWidth), PaletteBackgroundMain)+margin)",
		to:        "\tif c.Outgoing {\n\t\treturn append(lines, margin+time+renderedFill(max(0, inner-tailWidth), PaletteBackgroundMain)+readGap+readCheck+margin)\n\t}\n\treturn append(lines, margin+time+renderedFill(max(0, inner-tailWidth), PaletteBackgroundMain)+margin)",
		wantFails: "TestZoomMessagesAlignByOutgoingSide",
	},
}

// TestZoomMutationsAreCaught — каждая мутация из mutantCases обязана быть поймана
// указанным тестом.
//
// Мутируется КОПИЯ пакета в t.TempDir: настоящие исходники не трогаются, а
// сборка идёт обычным `go test`, так что проверяется ровно то, что проверяла бы
// правка в коде. Мутация обязана давать СБОРКУ или ПАДЕНИЕ, а не «упало что-то
// другое»: иначе она ничего не доказывает, и такой случай заведён
// wantCompileFail.
func TestZoomMutationsAreCaught(t *testing.T) {
	// Модуль без cgo: у tgwall нет зависимости от TDLib (клиент приходит
	// интерфейсом), поэтому копии достаточно go.mod без cgo-флагов. Каталог
	// нужен свой, чтобы не мешать кэшу и не трогать исходное дерево.
	for _, mutation := range mutantCases {
		t.Run(mutation.name, func(t *testing.T) {
			// Свежая копия на КАЖДУЮ мутацию: две из них правят один и тот же
			// фрагмент, и на общем дереве вторая не нашла бы свой образец уже
			// изменённым первой.
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

// copyGoModule — копия модуля в целевой каталог.
//
// Копируется ВСЁ дерево internal/, а не только tgwall с auth: у auth есть свои
// импорты (config и другие пакеты), и попытка угадать полный список завела бы
// сборку копии в тупик. Каталог целиком копируется дёшево, а список пакетов
// перестаёт быть нашим знанием. Каталоги служебного мусора (.git, bin,
// .worktrees) пропускаются: они не нужны для сборки и только замедляют копию.
func copyGoModule(t *testing.T, destination string) error {
	t.Helper()
	root := filepath.Join("..", "..")
	for _, dir := range []string{"internal", "cmd"} {
		if err := copyTree(filepath.Join(root, dir), filepath.Join(destination, dir)); err != nil {
			return err
		}
	}
	// Локальные замены копируются ДО go.mod: пока в копии нет каталога, на
	// который указывает `replace`, копия не собирается вообще.
	if err := copyLocalReplacements(t, root, destination); err != nil {
		return err
	}
	for _, name := range []string{"go.mod", "go.sum"} {
		if err := copyFile(filepath.Join(root, name), filepath.Join(destination, name)); err != nil {
			return err
		}
	}
	return nil
}

// copyLocalReplacements — копия каталогов, на которые указывают `replace` в go.mod
// с относительным путём (диагностический патч ultraviolet, задача 0160).
//
// Без этого копия модуля не собирается: `replace ... =>
// ./vendor-patch/ultraviolet` указывает на каталог, которого в копии нет, и сборка
// падает с «replacement directory does not exist» — то есть мутация падает на
// чужой ошибке и ничего не доказывает. Список берётся из самого go.mod, а не
// зашивается в помощника: и переименование каталога, и вторая локальная замена
// подхватятся сами и не сломают проверку молча. Модулей без таких замен это не
// касается — список пуст, не копируется ничего.
func copyLocalReplacements(t *testing.T, root, destination string) error {
	t.Helper()
	for _, dir := range localReplaceDirs(t, filepath.Join(root, "go.mod")) {
		if err := copyWholeModule(filepath.Join(root, dir), filepath.Join(destination, dir)); err != nil {
			return err
		}
	}
	return nil
}

// localReplaceDirs — относительные пути из `replace` в go.mod. Абсолютные
// отбрасываются: они указывают на каталог вне дерева модуля, и копировать их
// нечего.
func localReplaceDirs(t *testing.T, goMod string) []string {
	t.Helper()
	//nolint:gosec // путь к go.mod под контролем теста, не извне
	output, err := exec.Command("go", "mod", "edit", "-json", goMod).Output()
	if err != nil {
		t.Fatalf("go mod edit -json %s: %v", goMod, err)
	}
	var mod struct {
		Replace []struct {
			New struct {
				Path string
			}
		}
	}
	if err := json.Unmarshal(output, &mod); err != nil {
		t.Fatalf("разбор вывода go mod edit -json: %v", err)
	}
	var dirs []string
	for _, replacement := range mod.Replace {
		if path := replacement.New.Path; !filepath.IsAbs(path) {
			dirs = append(dirs, filepath.Clean(path))
		}
	}
	return dirs
}

// copyWholeModule — копия целого вложенного модуля: .go без _test плюс
// go.mod/go.sum. copyTree для этого не годится — он берёт только .go, а
// заменённому модулю нужен собственный go.mod, иначе он не является модулем.
// Остальное (yml, md, .github) сборке не нужно и только замедляло бы каждую из
// мутаций.
func copyWholeModule(from, to string) error {
	return filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(to, relative), 0o755)
		}
		name := info.Name()
		if !strings.HasSuffix(name, ".go") && name != "go.mod" && name != "go.sum" {
			return nil
		}
		if strings.HasSuffix(name, "_test.go") {
			return nil
		}
		return copyFile(path, filepath.Join(to, relative))
	})
}

func copyTree(from, to string) error {
	return filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, relative)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		// Копируются только исходники Go: тесты, сгенерированные файлы и
		// бинарники в сборку пакета не входят, а копировать их — тратить время
		// каждой из мутаций.
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		return copyFile(path, target)
	})
}

func copyFile(from, to string) error {
	content, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	return os.WriteFile(to, content, 0o644)
}

// applyMutation — замена `from` на `to` в указанном файле копии. Замена ровно
// одна: ноль означал бы, что образец устарел и мутация ничего не проверила, а
// несколько — что мутировалось не то место.
func applyMutation(t *testing.T, root string, mutation mutantCase) error {
	t.Helper()
	path := filepath.Join(root, "internal", "tgwall", mutation.file)
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	count := strings.Count(string(content), mutation.from)
	if count != 1 {
		return fmt.Errorf("мутация %q: образец встречается %d раз (ждали ровно 1) в %s:\n%s",
			mutation.name, count, mutation.file, mutation.from)
	}
	mutated := strings.Replace(string(content), mutation.from, mutation.to, 1)
	return os.WriteFile(path, []byte(mutated), 0o644)
}

// runGoTest — сборка и прогон указанного набора тестов в копии модуля.
func runGoTest(t *testing.T, root, run string) ([]byte, error) {
	t.Helper()
	//nolint:gosec // каталог и аргументы под контролем теста, не извне
	cmd := exec.Command("go", "test", "./internal/tgwall/", "-count=1", "-run", "^"+run)
	cmd.Dir = root
	// Модуль копии не должен тянуть сеть: GOFLAGS=-mod=mod и локальный кэш модулей
	// достаточны, все зависимости копии — из того же go.sum.
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod")
	return cmd.CombinedOutput()
}

// firstFailure — имя упавшего теста из вывода go test, для сообщения об ошибке.
func firstFailure(output []byte) string {
	for _, line := range strings.Split(string(output), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "--- FAIL:") {
			return strings.TrimPrefix(strings.TrimPrefix(line, "--- FAIL:"), " ")
		}
	}
	return "(падение без строки --- FAIL)"
}

// tail — последние строки вывода: полный лог мутации в сообщение об ошибке
// бесполезен (в нём строки исходников), а конец в нём всегда содержателен.
func tail(output []byte) string {
	lines := strings.Split(strings.TrimRight(string(output), "\n"), "\n")
	if len(lines) > 25 {
		lines = lines[len(lines)-25:]
	}
	return strings.Join(lines, "\n")
}
