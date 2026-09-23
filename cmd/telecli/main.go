package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
	"github.com/spf13/cobra"

	"telecli/internal/auth"
	"telecli/internal/config"
	"telecli/internal/tdclient"
	"telecli/internal/tui"
)

// version — версия релиза, задаётся при сборке через
// -ldflags "-X main.version=vX.Y.Z". Без этого флага (обычная локальная
// сборка) остаётся "dev" — internal/update.IsNewer трактует "dev" как
// "не показывать доступное обновление", не как реальный номер версии.
var version = "dev"

func main() {
	rootCmd := &cobra.Command{
		Use:   "telecli",
		Short: "Терминальный клиент Telegram",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runTUI()
		},
	}
	rootCmd.AddCommand(newSendCmd())
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

	keys, err := config.LoadKeyBindings()
	if err != nil {
		return fmt.Errorf("загрузка конфигурации клавиш: %w", err)
	}

	settings, err := config.LoadSettings()
	if err != nil {
		return fmt.Errorf("загрузка настроек: %w", err)
	}

	// Таймаут 5 минут разумен для интерактивного stdin-диалога аутентификации,
	// но мал для всей TUI-сессии — на TUI-часть применяется отдельный контекст
	// без таймаута, отменяемый по выходу из Program.Run().
	authCtx, authCancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer authCancel()

	client := tdclient.NewClient()
	defer client.Close()

	if err := auth.Authenticate(authCtx, client, creds, auth.NewStdinPrompter()); err != nil {
		return fmt.Errorf("аутентификация: %w", err)
	}

	// TUI живёт всё время интерактивной сессии — контекст без таймаута,
	// отменяется после выхода из Program.Run() (предусмотренный вход в
	// `ctx` нельзя держать «отменённым» между кадрами).
	tuiCtx, tuiCancel := context.WithCancel(context.Background())
	defer tuiCancel()

	model := tui.New(client, tuiCtx, keys, settings, version)

	// Настройка цветового профиля: если терминал поддерживает truecolor
	// (COLORTERM=truecolor или 24bit), форсируем профиль в lipgloss.
	// Это исправляет проблему, когда автоопределение не подхватывает
	// поддержку truecolor в некоторых окружениях.
	setupColorProfile()

	// WithMouseCellMotion — без неё колесо мыши не долетает до приложения
	// как tea.MouseMsg (bubbles/viewport уже умеет прокручивать по колесу
	// «из коробки», MouseWheelEnabled=true по умолчанию — не хватало только
	// захвата мыши на уровне Program). Без захвата некоторые терминалы/
	// мультиплексоры (например, tmux без mouse-режима у самого приложения)
	// откатываются на собственную прокрутку истории поверх alt-screen,
	// из-за чего видно вывод, который был в консоли до запуска.
	program := tea.NewProgram(model, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("TUI: %w", err)
	}
	return nil
}

// setupColorProfile настраивает цветовой профиль lipgloss на основе
// переменных окружения. Если терминал декларирует поддержку truecolor
// (COLORTERM=truecolor или COLORTERM=24bit), форсируем профиль TrueColor.
// Не форсируем слепо для всех терминалов — если COLORTERM не установлен
// или TERM не подразумевает truecolor, оставляем автоопределение
// (корректная деградация до 256/16 цветов — ожидаемое поведение).
func setupColorProfile() {
	colorTerm := strings.ToLower(os.Getenv("COLORTERM"))
	term := os.Getenv("TERM")

	switch colorTerm {
	case "truecolor", "24bit":
		// Дополнительная проверка для screen/tmux: screen не поддерживает
		// truecolor, tmux — поддерживает. TERM_PROGRAM=tmux указывает на tmux.
		if strings.HasPrefix(term, "screen") && os.Getenv("TERM_PROGRAM") != "tmux" {
			// screen без tmux — только ANSI256
			return
		}
		lipgloss.SetColorProfile(termenv.TrueColor)
	case "yes", "true":
		// Явный запрос на цвет, но не truecolor — оставляем автоопределение
		// (обычно даст ANSI256)
		return
	}

	// Дополнительная эвристика: известные терминалы с встроенной поддержкой truecolor
	// даже без COLORTERM (как в termenv).
	trueColorTerms := []string{
		"alacritty",
		"contour",
		"rio",
		"wezterm",
		"xterm-ghostty",
		"xterm-kitty",
	}
	for _, t := range trueColorTerms {
		if term == t {
			lipgloss.SetColorProfile(termenv.TrueColor)
			return
		}
	}
}
