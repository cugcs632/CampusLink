package profile

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type memoryKeyring map[string]string

func (m memoryKeyring) Get(s, u string) (string, error) {
	value, ok := m[s+u]
	if !ok {
		return "", errors.New("missing")
	}
	return value, nil
}
func (m memoryKeyring) Set(s, u, p string) error { m[s+u] = p; return nil }

func TestCredentialStorage(t *testing.T) {
	store, _ := Open(t.TempDir())
	store.Keyring = memoryKeyring{}
	c := Config{Username: "user", BaseURL: "https://portal.example", Timeout: 8, Storage: "file"}
	secret := " p&中文\n "
	if err := store.Save(c, secret); err != nil {
		t.Fatal(err)
	}
	saved, err := store.Load()
	if err != nil || saved.Version != 1 {
		t.Fatalf("config=%v err=%v", saved, err)
	}
	if password, err := store.Password(saved); err != nil || password != secret {
		t.Fatalf("password failed: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(store.Dir, "config.json"))
	if strings.Contains(string(data), secret) {
		t.Fatal("config contains password")
	}
	if runtime.GOOS != "windows" {
		for _, name := range []string{"config.json", "password"} {
			info, _ := os.Stat(filepath.Join(store.Dir, name))
			if info.Mode().Perm() != 0600 {
				t.Errorf("%s permissions=%v", name, info.Mode())
			}
		}
	}
	c.Storage = "keyring"
	if err := store.Save(c, secret); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.Dir, "password")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("plaintext password remains after switching to keyring")
	}
	if password, err := store.Password(c); err != nil || password != secret {
		t.Fatalf("keyring failed: %v", err)
	}
	c.BaseURL = "https://other.example"
	if _, err := store.Password(c); err == nil {
		t.Fatal("credential leaked across gateways")
	}
}

func TestLockAndState(t *testing.T) {
	store, _ := Open(t.TempDir())
	unlock, err := store.Lock()
	if err != nil {
		t.Fatal(err)
	}
	if release, err := store.Lock(); err == nil {
		release()
		t.Fatal("second concurrent lock succeeded")
	}
	unlock()
	release, err := store.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := store.WriteJSON("state.json", State{Paused: "verification required"}); err != nil {
		t.Fatal(err)
	}
	state, err := store.ReadState()
	if err != nil || state.Paused != "verification required" {
		t.Fatalf("state=%v err=%v", state, err)
	}
	if err := store.WriteJSON("state.json", State{}); err != nil {
		t.Fatal(err)
	}
	state, err = store.ReadState()
	if err != nil || state.Paused != "" {
		t.Fatalf("state overwrite failed: %v %v", state, err)
	}
}

func TestLogRotationAndInvalidConfig(t *testing.T) {
	store, _ := Open(t.TempDir())
	if err := store.Write("activity.log", []byte(strings.Repeat("a", 256<<10))); err != nil {
		t.Fatal(err)
	}
	if err := store.Log("online"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(store.Dir, "activity.log.1")); err != nil {
		t.Fatal(err)
	}
	data, err := store.Logs()
	if err != nil || len(data) > 200 || !strings.Contains(string(data), "online") {
		t.Fatalf("rotation failed: %v", err)
	}
	if err := store.Write("config.json", []byte(`{"version":99}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("unknown config version accepted")
	}
	if err := store.Write("config.json", []byte(`password-secret-not-json`)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil || strings.Contains(err.Error(), "password-secret") {
		t.Fatalf("invalid config error leaked data: %v", err)
	}
}
