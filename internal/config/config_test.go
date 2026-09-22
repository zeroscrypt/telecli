package config

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	kr "github.com/zalando/go-keyring"
)

type mockKeyring struct {
	store map[string]string
	err   error
}

func newMockKeyring() *mockKeyring {
	return &mockKeyring{store: make(map[string]string)}
}

func (m *mockKeyring) Get(service, user string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	key := service + "/" + user
	val, ok := m.store[key]
	if !ok {
		return "", kr.ErrNotFound
	}
	return val, nil
}

func (m *mockKeyring) Set(service, user, secret string) error {
	if m.err != nil {
		return m.err
	}
	key := service + "/" + user
	m.store[key] = secret
	return nil
}

func (m *mockKeyring) Delete(service, user string) error {
	if m.err != nil {
		return m.err
	}
	key := service + "/" + user
	delete(m.store, key)
	return nil
}

func setupTest(t *testing.T) (string, func()) {
	mock := newMockKeyring()
	mock.err = kr.ErrUnsupportedPlatform
	SetKeyringBackend(mock)

	tmpDir := t.TempDir()
	fallbackFile := filepath.Join(tmpDir, "config.toml")
	SetFallbackPathForTest(fallbackFile)

	cleanup := func() {
		SetKeyringBackend(realKeyring{})
		SetFallbackPathForTest("")
	}

	return tmpDir, cleanup
}

func TestSaveAndLoadFallback(t *testing.T) {
	_, cleanup := setupTest(t)
	defer cleanup()

	creds := Credentials{APIID: 12345, APIHash: "test-hash-value"}
	err := Save(creds)
	require.NoError(t, err)

	loaded, err := Load()
	require.NoError(t, err)
	require.Equal(t, creds.APIID, loaded.APIID)
	require.Equal(t, creds.APIHash, loaded.APIHash)

	loaded2, err := Load()
	require.NoError(t, err)
	require.Equal(t, creds.APIID, loaded2.APIID)
	require.Equal(t, creds.APIHash, loaded2.APIHash)
}

func TestLoadFromEnvVars(t *testing.T) {
	_, cleanup := setupTest(t)
	defer cleanup()

	t.Setenv("TELECLI_API_ID", "99999")
	t.Setenv("TELECLI_API_HASH", "env-hash-value")

	creds, err := Load()
	require.NoError(t, err)
	require.Equal(t, int32(99999), creds.APIID)
	require.Equal(t, "env-hash-value", creds.APIHash)

	t.Setenv("TELECLI_API_ID", "")
	t.Setenv("TELECLI_API_HASH", "")

	creds2, err := Load()
	require.NoError(t, err)
	require.Equal(t, int32(99999), creds2.APIID)
	require.Equal(t, "env-hash-value", creds2.APIHash)
}

func TestLoadErrorWhenNothingConfigured(t *testing.T) {
	_, cleanup := setupTest(t)
	defer cleanup()

	_, err := Load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "Telegram API credentials not found")
	require.Contains(t, err.Error(), "my.telegram.org")
	require.Contains(t, err.Error(), "TELECLI_API_ID")
	require.Contains(t, err.Error(), "TELECLI_API_HASH")
}

func TestLoadInvalidAPIID(t *testing.T) {
	_, cleanup := setupTest(t)
	defer cleanup()

	t.Setenv("TELECLI_API_ID", "not-a-number")
	t.Setenv("TELECLI_API_HASH", "some-hash")

	_, err := Load()
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid TELECLI_API_ID")
}

func TestKeyringUnavailableFallsBackToFile(t *testing.T) {
	_, cleanup := setupTest(t)
	defer cleanup()

	creds := Credentials{APIID: 11111, APIHash: "keyring-unavailable"}
	err := Save(creds)
	require.NoError(t, err)

	loaded, err := Load()
	require.NoError(t, err)
	require.Equal(t, creds.APIID, loaded.APIID)
	require.Equal(t, creds.APIHash, loaded.APIHash)
}

func TestLoadFromKeyring(t *testing.T) {
	mock := newMockKeyring()
	SetKeyringBackend(mock)
	defer SetKeyringBackend(realKeyring{})

	creds := Credentials{APIID: 55555, APIHash: "keyring-hash"}
	data, _ := json.Marshal(creds)
	mock.store["telecli/api-credentials"] = string(data)

	loaded, err := Load()
	require.NoError(t, err)
	require.Equal(t, creds.APIID, loaded.APIID)
	require.Equal(t, creds.APIHash, loaded.APIHash)
}

func TestKeyringNotFoundFallsBackToFile(t *testing.T) {
	mock := newMockKeyring()
	mock.err = kr.ErrNotFound
	SetKeyringBackend(mock)
	defer SetKeyringBackend(realKeyring{})

	_, cleanup := setupTest(t)
	defer cleanup()

	creds := Credentials{APIID: 77777, APIHash: "fallback-hash"}
	err := Save(creds)
	require.NoError(t, err)

	loaded, err := Load()
	require.NoError(t, err)
	require.Equal(t, creds.APIID, loaded.APIID)
	require.Equal(t, creds.APIHash, loaded.APIHash)
}
