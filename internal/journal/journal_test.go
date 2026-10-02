package journal

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixedTime(second int) time.Time {
	return time.Date(2026, 9, 27, 14, 22, second, 0, time.UTC)
}

// Кольцо: 1001-я строка вытесняет первую, вторая становится первой, и так по кругу.
// Это ровно то, что описал человек, и не усечение с конца.
func TestNewLineReplacesTheOldestOne(t *testing.T) {
	j := New(3, "")
	for _, second := range []int{1, 2, 3} {
		j.AddAt(LevelInfo, "строка "+string(rune('0'+second)), fixedTime(second))
	}
	j.AddAt(LevelInfo, "строка 4", fixedTime(4))

	lines := j.Lines(LevelInfo)
	if len(lines) != 3 {
		t.Fatalf("в кольце %d строк, want 3", len(lines))
	}
	for index, want := range []string{"строка 2", "строка 3", "строка 4"} {
		if lines[index].Text != want {
			t.Fatalf("строка %d = %q, want %q", index, lines[index].Text, want)
		}
	}
	if j.Total() != 3 {
		t.Fatalf("всего %d строк, want 3", j.Total())
	}
}

// Предел в строках, а не в байтах: файл из тысяч коротких строк остаётся
// читаемым, и ни одна строка не режется на середине.
func TestTheFileNeverGrowsPastTheLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tgcli.log")
	j := New(4, path)
	for second := range 12 {
		j.AddAt(LevelInfo, "строка", fixedTime(second%60))
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("журнал не записан: %v", err)
	}
	lines := splitLines(string(data))
	if len(lines) != 4 {
		t.Fatalf("в файле %d строк, want 4: %q", len(lines), string(data))
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "14:22:") {
			t.Fatalf("строка файла не похожа на запись журнала: %q", line)
		}
	}
}

// Файл и память — одно и то же: окно показывает содержимое файла, и разойтись они
// не могут, иначе человек читал бы одно, а потом нашёл бы другое в том же файле.
func TestTheFileHoldsExactlyWhatMemoryHolds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tgcli.log")
	j := New(10, path)
	j.AddAt(LevelError, "отправка не удалась", fixedTime(1))
	j.AddAt(LevelWarning, "часть чатов не загружена", fixedTime(2))

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("журнал не записан: %v", err)
	}
	want := []string{
		"14:22:01 ошибка отправка не удалась",
		"14:22:02 предупреждение часть чатов не загружена",
	}
	got := splitLines(string(data))
	if len(got) != len(want) {
		t.Fatalf("строк в файле %d, want %d: %q", len(got), len(want), string(data))
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("строка %d файла = %q, want %q", index, got[index], want[index])
		}
	}
}

// Уровни накопительные: строже значит «важнее», и каждый следующий уровень включает
// предыдущий. Окно не должно показывать ошибки, когда выбран уровень действий.
func TestLevelsAreCumulative(t *testing.T) {
	j := New(10, "")
	j.AddAt(LevelInfo, "отправлено", fixedTime(1))
	j.AddAt(LevelWarning, "часть чатов не загружена", fixedTime(2))
	j.AddAt(LevelError, "чат не открылся", fixedTime(3))

	for _, test := range []struct {
		minLevel Level
		want     []string
	}{
		{LevelInfo, []string{"отправлено", "часть чатов не загружена", "чат не открылся"}},
		{LevelWarning, []string{"часть чатов не загружена", "чат не открылся"}},
		{LevelError, []string{"чат не открылся"}},
	} {
		lines := j.Lines(test.minLevel)
		if len(lines) != len(test.want) {
			t.Fatalf("уровень %s: строк %d, want %d", test.minLevel, len(lines), len(test.want))
		}
		for index, want := range test.want {
			if lines[index].Text != want {
				t.Fatalf("уровень %s: строка %d = %q, want %q", test.minLevel, index, lines[index].Text, want)
			}
		}
	}
}

// Журнал прошлой сессии открывается: человек смотрит в файл после того, как
// приложение закрылось, иначе вопрос «почему часть чатов не загружена» после
// перезапуска был бы неразрешим.
func TestReloadReadsThePreviousSessionsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tgcli.log")
	previous := New(10, path)
	previous.AddAt(LevelError, "вчера: чат не загружен", fixedTime(1))
	previous.AddAt(LevelWarning, "вчера: часть чатов", fixedTime(2))

	j := New(10, path)
	j.Reload()
	lines := j.Lines(LevelInfo)
	if len(lines) != 2 {
		t.Fatalf("после Reload строк %d, want 2", len(lines))
	}
	if lines[0].Text != "вчера: чат не загружен" || lines[1].Level != LevelWarning {
		t.Fatalf("после Reload содержимое не то: %+v", lines)
	}
}

