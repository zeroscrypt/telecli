package auth

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"telecli/internal/config"
)

type Prompter interface {
	PhoneNumber() (string, error)
	Code() (string, error)
	Password() (string, error)
}

// failFastPrompter — Prompter для неинтерактивных точек входа (CLI send):
// если TDLib запрашивает телефон/код/пароль (сессии ещё нет или она
// невалидна), это должно быть явной ошибкой сразу, а не зависанием на чтении
// stdin (который в CLI-режиме может быть занят под текст сообщения) и не
// попыткой угадать/автоматизировать ввод секретов.
type failFastPrompter struct{}

// NewFailFastPrompter возвращает Prompter для неинтерактивных точек входа
// (CLI send): каждый метод отвечает немедленной ошибкой вместо чтения stdin.
func NewFailFastPrompter() Prompter {
	return failFastPrompter{}
}

func (failFastPrompter) PhoneNumber() (string, error) {
	return "", errors.New("сессия не авторизована — сначала войдите через обычный запуск telecli (без аргументов)")
}
func (failFastPrompter) Code() (string, error) {
	return "", errors.New("сессия не авторизована — сначала войдите через обычный запуск telecli (без аргументов)")
}
func (failFastPrompter) Password() (string, error) {
	return "", errors.New("сессия не авторизована — сначала войдите через обычный запуск telecli (без аргументов)")
}

type StdinPrompter struct {
	reader *bufio.Reader
}

func NewStdinPrompter() *StdinPrompter {
	return &StdinPrompter{reader: bufio.NewReader(os.Stdin)}
}

func (p *StdinPrompter) PhoneNumber() (string, error) {
	fmt.Print("Введите номер телефона: ")
	input, err := p.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(input), nil
}

func (p *StdinPrompter) Code() (string, error) {
	fmt.Print("Введите код: ")
	input, err := p.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(input), nil
}

func (p *StdinPrompter) Password() (string, error) {
	fmt.Print("Введите пароль 2FA: ")
	input, err := p.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(input), nil
}

type TDClientInterface interface {
	Send(ctx context.Context, request map[string]interface{}) (map[string]interface{}, error)
	Execute(request map[string]interface{}) (map[string]interface{}, error)
	AuthUpdates() <-chan map[string]interface{}
	MessageUpdates() <-chan map[string]interface{}
	SendStatusUpdates() <-chan map[string]interface{}
	ChatFolderUpdates() <-chan map[string]interface{}
	ChatReadInboxUpdates() <-chan map[string]interface{}
	ChatReadOutboxUpdates() <-chan map[string]interface{}
	ChatTitleUpdates() <-chan map[string]interface{}
	UnreadCountUpdates() <-chan map[string]interface{}
	UnreadChatCountUpdates() <-chan map[string]interface{}
	Close()
}

func Authenticate(ctx context.Context, client TDClientInterface, creds config.Credentials, prompter Prompter) error {
	for {
		select {
		case update := <-client.AuthUpdates():
			state, ok := update["authorization_state"].(map[string]interface{})
			if !ok {
				continue
			}

			stateType, _ := state["@type"].(string)

			switch stateType {
			case "authorizationStateWaitTdlibParameters":
				// Актуальная схема (сверено с td_api.tl текущего master, TDLib ~1.8.67): поля
				// setTdlibParameters плоские, без вложенного объекта "parameters" — это
				// изменилось по сравнению со старой схемой (см. историю задачи 0003, где
				// использовалась вложенная форма для TDLib 1.8.0 из Homebrew). Отдельного шага
				// checkDatabaseEncryptionKey/authorizationStateWaitEncryptionKey в этой схеме
				// больше нет — ключ шифрования передаётся прямо здесь, полем database_encryption_key.
				params := map[string]interface{}{
					"@type":                   "setTdlibParameters",
					"use_test_dc":             false,
					"database_directory":      databaseDirectory(),
					"files_directory":         filesDirectory(),
					"database_encryption_key": "",
					"use_file_database":       false,
					"use_chat_info_database":  false,
					"use_message_database":    true,
					"use_secret_chats":        false,
					"api_id":                  creds.APIID,
					"api_hash":                creds.APIHash,
					"system_language_code":    "en",
					"device_model":            "telecli",
					"system_version":          "",
					"application_version":     "0.1.0",
				}
				if _, err := client.Send(ctx, params); err != nil {
					return fmt.Errorf("setTdlibParameters failed: %w", err)
				}

			case "authorizationStateWaitPhoneNumber":
				phone, err := prompter.PhoneNumber()
				if err != nil {
					return fmt.Errorf("failed to read phone number: %w", err)
				}
				params := map[string]interface{}{
					"@type":        "setAuthenticationPhoneNumber",
					"phone_number": phone,
				}
				if _, err := client.Send(ctx, params); err != nil {
					return fmt.Errorf("setAuthenticationPhoneNumber failed: %w", err)
				}

			case "authorizationStateWaitCode":
				code, err := prompter.Code()
				if err != nil {
					return fmt.Errorf("failed to read code: %w", err)
				}
				params := map[string]interface{}{
					"@type": "checkAuthenticationCode",
					"code":  code,
				}
				if _, err := client.Send(ctx, params); err != nil {
					return fmt.Errorf("checkAuthenticationCode failed: %w", err)
				}

			case "authorizationStateWaitPassword":
				password, err := prompter.Password()
				if err != nil {
					return fmt.Errorf("failed to read password: %w", err)
				}
				params := map[string]interface{}{
					"@type":    "checkAuthenticationPassword",
					"password": password,
				}
				if _, err := client.Send(ctx, params); err != nil {
					return fmt.Errorf("checkAuthenticationPassword failed: %w", err)
				}

			case "authorizationStateReady":
				return nil

			case "authorizationStateClosed":
				return errors.New("tdlib: authorization closed unexpectedly")

			default:
			}

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func databaseDirectory() string {
	return tdlibSubdir("tdlib-db")
}

func filesDirectory() string {
	return tdlibSubdir("tdlib-files")
}

// tdlibSubdir возвращает путь <UserConfigDir>/telecli/<name> и создаёт его (с правами 0700),
// если каталога ещё нет — TDLib в некоторых версиях полагается на то, что каталог уже существует.
func tdlibSubdir(name string) string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = "."
	}
	dir := filepath.Join(configDir, "telecli", name)
	_ = os.MkdirAll(dir, 0700)
	return dir
}
