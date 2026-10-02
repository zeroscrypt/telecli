package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"telecli/internal/auth"
	"telecli/internal/config"
	"telecli/internal/tdclient"
	"telecli/internal/tgwall"
	"telecli/internal/update"
)

// version — версия релиза, задаётся при сборке через
// -ldflags "-X main.version=vX.Y.Z". Без этого флага (обычная локальная
// сборка) остаётся "dev" — internal/update.IsNewer трактует "dev" как
// "не показывать доступное обновление", не как реальный номер версии.
var version = "dev"

// appName — как приложение называет себя в нижней строке стены. Живёт здесь, а
// не константой internal/tgwall: имя приходит из точки входа, у которой есть
// бинарник, и на этом же месте меняется одной строкой, а не правкой пакета.
var appName = "TELECLi"

func main() {
	rootCmd := &cobra.Command{
		Use:   "telecli",
		Short: "Терминальный клиент Telegram",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI()
		},
	}
	rootCmd.AddCommand(newSendCmd())
	rootCmd.AddCommand(newUpdateCmd())
	// Не печатать usage-подсказку на обычных рантайм-ошибках.
	rootCmd.SilenceUsage = true
	// cobra по умолчанию сама печатает "Error: ..." (ErrPrefix) в stderr —
	// глушим, чтобы единственной точкой вывода ошибки оставался блок ниже "Ошибка: ...".
	rootCmd.SilenceErrors = true
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка: %v\n", err)
		os.Exit(1)
	}
}

func newSendCmd() *cobra.Command {
	var messageText, filePath string
	cmd := &cobra.Command{
		Use:   "send <@username|chat_id|me>",
		Short: "Отправить текст и/или файл, не заходя в TUI",
		// chat_id групп/каналов в Telegram обычно отрицательный (например,
		// -1001234567890) — cobra/pflag воспринимает позиционный аргумент,
		// начинающийся с "-", как попытку флага ("unknown shorthand flag"),
		// если он не отделён от флагов через "--". Это стандартное для
		// POSIX-CLI поведение (та же особенность у git/docker/kubectl), не
		// баг конкретно этой команды — но без примера в --help пользователь
		// не догадается сам, поэтому показываем этот случай явно.
		Example: `  telecli send @friend -m "привет"
  telecli send @friend -f screenshot.png -m "вот скрин"
  telecli send me -m "заметка себе"
  telecli send 123456789 -m "по chat_id"
  telecli send -m "для канала" -- -1001234567890  # отрицательный chat_id — после "--", иначе будет принят за флаг`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSend(args[0], messageText, filePath)
		},
	}
	cmd.Flags().StringVarP(&messageText, "message", "m", "", "текст сообщения (или подпись к файлу)")
	cmd.Flags().StringVarP(&filePath, "file", "f", "", "путь к файлу для отправки")
	return cmd
}

