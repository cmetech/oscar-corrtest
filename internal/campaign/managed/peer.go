// Package managed drives an isolated generic-tl1-example@1 device socket.
// The independent collector owns its journal, source timestamps and episode IDs.
package managed

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Config struct {
	Listen            string
	ProbeIP           string
	TID               string
	SourceID          string
	Locator           string
	MappingRevision   string
	CredentialFile    string
	MySQLDefaultsFile string
	SessionCookieFile string
	// Exact values expected at the test ticket provider for this site's handler.
	TicketPriority string
}

var token = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}$")
var credential = regexp.MustCompile("^[A-Za-z0-9_.!@%+#$?-]{8,128}$")

func privateFile(path string) ([]byte, error) {
	meta, err := os.Lstat(path)
	if err != nil || !meta.Mode().IsRegular() {
		return nil, errors.New("protected campaign file must be regular")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("protected campaign file unavailable")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !os.SameFile(meta, st) || !st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0 || st.Size() > 16384 {
		return nil, errors.New("protected campaign file must be a private regular file of at most 16 KiB")
	}
	return io.ReadAll(io.LimitReader(f, 16385))
}
func (c Config) Validate() error {
	addr, err := netip.ParseAddrPort(c.Listen)
	remote, rerr := netip.ParseAddr(c.ProbeIP)
	private := func(ip netip.Addr) bool { return ip.IsPrivate() || ip.IsLoopback() }
	if err != nil || rerr != nil || addr.Port() == 0 || !private(addr.Addr()) || !private(remote) || !token.MatchString(c.TID) || !token.MatchString(c.SourceID) || !token.MatchString(c.Locator) || !regexp.MustCompile("^[a-f0-9]{64}$").MatchString(c.MappingRevision) {
		return errors.New("TL1 requires explicit private listener, probe IP, TID and source ID")
	}
	if _, err := c.login(); err != nil {
		return err
	}
	if _, err := privateFile(c.MySQLDefaultsFile); err != nil {
		return err
	}
	if _, err := c.Cookie(); err != nil {
		return err
	}
	if !token.MatchString(c.TicketPriority) {
		return errors.New("expected mapped test ticket priority required")
	}
	return nil
}

type login struct{ Username, Password string }

func (c Config) login() (login, error) {
	raw, err := privateFile(c.CredentialFile)
	var l login
	if err != nil {
		return l, err
	}
	if json.Unmarshal(raw, &l) != nil || !credential.MatchString(l.Username) || !credential.MatchString(l.Password) {
		return l, errors.New("invalid private TL1 test credential")
	}
	return l, nil
}
func (c Config) Cookie() (string, error) {
	b, err := privateFile(c.SessionCookieFile)
	if err != nil {
		return "", err
	}
	s := strings.TrimSpace(string(b))
	if s == "" || len(s) > 16000 || strings.ContainsAny(s, "\r\n\x00") || !strings.Contains(s, "=") {
		return "", errors.New("invalid test operator session cookie")
	}
	return s, nil
}

// Peer serves exactly one authenticated probe on a single run. A lost session
// cannot be replaced: the collector may have lost clear authority across the gap.
type Peer struct {
	cfg           Config
	listener      net.Listener
	done          chan struct{}
	ready         chan struct{}
	closeOnce     sync.Once
	closing       bool
	mu            sync.Mutex
	conn          net.Conn
	sequence      uint64
	authenticated bool
}

func Open(c Config) (*Peer, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	l, err := c.login()
	if err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return nil, err
	}
	p := &Peer{cfg: c, listener: listener, done: make(chan struct{}), ready: make(chan struct{})}
	go p.serve(l)
	return p, nil
}
func (p *Peer) Close() {
	p.closeOnce.Do(func() {
		p.listener.Close()
		p.mu.Lock()
		p.closing = true
		if p.conn != nil {
			p.conn.Close()
		}
		p.mu.Unlock()
		<-p.done
	})
}
func (p *Peer) Await(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.done:
		return errors.New("TL1 peer closed before login")
	case <-p.ready:
		return nil
	}
}
func same(a, b string) bool {
	x, y := sha256.Sum256([]byte(a)), sha256.Sum256([]byte(b))
	return subtle.ConstantTimeCompare(x[:], y[:]) == 1
}
func (p *Peer) serve(l login) {
	defer close(p.done)
	for {
		conn, err := p.listener.Accept()
		if err != nil {
			return
		}
		if conn.RemoteAddr().(*net.TCPAddr).AddrPort().Addr().Unmap().String() != p.cfg.ProbeIP {
			conn.Close()
			continue
		}
		p.mu.Lock()
		if p.closing {
			p.mu.Unlock()
			conn.Close()
			return
		}
		p.conn = conn
		p.mu.Unlock()
		// The probe uses plain TCP for this fixture, with no Telnet negotiation.
		conn.SetReadDeadline(time.Now().Add(20 * time.Second))
		r := bufio.NewReaderSize(conn, 4096)
		logged := false
		for {
			line, err := r.ReadSlice(';')
			if err != nil {
				break
			}
			fields := strings.Split(strings.TrimSpace(string(line[:len(line)-1])), ":")
			if len(fields) < 4 || !token.MatchString(fields[3]) || fields[1] != p.cfg.TID {
				break
			}
			ok := false
			switch fields[0] {
			case "ACT-USER":
				ok = !logged && len(fields) == 6 && fields[4] == "" && same(fields[2], l.Username) && same(fields[5], l.Password)
			case "RTRV-HDR":
				ok = len(fields) == 4 && fields[2] == ""
			case "CANC-USER":
				ok = logged && len(fields) == 4 && same(fields[2], l.Username)
			}
			status := "DENY"
			if ok {
				status = "COMPLD"
			}
			p.mu.Lock()
			err = p.writeLocked(fmt.Sprintf("\r\n%s %s\r\nM %s %s\r\n;\r\n", p.cfg.TID, time.Now().UTC().Format("06-01-02 15:04:05"), fields[3], status))
			if ok && err == nil && fields[0] == "ACT-USER" {
				logged = true
				p.authenticated = true
				close(p.ready)
				conn.SetReadDeadline(time.Time{})
			}
			p.mu.Unlock()
			if !ok || err != nil || fields[0] == "CANC-USER" {
				break
			}
		}
		conn.Close()
		p.mu.Lock()
		p.authenticated = false
		p.conn = nil
		p.mu.Unlock()
		if logged {
			return
		}
	}
}
func (p *Peer) writeLocked(s string) error {
	if err := p.conn.SetWriteDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return err
	}
	_, err := io.WriteString(p.conn, s)
	return err
}

