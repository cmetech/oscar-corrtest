package oscar

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

func existingFeedSpec(t *testing.T, profile string) ExistingFeedManifestSpec {
	t.Helper()
	path, hash := "splunk/baseline.json", "91913d137828c67d014bdd40124096d7624e2924f19c6cf65910604cb6e5244b"
	if profile == "middleware_custom_alert" {
		path, hash = "middleware_custom_alert/EXT_CUSTOM_ALERTS.json", "faff8119b0665b4d15b7fcce6c7bc65f160f092af83c928d2c5d2d35a0c8b51e"
	}
	body, err := os.ReadFile("testdata/existing_feed/" + profile + ".json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(body)
	return ExistingFeedManifestSpec{CohortID: "0123456789abcdef0123456789abcdef", TargetID: "owned-target", ReceiverOrigin: "https://owned-receiver:8443", Profile: profile, ProfileHash: hash, RequestHost: "owned-receiver", UserAgent: "owned-existing-feed/1", StartsAt: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC), Mapping: ExistingFeedMappingSnapshot{Source: "tests/oscar-webhookreceiver/fixtures/" + path, SHA256: hex.EncodeToString(sum[:]), BodyBase64: base64.StdEncoding.EncodeToString(body)}}
}

func TestExistingFeedPreparedFixtures(t *testing.T) {
	for _, profile := range []string{"splunk", "middleware_custom_alert"} {
		t.Run(profile, func(t *testing.T) {
			spec := existingFeedSpec(t, profile)
			manifest, err := FreezeExistingFeedManifest(spec)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 6; i++ {
				evidence, body, err := manifest.Prepare(i)
				if err != nil {
					t.Fatal(err)
				}
				host := "owned-existing-0123456789abcdef0123456789abcdef-" + string(rune('0'+i))
				name := "OrdinaryWindowProof"
				if i == 5 {
					name = "OrdinaryWindowNoMatch"
				}
				var wantBody string
				var wantLabels map[string]string
				if profile == "splunk" {
					wantBody = `{"result":{"_time":"1791979200","host":"` + host + `","severity":"critical","site":"NYC-splunk"},"search_name":"` + name + `","sid":"owned-existing-0123456789abcdef0123456789abcdef-"}`
					wantLabels = map[string]string{"alertname": name, "node": host, "severity": "critical", "source": host, "category": "webhook_splunk", "sid": "owned-existing-0123456789abcdef0123456789abcdef-", "site": "NYC-splunk", "user_agent": "owned-existing-feed/1", "mapped": "true", "summary_copy": name + " on " + host + " (critical)"}
				} else {
					wantBody = `{"alertname":"` + name + `","event_timestamp":"2026-10-14T12:00:00+00:00","hostname":"` + host + `","site":"NYC-middleware_custom_alert","type":"alternate"}`
					wantLabels = map[string]string{"alertname": name, "hostname": host, "site": "NYC-middleware_custom_alert", "event_timestamp": "2026-10-14T12:00:00+00:00", "severity": "warning", "category": "alternate", "alert_type": "alternate", "application": "alternate", "instance": host}
				}
				if string(body) != wantBody {
					t.Fatalf("body = %s; want %s", body, wantBody)
				}
				if !reflect.DeepEqual(evidence.Expected.Labels, wantLabels) || !evidence.Expected.StartsAt.Equal(spec.StartsAt) {
					t.Fatalf("identity = %+v", evidence.Expected)
				}
				decoded, err := base64.StdEncoding.DecodeString(evidence.Request.BodyBase64)
				sum := sha256.Sum256(body)
				if err != nil || string(decoded) != string(body) || evidence.Request.BodySHA256 != hex.EncodeToString(sum[:]) || evidence.Request.BodyBytes != len(body) {
					t.Fatal("request bytes/hash differ")
				}
				if evidence.Request.Endpoint != spec.ReceiverOrigin+"/api/v1/webhookreceiver/webhook/"+profile || evidence.Request.ProfilePrecondition != `"`+spec.ProfileHash+`"` || evidence.Request.Method != "POST" || evidence.Request.ContentType != "application/json" || evidence.Manifest != spec || evidence.FixtureIndex != i {
					t.Fatalf("evidence = %+v", evidence)
				}
				body[0] = '!'
				evidence.Expected.Labels["alertname"] = "foreign"
				again, bytesAgain, err := manifest.Prepare(i)
				if err != nil || string(bytesAgain) != wantBody || !reflect.DeepEqual(again.Expected.Labels, wantLabels) {
					t.Fatal("prepared output aliases frozen manifest")
				}
			}
			for _, index := range []int{-1, 6} {
				if _, _, err := manifest.Prepare(index); err == nil {
					t.Fatal("accepted foreign fixture index")
				}
			}
			saved, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			var restored ExistingFeedManifestSpec
			if err := json.Unmarshal(saved, &restored); err != nil {
				t.Fatal(err)
			}
			if _, err := FreezeExistingFeedManifest(restored); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExistingFeedManifestRefusesUnpinnedInputs(t *testing.T) {
	cases := map[string]func(*ExistingFeedManifestSpec){
		"missing snapshot": func(s *ExistingFeedManifestSpec) { s.Mapping.BodyBase64 = "" },
		"snapshot altered": func(s *ExistingFeedManifestSpec) {
			s.Mapping.BodyBase64 = base64.StdEncoding.EncodeToString([]byte(`{}`))
		},
		"foreign snapshot hash": func(s *ExistingFeedManifestSpec) { s.Mapping.SHA256 = strings.Repeat("0", 64) },
		"foreign provenance":    func(s *ExistingFeedManifestSpec) { s.Mapping.Source = "live/database" },
		"foreign profile":       func(s *ExistingFeedManifestSpec) { s.Profile = "custom" },
		"profile edited":        func(s *ExistingFeedManifestSpec) { s.ProfileHash = strings.Repeat("0", 64) },
		"different user agent":  func(s *ExistingFeedManifestSpec) { s.UserAgent = "Go-http-client/1.1" },
		"different host":        func(s *ExistingFeedManifestSpec) { s.RequestHost = "owned-existing-0123456789abcdef0123456789abcdef-0" },
		"missing host":          func(s *ExistingFeedManifestSpec) { s.RequestHost = "" },
		"missing ownership":     func(s *ExistingFeedManifestSpec) { s.CohortID = "" },
		"invalid ownership":     func(s *ExistingFeedManifestSpec) { s.CohortID = "../foreign" },
		"missing target":        func(s *ExistingFeedManifestSpec) { s.TargetID = "" },
		"missing start":         func(s *ExistingFeedManifestSpec) { s.StartsAt = time.Time{} },
		"fractional start":      func(s *ExistingFeedManifestSpec) { s.StartsAt = s.StartsAt.Add(time.Millisecond) },
		"offset start":          func(s *ExistingFeedManifestSpec) { s.StartsAt = s.StartsAt.In(time.FixedZone("offset", 3600)) },
		"userinfo":              func(s *ExistingFeedManifestSpec) { s.ReceiverOrigin = "https://user:secret@owned-receiver" },
		"query":                 func(s *ExistingFeedManifestSpec) { s.ReceiverOrigin += "?token=secret" },
		"empty query":           func(s *ExistingFeedManifestSpec) { s.ReceiverOrigin += "?" },
		"fragment":              func(s *ExistingFeedManifestSpec) { s.ReceiverOrigin += "#fragment" },
		"arbitrary path":        func(s *ExistingFeedManifestSpec) { s.ReceiverOrigin += "/another/profile" },
		"non HTTP":              func(s *ExistingFeedManifestSpec) { s.ReceiverOrigin = "file:///tmp/receiver" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			spec := existingFeedSpec(t, "splunk")
			mutate(&spec)
			if _, err := FreezeExistingFeedManifest(spec); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
	if _, _, err := (ExistingFeedManifest{}).Prepare(0); err == nil {
		t.Fatal("zero manifest prepared")
	}
}

func TestExistingFeedIdentityRefusesMissingAmbiguousOrForeignNative(t *testing.T) {
	for _, profile := range []string{"splunk", "middleware_custom_alert"} {
		t.Run(profile, func(t *testing.T) {
			manifest, err := FreezeExistingFeedManifest(existingFeedSpec(t, profile))
			if err != nil {
				t.Fatal(err)
			}
			e, _, err := manifest.Prepare(0)
			if err != nil {
				t.Fatal(err)
			}
			valid := ExistingFeedNativeObservation{Labels: cloneStringMap(e.Expected.Labels), StartsAt: e.Expected.StartsAt, Fingerprint: "0123456789abcdef"}
			if err := manifest.VerifyNative(0, []ExistingFeedNativeObservation{valid}); err != nil {
				t.Fatal(err)
			}
			for _, rows := range [][]ExistingFeedNativeObservation{nil, {valid, valid}} {
				if err := manifest.VerifyNative(0, rows); err == nil {
					t.Fatal("accepted missing/ambiguous identity")
				}
			}
			for _, mutation := range []func(*ExistingFeedNativeObservation){
				func(r *ExistingFeedNativeObservation) { r.Labels["severity"] = "major" },
				func(r *ExistingFeedNativeObservation) { r.Labels["extra"] = "foreign" },
				func(r *ExistingFeedNativeObservation) { delete(r.Labels, "alertname") },
				func(r *ExistingFeedNativeObservation) { r.StartsAt = r.StartsAt.Add(time.Second) },
				func(r *ExistingFeedNativeObservation) { r.Fingerprint = "" },
			} {
				row := valid
				row.Labels = cloneStringMap(valid.Labels)
				mutation(&row)
				if err := manifest.VerifyNative(0, []ExistingFeedNativeObservation{row}); err == nil {
					t.Fatal("accepted foreign/partial identity")
				}
			}
			if err := manifest.VerifyNative(1, []ExistingFeedNativeObservation{valid}); err == nil {
				t.Fatal("accepted wrong fixture")
			}
		})
	}
}
