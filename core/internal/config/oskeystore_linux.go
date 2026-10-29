//go:build linux

package config

import (
	"fmt"
	"sort"

	dbus "github.com/godbus/dbus/v5"
	ss "github.com/zalando/go-keyring/secret_service"
)

// OsKeyStore is the OS keychain-backed key store (Secret Service on
// Linux). Items are identified by service + username + target, which is
// exactly what the keyring-rs crate searches on, so keys written by a
// keyring-rs client stay readable and ours are readable by it. The
// application attribute is metadata and is never part of the identity.
type OsKeyStore struct{}

const (
	secretServiceName = "org.freedesktop.secrets"
	secretServicePath = "/org/freedesktop/secrets"
	secretServiceIf   = "org.freedesktop.Secret.Service"

	defaultCollectionAlias = "/org/freedesktop/secrets/aliases/default"

	collectionLabelIface = "org.freedesktop.Secret.Collection.Label"
	itemIface            = "org.freedesktop.Secret.Item"

	// libsecret/desktop keyrings use this schema for generic passwords; it
	// makes the item show up in Seahorse and other keyring browsers.
	genericSchema = "org.freedesktop.Secret.Generic"
)

// createdAttributes are stored with a new item. Items written by older
// builds (keyring-rs v3, application=rust-keyring) use the same four
// keys, so they still resolve; the extra schema attribute is what
// libsecret-backed tools write.
func createdAttributes(provider string) map[string]string {
	return map[string]string{
		"service":     KeyringService,
		"username":    provider,
		"target":      "default",
		"application": "paladin",
		"xdg:schema":  genericSchema,
	}
}

// identityAttributes is the search key. keyring-rs searches service,
// username and target (never application), so items written by older
// paladin builds or by keyring-rs are found regardless of their
// application value.
func identityAttributes(provider string) map[string]string {
	return map[string]string{
		"service":  KeyringService,
		"username": provider,
		"target":   "default",
	}
}

// legacyAttributes match keyring v1 items, which had no target. This is
// the same fallback keyring-rs performs when the identity search is empty.
func legacyAttributes(provider string) map[string]string {
	return map[string]string{
		"service":  KeyringService,
		"username": provider,
	}
}

func itemLabel(provider string) string {
	return fmt.Sprintf("%s@%s:default (paladin)", provider, KeyringService)
}

func keychainErr(err error) error {
	return fmt.Errorf("keychain unavailable: %v", err)
}

// searchAll runs a service-wide search (like keyring-rs) and unlocks any
// locked results so they can be read.
func searchAll(svc *ss.SecretService, attrs map[string]string) ([]dbus.ObjectPath, error) {
	var unlocked, locked []dbus.ObjectPath
	obj := svc.Object(secretServiceName, dbus.ObjectPath(secretServicePath))
	if err := obj.Call(secretServiceIf+".SearchItems", 0, attrs).Store(&unlocked, &locked); err != nil {
		return nil, err
	}
	for _, item := range locked {
		if err := svc.Unlock(item); err != nil {
			return nil, err
		}
	}
	return append(unlocked, locked...), nil
}

// findItems returns every item for provider, newest first. It first
// searches the modern identity attributes and falls back to the v1
// (no target) shape when nothing matches.
func findItems(svc *ss.SecretService, provider string) ([]dbus.ObjectPath, error) {
	items, err := searchAll(svc, identityAttributes(provider))
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		items, err = searchAll(svc, legacyAttributes(provider))
		if err != nil {
			return nil, err
		}
	}
	if len(items) > 1 {
		sort.SliceStable(items, func(i, j int) bool {
			return itemModified(svc, items[i]) > itemModified(svc, items[j])
		})
	}
	return items, nil
}

// itemModified returns the item's modification time, used only to pick
// the freshest copy when a keyring already contains duplicates.
func itemModified(svc *ss.SecretService, item dbus.ObjectPath) uint64 {
	v, err := svc.Object(secretServiceName, item).GetProperty(itemIface + ".Modified")
	if err != nil {
		return 0
	}
	n, _ := v.Value().(uint64)
	return n
}

// defaultCollection resolves the desktop's default collection, preferring
// the standard alias (what keyring-rs writes to) and falling back to the
// login collection on services that do not expose the alias.
func defaultCollection(svc *ss.SecretService) dbus.BusObject {
	alias := svc.Object(secretServiceName, dbus.ObjectPath(defaultCollectionAlias))
	if _, err := alias.GetProperty(collectionLabelIface); err == nil {
		return alias
	}
	return svc.GetLoginCollection()
}

func (OsKeyStore) Get(provider string) (string, bool, error) {
	svc, err := ss.NewSecretService()
	if err != nil {
		return "", false, keychainErr(err)
	}
	items, err := findItems(svc, provider)
	if err != nil {
		return "", false, keychainErr(err)
	}
	if len(items) == 0 {
		return "", false, nil
	}
	session, err := svc.OpenSession()
	if err != nil {
		return "", false, keychainErr(err)
	}
	defer svc.Close(session)
	// Duplicates should not exist, but if they do, try each newest first
	// instead of failing on one unreadable item.
	var lastErr error
	for _, item := range items {
		secret, err := svc.GetSecret(item, session.Path())
		if err != nil {
			lastErr = err
			continue
		}
		return string(secret.Value), true, nil
	}
	return "", false, keychainErr(lastErr)
}

func (OsKeyStore) Set(provider, key string) error {
	svc, err := ss.NewSecretService()
	if err != nil {
		return keychainErr(err)
	}
	session, err := svc.OpenSession()
	if err != nil {
		return keychainErr(err)
	}
	defer svc.Close(session)

	secret := ss.NewSecret(session.Path(), key)
	items, err := findItems(svc, provider)
	if err != nil {
		return keychainErr(err)
	}
	// Update in place when the key already exists: replacing the secret
	// cannot lose the credential if the daemon is killed halfway through.
	// Any duplicates left by older builds are removed afterwards.
	if len(items) > 0 {
		if err := svc.Unlock(items[0]); err != nil {
			return keychainErr(err)
		}
		if err := svc.Object(secretServiceName, items[0]).Call(itemIface+".SetSecret", 0, secret).Err; err != nil {
			return keychainErr(err)
		}
		for _, dup := range items[1:] {
			if err := svc.Delete(dup); err != nil {
				return keychainErr(err)
			}
		}
		return nil
	}

	collection := defaultCollection(svc)
	if err := svc.Unlock(collection.Path()); err != nil {
		return keychainErr(err)
	}
	if err := svc.CreateItem(collection, itemLabel(provider), createdAttributes(provider), secret); err != nil {
		return keychainErr(err)
	}
	return nil
}

func (OsKeyStore) Delete(provider string) error {
	svc, err := ss.NewSecretService()
	if err != nil {
		return keychainErr(err)
	}
	items, err := findItems(svc, provider)
	if err != nil {
		return keychainErr(err)
	}
	for _, item := range items {
		if err := svc.Delete(item); err != nil {
			return keychainErr(err)
		}
	}
	return nil
}
