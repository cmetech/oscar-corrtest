// Package receivers sends campaign probes through OSCAR's webhook, SNMP trap
// and SMTP receivers (the receivers lane of alerting-day-3ps@v3): every
// other campaign alert enters through the Alertmanager v2 ingest.
package receivers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/smtp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Receivers in the order the lane rotates them.
var Names = []string{"webhook", "trap", "smtp"}

// Lane is the probes' campaign_lane label.
const Lane = "receivers"

// Target endpoints: the webhook profile route behind the reverse proxy, the
// trap receiver's standard port (host network) and the SMTP receiver's
// plaintext legacy port (host network; 2525 is TLS).
const (
	WebhookPath = "/webhookreceiver/webhook/"
	TrapPort    = "162"
	SMTPPort    = "2526"
)

// Mapping namespaces the lane installs into: webhook profiles, and the trap
// receiver's TRAPRECEIVER_NAMESPACE (OSCAR matches names case-insensitively).
const (
	ProfileNamespace = "webhook_profiles"
	TrapNamespace    = "EXT_TRAP_ALERTS"
)

// Probe is one receiver probe. Labels are the alert's labels; Clear sends
// the same labels as a clear (severity "clear" for the trap and SMTP, whose
// receivers always post firing alerts and OSCAR closes the History row on a
// clear severity; endsAt now for the webhook profile).
type Probe struct {
	Labels   map[string]string
	StartsAt time.Time
	Clear    bool
}

// ClearSeverity is the severity OSCAR treats as a clear (alert_recovery.py).
const ClearSeverity = "clear"

func (p Probe) labels() map[string]string {
	out := make(map[string]string, len(p.Labels))
	for k, v := range p.Labels {
		out[k] = v
	}
	if p.Clear {
		out["severity"] = ClearSeverity
	}
	return out
}

// --- SNMP v2c trap ---------------------------------------------------------

// TrapOID is the campaign probe trap: under 32473, the IANA enterprise number
// reserved for documentation (RFC 5612), so it names no real device.
const TrapOID = "1.3.6.1.4.1.32473.7.1"

// TrapEnterpriseOID is TrapOID's enterprise (the receiver's stage 1 mapping).
const TrapEnterpriseOID = "1.3.6.1.4.1.32473"

// TrapLabels are the probe labels a trap carries, as varbinds 1..n.
var TrapLabels = []string{"alertname", "campaign_run", "campaign_lane", "severity", "instance", "hostname", "meta_hostname", "meta_ipaddress"}

// TrapVarbindOID is the OID of data varbind i (1-based).
func TrapVarbindOID(i int) string { return "1.3.6.1.4.1.32473.7.2." + strconv.Itoa(i) }

// MappingElement is one OSCAR Mapping element.
type MappingElement struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// TrapMapping is the campaign trap mapping (namespace EXT_TRAP_ALERTS, name
// TrapOID): plain put~ elements from positional varbinds ({{varbind_N}}: the
// receiver numbers data varbinds after sysUpTime and snmpTrapOID), node from
// the address, and keep~ of exactly the probe labels, so the receiver's own
// labels (the sender's address in instance/node/trap_source, the raw varbind
// labels) do not reach the alert and a clear has the firing's labels.
func TrapMapping() []MappingElement {
	var els []MappingElement
	for i, l := range TrapLabels {
		els = append(els, MappingElement{"put~" + l, "{{varbind_" + strconv.Itoa(i+1) + "}}"})
	}
	els = append(els, MappingElement{"put~node", "{{varbind_" + strconv.Itoa(len(TrapLabels)) + "}}"},
		MappingElement{"keep~^(" + strings.Join(append(append([]string(nil), TrapLabels...), "node"), "|") + ")$", "campaign probe labels"})
	return els
}

func berLen(n int) []byte {
	switch {
	case n < 0x80:
		return []byte{byte(n)} // #nosec G115 -- n < 0x80
	case n < 0x100:
		return []byte{0x81, byte(n)}
	default:
		return []byte{0x82, byte(n >> 8), byte(n)} // #nosec G115 -- byte-wise BER length; packets are far below 64 KiB
	}
}

func tlv(tag byte, content ...[]byte) []byte {
	body := bytes.Join(content, nil)
	return append(append([]byte{tag}, berLen(len(body))...), body...)
}

