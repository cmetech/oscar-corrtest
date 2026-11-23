package managed

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixtureConfig(t *testing.T) Config {
	t.Helper()
	dir := t.TempDir()
	secret := func(name, value string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	return Config{Listen: "127.0.0.1:19980", ProbeIP: "127.0.0.1", SourceID: "fixture", TID: "NE01", Locator: "fixture", MappingRevision: strings.Repeat("a", 64), TicketPriority: "1", CredentialFile: secret("login", `{"username":"TestDevice","password":"OnlyForTest"}`), SessionCookieFile: secret("cookie", "session=fixture"), MySQLDefaultsFile: secret("client.cnf", "[client]")}
}
func TestManagedPeerUsesOnlyAuthenticatedOriginalSession(t *testing.T) {
	cfg := fixtureConfig(t)
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &Peer{cfg: cfg, listener: listener, ready: make(chan struct{}), done: make(chan struct{})}
	l, err := cfg.login()
	if err != nil {
		t.Fatal(err)
	}
	go p.serve(l)
	defer p.Close()
	pre, err := p.Emit("owned", "LOS", "MN")
	if err == nil || pre.Outcome != "not_entered" {
		t.Fatal(pre)
	}
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	_, err = fmt.Fprint(conn, "ACT-USER:NE01:TestDevice:17::OnlyForTest;")
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	reply, err := reader.ReadString(';')
	if err != nil || !strings.Contains(reply, "M 17 COMPLD") {
		t.Fatal("login")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = p.Await(ctx); err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"MN", "MJ", "CL"} {
		e, err := p.Emit("owned", "LOS", code)
		if err != nil {
			t.Fatal(err)
		}
		wire, err := reader.ReadString(';')
		if err != nil || !strings.Contains(wire, "REPT ALM OC3") || !strings.Contains(wire, "\"owned:"+code+",LOS,SA:") {
			t.Fatal("incorrect TL1 PDU")
		}
		raw, _ := json.Marshal(e)
		if strings.Contains(string(raw), "OnlyForTest") {
			t.Fatal("credential in evidence")
		}
	}
	conn.Close()
	select {
	case <-p.done:
	case <-ctx.Done():
		t.Fatal("session did not close")
	}
	e, err := p.Emit("owned", "LOS", "CL")
	if err == nil || e.Outcome != "not_entered" {
		t.Fatal("lost session reused")
	}
}
func TestMixedOfferCompleteAndBounded(t *testing.T) {
	for _, seconds := range []int{60, 601, 9000} {
		counts := map[int]string{}
		var n int
		for ; n < 50000; n++ {
			e, ok := MixedOffer(n, .2, time.Duration(seconds)*time.Second)
			if !ok {
				break
			}
			if e.At >= time.Duration(seconds)*time.Second {
				t.Fatal("overflow")
			}
			counts[e.Slot] += e.Code
		}
		for _, seq := range counts {
			if seq != "MNMJCL" {
				t.Fatal(seq)
			}
		}
		if n != int(float64(seconds)*.2)/3*3 {
			t.Fatal(n)
		}
	}
}
