// Package journal хранит журнал событий приложения: последние строки в памяти и
// тот же набор строк в файле.
//
// Файл — не append-журнал, а окно: в нём никогда не бывает больше limit строк.
// Когда приходит строка сверх лимита, первая вытесняется, вторая становится первой,
// и так по кругу (кольцо, а не усечение с конца — так человек и описал). Причина
// не в экономии места, а в читаемости: журнал открывают глазами в окне приложения,
// и сто строк или две тысячи строк — это разный объём, который ещё можно пролистать
// и найти нужное. Ограничение в байтах давало бы файл, который либо ничего не
// показывает, либо молча вырезает начало посреди сообщения.
//
// Хранение и запись разведены: Journal — кольцо в памяти, а файл переписывается
// целиком, потому что кольцо не умеет дописывать в середину. Перезапись на событие
// дёшева: строк по умолчанию тысяча, а события — не то, что происходит на каждый
// символ клавиатуры.
package journal

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Level — важность события. Уровни идут по возрастанию, и «строже» значит «важнее»:
// окно показывает события уровня не ниже выбранного, поэтому каждый следующий
// уровень включает предыдущий (человек описал именно так).
type Level int

const (
	// LevelInfo — обычное событие: чаты загрузились, сообщение отправлено, чат открыт.
	LevelInfo Level = iota
	// LevelWarning — приложение работает, но что-то пошло не так: часть чатов не
	// загрузилась, голосовое ещё играет.
	LevelWarning
	// LevelError — действие не удалось: отправка, загрузка истории, удаление.
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelWarning:
		return "предупреждение"
	case LevelError:
		return "ошибка"
	default:
		return "действие"
	}
}

// Line — одна запись журнала: время, уровень и текст. Время хранится отдельно от
// текста, чтобы окно могло выровнять колонки, а не искать их в строке.
type Line struct {
	At    time.Time
	Level Level
	Text  string
}

// String — строка журнала в том виде, в каком она лежит в файле и в окне:
// «14:22:31 ошибка чат не загружен: Chat not found».
func (l Line) String() string {
	return fmt.Sprintf("%s %s %s", l.At.Format("15:04:05"), l.Level, l.Text)
}

// Journal — кольцо последних записей и файл, в который они пишутся.
//
// Одновременный доступ защищён: записи приходят из обработчиков TDLib, а окно
// читает их из отрисовки, и без замка одно и то же поле читалось бы наполовину.
type Journal struct {
	mu     sync.Mutex
	lines  []Line
	limit  int
	path   string
	broken bool
}

// New — журнал с пределом в limit строк. Пустой или отрицательный limit берётся
// как один: журнал без единой строки бесполезен, а ноль означал бы, что писать
// некуда вовсе. Пустой путь отключает запись в файл, но не память: окно всё равно
// покажет, что происходило в этой сессии.
//
// Журнал прошлой сессии загружается сразу, при создании, а не при первом событии и
// не при открытии окна. Иначе первая же запись новой сессии переписала бы файл
// целиком — а перечитывать его потом уже нечего: человек открыл бы окно и увидел
// пустой журнал вместо «что было в прошлый раз».
func New(limit int, path string) *Journal {
	if limit <= 0 {
		limit = 1
	}
	j := &Journal{limit: limit, path: path, lines: make([]Line, 0, min(limit, defaultPrealloc))}
	j.Reload()
	return j
}

// defaultPrealloc — сколько строк заготовить сразу, чтобы не расти рывками.
// Больше предела готовить бессмысленно.
const defaultPrealloc = 256

// Add — записать событие: строка попадает в кольцо, а кольцо переписывает файл.
func (j *Journal) Add(level Level, text string) {
	if j == nil {
		return
	}
	j.AddAt(level, text, time.Now())
}

// AddAt — то же с явным временем: так проверяются часы и порядок строк, а по
// факту времени идти в Add.
func (j *Journal) AddAt(level Level, text string, at time.Time) {
	if j == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.lines = append(j.lines, Line{At: at, Level: level, Text: text})
	if len(j.lines) > j.limit {
		// Кольцо: первая строка уходит, остальные сдвигаются на одну влево.
		j.lines = append(j.lines[:0], j.lines[1:]...)
	}
	j.flushLocked()
}

