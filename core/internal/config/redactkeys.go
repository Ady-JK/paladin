package config

import "github.com/Ady-JK/paladin/core/internal/redact"

// redactingKeys registers every key it sees with the redactor, so a
// stored provider key echoed by a shell command, error or log is masked.
type redactingKeys struct{ inner KeyStore }

func (k redactingKeys) Get(provider string) (string, bool, error) {
	v, ok, err := k.inner.Get(provider)
	if ok {
		redact.Register(v)
	}
	return v, ok, err
}

func (k redactingKeys) Set(provider, key string) error {
	if err := k.inner.Set(provider, key); err != nil {
		return err
	}
	redact.Register(key)
	return nil
}

func (k redactingKeys) Delete(provider string) error {
	if v, ok, _ := k.inner.Get(provider); ok {
		redact.Unregister(v)
	}
	return k.inner.Delete(provider)
}
