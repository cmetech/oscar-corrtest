package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type manager struct{ options Options }

// NewManager selects a platform adapter without invoking it.
func NewManager(options Options) (Manager, error) {
	if options.GOOS != "linux" && options.GOOS != "darwin" && options.GOOS != "windows" {
		return nil, fmt.Errorf("unsupported service platform %q", options.GOOS)
	}
	if !filepath.IsAbs(options.Executable) || options.Paths.ServiceDefinition == "" || options.Runner == nil {
		return nil, fmt.Errorf("service executable, definition path, and runner are required")
	}
	if options.Stdout == nil {
		options.Stdout = io.Discard
	}
	if options.Stderr == nil {
		options.Stderr = io.Discard
	}
	return &manager{options: options}, nil
}

func (m *manager) DefinitionPath() string { return m.options.Paths.ServiceDefinition }

func (m *manager) Install(ctx context.Context) error {
	doc, err := renderDefinition(m.options.GOOS, m.options.Executable, m.options.Paths)
	if err != nil {
		return err
	}
	if err := atomicWrite(m.DefinitionPath(), doc); err != nil {
		return fmt.Errorf("write user service definition: %w", err)
	}
	switch m.options.GOOS {
	case "linux":
		if _, err := m.options.Runner.Run(ctx, "systemctl", "--user", "daemon-reload"); err != nil {
			return fmt.Errorf("reload user service manager: %w", err)
		}
		if _, err := m.options.Runner.Run(ctx, "systemctl", "--user", "enable", "oscar-corrtest.service"); err != nil {
			return fmt.Errorf("enable user service: %w", err)
		}
	case "windows":
		if _, err := m.options.Runner.Run(ctx, "schtasks.exe", "/Create", "/TN", "OSCAR CorrTest", "/XML", m.DefinitionPath(), "/F"); err != nil {
			return fmt.Errorf("register user task: %w", err)
		}
	}
	return nil
}

func (m *manager) Start(ctx context.Context) error {
	status, err := m.Status(ctx)
	if err == nil && status.State == StateRunning {
		return nil
	}
	switch m.options.GOOS {
	case "linux":
		_, err = m.options.Runner.Run(ctx, "systemctl", "--user", "start", "oscar-corrtest.service")
	case "darwin":
		domain := "gui/" + currentUserID()
		_, bootstrapErr := m.options.Runner.Run(ctx, "launchctl", "bootstrap", domain, m.DefinitionPath())
		if bootstrapErr != nil && !isAlreadyLoaded(bootstrapErr.Error()) {
			return fmt.Errorf("load launch agent: %w", bootstrapErr)
		}
		_, err = m.options.Runner.Run(ctx, "launchctl", "kickstart", "-k", domain+"/io.cmetech.oscar-corrtest")
	case "windows":
		_, err = m.options.Runner.Run(ctx, "schtasks.exe", "/Run", "/TN", "OSCAR CorrTest")
	}
	if err != nil {
		return fmt.Errorf("start user service: %w", err)
	}
	return nil
}

func (m *manager) Stop(ctx context.Context) error {
	status, err := m.Status(ctx)
	if err == nil && (status.State == StateStopped || status.State == StateNotInstalled) {
		return nil
	}
	switch m.options.GOOS {
	case "linux":
		_, err = m.options.Runner.Run(ctx, "systemctl", "--user", "stop", "oscar-corrtest.service")
	case "darwin":
		_, err = m.options.Runner.Run(ctx, "launchctl", "kill", "SIGTERM", "gui/"+currentUserID()+"/io.cmetech.oscar-corrtest")
	case "windows":
		_, err = m.options.Runner.Run(ctx, "schtasks.exe", "/End", "/TN", "OSCAR CorrTest")
	}
	if err != nil && !isNotFound(err.Error()) {
		return fmt.Errorf("stop user service: %w", err)
	}
	return nil
}

func (m *manager) Restart(ctx context.Context) error {
	if m.options.GOOS == "linux" {
		if _, err := m.options.Runner.Run(ctx, "systemctl", "--user", "restart", "oscar-corrtest.service"); err != nil {
			return fmt.Errorf("restart user service: %w", err)
		}
		return nil
	}
	if err := m.Stop(ctx); err != nil {
		return err
	}
	return m.Start(ctx)
}

