// Package profile stores per-user settings, credentials and operational state.
package profile

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/zalando/go-keyring"
)

const maxFileBytes = 1 << 20

type Config struct {
	Version  int    `json:"version"`
	Username string `json:"username"`
	BaseURL  string `json:"base_url"`
	IP       string `json:"ip,omitempty"`
	NASID    string `json:"nas_id,omitempty"`
	ISP      string `json:"isp,omitempty"`
	Timeout  int    `json:"timeout_seconds"`
	Storage  string `json:"password_storage"`
}

type State struct {
	LastAttempt time.Time `json:"last_attempt,omitempty"`
	LastSuccess time.Time `json:"last_success,omitempty"`
	Paused      string    `json:"paused,omitempty"`
}

type Keyring interface {
	Get(string, string) (string, error)
	Set(string, string, string) error
}

type SystemKeyring struct{}

func (SystemKeyring) Get(service, user string) (string, error) { return keyring.Get(service, user) }
func (SystemKeyring) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

type Store struct {
	Dir     string
	Keyring Keyring
}

func Open(dir string) (*Store, error) {
	if dir == "" {
		base, err := os.UserConfigDir()
		if err != nil {
			return nil, err
		}
		dir = filepath.Join(base, "CampusLink")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	return &Store{Dir: dir, Keyring: SystemKeyring{}}, nil
}

func (s *Store) Ensure() error {
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return err
	}
	return restrictDir(s.Dir)
}

func (s *Store) Load() (Config, error) {
	var c Config
	err := s.readJSON("config.json", &c)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("read saved configuration: %w", err)
	}
	if c.Version != 1 || c.Username == "" || (c.Storage != "keyring" && c.Storage != "file") || c.Timeout < 1 || c.Timeout > 3600 {
		return c, errors.New("invalid saved configuration; run campuslink setup")
	}
	return c, nil
}

func (s *Store) service(c Config) string {
	sum := sha256.Sum256([]byte(s.Dir + "\x00" + c.BaseURL))
	return "io.github.campuslink." + hex.EncodeToString(sum[:16])
}

func (s *Store) Password(c Config) (string, error) {
	switch c.Storage {
	case "keyring":
		value, err := s.Keyring.Get(s.service(c), c.Username)
		if err != nil {
			return "", errors.New("cannot read saved password from system credential store; unlock it or run campuslink setup")
		}
		return value, nil
	case "file":
		data, err := s.read("password")
		if err != nil {
			return "", errors.New("cannot read saved password file; run campuslink setup")
		}
		return string(data), nil
	default:
		return "", errors.New("no saved password; run campuslink setup")
	}
}

func (s *Store) Save(c Config, password string) error {
	if password == "" || c.Username == "" {
		return errors.New("username and password are required")
	}
	if err := s.Ensure(); err != nil {
		return err
	}
	switch c.Storage {
	case "keyring":
		if err := s.Keyring.Set(s.service(c), c.Username, password); err != nil {
			return errors.New("cannot save password in system credential store; unlock it, or explicitly choose --storage file")
		}
	case "file":
		if err := s.Write("password", []byte(password)); err != nil {
			return err
		}
	default:
		return errors.New("storage must be keyring or file")
	}
	c.Version = 1
	if err := s.WriteJSON("config.json", c); err != nil {
		return err
	}
	if c.Storage == "keyring" {
		if err := os.Remove(filepath.Join(s.Dir, "password")); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func (s *Store) ReadState() (State, error) {
	var state State
	err := s.readJSON("state.json", &state)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return state, err
}

func (s *Store) WriteJSON(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return s.Write(name, append(data, '\n'))
}

// Write uses a private temporary file and rename so interrupted writes do not
// truncate the last usable configuration. Callers pass fixed file names only.
func (s *Store) Write(name string, data []byte) error {
	if err := s.Ensure(); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.Dir, ".write-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(s.Dir, name))
}

func (s *Store) read(name string) ([]byte, error) {
	f, err := os.Open(filepath.Join(s.Dir, name))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if len(data) > maxFileBytes {
		return nil, errors.New("saved file exceeds size limit")
	}
	return data, err
}

func (s *Store) readJSON(name string, value any) error {
	data, err := s.read(name)
	if err != nil {
		return err
	}
	if json.Unmarshal(data, value) != nil {
		return errors.New("invalid JSON in " + name)
	}
	return nil
}

func (s *Store) Log(message string) error {
	if err := s.Ensure(); err != nil {
		return err
	}
	path := filepath.Join(s.Dir, "activity.log")
	if info, err := os.Stat(path); err == nil && info.Size() >= 256<<10 {
		backup := path + ".1"
		if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(path, backup); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339), message)
	return err
}

func (s *Store) Logs() ([]byte, error) {
	data, err := s.read("activity.log")
	if errors.Is(err, os.ErrNotExist) {
		return []byte("No activity recorded yet.\n"), nil
	}
	return data, err
}