type WireEvent struct {
	FinishedAt           time.Time
	At                   time.Time
	AID, Condition, Code string
	Sequence             uint64
	Outcome              string
}

func (p *Peer) Emit(aid, condition, code string) (WireEvent, error) {
	e := WireEvent{At: time.Now().UTC(), AID: aid, Condition: condition, Code: code, Outcome: "not_entered"}
	if !token.MatchString(aid) || !token.MatchString(condition) || (code != "MN" && code != "MJ" && code != "CL") {
		return e, errors.New("invalid managed event")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.authenticated {
		return e, errors.New("original TL1 probe session unavailable")
	}
	p.sequence++
	e.Sequence = p.sequence
	err := p.writeLocked(fmt.Sprintf("\r\n%s %s\r\nA %d REPT ALM OC3\r\n\"%s:%s,%s,SA:Campaign %s %s\"\r\n;\r\n", p.cfg.TID, e.At.Format("06-01-02 15:04:05"), e.Sequence, aid, code, condition, aid, condition))
	if err != nil {
		e.Outcome = "unknown"
		p.authenticated = false
		p.conn.Close()
		return e, errors.New("TL1 write outcome unknown")
	}
	e.FinishedAt = time.Now().UTC()
	e.Outcome = "written"
	return e, nil
}
