package tgwall

import (
	"testing"
	"time"

	"telecli/internal/auth"
)

// ФОРМАТ сведений об источнике (задача 0163): число участников и статус
// собеседника в строку для строки заголовка.
//
// Живая доставка этих значений проверяется отдельно и по всей цепочке — в
// sourceinfo_test.go (модель, каналы, апдейты TDLib). Здесь проверяется ровно то,
// чего там нет: САМА ФОРМАТИРОВКА, то есть числа и бакеты времени. Без неё
// смена порога («с 1,5K на 1,6K») или бакет «вчера» прошла бы незамеченной, пока
// сломавшийся формат не показался бы вживую на глазах у человека.
//
// Пороги и формулировки — UI-текст, а не системный API: по прямому указанию
// файла задачи они выбраны разумным умолчанием и вынесены в REPORT.md на
// ревью, а не спрошены у человека в этом запуске.

// statusTestNow — фиксированное «сейчас» для всех проверок статуса: с реальным
// временем тест ловился бы о полуночи и о смене суток. 2026-09-30 12:00 местного.
func statusTestNow() time.Time {
	return time.Date(2026, time.September, 30, 12, 0, 0, 0, time.Local)
}

// TestMemberCountText — компактная запись числа участников.
//
// Отдельно от счётчика непрочитанных у него НЕТ потолка «99+»: у канала может
// быть и 200 тысяч подписчиков, и это осмысленное число, а не факт «много».
//
// Ноль и отрицательное значение дают ПУСТУЮ строку, а не «0»: это «данных ещё
// нет» (апдейт не пришёл), и ноль рядом с именем читался бы как «у канала ноль
// подписчиков».
func TestMemberCountText(t *testing.T) {
	cases := []struct {
		count int32
		want  string
	}{
		{count: 1, want: "1"},
		{count: 999, want: "999"},
		// Порог переключения на тысячи — ровно 1000, без «999 → 1K».
		{count: 1000, want: "1K"},
		{count: 1200, want: "1,2K"},
		{count: 1500, want: "1,5K"},
		{count: 12345, want: "12,3K"},
		// Знак после запятой ставится, только если что-то значит: 45K, а не 45,0K.
		{count: 45000, want: "45K"},
		{count: 99999, want: "100K"},
		// Округление, выносящее мантиссу за единицу, переезжает в следующую:
		// «1000K» читается хуже, чем «1M».
		{count: 999999, want: "1M"},
		{count: 1000000, want: "1M"},
		{count: 2100000, want: "2,1M"},
		{count: 123456789, want: "123,5M"},
		{count: 0, want: ""},
		{count: -5, want: ""},
	}
	for _, testCase := range cases {
		if got := memberCountText(testCase.count); got != testCase.want {
			t.Errorf("memberCountText(%d) = %q, want %q", testCase.count, got, testCase.want)
		}
	}
}

// TestUserStatusText — каждый вариант userStatus даёт ожидаемую строку, а
// «статуса нет» — пустую.
//
// Пустая строка здесь НЕ пустая заглушка на экране: карточка без сведений
// вообще ничего не занимает (см. TestCardWithoutSourceInfoShowsNoStrayGap), и
// имя у неё стоит там же, где у карточек со статусом.
//
// «был(а)» — с «(а)», а не с угаданным родом: пола у нас нет, а ошибиться в
// этом заметно хуже нейтральной формы.
func TestUserStatusText(t *testing.T) {
	now := statusTestNow()
	cases := []struct {
		name   string
		status auth.UserStatus
		want   string
	}{
		{name: "online", status: auth.UserStatus{Kind: auth.UserStatusOnline}, want: "в сети"},
		{name: "recently", status: auth.UserStatus{Kind: auth.UserStatusRecently}, want: "был(а) недавно"},
		{name: "last_week", status: auth.UserStatus{Kind: auth.UserStatusLastWeek}, want: "был(а) на этой неделе"},
		{name: "last_month", status: auth.UserStatus{Kind: auth.UserStatusLastMonth}, want: "был(а) в этом месяце"},
		{name: "empty_shows_nothing", status: auth.UserStatus{Kind: auth.UserStatusEmpty}, want: ""},
		// Битый userStatusOffline без времени: показать «1970 в 00:00» нельзя, а
		// молчать тоже нельзя — статус «вышел из сети» есть.
		{name: "offline_without_time", status: auth.UserStatus{Kind: auth.UserStatusOffline}, want: "был(а) недавно"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := userStatusText(testCase.status, now); got != testCase.want {
				t.Errorf("userStatusText(%+v) = %q, want %q", testCase.status, got, testCase.want)
			}
		})
	}
}

// TestUserLastSeenTextBuckets — относительное время выхода из сети раскладывается
// по бакетам мессенджеров: недавно / сегодня в HH:MM / вчера в HH:MM / ДД.ММ /
// ДД.ММ.ГГГГ.
//
// Границы приблизительные и будут уточнены человеком по живому виду, поэтому
// проверяется не «точная арифметика», а ПОРЯДОК бакетов и то, что год в
// сокращении не пишется, пока он текущий («15.09» внутри известного года
// читается однозначно).
func TestUserLastSeenTextBuckets(t *testing.T) {
	now := statusTestNow()
	at := func(offset time.Duration) int64 { return now.Add(-offset).Unix() }
	cases := []struct {
		name      string
		wasOnline int64
		want      string
	}{
		{name: "минуту назад", wasOnline: at(time.Minute), want: "недавно"},
		{name: "полчаса назад", wasOnline: at(30 * time.Minute), want: "недавно"},
		{name: "сегодня", wasOnline: at(2 * time.Hour), want: "сегодня в 10:00"},
		{name: "вчера", wasOnline: at(26 * time.Hour), want: "вчера в 10:00"},
		{name: "на той же неделе", wasOnline: at(5 * 24 * time.Hour), want: "25.09"},
		{name: "в прошлом году", wasOnline: at(400 * 24 * time.Hour), want: "26.08.2025"},
		{name: "битое время", wasOnline: 0, want: "был(а) недавно"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := userLastSeenText(testCase.wasOnline, now); got != testCase.want {
				t.Errorf("userLastSeenText(%d) = %q, want %q", testCase.wasOnline, got, testCase.want)
			}
		})
	}
}

// TestIsYesterdayDoesNotDriftOverMidnight — «вчера» определяется по календарным
// суткам, а не вычитанием 24 часов.
//
// Ровно на этом ломается сравнение с «now минус 24 часа»: после полуночи до
// вчерашнего момента прошло меньше суток, и дата уезжает на позавчера.
func TestIsYesterdayDoesNotDriftOverMidnight(t *testing.T) {
	afterMidnight := time.Date(2026, time.September, 30, 0, 30, 0, 0, time.Local)
	// 2026-09-29 23:30 — вчерашний момент при «сейчас» в 00:30.
	yesterdayLate := time.Date(2026, time.September, 29, 23, 30, 0, 0, time.Local)
	if !isYesterday(yesterdayLate, afterMidnight) {
		t.Error("вчерашний момент после полуночи не признан вчерашним")
	}
	// 2026-09-28 23:30 — уже позавчера, вчера быть не может.
	twoDaysLate := time.Date(2026, time.September, 28, 23, 30, 0, 0, time.Local)
	if isYesterday(twoDaysLate, afterMidnight) {
		t.Error("позавчерашний момент признан вчерашним")
	}
}