// Reload читает только последние limit строк: файл мог быть длиннее лимита, если
// его писала другая версия или его правили руками.
func TestReloadKeepsOnlyTheLastLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tgcli.log")
	long := New(50, path)
	for second := range 20 {
		long.AddAt(LevelInfo, "строка "+string(rune('a'+second)), fixedTime(second%60))
	}

	j := New(3, path)
	j.Reload()
	if got := j.Total(); got != 3 {
		t.Fatalf("после Reload строк %d, want 3", got)
	}
	if lines := j.Lines(LevelInfo); lines[0].Text != "строка r" {
		t.Fatalf("после Reload первая строка = %q, want %q", lines[0].Text, "строка r")
	}
}

// Пустой путь — журнал живёт в памяти: окно всё равно покажет, что происходило, и
// приложение не пишет ничего на диск там, где путь неизвестен.
func TestEmptyPathKeepsTheJournalInMemoryOnly(t *testing.T) {
	j := New(2, "")
	j.AddAt(LevelInfo, "событие", fixedTime(1))
	if j.Path() != "" {
		t.Fatalf("путь = %q, want empty", j.Path())
	}
	if len(j.Lines(LevelInfo)) != 1 {
		t.Fatal("событие потерялось в памяти")
	}
	if err := j.WriteError(); err != nil {
		t.Fatalf("журнал без файла сообщил об ошибке записи: %v", err)
	}
}

// Предел в ноль или меньше недопустим: журнал без единой строки бесполезен, поэтому
// такое значение приводится к одной строке, а не выключает журнал молча.
func TestNonPositiveLimitStillKeepsOneLine(t *testing.T) {
	for _, limit := range []int{0, -5} {
		j := New(limit, "")
		j.AddAt(LevelInfo, "первая", fixedTime(1))
		j.AddAt(LevelInfo, "вторая", fixedTime(2))
		lines := j.Lines(LevelInfo)
		if len(lines) != 1 || lines[0].Text != "вторая" {
			t.Fatalf("лимит %d: в кольце %+v, want только последняя строка", limit, lines)
		}
	}
}

// Журнал не должен падать из-за прав доступа: ошибка фиксируется и видна через
// WriteError, а не роняет приложение.
func TestUnwritableFileIsReportedNotFatal(t *testing.T) {
	// Каталог вместо файла: запись в него невозможна, и это не должно паниковать.
	dir := t.TempDir()
	j := New(4, dir)
	j.AddAt(LevelInfo, "событие", fixedTime(1))
	if err := j.WriteError(); err == nil {
		t.Fatal("незаписываемый путь не сообщил об ошибке")
	}
	if lines := j.Lines(LevelInfo); len(lines) != 1 {
		t.Fatal("событие потерялось из-за ошибки записи")
	}
}

// Пишут и читают разные горутины: записи приходят из обработчиков TDLib, а окно
// читает из отрисовки. Без замка строка читалась бы наполовину.
func TestConcurrentAddAndRead(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tgcli.log")
	j := New(200, path)
	var wg sync.WaitGroup
	for worker := range 4 {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for index := range 50 {
				j.AddAt(LevelInfo, "событие", fixedTime((worker*7+index)%60))
			}
		}(worker)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 50 {
			_ = j.Lines(LevelInfo)
			_ = j.Total()
		}
	}()
	wg.Wait()
	if got := j.Total(); got != 200 {
		t.Fatalf("после параллельных записей строк %d, want 200", got)
	}
}

// Reload заменяет содержимое кольца, а не дописывает в него. Разница видна ровно в
// том случае, ради которого Reload и вызывается: в памяти уже есть события этой
// сессии, и после перечитывания они должны остаться в файле, а не удвоиться.
// Иначе окно показывало бы каждое событие дважды.
func TestReloadReplacesTheRingInsteadOfAppendingToIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tgcli.log")
	previous := New(10, path)
	previous.AddAt(LevelInfo, "вчера: лента открыта", fixedTime(1))
	previous.AddAt(LevelInfo, "вчера: чат не открылся", fixedTime(2))

	// Новый запуск сразу видит вчерашний журнал: иначе первая же запись сегодняшней
	// сессии переписала бы файл, и человек открыл бы окно уже с пустым журналом.
	j := New(10, path)
	if got := j.Total(); got != 2 {
		t.Fatalf("при создании журнала в кольце %d строк прошлой сессии, want 2", got)
	}
	j.AddAt(LevelInfo, "сегодня: чаты загружены", fixedTime(3))
	if got := j.Total(); got != 3 {
		t.Fatalf("после события сегодняшней сессии в кольце %d строк, want 3", got)
	}

	j.Reload()
	lines := j.Lines(LevelInfo)
	if len(lines) != 3 {
		t.Fatalf("после Reload строк %d, want 3: %+v", len(lines), lines)
	}
	if lines[0].Text != "вчера: лента открыта" {
		t.Fatalf("первая строка после Reload = %q, want %q", lines[0].Text, "вчера: лента открыта")
	}
	if lines[2].Text != "сегодня: чаты загружены" {
		t.Fatalf("последняя строка после Reload = %q, want %q", lines[2].Text, "сегодня: чаты загружены")
	}
}
