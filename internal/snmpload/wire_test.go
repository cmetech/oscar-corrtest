package snmpload_test

import (
	"context"
	"encoding/asn1"
	"github.com/cmetech/oscar-corrtest/internal/snmpload"
	"net"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestNativeNetSNMPPacketsMatchTypedFixtures(t *testing.T) {
	if _, err := exec.LookPath("snmptrap"); err != nil {
		t.Skip("Net-SNMP is required for actual UDP wire validation")
	}
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_, port, _ := net.SplitHostPort(conn.LocalAddr().String())
	n, _ := strconv.Atoi(port)
	sender, err := snmpload.NewSender("127.0.0.1", n, "wire-test")
	if err != nil {
		t.Fatal(err)
	}
	defer sender.Close()
	fields := func(body []byte) []asn1.RawValue {
		result := []asn1.RawValue{}
		for len(body) > 0 {
			var v asn1.RawValue
			tail, err := asn1.Unmarshal(body, &v)
			if err != nil {
				t.Fatal(err)
			}
			result = append(result, v)
			body = tail
		}
		return result
	}
	for _, family := range snmpload.Families {
		for _, kind := range []string{"problem", "repeat", "clear"} {
			t.Run(family+"/"+kind, func(t *testing.T) {
				e, err := snmpload.Fixture("crt_wire", 7, family)
				if err != nil {
					t.Fatal(err)
				}
				if err := sender.Send(context.Background(), e, kind); err != nil {
					t.Fatal(err)
				}
				conn.SetReadDeadline(time.Now().Add(2 * time.Second))
				data := make([]byte, 65535)
				size, _, err := conn.ReadFrom(data)
				if err != nil {
					t.Fatal(err)
				}
				outer := fields(data[:size])
				if len(outer) != 1 {
					t.Fatal("trailing packet data")
				}
				message := fields(outer[0].Bytes)
				if len(message) != 3 || message[2].Class != 2 || message[2].Tag != 7 {
					t.Fatal("not an SNMPv2 trap")
				}
				if string(message[1].Bytes) != "wire-test" {
					t.Fatal("private community configuration not used")
				}
				pdu := fields(message[2].Bytes)
				bindings := fields(pdu[3].Bytes)
				if len(bindings) != len(e.Varbinds)+2 {
					t.Fatal("wrong varbind count")
				}
				trap := fields(bindings[1].Bytes)
				var trapOID asn1.ObjectIdentifier
				if _, err := asn1.Unmarshal(trap[1].FullBytes, &trapOID); err != nil {
					t.Fatal(err)
				}
				want := e.ProblemOID
				if kind == "clear" {
					want = e.ClearOID
				}
				if trapOID.String() != want {
					t.Fatalf("trap OID %s != %s", trapOID, want)
				}
				for i, v := range e.Varbinds {
					pair := fields(bindings[i+2].Bytes)
					var oid asn1.ObjectIdentifier
					asn1.Unmarshal(pair[0].FullBytes, &oid)
					if oid.String() != v.OID {
						t.Fatalf("varbind OID %s != %s", oid, v.OID)
					}
					if v.Type == "s" {
						if pair[1].Class != 0 || pair[1].Tag != 4 || string(pair[1].Bytes) != v.Value {
							t.Fatal("wrong text varbind")
						}
					} else if v.Type == "u" {
						if pair[1].Class != 1 || pair[1].Tag != 2 {
							t.Fatal("Unsigned32 not encoded as Gauge32")
						}
						value := 0
						for _, b := range pair[1].Bytes {
							value = value*256 + int(b)
						}
						if strconv.Itoa(value) != v.Value {
							t.Fatal("wrong unsigned value")
						}
					} else {
						t.Fatal("unexpected fixture type")
					}
				}
			})
		}
	}
}
