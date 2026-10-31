package snmpload

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

type Sender struct{ directory, destination, executable string }

func NewSender(host string, port int, community string) (*Sender, error) {
	if host == "" || port < 1 || port > 65535 {
		return nil, fmt.Errorf("invalid trap destination")
	}
	if community == "" || len(community) > 128 || strings.ContainsAny(community, "#\"'\\") || strings.IndexFunc(community, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return nil, fmt.Errorf("SNMP community is unavailable or cannot be represented safely in Net-SNMP configuration")
	}
	executable, err := exec.LookPath("snmptrap")
	if err != nil {
		return nil, fmt.Errorf("snmptrap executable is required on the CorrTest host")
	}
	directory, err := os.MkdirTemp("", "corrtest-snmp-load-")
	if err != nil {
		return nil, err
	}
	if err = os.WriteFile(filepath.Join(directory, "snmp.conf"), []byte("defCommunity "+community+"\n"), 0600); err != nil {
		os.RemoveAll(directory)
		return nil, fmt.Errorf("write private SNMP credential: %w", err)
	}
	destination := "udp:" + net.JoinHostPort(host, strconv.Itoa(port))
	if strings.Contains(host, ":") {
		destination = "udp6:" + net.JoinHostPort(host, strconv.Itoa(port))
	}
	return &Sender{directory: directory, destination: destination, executable: executable}, nil
}
func (s *Sender) Close() error { return os.RemoveAll(s.directory) }
func (s *Sender) Send(ctx context.Context, e Episode, kind string) error {
	oid := e.ProblemOID
	if kind == "clear" {
		oid = e.ClearOID
	} else if kind != "problem" && kind != "repeat" {
		return fmt.Errorf("invalid trap transition")
	}
	args := []string{"-m", "", "-v", "2c", s.destination, "", oid}
	for _, v := range e.Varbinds {
		args = append(args, v.OID, v.Type, v.Value)
	}
	sendCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(sendCtx, s.executable, args...)
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "SNMPCONFPATH=") && !strings.HasPrefix(v, "MIBS=") && !strings.HasPrefix(v, "CORRTEST_SNMP_COMMUNITY=") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "SNMPCONFPATH="+s.directory, "MIBS=")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SNMP local send failed (%T); receiver outcome unknown", err)
	}
	return nil
}
