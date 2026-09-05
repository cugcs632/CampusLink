// Package autostart manages user-scoped native schedulers, without credentials
// in task definitions. It installs a stable private copy of the executable.
package autostart

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cugcs632/CampusLink/internal/profile"
)

const label = "io.github.campuslink.login"

type Manager struct {
	OS, Home, UserID string
	Store            *profile.Store
	Run              func(string, ...string) (string, error)
}

func New(store *profile.Store) (*Manager, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	u, err := user.Current()
	if err != nil {
		return nil, err
	}
	return &Manager{OS: runtime.GOOS, Home: home, UserID: u.Uid, Store: store, Run: run}, nil
}

func run(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s failed: %w; %s", name, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func (m *Manager) Executable() string {
	name := "campuslink"
	if m.OS == "windows" {
		name += ".exe"
	}
	return filepath.Join(m.Store.Dir, "bin", name)
}

func (m *Manager) taskPath() string {
	switch m.OS {
	case "windows":
		return filepath.Join(m.Store.Dir, "task.xml")
	case "darwin":
		return filepath.Join(m.Home, "Library", "LaunchAgents", label+".plist")
	default:
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(m.Home, ".config")
		}
		return filepath.Join(base, "systemd", "user", "campuslink-login.service")
	}
}

func (m *Manager) supported() error {
	switch m.OS {
	case "windows", "darwin":
		return nil
	case "linux":
		if _, err := m.Run("systemctl", "--user", "show-environment"); err != nil {
			return errors.New("a running systemd user session is required for autostart; manual login remains available")
		}
		return nil
	default:
		return errors.New("autostart is supported on Windows, macOS and Linux with systemd")
	}
}

func (m *Manager) Enable(source string) error {
	if err := m.supported(); err != nil {
		return err
	}
	if err := m.Store.Ensure(); err != nil {
		return err
	}
	// Stop the old definition before replacing an executable that may be running.
	if _, err := os.Stat(m.taskPath()); err == nil {
		if err := m.Disable(); err != nil {
			return err
		}
	}
	if err := install(source, m.Executable()); err != nil {
		return err
	}
	definition, timer := m.Definition()
	if err := writeFile(m.taskPath(), []byte(definition), 0600); err != nil {
		return err
	}
	switch m.OS {
	case "windows":
		_, err := m.Run("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; Register-ScheduledTask -TaskName 'CampusLink' -Xml ([IO.File]::ReadAllText("+psQuote(m.taskPath())+")) -Force | Out-Null")
		return err
	case "darwin":
		_, err := m.Run("launchctl", "bootstrap", "gui/"+m.UserID, m.taskPath())
		return err
	default:
		if err := writeFile(strings.TrimSuffix(m.taskPath(), ".service")+".timer", []byte(timer), 0600); err != nil {
			return err
		}
		if _, err := m.Run("systemctl", "--user", "daemon-reload"); err != nil {
			return err
		}
		_, err := m.Run("systemctl", "--user", "enable", "--now", "campuslink-login.timer")
		return err
	}
}

