package tgwall

import (
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// Строка временных системных сообщений: одна строка между стеной и блоком ввода,
// где короткое сообщение («Отправлено», «Нет связи») живёт systemNoticeDuration и
// гаснет само.
//
// Строка занимает своё место всегда, с сообщением или без: иначе экран прыгал бы
// на строку в момент появления и исчезновения текста.

// systemNoticeDuration — сколько сообщение висит на экране.
const systemNoticeDuration = 5 * time.Second

// wallSystemNotice — видимое сейчас сообщение и его версия.
type wallSystemNotice struct {
	text string
	// id — версия сообщения: растёт на каждый вызов ShowSystemNotice. Таймер на
	// пять секунд, заведённый для СТАРОГО сообщения, не должен погасить то,
	// которое успело смениться за это время (тот же класс ошибки, что уже ловили
	// на wallZoom.loadID: устаревший ответ не применяется).
	id int
}

// systemNoticeExpiredMsg — сработал таймер гашения. Несёт версию того сообщения,
// ради которого он и был заведён.
type systemNoticeExpiredMsg struct{ id int }

// systemNoticeClearCmd — таймер гашения своего сообщения: обычный tea.Tick, тот же
// примитив, что у кадров спиннера.
func systemNoticeClearCmd(id int) tea.Cmd {
	return tea.Tick(systemNoticeDuration, func(time.Time) tea.Msg {
		return systemNoticeExpiredMsg{id: id}
	})
}

// ShowSystemNotice — показать временное системное сообщение в строке над полем
// ввода на 5 секунд.
//
// Публичный метод: будущие задачи (индикаторы отправки, ошибки) вызывают именно
// его, а не пишут в поле notice напрямую — версия сообщения и таймер гашения
// должны остаться в одном месте, иначе сообщение либо зависнет на экране (без
// таймера), либо погаснет чужое (без сверки версии).
func (m Model) ShowSystemNotice(text string) (Model, tea.Cmd) {
	m.noticeID++
	m.notice = &wallSystemNotice{text: text, id: m.noticeID}
	return m, systemNoticeClearCmd(m.noticeID)
}

// renderSystemNotice — строка временных сообщений между стеной и блоком ввода.
// Сообщения нет (nil) — строка всё равно рисуется, пустая и фоном стены.
//
// Текст по центру, длинный обрезается по ширине: «одно-два слова» это ожидаемый
// ввод, а не гарантия, которую код обязан проверять. Фон строки — основной, а не
// панельный: строка часть потока стены, а не отдельная панель.
func renderSystemNotice(notice *wallSystemNotice, width int) string {
	if width <= 0 {
		return ""
	}
	background := PaletteBackgroundMain
	if notice == nil {
		return renderedFill(width, background)
	}
	// MaxHeight(1) — страховка от перевода строки внутри самого текста: строка
	// экрана обязана быть ровно одна, иначе расчёт высоты стены разъехался бы с
	// отрисовкой.
	return lipgloss.NewStyle().Foreground(PaletteTextMuted).Background(background).
		Width(width).MaxWidth(width).MaxHeight(1).Align(lipgloss.Center).
		Render(truncateVisible(notice.text, width))
}