func runSend(target, messageText, filePath string) error {
	if messageText == "" {
		if piped, err := readStdinIfPiped(); err == nil && piped != "" {
			messageText = piped
		}
	}
	if err := validateSendInput(messageText, filePath); err != nil {
		return err
	}

	creds, err := config.Load()
	if err != nil {
		return fmt.Errorf("ошибка загрузки конфигурации: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	client := tdclient.NewClient()
	defer client.Close()

	if err := auth.Authenticate(ctx, client, creds, auth.NewFailFastPrompter()); err != nil {
		return fmt.Errorf("сессия не готова: %w", err)
	}

	chatID, err := auth.ResolveTarget(ctx, client, target)
	if err != nil {
		return fmt.Errorf("не удалось найти получателя %q: %w", target, err)
	}

	var sentMessage auth.Message
	if filePath != "" {
		sentMessage, err = auth.SendFile(ctx, client, chatID, filePath, messageText)
		if err != nil {
			return fmt.Errorf("не удалось отправить файл: %w", err)
		}
	} else {
		sentMessage, err = auth.SendMessage(ctx, client, chatID, messageText)
		if err != nil {
			return fmt.Errorf("не удалось отправить сообщение: %w", err)
		}
	}

	// sendMessage возвращается быстро, ставя сообщение в очередь, а реальная
	// передача (включая загрузку файла) идёт в фоне уже после ответа. CLI-процесс
	// завершается сразу после main() — без явного ожидания подтверждения фоновую
	// отправку оборвёт ОС (TDLib дозавершит такое сообщение при следующем запуске
	// с этой сессией, см. файл задачи 0011).
	//
	// Таймаут разный для файла и голого текста: загрузка большого файла может
	// занять больше двух минут, поэтому ей даём 10 минут. Для текста без
	// вложения нет разумной причины ждать так же долго — но поведение TDLib
	// (обязательно ли updateMessageSendSucceeded приходит для текстовых
	// сообщений так же надёжно, как для файлов, или синхронный ответ
	// sendMessage иногда УЖЕ терминальный) не подтверждено живым прогоном
	// (недоступен на момент ревью — см. REPORT.md). Короткий таймаут для
	// текста — защита на этот случай: если предположение неверно, процесс
	// упадёт с понятной ошибкой через минуту, а не зависнет на 10 минут при
	// каждой самой обычной отправке.
	confirmTimeout := 10 * time.Minute
	if filePath == "" {
		confirmTimeout = 1 * time.Minute
	}
	fmt.Fprintln(os.Stderr, "Отправка… (ожидание подтверждения от Telegram)")
	confirmCtx, confirmCancel := context.WithTimeout(context.Background(), confirmTimeout)
	defer confirmCancel()
	if err := auth.WaitForSendConfirmation(confirmCtx, client, sentMessage.ID); err != nil {
		return fmt.Errorf("не удалось подтвердить доставку: %w", err)
	}
	return nil
}

func newUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Скачать и установить последнюю версию telecli",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate()
		},
	}
}

// Таймауты обновления. Проверка релиза — короткий запрос за одной записью JSON,
// здесь важнее быстро сдаться, чем держать человека перед пустым экраном;
// скачивание — передача десятков мегабайт, и таймаут взят по прецеденту runSend
// в этом же файле, где столько же даётся отправке файла (2 минуты там стоят
// аутентификации, а не передаче файла).
const (
	updateCheckTimeout    = 5 * time.Second
	updateDownloadTimeout = 10 * time.Minute
)

// updatePlan — что выяснила проверка обновлений и что предстоит сделать. Отдельная
// структура вместо того, чтобы проверять всё прямо в runUpdate: решение «есть
// ли обновление, какой ассет качать» не печатает ничего и не трогает диск,
// поэтому проверяется тестами без сети и без настоящего бинарника (адрес GitHub
// подменяется на httptest тем же update.SetAPIURLForTest, что и в тестах
// internal/update).
type updatePlan struct {
	// UpToDate — обновляться нечего: стоит уже последняя версия либо эта сборка
	// вообще не привязана к релизу (version == "dev").
	UpToDate bool
	// Version — версия, ради которой обновляемся (тег нового релиза). Осмысленна
	// только при UpToDate == false.
	Version string
	// AssetURL — откуда качать бинарник этой платформы.
	AssetURL string
	// ReleaseURL — страница релиза на GitHub. Показывается в ошибках: если
	// готового бинарника нет, единственное, что можно предложить человеку, —
	// скачать вручную оттуда.
	ReleaseURL string
}