func (m *Manager) Disable() error {
	if _, err := os.Stat(m.taskPath()); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	switch m.OS {
	case "windows":
		_, err := m.Run("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; $t=Get-ScheduledTask -TaskName 'CampusLink' -ErrorAction SilentlyContinue; if ($t) { Stop-ScheduledTask -InputObject $t; Unregister-ScheduledTask -InputObject $t -Confirm:$false }")
		if err != nil {
			return err
		}
	case "darwin":
		if _, err := m.Run("launchctl", "print", "gui/"+m.UserID+"/"+label); err == nil {
			if _, err := m.Run("launchctl", "bootout", "gui/"+m.UserID+"/"+label); err != nil {
				return err
			}
		}
	case "linux":
		if err := m.supported(); err != nil {
			return err
		}
		if _, err := m.Run("systemctl", "--user", "disable", "--now", "campuslink-login.timer"); err != nil {
			return err
		}
		if _, err := m.Run("systemctl", "--user", "stop", "campuslink-login.service"); err != nil {
			return err
		}
		if err := os.Remove(strings.TrimSuffix(m.taskPath(), ".service") + ".timer"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	default:
		return errors.New("unsupported autostart platform")
	}
	if err := os.Remove(m.taskPath()); err != nil {
		return err
	}
	if m.OS == "linux" {
		_, err := m.Run("systemctl", "--user", "daemon-reload")
		return err
	}
	return nil
}

func (m *Manager) Status() (string, error) {
	if _, err := os.Stat(m.taskPath()); errors.Is(err, os.ErrNotExist) {
		return "disabled", nil
	} else if err != nil {
		return "unknown", err
	}
	var err error
	switch m.OS {
	case "windows":
		var result string
		result, err = m.Run("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", "$ErrorActionPreference='Stop'; (Get-ScheduledTask -TaskName 'CampusLink').State.ToString()")
		if err == nil && result == "Disabled" {
			return "disabled", nil
		}
	case "darwin":
		_, err = m.Run("launchctl", "print", "gui/"+m.UserID+"/"+label)
	case "linux":
		_, err = m.Run("systemctl", "--user", "is-active", "campuslink-login.timer")
	default:
		return "unsupported", nil
	}
	if err != nil {
		return "not running", err
	}
	return "enabled", nil
}

func (m *Manager) Definition() (string, string) {
	exe, dir := m.Executable(), m.Store.Dir
	switch m.OS {
	case "windows":
		command := "-NoProfile -NonInteractive -WindowStyle Hidden -Command \"$ErrorActionPreference='Stop'; & " + psQuote(exe) + " --config-dir " + psQuote(dir) + " background; exit $LASTEXITCODE\""
		return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers>
    <LogonTrigger><Enabled>true</Enabled><UserId>%s</UserId><Delay>PT10S</Delay></LogonTrigger>
    <TimeTrigger><Repetition><Interval>PT5M</Interval><StopAtDurationEnd>false</StopAtDurationEnd></Repetition><StartBoundary>%s</StartBoundary><Enabled>true</Enabled></TimeTrigger>
    <EventTrigger><Enabled>true</Enabled><Subscription>%s</Subscription><Delay>PT10S</Delay></EventTrigger>
  </Triggers>
  <Principals><Principal id="User"><UserId>%s</UserId><LogonType>InteractiveToken</LogonType><RunLevel>LeastPrivilege</RunLevel></Principal></Principals>
  <Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries><StopIfGoingOnBatteries>false</StopIfGoingOnBatteries><StartWhenAvailable>true</StartWhenAvailable><Enabled>true</Enabled><WakeToRun>false</WakeToRun><ExecutionTimeLimit>PT2M</ExecutionTimeLimit></Settings>
  <Actions Context="User"><Exec><Command>powershell.exe</Command><Arguments>%s</Arguments></Exec></Actions>
</Task>
`, escape(m.UserID), time.Now().Add(15*time.Second).Format("2006-01-02T15:04:05"), escape(`<QueryList><Query Id="0" Path="Microsoft-Windows-NetworkProfile/Operational"><Select Path="Microsoft-Windows-NetworkProfile/Operational">*[System[EventID=10000]]</Select></Query></QueryList>`), escape(m.UserID), escape(command)), ""
	case "darwin":
		return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>%s</string>
<key>ProgramArguments</key><array><string>%s</string><string>--config-dir</string><string>%s</string><string>background</string></array>
<key>RunAtLoad</key><true/><key>StartInterval</key><integer>300</integer>
<key>ProcessType</key><string>Background</string>
</dict></plist>
`, label, escape(exe), escape(dir)), ""
	default:
		return fmt.Sprintf("[Unit]\nDescription=CampusLink automatic login\n\n[Service]\nType=oneshot\nExecStart=%s --config-dir %s background\nTimeoutStartSec=120\nUMask=0077\n", unitQuote(exe), unitQuote(dir)), "[Unit]\nDescription=CampusLink periodic login\n\n[Timer]\nOnStartupSec=10s\nOnUnitInactiveSec=5min\nAccuracySec=10s\nUnit=campuslink-login.service\n\n[Install]\nWantedBy=timers.target\n"
	}
}

func escape(value string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}
func psQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
func unitQuote(value string) string {
	value = strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "%", "%%", "$", "$$", "\n", "\\n", "\r", "\\r", "\t", "\\t").Replace(value)
	return "\"" + value + "\""
}

func install(source, target string) error {
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	if source == target {
		return nil
	}
	f, err := os.Open(source)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	out, err := os.CreateTemp(filepath.Dir(target), ".install-*")
	if err != nil {
		return err
	}
	defer os.Remove(out.Name())
	if _, err := io.Copy(out, f); err != nil {
		out.Close()
		return err
	}
	if err := out.Chmod(0700); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(out.Name(), target)
}

func writeFile(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	return os.WriteFile(path, data, mode)
}