func (m *manager) Status(ctx context.Context) (Status, error) {
	mechanism := map[string]string{"linux": "systemd-user", "darwin": "launchd-user", "windows": "task-scheduler-user"}[m.options.GOOS]
	if _, err := os.Stat(m.DefinitionPath()); errors.Is(err, os.ErrNotExist) {
		return Status{State: StateNotInstalled, Mechanism: mechanism}, nil
	} else if err != nil {
		return Status{State: StateUnknown, Mechanism: mechanism}, fmt.Errorf("inspect service definition: %w", err)
	}
	var output []byte
	var err error
	switch m.options.GOOS {
	case "linux":
		output, err = m.options.Runner.Run(ctx, "systemctl", "--user", "show", "oscar-corrtest.service", "--property=ActiveState,MainPID", "--no-pager")
	case "darwin":
		output, err = m.options.Runner.Run(ctx, "launchctl", "print", "gui/"+currentUserID()+"/io.cmetech.oscar-corrtest")
	case "windows":
		output, err = m.options.Runner.Run(ctx, "schtasks.exe", "/Query", "/TN", "OSCAR CorrTest", "/FO", "LIST", "/V")
	}
	if err != nil {
		if isNotFound(string(output) + " " + err.Error()) {
			return Status{State: StateStopped, Mechanism: mechanism}, nil
		}
		return Status{State: StateUnknown, Mechanism: mechanism}, fmt.Errorf("query user service: %w", err)
	}
	return parseStatus(m.options.GOOS, mechanism, string(output)), nil
}

func (m *manager) Logs(ctx context.Context, lines int, follow bool) error {
	if lines < 1 || lines > 5000 {
		return fmt.Errorf("log line count must be between 1 and 5000")
	}
	path := m.options.Paths.ApplicationLog
	position, err := writeTail(path, m.options.Stdout, lines)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if !follow {
		return nil
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			file, openErr := os.Open(path) // #nosec G304 -- path is the resolved application log.
			if openErr != nil {
				continue
			}
			info, statErr := file.Stat()
			if statErr == nil && info.Size() < position {
				position = 0
			}
			_, _ = file.Seek(position, io.SeekStart)
			written, _ := io.Copy(m.options.Stdout, file)
			position += written
			_ = file.Close()
		}
	}
}

func (m *manager) Uninstall(ctx context.Context) error {
	_ = m.Stop(ctx)
	var err error
	switch m.options.GOOS {
	case "linux":
		_, err = m.options.Runner.Run(ctx, "systemctl", "--user", "disable", "oscar-corrtest.service")
	case "darwin":
		_, err = m.options.Runner.Run(ctx, "launchctl", "bootout", "gui/"+currentUserID(), m.DefinitionPath())
	case "windows":
		_, err = m.options.Runner.Run(ctx, "schtasks.exe", "/Delete", "/TN", "OSCAR CorrTest", "/F")
	}
	if err != nil && !isNotFound(err.Error()) {
		return fmt.Errorf("unregister user service: %w", err)
	}
	if removeErr := os.Remove(m.DefinitionPath()); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
		return fmt.Errorf("remove user service definition: %w", removeErr)
	}
	if m.options.GOOS == "linux" {
		_, _ = m.options.Runner.Run(ctx, "systemctl", "--user", "daemon-reload")
	}
	return nil
}

func parseStatus(goos, mechanism, output string) Status {
	lower := strings.ToLower(output)
	result := Status{State: StateStopped, Mechanism: mechanism}
	switch goos {
	case "linux":
		if strings.Contains(lower, "activestate=active") {
			result.State = StateRunning
		} else if strings.Contains(lower, "activestate=activating") {
			result.State = StateStarting
		} else if strings.Contains(lower, "activestate=failed") {
			result.State = StateFailed
		}
		for _, line := range strings.Split(output, "\n") {
			if value, ok := strings.CutPrefix(line, "MainPID="); ok {
				result.PID, _ = strconv.Atoi(strings.TrimSpace(value))
			}
		}
	case "darwin":
		if strings.Contains(lower, "state = running") || strings.Contains(lower, "pid =") {
			result.State = StateRunning
		}
	case "windows":
		if strings.Contains(lower, "running") {
			result.State = StateRunning
		}
	}
	return result
}

func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".service-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func writeTail(path string, output io.Writer, count int) (int64, error) {
	file, err := os.Open(path) // #nosec G304 -- path is the resolved application log.
	if err != nil {
		return 0, err
	}
	defer file.Close()
	var values []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		values = append(values, scanner.Text())
		if len(values) > count {
			values = values[1:]
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, err
	}
	for _, value := range values {
		_, _ = fmt.Fprintln(output, value)
	}
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func isNotFound(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "not found") || strings.Contains(lower, "could not be found") || strings.Contains(lower, "no such process") || strings.Contains(lower, "does not exist")
}

func isAlreadyLoaded(value string) bool {
	lower := strings.ToLower(value)
	return strings.Contains(lower, "already loaded") || strings.Contains(lower, "service already loaded")
}