// planUpdate спрашивает у GitHub последний релиз и решает, что делать дальше.
//
// currentVersion подставляется снаружи (это main.version), а не читается внутри:
// проверка «новая ли версия» — единственное место, где сборка сравнивает себя с
// релизом, и подмена версии в тесте обязана быть видна здесь, а не прятаться
// внутри функции.
func planUpdate(ctx context.Context, client *http.Client, currentVersion string) (updatePlan, error) {
	rel, err := update.CheckLatest(ctx, client, updateCheckTimeout)
	if err != nil {
		return updatePlan{}, fmt.Errorf("проверка обновлений: %w", err)
	}
	// IsNewer для "dev" всегда false — локальная dev-сборка не привязана к
	// номеру релиза, сравнивать нечего. Это ожидаемое поведение, а не сбой:
	// обойти его значило бы притвориться, что dev-сборка старше любого релиза.
	if !update.IsNewer(currentVersion, rel.TagName) {
		return updatePlan{UpToDate: true, Version: currentVersion}, nil
	}

	assetName, ok := update.AssetNameForPlatform()
	if !ok {
		return updatePlan{}, fmt.Errorf("готового бинарника для этой платформы нет (%s/%s) — скачайте вручную: %s",
			runtime.GOOS, runtime.GOARCH, rel.HTMLURL)
	}
	asset, ok := update.FindAsset(rel, assetName)
	if !ok {
		return updatePlan{}, fmt.Errorf("ассет %q не найден в релизе %s (возможно, сборка для этой платформы не удалась) — скачайте вручную: %s",
			assetName, rel.TagName, rel.HTMLURL)
	}
	return updatePlan{Version: rel.TagName, AssetURL: asset.BrowserDownloadURL, ReleaseURL: rel.HTMLURL}, nil
}

// upToDateNotice — что сказать, когда обновляться нечего. Отдельная функция
// ради одного различия, которое видно человеку: у dev-сборки «последняя
// версия» звучала бы как уверенное утверждение, а на деле версии у неё нет.
func upToDateNotice(currentVersion string) string {
	if currentVersion == "dev" {
		return "Это локальная сборка без номера релиза (dev) — она никогда не считается устаревшей. " +
			"Чтобы обновления работали, соберите с -ldflags \"-X main.version=vX.Y.Z\"."
	}
	return fmt.Sprintf("У вас уже последняя версия: %s", currentVersion)
}

// executablePath — путь к файлу, который нужно заменить.
//
// Симлинки разрешаются ДО InstallBinary, и это не косметика: InstallBinary
// создаёт временный файл в filepath.Dir(execPath) и переименовывает его в
// execPath. Без разрешения на пути /usr/local/bin/telecli -> /opt/telecli/bin/telecli
// переименование заменило бы САМ СИМЛИНК бинарником, и symlink-раскладка
// (как её делают most-дистрибутивы и brew) перестала бы работать: /opt/telecli
// остался бы старой версией навсегда. С разрешением заменяется настоящий файл,
// а ссылка продолжает на него указывать.
func executablePath() (string, error) {
	execPath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("определение пути исполняемого файла: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(execPath)
	if err != nil {
		return "", fmt.Errorf("разрешение симлинков исполняемого файла: %w", err)
	}
	return resolved, nil
}

// runUpdate — тело подкоманды `telecli update`: план, скачивание, установка.
// Вывод разделён намеренно: прогресс идёт в stderr, результат — в stdout, чтобы
// `telecli update` можно было перенаправить и прочитать только ответ.
func runUpdate() error {
	ctx := context.Background()

	fmt.Fprintln(os.Stderr, "Проверка обновлений…")
	plan, err := planUpdate(ctx, http.DefaultClient, version)
	if err != nil {
		return err
	}
	if plan.UpToDate {
		fmt.Fprintln(os.Stdout, upToDateNotice(plan.Version))
		return nil
	}

	fmt.Fprintf(os.Stderr, "Скачивание %s…\n", plan.Version)
	data, err := update.DownloadBinary(ctx, http.DefaultClient, plan.AssetURL, updateDownloadTimeout)
	if err != nil {
		return fmt.Errorf("скачивание бинарника: %w", err)
	}

	execPath, err := executablePath()
	if err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "Установка…")
	if err := update.InstallBinary(data, execPath); err != nil {
		return fmt.Errorf("установка бинарника: %w", err)
	}

	fmt.Fprintf(os.Stdout, "Обновлено до %s\n", plan.Version)
	return nil
}

