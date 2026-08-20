package service

import (
	"fmt"
	"html"
	"strconv"
	"strings"

	"github.com/cmetech/oscar-corrtest/internal/platformpaths"
)

func renderDefinition(goos, executable string, paths platformpaths.Paths) ([]byte, error) {
	switch goos {
	case "linux":
		return []byte(fmt.Sprintf(`[Unit]
Description=OSCAR Correlation Test Harness
After=network-online.target

[Service]
Type=simple
ExecStart=%s serve
WorkingDirectory=%s
Restart=on-failure
RestartSec=3
StandardOutput=append:%s
StandardError=append:%s

[Install]
WantedBy=default.target
`, systemdQuote(executable), systemdQuote(paths.StateDir), systemdQuote(paths.BootstrapLog), systemdQuote(paths.BootstrapLog))), nil
	case "darwin":
		return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>io.cmetech.oscar-corrtest</string>
  <key>ProgramArguments</key><array><string>%s</string><string>serve</string></array>
  <key>WorkingDirectory</key><string>%s</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, html.EscapeString(executable), html.EscapeString(paths.StateDir), html.EscapeString(paths.BootstrapLog), html.EscapeString(paths.BootstrapLog))), nil
	case "windows":
		arguments := fmt.Sprintf(`/d /s /c ""%s" serve >> "%s" 2>&amp;1"`, html.EscapeString(executable), html.EscapeString(paths.BootstrapLog))
		return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <Triggers><LogonTrigger><Enabled>true</Enabled></LogonTrigger></Triggers>
  <Settings><MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy><RestartOnFailure><Interval>PT3S</Interval><Count>3</Count></RestartOnFailure></Settings>
  <Actions Context="Author"><Exec><Command>cmd.exe</Command><Arguments>%s</Arguments><WorkingDirectory>%s</WorkingDirectory></Exec></Actions>
</Task>
`, arguments, html.EscapeString(paths.StateDir))), nil
	default:
		return nil, fmt.Errorf("unsupported service platform %q", goos)
	}
}

func systemdQuote(value string) string {
	quoted := strconv.Quote(strings.ReplaceAll(value, "%", "%%"))
	return quoted
}
