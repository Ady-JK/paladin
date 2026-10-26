//go:build !linux

package config

import (
	"errors"
	"fmt"

	"github.com/zalando/go-keyring"
)

// OsKeyStore is the OS keychain-backed key store (Keychain on macOS,
// Credential Manager on Windows).
type OsKeyStore struct{}

func (OsKeyStore) Get(provider string) (string, bool, error) {
	key, err := keyring.Get(KeyringService, provider)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("keychain unavailable: %v", err)
	}
	return key, true, nil
}

func (OsKeyStore) Set(provider, key string) error {
	if err := keyring.Set(KeyringService, provider, key); err != nil {
		return fmt.Errorf("keychain unavailable: %v", err)
	}
	return nil
}

func (OsKeyStore) Delete(provider string) error {
	if err := keyring.Delete(KeyringService, provider); err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("keychain unavailable: %v", err)
	}
	return nil
}
