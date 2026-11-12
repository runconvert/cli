// Package auth stores the API key between runs: in the system keychain
// (macOS Keychain, Windows Credential Manager, Linux Secret Service) when
// there is one, else in a file only this user can read.
package auth

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/zalando/go-keyring"
)

const (
	service = "runconvert"
	account = "api-key"
)

type Config struct {
	// APIKey is set only when the keychain wasn't available.
	APIKey string `json:"api_key,omitempty"`
	// Keychain records that the key is in the system keychain.
	Keychain bool `json:"keychain,omitempty"`
}

// Path is ~/.config/runconvert/config.json on Linux, ~/Library/Application
// Support/runconvert on macOS and %AppData%\runconvert on Windows.
func Path() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "runconvert", "config.json"), nil
}

func load() (Config, error) {
	var c Config
	p, err := Path()
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	return c, json.Unmarshal(b, &c)
}

func write(c Config) error {
	p, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}

// SaveKey stores the key, returning where it went ("system keychain" or the file path).
func SaveKey(key string) (string, error) {
	if err := keyring.Set(service, account, key); err == nil {
		return "the system keychain", write(Config{Keychain: true})
	}
	p, err := Path()
	if err != nil {
		return "", err
	}
	return p, write(Config{APIKey: key})
}

// SavedKey returns the key saved by SaveKey, or "".
func SavedKey() string {
	c, _ := load()
	if c.APIKey != "" {
		return c.APIKey
	}
	if c.Keychain {
		if k, err := keyring.Get(service, account); err == nil {
			return k
		}
	}
	return ""
}

// Remove deletes the saved key from the keychain and the file.
func Remove() error {
	// Errors mean no keychain or no saved key: nothing to delete there.
	_ = keyring.Delete(service, account)
	p, err := Path()
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// Key returns the API key: --key, then RUNCONVERT_API_KEY, then the saved one.
func Key(flag string) string {
	if flag != "" {
		return flag
	}
	if k := os.Getenv("RUNCONVERT_API_KEY"); k != "" {
		return k
	}
	return SavedKey()
}