// berUint is a non-negative INTEGER-style body (a leading 0 keeps it positive).
func berUint(v uint64) []byte {
	b := []byte{byte(v)} // #nosec G115 -- low byte, by design
	for v >>= 8; v > 0; v >>= 8 {
		b = append([]byte{byte(v)}, b...) // #nosec G115 -- low byte, by design
	}
	if b[0]&0x80 != 0 {
		b = append([]byte{0}, b...)
	}
	return b
}

func berOID(oid string) []byte {
	parts := strings.Split(oid, ".")
	arcs := make([]uint64, len(parts))
	for i, p := range parts {
		arcs[i], _ = strconv.ParseUint(p, 10, 32) // constant OIDs only
	}
	out := []byte{byte(arcs[0]*40 + arcs[1])} // #nosec G115 -- constant OIDs: arcs 1 and 3 give 43
	for _, a := range arcs[2:] {
		enc := []byte{byte(a & 0x7f)} // #nosec G115 -- masked to 7 bits
		for a >>= 7; a > 0; a >>= 7 {
			enc = append([]byte{byte(a&0x7f) | 0x80}, enc...) // #nosec G115 -- masked to 7 bits
		}
		out = append(out, enc...)
	}
	return tlv(0x06, out)
}

// TrapPacket is an SNMPv2c SNMPv2-Trap-PDU: sysUpTime.0, snmpTrapOID.0 =
// TrapOID, then one OCTET STRING varbind per TrapLabels entry.
func TrapPacket(community string, requestID uint32, uptime uint32, p Probe) []byte {
	l := p.labels()
	vbs := [][]byte{
		tlv(0x30, berOID("1.3.6.1.2.1.1.3.0"), tlv(0x43, berUint(uint64(uptime)))),
		tlv(0x30, berOID("1.3.6.1.6.3.1.1.4.1.0"), berOID(TrapOID)),
	}
	for i, name := range TrapLabels {
		vbs = append(vbs, tlv(0x30, berOID(TrapVarbindOID(i+1)), tlv(0x04, []byte(l[name]))))
	}
	pdu := tlv(0xa7, tlv(0x02, berUint(uint64(requestID))), tlv(0x02, []byte{0}), tlv(0x02, []byte{0}), tlv(0x30, vbs...))
	return tlv(0x30, tlv(0x02, []byte{1}), tlv(0x04, []byte(community)), pdu)
}

var started = time.Now()

// SendTrap sends p as one SNMPv2c trap datagram to addr (host:port). UDP has
// no acknowledgement: arrival is judged from History.
func SendTrap(ctx context.Context, addr, community string, p Probe) error {
	var d net.Dialer
	c, err := d.DialContext(ctx, "udp", addr)
	if err != nil {
		return err
	}
	defer c.Close()
	var id [4]byte
	_, _ = rand.Read(id[:])
	reqID := uint32(id[0])<<23 | uint32(id[1])<<15 | uint32(id[2])<<7 | uint32(id[3])>>1 // 31 bits: a positive INTEGER
	uptime := uint32(time.Since(started) / (10 * time.Millisecond))                      // #nosec G115 -- sysUpTime TimeTicks wrap at 2^32 by definition
	_, err = c.Write(TrapPacket(community, reqID, uptime, p))
	return err
}

// --- webhook profile -------------------------------------------------------

// WebhookProfile is the campaign webhook profile's elements (namespace
// webhook_profiles), CorrTest's ordinary profile (docs/fixtures/
// corrtest_ordinary_v1.json): labels and annotations copied through, explicit
// RFC 3339 startsAt and endsAt. Plain projections only.
func WebhookProfile() []MappingElement {
	return []MappingElement{{"profile~schema_version", "1"}, {"profile~enabled", "true"},
		{"profile~required", "labels.alertname,event_timestamp,ends_at"},
		{"project_object~labels", "source=body:/labels"}, {"project_object~annotations", "source=body:/annotations"},
		{"parse_time~event_timestamp", "body:/startsAt|rfc3339"}, {"parse_time~ends_at", "body:/endsAt|rfc3339"}}
}

// WebhookValidity is a firing probe's endsAt after its start.
const WebhookValidity = time.Hour

