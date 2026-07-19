package capabilities

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// secretVault persists only DPAPI-protected blobs. Plaintext values exist only
// while handling an authenticated loopback request or building one launch.
type secretVault struct {
	mu   sync.Mutex
	path string
}

func newSecretVault(path string) *secretVault { return &secretVault{path: path} }

func (v *secretVault) Set(itemID, name, value string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	values, err := v.load()
	if err != nil {
		return err
	}
	protected, err := protectSecret([]byte(value))
	if err != nil {
		return err
	}
	values[itemID+"/"+name] = base64.RawStdEncoding.EncodeToString(protected)
	return v.persist(values)
}

func (v *secretVault) Get(itemID, name string) (string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	values, err := v.load()
	if err != nil {
		return "", err
	}
	encoded, ok := values[itemID+"/"+name]
	if !ok {
		return "", errors.New("secret is not configured")
	}
	protected, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	plain, err := unprotectSecret(protected)
	return string(plain), err
}

func (v *secretVault) DeleteItem(itemID string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	values, err := v.load()
	if err != nil {
		return err
	}
	for key := range values {
		if len(key) > len(itemID) && key[:len(itemID)+1] == itemID+"/" {
			delete(values, key)
		}
	}
	return v.persist(values)
}

func (v *secretVault) load() (map[string]string, error) {
	data, err := os.ReadFile(v.path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	values := map[string]string{}
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, err
	}
	return values, nil
}

func (v *secretVault) persist(values map[string]string) error {
	if err := os.MkdirAll(filepath.Dir(v.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(values, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(v.path), ".secrets-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, v.path)
}
