//go:build linux

package config

import (
	"fmt"

	dbus "github.com/godbus/dbus/v5"
	ss "github.com/zalando/go-keyring/secret_service"
)

// OsKeyStore is the OS keychain-backed key store (Secret Service on
// Linux). Item attributes mirror the Rust keyring crate (service,
// username, target, application) so keys written by the Rust core stay
// readable, and vice versa.
type OsKeyStore struct{}

func rustAttributes(provider string) map[string]string {
	return map[string]string{
		"service":     KeyringService,
		"username":    provider,
		"target":      "default",
		"application": "rust-keyring-client",
	}
}

func rustLabel(provider string) string {
	return fmt.Sprintf("%s@%s:default (paladin go core)", provider, KeyringService)
}

func findItem(svc *ss.SecretService, provider string) (dbus.ObjectPath, error) {
	collection := svc.GetLoginCollection()
	if err := svc.Unlock(collection.Path()); err != nil {
		return "", err
	}
	results, err := svc.SearchItems(collection, rustAttributes(provider))
	if err != nil {
		return "", err
	}
	if len(results) == 0 {
		return "", errNoEntry
	}
	return results[0], nil
}

type keyringNotFound struct{}

func (keyringNotFound) Error() string { return "no entry" }

var errNoEntry = keyringNotFound{}

// IsNotFound reports whether err means "no such key".
func IsNotFound(err error) bool {
	_, ok := err.(keyringNotFound)
	return ok
}

func (OsKeyStore) Get(provider string) (string, bool, error) {
	svc, err := ss.NewSecretService()
	if err != nil {
		return "", false, fmt.Errorf("keychain unavailable: %v", err)
	}
	item, err := findItem(svc, provider)
	if err != nil {
		if IsNotFound(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("keychain unavailable: %v", err)
	}
	session, err := svc.OpenSession()
	if err != nil {
		return "", false, fmt.Errorf("keychain unavailable: %v", err)
	}
	defer svc.Close(session)
	if err := svc.Unlock(item); err != nil {
		return "", false, fmt.Errorf("keychain unavailable: %v", err)
	}
	secret, err := svc.GetSecret(item, session.Path())
	if err != nil {
		return "", false, fmt.Errorf("keychain unavailable: %v", err)
	}
	return string(secret.Value), true, nil
}

func (OsKeyStore) Set(provider, key string) error {
	svc, err := ss.NewSecretService()
	if err != nil {
		return fmt.Errorf("keychain unavailable: %v", err)
	}
	session, err := svc.OpenSession()
	if err != nil {
		return fmt.Errorf("keychain unavailable: %v", err)
	}
	defer svc.Close(session)
	collection := svc.GetLoginCollection()
	if err := svc.Unlock(collection.Path()); err != nil {
		return fmt.Errorf("keychain unavailable: %v", err)
	}
	// Replace like the Rust core: delete first so Set never duplicates.
	if item, err := findItem(svc, provider); err == nil {
		if derr := svc.Delete(item); derr != nil {
			return fmt.Errorf("keychain unavailable: %v", derr)
		}
	} else if !IsNotFound(err) {
		return fmt.Errorf("keychain unavailable: %v", err)
	}
	secret := ss.NewSecret(session.Path(), key)
	if err := svc.CreateItem(collection, rustLabel(provider), rustAttributes(provider), secret); err != nil {
		return fmt.Errorf("keychain unavailable: %v", err)
	}
	return nil
}

func (OsKeyStore) Delete(provider string) error {
	svc, err := ss.NewSecretService()
	if err != nil {
		return fmt.Errorf("keychain unavailable: %v", err)
	}
	item, err := findItem(svc, provider)
	if err != nil {
		if IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("keychain unavailable: %v", err)
	}
	if err := svc.Delete(item); err != nil {
		return fmt.Errorf("keychain unavailable: %v", err)
	}
	return nil
}