// validateSendInput проверяет, что задан хотя бы один источник содержимого
// (-m текст или -f файл) и что указанный файл существует.
func validateSendInput(messageText, filePath string) error {
	if messageText == "" && filePath == "" {
		return errors.New("нужен хотя бы -m/--message или -f/--file")
	}
	if filePath != "" {
		if _, err := os.Stat(filePath); err != nil {
			return fmt.Errorf("файл недоступен: %w", err)
		}
	}
	return nil
}

// readStdinIfPiped читает весь stdin целиком, только если он НЕ интерактивный
// терминал (т.е. данные реально пришли по пайпу/редиректу из файла). Для
// интерактивного терминала возвращает "" — читать нечего и не нужно блокировать
// выполнение. Используется в runSend как источник текста/подписи, если -m не задан.
func readStdinIfPiped() (string, error) {
	stat, err := os.Stdin.Stat()
	if err != nil {
		return "", nil // не удалось определить — считаем "не пайп", не блокируем выполнение
	}
	if stat.Mode()&os.ModeCharDevice != 0 {
		return "", nil // интерактивный терминал — ничего не читаем
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(data), "\n"), nil
}

func runTUI() error {
	creds, err := config.Load()
	if err != nil {
		return fmt.Errorf("загрузка конфигурации: %w", err)
	}

	authCtx, authCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer authCancel()

	client := tdclient.NewClient()
	defer client.Close()

	// Ошибка авторизации — до запуска tea.Program: внутри TUI её некуда показать,
	// промптер печатает телефон/код/пароль прямо в терминал, и bubbletea с
	// альт-экраном стёр бы этот вывод.
	if err := auth.Authenticate(authCtx, client, creds, auth.NewStdinPrompter()); err != nil {
		return fmt.Errorf("аутентификация: %w", err)
	}

	tuiCtx, tuiCancel := context.WithCancel(context.Background())
	defer tuiCancel()

	// Собственный ник аккаунта — рядом с лого. Ника нет — значит не показываем
	// ничего; сбой getMe — тоже: стена от этого не перестаёт работать, а вот
	// пустой «@» или выдуманная подпись читались бы как поломка.
	username, err := auth.GetOwnUsername(tuiCtx, client)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Не удалось получить @ник аккаунта: %v\n", err)
	}

	// Раскладка клавиш — из конфигурации, тем же файлом и тем же разделом, что у
	// остальных интерфейсов: подсказка под полем показывает ровно то, на что стена
	// реагирует (см. internal/tgwall/keymap.go). Ошибка чтения — до запуска tea,
	// как и ошибка конфигурации: показать её в TUI негде.
	keys, err := config.LoadTgwallKeyBindings()
	if err != nil {
		return fmt.Errorf("загрузка конфигурации клавиш tgwall: %w", err)
	}

	// Настройки нужны стене ради сохранённого фильтра источников (что показывать):
	// без него стена открывалась бы в режиме «видно всё» и каждый раз теряла бы
	// выбор человека. Файл настроек не про секреты и не про клавиш, отдельной
	// ошибки у него нет — дефолты при недоступном файле означают «показать всё»,
	// то есть ровно то, что стена делала до фильтра.
	settings, err := config.LoadSettings()
	if err != nil {
		return fmt.Errorf("загрузка настроек tgwall: %w", err)
	}

	model := tgwall.New(tuiCtx, client, username, keys, settings, appName, version)

	// Альт-экран и режим мыши в v2 — поля View, а не опции программы: их
	// выставляет View() модели. Контекст остался программной опцией.
	program := tea.NewProgram(model, tea.WithContext(tuiCtx))
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("TUI: %w", err)
	}
	return nil
}