// Lines — копия последних записей, не старше уровня minLevel. Копия, а не сам срез:
// окно держит её между кадрами, а журнал в это время дописывается.
func (j *Journal) Lines(minLevel Level) []Line {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Line, 0, len(j.lines))
	for _, line := range j.lines {
		if line.Level >= minLevel {
			out = append(out, line)
		}
	}
	return out
}

// Total — сколько строк всего в кольце, независимо от уровня: в шапке окна это
// «показано N из M», и без общего счётчика фильтр выглядел бы как обрыв на ровном
// месте.
func (j *Journal) Total() int {
	if j == nil {
		return 0
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.lines)
}

// Path — куда пишется журнал. Пустая строка означает «только память».
func (j *Journal) Path() string {
	if j == nil {
		return ""
	}
	return j.path
}

// Limit — сколько строк помещается в кольцо.
func (j *Journal) Limit() int {
	if j == nil {
		return 0
	}
	return j.limit
}

// Reload — перечитать файл в кольцо. Нужно при открытии окна: если приложение было
// закрыто, в файле остался журнал прошлой сессии, и человек идёт смотреть именно за
// ним («почему часть чатов не загрузилась» — вопрос как раз из прошлого запуска).
//
// Читаются последние limit строк файла: файл мог остаться длиннее лимита, если его
// писала другая версия или он правился руками.
func (j *Journal) Reload() {
	if j == nil || j.path == "" {
		return
	}
	data, err := os.ReadFile(j.path)
	if err != nil {
		if !os.IsNotExist(err) {
			j.mu.Lock()
			j.broken = true
			j.mu.Unlock()
		}
		return
	}
	lines := splitLines(string(data))
	if len(lines) > j.limit {
		lines = lines[len(lines)-j.limit:]
	}
	parsed := make([]Line, 0, len(lines))
	for _, text := range lines {
		if line, ok := parseLine(text); ok {
			parsed = append(parsed, line)
		}
	}
	j.mu.Lock()
	j.lines = append(j.lines[:0], parsed...)
	j.broken = false
	j.mu.Unlock()
}

// flushLocked — переписать файл целиком. Кольцо не умеет дописывать в середину,
// поэтому файл держится ровно тем же набором строк, что и память. Ошибка
// фиксируется, но не падает: неработающий журнал не должен ронять приложение.
func (j *Journal) flushLocked() {
	if j.path == "" || j.broken {
		return
	}
	if dir := filepath.Dir(j.path); dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			j.broken = true
			return
		}
	}
	var out strings.Builder
	for _, line := range j.lines {
		out.WriteString(line.String())
		out.WriteString("\n")
	}
	if err := os.WriteFile(j.path, []byte(out.String()), 0o600); err != nil {
		j.broken = true
	}
}

// WriteError — ошибка записи, если она случилась и повлияла на работу: без неё окно
// молча показывало бы пустой журнал, а человек искал бы причину не там.
func (j *Journal) WriteError() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.broken {
		return nil
	}
	return fmt.Errorf("журнал не пишется в %s", j.path)
}

func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	raw := strings.Split(strings.TrimRight(text, "\n"), "\n")
	out := raw[:0]
	for _, line := range raw {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// parseLine — разбор строки файла обратно в запись. Время разбирается, а уровень —
// по названию: неизвестное или поданное вручную слово считается обычным действием,
// а не ошибкой: журнал должен открываться даже после правки руками.
func parseLine(text string) (Line, bool) {
	fields := strings.SplitN(text, " ", 3)
	if len(fields) < 3 {
		return Line{}, false
	}
	at, err := time.Parse("15:04:05", fields[0])
	if err != nil {
		return Line{}, false
	}
	level := LevelInfo
	switch fields[1] {
	case LevelWarning.String():
		level = LevelWarning
	case LevelError.String():
		level = LevelError
	}
	// Дата не разбирается намеренно: в файле только время суток, а журнал за
	// прошлые дни всё равно читается как «что было в этой сессии».
	return Line{At: at, Level: level, Text: fields[2]}, true
}
