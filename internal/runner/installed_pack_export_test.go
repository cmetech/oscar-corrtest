//go:build linux || darwin

package runner

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"github.com/cmetech/oscar-corrtest/internal/oscar"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/artifact"
	"github.com/cmetech/oscar-corrtest/internal/domain"
	"github.com/cmetech/oscar-corrtest/internal/evidence"
)

// Uses the actual runner/adapters and SQLite/artifact stores with the existing
// controlled HTTP peer. This is not deployed target/RBAC or exclusion proof.
func TestInstalledRunnerEvidenceRoundTrip(t *testing.T) {
	for _, fault := range []string{"", "wrong revision", "transient violation", "preflight", "partial audit", "observed event failure", "late observation clock", "finalization lost", "registration failure"} {
		t.Run(fault, func(t *testing.T) {
			p := newInstalledRunnerPeer(t, fault)
			if fault == "registration failure" {
				p.rawSQL(`CREATE TRIGGER fail_registration BEFORE INSERT ON installed_pack_runs BEGIN SELECT RAISE(ABORT,'controlled registration failure'); END`)
			}
			if fault == "finalization lost" {
				p.rawSQL(`CREATE TRIGGER fail_complete BEFORE UPDATE OF status ON runs WHEN NEW.status='COMPLETED' BEGIN SELECT RAISE(ABORT,'controlled finalization failure'); END`)
			}
			if fault == "late observation clock" {
				p.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					recorded := httptest.NewRecorder()
					p.serve(recorded, r)
					if r.URL.Query().Get("fingerprint") == "000000000000000f" {
						p.mu.Lock()
						p.now = p.now.Add(181 * time.Second)
						p.mu.Unlock()
					}
					for k, values := range recorded.Header() {
						for _, v := range values {
							w.Header().Add(k, v)
						}
					}
					w.WriteHeader(recorded.Code)
					w.Write(recorded.Body.Bytes())
				})
			}
			if fault == "observed event failure" {
				p.rawSQL(`CREATE TRIGGER fail_observed BEFORE INSERT ON run_events WHEN NEW.type='installed_pack.observed' BEGIN SELECT RAISE(ABORT,'controlled observed event persistence failure'); END`)
			}
			err := p.engine().Execute(context.Background(), p.run)
			if fault == "" && err != nil {
				t.Fatal(err)
			}
			if fault == "finalization lost" {
				if err == nil {
					t.Fatal("missing actual finalization failure")
				}
				n, e := p.db.RecoverInterruptedRuns(context.Background(), p.clock())
				if e != nil || n != 1 {
					t.Fatal(n, e)
				}
			}
			snapshot, err := p.db.ReadInstalledPackExport(context.Background(), p.run.ID)
			if err != nil {
				t.Fatal(err)
			}
			var attachments []evidence.Attachment
			for _, a := range snapshot.Artifacts {
				if a.Availability == domain.ArtifactPending {
					continue
				}
				raw, err := p.artifacts.ReadVerifiedBounded(context.Background(), artifact.Manifest{RelativePath: a.RelativePath, MIMEType: a.MIMEType, SHA256: a.SHA256, ByteSize: a.ByteSize}, 16<<20)
				if err != nil {
					t.Fatal(err)
				}
				attachments = append(attachments, evidence.Attachment{Name: a.RelativePath, Content: raw, SHA256: a.SHA256, ByteSize: a.ByteSize})
			}
			destination := filepath.Join(t.TempDir(), "installed.zip")
			if _, err = evidence.WriteInstalledPack(context.Background(), destination, snapshot, attachments); err != nil {
				t.Fatal(err)
			}
			beforeGets, beforePosts := p.gets, p.posts
			if err = evidence.Verify(context.Background(), destination); err != nil {
				t.Fatal(err)
			}
			if fault == "late observation clock" {
				var changed oscar.InstalledPackRunReport
				if err := json.Unmarshal(snapshot.Run.CanonicalReportJSON, &changed); err != nil {
					t.Fatal(err)
				}
				changed.Evaluation.Reason = "invented without raw evidence"
				snapshot.Run.CanonicalReportJSON, _ = json.Marshal(changed)
				if _, err := evidence.WriteInstalledPack(context.Background(), filepath.Join(t.TempDir(), "bad-tail.zip"), snapshot, attachments); err == nil || !strings.Contains(err.Error(), "matching retained raw tail") {
					t.Fatal("unjustified failed evaluation", err)
				}
			}
			if p.gets != beforeGets || p.posts != beforePosts {
				t.Fatal("offline verification issued HTTP")
			}
		})
	}
}

