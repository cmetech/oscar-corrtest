package receivers

import (
	"bufio"
	"context"
	"encoding/asn1"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

var probe = Probe{Labels: map[string]string{"alertname": "Campaign-x-rx-trap-1", "campaign_run": "camp_x", "campaign_lane": "receivers",
	"severity": "warning", "instance": "ct-srv-41.home.arpa:9100", "hostname": "ct-srv-41", "meta_hostname": "ct-srv-41.home.arpa",
	"meta_ipaddress": "198.51.100.51"}, StartsAt: time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)}

// TestTrapPacketIsSNMPv2cTrap decodes the packet with encoding/asn1: version
// 1 (v2c), the community, an SNMPv2-Trap-PDU ([2] context 7) whose varbinds
// are sysUpTime.0, snmpTrapOID.0 = TrapOID and then the probe values in
// TrapLabels order.
func TestTrapPacketIsSNMPv2cTrap(t *testing.T) {
	b := TrapPacket("s3cret", 42, 1234, probe)
	var msg struct {
		Version   int
		Community []byte
		PDU       asn1.RawValue
	}
	if rest, err := asn1.Unmarshal(b, &msg); err != nil || len(rest) != 0 {
		t.Fatalf("message: %v rest %d", err, len(rest))
	}
	if msg.Version != 1 || string(msg.Community) != "s3cret" || msg.PDU.Class != asn1.ClassContextSpecific || msg.PDU.Tag != 7 {
		t.Fatalf("header %+v", msg)
	}
	var pdu struct {
		RequestID, ErrStatus, ErrIndex int
		Varbinds                       []struct {
			OID   asn1.ObjectIdentifier
			Value asn1.RawValue
		}
	}
	if _, err := asn1.UnmarshalWithParams(msg.PDU.FullBytes, &pdu, "tag:7"); err != nil {
		t.Fatal(err)
	}
	if pdu.RequestID != 42 || len(pdu.Varbinds) != 2+len(TrapLabels) {
		t.Fatalf("pdu %+v", pdu)
	}
	if pdu.Varbinds[0].OID.String() != "1.3.6.1.2.1.1.3.0" || pdu.Varbinds[0].Value.Class != asn1.ClassApplication || pdu.Varbinds[0].Value.Tag != 3 {
		t.Errorf("uptime %+v", pdu.Varbinds[0])
	}
	var trapOID asn1.ObjectIdentifier
	if _, err := asn1.Unmarshal(pdu.Varbinds[1].Value.FullBytes, &trapOID); err != nil || pdu.Varbinds[1].OID.String() != "1.3.6.1.6.3.1.1.4.1.0" || trapOID.String() != TrapOID {
		t.Errorf("trap oid %v %v", pdu.Varbinds[1].OID, trapOID)
	}
	for i, l := range TrapLabels {
		vb := pdu.Varbinds[2+i]
		if vb.OID.String() != TrapVarbindOID(i+1) || vb.Value.Tag != asn1.TagOctetString || string(vb.Value.Bytes) != probe.Labels[l] {
			t.Errorf("varbind %d (%s): %v %q", i+1, l, vb.OID, vb.Value.Bytes)
		}
	}
}

// TestTrapMappingIsPlain: the campaign trap mapping only puts labels from
// positional varbinds and keeps the probe labels; no eval or conditional.
func TestTrapMappingIsPlain(t *testing.T) {
	els := TrapMapping()
	keep := 0
	for _, e := range els {
		switch {
		case strings.HasPrefix(e.Key, "put~"):
		case strings.HasPrefix(e.Key, "keep~"):
			keep++
		default:
			t.Errorf("non-plain element %+v", e)
		}
		if strings.Contains(e.Key, "eval") || strings.Contains(e.Key, "if~") {
			t.Errorf("expression element %+v", e)
		}
	}
	if keep != 1 || len(els) != len(TrapLabels)+2 { // + node and keep
		t.Errorf("elements %+v", els)
	}
}

func TestSendTrapUDP(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if err := SendTrap(context.Background(), pc.LocalAddr().String(), "c", probe); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2048)
	_ = pc.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, err := pc.ReadFrom(buf)
	if err != nil || n < 50 || buf[0] != 0x30 {
		t.Fatalf("read %d %v", n, err)
	}
}

func TestSendWebhookProfileBody(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/webhookreceiver/webhook/ct-rx-x" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request %s %s", r.Method, r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = io.WriteString(w, `{"status":"ok"}`)
	}))
	defer srv.Close()
	clear := probe
	clear.Clear = true
	if err := SendWebhook(context.Background(), srv.Client(), srv.URL+"/webhookreceiver/webhook/ct-rx-x", clear, time.Date(2026, 11, 25, 12, 1, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	labels := got["labels"].(map[string]any)
	if labels["meta_ipaddress"] != "198.51.100.51" || got["startsAt"] != "2026-11-25T12:00:00Z" || got["endsAt"] != "2026-11-25T12:01:00Z" {
		t.Errorf("body %v", got)
	}
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, `{"status":"acl_denied"}`) })
	if SendWebhook(context.Background(), srv.Client(), srv.URL+"/x", probe, time.Now()) == nil {
		t.Error("acl_denied must be an error")
	}
}

// TestSendSMTPKeyValues: the subject and body carry every label as key=value
// (the SMTP receiver's default handler), each message with its own Message-ID.
func TestSendSMTPKeyValues(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	data := make(chan string, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r, w := bufio.NewReader(c), func(s string) { _, _ = io.WriteString(c, s+"\r\n") }
		w("220 test")
		var msg strings.Builder
		inData := false
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			switch {
			case inData && line == ".\r\n":
				inData = false
				data <- msg.String()
				w("250 ok")
			case inData:
				msg.WriteString(line)
			case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
				w("250 test")
			case strings.HasPrefix(line, "DATA"):
				inData = true
				w("354 go")
			case strings.HasPrefix(line, "QUIT"):
				w("221 bye")
				return
			default:
				w("250 ok")
			}
		}
	}()
	if err := SendSMTP(context.Background(), ln.Addr().String(), probe); err != nil {
		t.Fatal(err)
	}
	m := <-data
	for _, want := range []string{"From: " + SMTPFrom, "To: " + SMTPTo, "Message-ID: <", "Subject: alertname=Campaign-x-rx-trap-1 ", "meta_ipaddress=198.51.100.51", "severity=warning"} {
		if !strings.Contains(m, want) {
			t.Errorf("message lacks %q:\n%s", want, m)
		}
	}
}
