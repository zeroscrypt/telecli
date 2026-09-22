package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
	"github.com/zalando/go-keyring"
)

type Credentials struct {
	APIID   int32  `json:"api_id" toml:"api_id"`
	APIHash string `json:"api_hash" toml:"api_hash"`
}

type KeyringBackend interface {
	Get(service, user string) (string, error)
	Set(service, user, secret string) error
	Delete(service, user string) error
}

type realKeyring struct{}

func (realKeyring) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (realKeyring) Set(service, user, secret string) error {
	return keyring.Set(service, user, secret)
}

func (realKeyring) Delete(service, user string) error {
	return keyring.Delete(service, user)
}

var defaultKeyring KeyringBackend = realKeyring{}
var fallbackPathOverride string

func SetKeyringBackend(k KeyringBackend) {
	defaultKeyring = k
}

func SetFallbackPathForTest(path string) {
	fallbackPathOverride = path
}

func FallbackPath() (string, error) {
	return fallbackPath()
}

func FallbackPathForTest() string {
	return fallbackPathOverride
}

const (
	serviceName = "telecli"
	userName    = "api-credentials"
)

func fallbackPath() (string, error) {
	if fallbackPathOverride != "" {
		return fallbackPathOverride, nil
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine user config dir: %w", err)
	}
	return filepath.Join(configDir, "telecli", "config.toml"), nil
}

func ensureFallbackDir() error {
	path, err := fallbackPath()
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	return os.MkdirAll(dir, 0700)
}

func Load() (Credentials, error) {
	if creds, err := loadFromKeyring(); err == nil {
		return creds, nil
	}

	if creds, err := loadFromFallback(); err == nil {
		return creds, nil
	}

	apiIDStr := os.Getenv("TELECLI_API_ID")
	apiHash := os.Getenv("TELECLI_API_HASH")
	if apiIDStr != "" && apiHash != "" {
		var apiID int32
		_, err := fmt.Sscanf(apiIDStr, "%d", &apiID)
		if err != nil {
			return Credentials{}, fmt.Errorf("invalid TELECLI_API_ID: must be a valid integer")
		}
		creds := Credentials{APIID: apiID, APIHash: apiHash}
		if err := Save(creds); err != nil {
			return Credentials{}, fmt.Errorf("failed to save credentials from env: %w", err)
		}
		return creds, nil
	}

	return Credentials{}, fmt.Errorf(
		"Telegram API credentials not found. Register an application at https://my.telegram.org, then set TELECLI_API_ID and TELECLI_API_HASH environment variables on first run",
	)
}

func loadFromKeyring() (Credentials, error) {
	data, err := defaultKeyring.Get(serviceName, userName)
	if err != nil {
		return Credentials{}, err
	}
	var creds Credentials
	if err := json.Unmarshal([]byte(data), &creds); err != nil {
		return Credentials{}, fmt.Errorf("failed to parse keyring data: %w", err)
	}
	return creds, nil
}

func LoadFromKeyringForTest() (Credentials, error) {
	return loadFromKeyring()
}

func loadFromFallback() (Credentials, error) {
	path, err := fallbackPath()
	if err != nil {
		return Credentials{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, err
	}
	var creds Credentials
	if err := toml.Unmarshal(data, &creds); err != nil {
		return Credentials{}, fmt.Errorf("failed to parse fallback config: %w", err)
	}
	return creds, nil
}

func Save(creds Credentials) error {
	data, err := json.Marshal(creds)
	if err != nil {
		return fmt.Errorf("failed to marshal credentials: %w", err)
	}

	if err := defaultKeyring.Set(serviceName, userName, string(data)); err != nil {
		return saveToFallback(creds)
	}
	return nil
}

func saveToFallback(creds Credentials) error {
	if err := ensureFallbackDir(); err != nil {
		return fmt.Errorf("failed to create fallback config dir: %w", err)
	}
	path, err := fallbackPath()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open fallback config: %w", err)
	}
	defer f.Close()
	enc := toml.NewEncoder(f)
	if err := enc.Encode(creds); err != nil {
		return fmt.Errorf("failed to encode fallback config: %w", err)
	}
	return nil
}