func TestInstalledRunnerEvidenceRejectsRehashedSemanticChanges(t *testing.T) {
	for _, fault := range []string{"", "transient violation"} {
		t.Run(fault, func(t *testing.T) {
			p := newInstalledRunnerPeer(t, fault)
			if err := p.engine().Execute(context.Background(), p.run); err != nil {
				t.Fatal(err)
			}
			snapshot, err := p.db.ReadInstalledPackExport(context.Background(), p.run.ID)
			if err != nil {
				t.Fatal(err)
			}
			var attachments []evidence.Attachment
			for _, a := range snapshot.Artifacts {
				raw, err := p.artifacts.ReadVerifiedBounded(context.Background(), artifact.Manifest{RelativePath: a.RelativePath, MIMEType: a.MIMEType, SHA256: a.SHA256, ByteSize: a.ByteSize}, 16<<20)
				if err != nil {
					t.Fatal(err)
				}
				attachments = append(attachments, evidence.Attachment{Name: a.RelativePath, Content: raw, SHA256: a.SHA256, ByteSize: a.ByteSize})
			}
			good := filepath.Join(t.TempDir(), "good.zip")
			if _, err = evidence.WriteInstalledPack(context.Background(), good, snapshot, attachments); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"coverage", "interval", "last-forward", "poll-evaluation", "adverse", "missing-event", "raw-active", "raw-mapping", "unjustified-failure-evaluation"} {
				t.Run(kind, func(t *testing.T) {
					bad := installedRunnerRewriteArchive(t, good, func(files map[string][]byte) {
						var report oscar.InstalledPackRunReport
						if err := json.Unmarshal(snapshot.Run.CanonicalReportJSON, &report); err != nil {
							t.Fatal(err)
						}
						switch kind {
						case "coverage":
							report.Coverage["effects"] = "physical delivery proved"
						case "interval":
							report.Polls[len(report.Polls)-1].ElapsedSinceLastForward = time.Second
						case "last-forward":
							at := report.LastForwardAt.Add(-time.Hour)
							report.LastForwardAt = &at
						case "poll-evaluation":
							report.Polls[0].Evaluation = oscar.InstalledFloodEvaluation{Disposition: oscar.InstalledFloodEligible, Reason: "invented"}
						case "adverse":
							if report.Adverse == nil {
								v := oscar.InstalledFloodEvaluation{Disposition: oscar.InstalledFloodViolation, Reason: "invented"}
								report.Adverse = &v
							} else {
								report.Adverse = nil
							}
						case "unjustified-failure-evaluation":
							report.Evaluation = oscar.InstalledFloodEvaluation{Disposition: oscar.InstalledFloodEligible, Reason: "invented"}
						case "missing-event":
							var events []domain.RunEvent
							json.Unmarshal(files["events.json"], &events)
							for i, e := range events {
								if e.Type == "installed_pack.observed" {
									events = append(events[:i], events[i+1:]...)
									break
								}
							}
							files["events.json"], _ = json.Marshal(events)
							return
						case "raw-active", "raw-mapping":
							var index []struct {
								Entry    string          `json:"entry"`
								Artifact domain.Artifact `json:"artifact"`
							}
							json.Unmarshal(files["installed-index.json"], &index)
							suffix := "/observations/000/active-raw-rows.json"
							if kind == "raw-mapping" {
								suffix = "/preflight/after/mapping.json"
							}
							for i, item := range index {
								if strings.HasSuffix(item.Artifact.RelativePath, suffix) {
									files[item.Entry] = []byte(`[]`)
									index[i].Artifact.SHA256 = installedRunnerHash(files[item.Entry])
									index[i].Artifact.ByteSize = 2
									for j := range report.Artifacts {
										if report.Artifacts[j].RelativePath == item.Artifact.RelativePath {
											report.Artifacts[j].SHA256 = index[i].Artifact.SHA256
											report.Artifacts[j].ByteSize = 2
										}
									}
									break
								}
							}
							files["installed-index.json"], _ = json.Marshal(index)
						}
						changed, err := json.Marshal(report)
						if err != nil {
							t.Fatal(err)
						}
						if !bytes.Contains(files["installed-run.json"], snapshot.Run.CanonicalReportJSON) {
							t.Fatal("report bytes not found")
						}
						files["installed-run.json"] = bytes.Replace(files["installed-run.json"], snapshot.Run.CanonicalReportJSON, changed, 1)
					})
					if err := evidence.Verify(context.Background(), bad); err == nil {
						t.Fatal("rehashed semantic contradiction accepted")
					}
				})
			}
		})
	}
}
func installedRunnerRewriteArchive(t *testing.T, src string, mutate func(map[string][]byte)) string {
	t.Helper()
	z, err := zip.OpenReader(src)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = raw
	}
	z.Close()
	mutate(files)
	type entry struct {
		Path     string `json:"path"`
		SHA256   string `json:"sha256"`
		ByteSize int64  `json:"byteSize"`
	}
	var m struct {
		APIVersion string    `json:"apiVersion"`
		RunID      string    `json:"runId"`
		CreatedAt  time.Time `json:"createdAt"`
		Files      []entry   `json:"files"`
	}
	if err = json.Unmarshal(files["manifest.json"], &m); err != nil {
		t.Fatal(err)
	}
	m.Files = nil
	for name, raw := range files {
		if name != "manifest.json" {
			m.Files = append(m.Files, entry{name, installedRunnerHash(raw), int64(len(raw))})
		}
	}
	files["manifest.json"], _ = json.Marshal(m)
	dst := filepath.Join(t.TempDir(), "rehashed.zip")
	f, err := os.Create(dst)
	if err != nil {
		t.Fatal(err)
	}
	out := zip.NewWriter(f)
	for name, raw := range files {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		h.SetMode(0600)
		w, err := out.CreateHeader(h)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = w.Write(raw); err != nil {
			t.Fatal(err)
		}
	}
	if err = out.Close(); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
}