// SendWebhook posts p to the profile URL. A firing ends WebhookValidity after
// its start; a clear ends now. Only HTTP 200 {"status":"ok"} is a send.
func SendWebhook(ctx context.Context, client *http.Client, url string, p Probe, now time.Time) error {
	end := p.StartsAt.Add(WebhookValidity)
	if p.Clear {
		end = now
	}
	body, _ := json.Marshal(map[string]any{"labels": p.labels(),
		"annotations": map[string]string{"summary": p.Labels["alertname"], "description": "OSCAR alerting campaign receiver probe"},
		"startsAt":    p.StartsAt.UTC().Format(time.RFC3339Nano), "endsAt": end.UTC().Format(time.RFC3339Nano)})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var ack struct {
		Status string `json:"status"`
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	_ = json.Unmarshal(b, &ack)
	if resp.StatusCode != http.StatusOK || ack.Status != "ok" {
		return fmt.Errorf("webhook HTTP %d status %q", resp.StatusCode, ack.Status)
	}
	return nil
}

// --- SMTP ------------------------------------------------------------------

// SMTPFrom and SMTPTo are inside the target receiver's sender-domain and
// recipient allow-lists (example.com is reserved for documentation).
const (
	SMTPFrom = "corrtest@example.com"
	SMTPTo   = "alerts@company.com"
)

// SendSMTP sends p as one plain SMTP message to addr (the receiver's legacy
// plaintext port). The subject and the body carry every label as key=value,
// which the receiver's default handler turns into labels.
func SendSMTP(ctx context.Context, addr string, p Probe) error {
	l := p.labels()
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sort.SliceStable(keys, func(i, j int) bool { return keys[i] == "alertname" }) // alertname first in the subject
	var kv []string
	for _, k := range keys {
		kv = append(kv, k+"="+l[k]) // probe values never contain spaces or quotes
	}
	var id [8]byte
	_, _ = rand.Read(id[:])
	msg := "From: " + SMTPFrom + "\r\nTo: " + SMTPTo + "\r\nSubject: " + strings.Join(kv, " ") +
		"\r\nMessage-ID: <" + hex.EncodeToString(id[:]) + "@corrtest.example.com>\r\nDate: " + time.Now().UTC().Format(time.RFC1123Z) +
		"\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" + strings.Join(kv, "\n") + "\r\n"
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	host, _, _ := net.SplitHostPort(addr)
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close() // the client failed to start; nothing to report beyond err
		return err
	}
	defer c.Close()
	if err := c.Mail(SMTPFrom); err != nil {
		return err
	}
	if err := c.Rcpt(SMTPTo); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := io.WriteString(w, msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

// --- Alertmanager resolve (OSC-210) ----------------------------------------

// ActiveAlert is a probe as OSCAR's native Alertmanager holds it: the exact
// labels the receiver posted (receivers add their own, e.g. the trap's
// alert_type, SMTP's timestamp) and its startsAt.
type ActiveAlert struct {
	Labels   map[string]string
	StartsAt time.Time
}

// IngestPath is native Alertmanager's v2 ingest behind OSCAR's reverse proxy
// (the worker lane's route).
const IngestPath = "/ext/alertmanager/api/v2/alerts"

// ResolveNative posts one resolve array for alerts: each with its identical
// labels and startsAt, so native Alertmanager closes that alert (same
// fingerprint) and OSCAR closes its History row. endsAt is 30 s in the past
// (never before startsAt): a controller clock ahead of the target must not
// leave it in the future, where Alertmanager would keep the alert active.
// Only a 2xx is a send.
func ResolveNative(ctx context.Context, client *http.Client, url string, alerts []ActiveAlert, now time.Time) error {
	batch := make([]any, 0, len(alerts))
	for _, a := range alerts {
		end := now.Add(-30 * time.Second)
		if !end.After(a.StartsAt) {
			end = a.StartsAt.Add(time.Millisecond)
		}
		batch = append(batch, map[string]any{"labels": a.Labels,
			"startsAt": a.StartsAt.UTC().Format(time.RFC3339Nano), "endsAt": end.UTC().Format(time.RFC3339Nano)})
	}
	body, _ := json.Marshal(batch)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("alertmanager ingest HTTP %d", resp.StatusCode)
	}
	return nil
}
