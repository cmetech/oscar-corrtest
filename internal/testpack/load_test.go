//go:build linux || darwin

package testpack

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func hashTest(b []byte) string { s := sha256.Sum256(b); return hex.EncodeToString(s[:]) }
func fixture(t *testing.T) (string, string) {
	t.Helper()
	d := t.TempDir()
	entries, e := os.ReadDir("testdata/kit")
	if e != nil {
		t.Fatal(e)
	}
	for _, f := range entries {
		b, e := os.ReadFile("testdata/kit/" + f.Name())
		if e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(d, f.Name()), b, 0600); e != nil {
			t.Fatal(e)
		}
	}
	raw, _ := os.ReadFile(filepath.Join(d, "manifest.json"))
	return d, hashTest(raw)
}
func readTest(t *testing.T, d, n string) []byte {
	t.Helper()
	b, e := os.ReadFile(filepath.Join(d, n))
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func writeTest(t *testing.T, d, n string, b []byte) {
	t.Helper()
	if e := os.WriteFile(filepath.Join(d, n), b, 0600); e != nil {
		t.Fatal(e)
	}
}
func jsonTest(v any) []byte {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		panic(err)
	}
	return b.Bytes()
}
func manifestTest(t *testing.T, d string) map[string]any {
	t.Helper()
	var m map[string]any
	if e := json.Unmarshal(readTest(t, d, "manifest.json"), &m); e != nil {
		t.Fatal(e)
	}
	return m
}
func rebind(t *testing.T, d string, m map[string]any) string {
	t.Helper()
	for _, f := range m["files"].([]any) {
		r := f.(map[string]any)
		b := readTest(t, d, r["path"].(string))
		r["size"] = len(b)
		r["sha256"] = hashTest(b)
	}
	b := jsonTest(m)
	writeTest(t, d, "manifest.json", b)
	h := hashTest(b)
	writeTest(t, d, "manifest.sha256", []byte(h+"\n"))
	return h
}
func refused(t *testing.T, d, h string) {
	t.Helper()
	if k, e := Load(d, h); e == nil || k != nil {
		t.Fatalf("accepted invalid kit: nonnil=%v error=%v", k != nil, e)
	}
}
func TestLoadGenuineKitAndDetachedCopies(t *testing.T) {
	d, h := fixture(t)
	k, e := Load(d, h)
	if e != nil {
		t.Fatal(e)
	}
	b := k.Bindings()
	if b.ManifestSHA256 != h || b.PackID != "corrtest_operational" || b.ProfileEffectiveSHA256 != "725ec4da863ea3da62a0bf6184ec86562a6c66a53ec79e653df5f1744e096bdb" {
		t.Fatalf("wrong bindings: %+v", b)
	}
	if len(k.Files()) != 8 || k.Scenario().InputCount() != 15 || k.Scenario().Budgets.ExecutionSeconds != 180 {
		t.Fatal("lost fixed scenario")
	}
	for _, get := range []func() []byte{k.ManifestJSON, k.ScenarioJSON, k.ProfileJSON, k.RuleJSON} {
		a := get()
		before := string(a)
		a[0] = 'X'
		if string(get()) != before {
			t.Fatal("mutable returned alias")
		}
	}
	files := k.Files()
	files[0].Path = "foreign"
	if k.Files()[0].Path == "foreign" {
		t.Fatal("mutable files")
	}
	raw, ok := k.Artifact("README.md")
	if !ok {
		t.Fatal("missing artifact")
	}
	raw[0] = 'X'
	again, _ := k.Artifact("README.md")
	if again[0] == 'X' {
		t.Fatal("mutable artifact")
	}
	if _, ok = k.Artifact("../manifest.json"); ok {
		t.Fatal("arbitrary artifact")
	}
	writeTest(t, d, "flood-installed.json", []byte("changed after Load"))
	if k.Scenario().InputCount() != 15 {
		t.Fatal("retained pathname rather than bytes")
	}
}
func TestLoadRejectsFilesystemAndPin(t *testing.T) {
	for _, mode := range []string{"wrong-pin", "uppercase-pin", "no-pin", "extra", "missing", "directory", "symlink", "fifo", "oversize", "manifest-change", "sidecar", "artifact-change", "root-symlink"} {
		t.Run(mode, func(t *testing.T) {
			d, h := fixture(t)
			p := filepath.Join(d, "README.md")
			switch mode {
			case "wrong-pin":
				h = strings.Repeat("0", 64)
			case "uppercase-pin":
				h = strings.ToUpper(h)
			case "no-pin":
				h = ""
			case "extra":
				writeTest(t, d, "extra", []byte("x"))
			case "missing":
				os.Remove(p)
			case "directory":
				os.Remove(p)
				os.Mkdir(p, 0700)
			case "symlink":
				os.Remove(p)
				os.Symlink("manifest.json", p)
			case "fifo":
				os.Remove(p)
				if e := unix.Mkfifo(p, 0600); e != nil {
					t.Fatal(e)
				}
			case "oversize":
				writeTest(t, d, "README.md", bytes.Repeat([]byte("x"), (1<<20)+1))
			case "manifest-change":
				writeTest(t, d, "manifest.json", append(readTest(t, d, "manifest.json"), ' '))
			case "sidecar":
				writeTest(t, d, "manifest.sha256", []byte(h))
			case "artifact-change":
				writeTest(t, d, "README.md", []byte("x"))
			case "root-symlink":
				link := filepath.Join(t.TempDir(), "link")
				os.Symlink(d, link)
				d = link
			}
			refused(t, d, h)
		})
	}
}
func TestLoadRejectsRehashedUnsupportedContracts(t *testing.T) {
	for _, mode := range []string{"unknown-manifest", "duplicate-manifest", "case-key", "unknown-profile", "nested-profile", "effective-hash", "unknown-rule", "path", "duplicate-file", "invalid-source-path", "source-hash", "scenario-count", "scenario-budget", "scenario-unknown", "scenario-duplicate", "scenario-float", "scenario-unsafe", "scenario-depth", "vector-hash", "vector-unknown"} {
		t.Run(mode, func(t *testing.T) {
			d, _ := fixture(t)
			m := manifestTest(t, d)
			switch mode {
			case "unknown-manifest":
				m["future"] = true
			case "case-key":
				m["ID"] = m["id"]
			case "unknown-profile":
				m["profile"].(map[string]any)["future"] = true
			case "nested-profile":
				m["profile"].(map[string]any)["nested_mappings"] = []any{"foreign"}
			case "effective-hash":
				m["profile"].(map[string]any)["effective_sha256"] = strings.Repeat("0", 64)
			case "unknown-rule":
				m["rule"].(map[string]any)["future"] = true
			case "path":
				m["files"].([]any)[0].(map[string]any)["path"] = "../README.md"
				raw := jsonTest(m)
				writeTest(t, d, "manifest.json", raw)
				h := hashTest(raw)
				writeTest(t, d, "manifest.sha256", []byte(h+"\n"))
				refused(t, d, h)
				return
			case "duplicate-file":
				m["files"].([]any)[1] = m["files"].([]any)[0]
			case "invalid-source-path":
				m["validator_sources"].(map[string]any)["oscar-correlator/src/app/../escape.py"] = strings.Repeat("a", 64)
			case "source-hash":
				for k := range m["validator_sources"].(map[string]any) {
					m["validator_sources"].(map[string]any)[k] = "invalid"
					break
				}
			case "scenario-count", "scenario-budget", "scenario-unknown":
				var s map[string]any
				json.Unmarshal(readTest(t, d, "flood-installed.json"), &s)
				if mode == "scenario-count" {
					s["groups"].([]any)[0].(map[string]any)["count"] = 6
				} else if mode == "scenario-budget" {
					s["budgets"].(map[string]any)["execution_seconds"] = 181
				} else {
					s["execute"] = "arbitrary"
				}
				writeTest(t, d, "flood-installed.json", jsonTest(s))
			case "scenario-duplicate":
				writeTest(t, d, "flood-installed.json", []byte(`{"id":"x","id":"y"}`))
			case "scenario-float":
				writeTest(t, d, "flood-installed.json", []byte(`{"x":1.0}`))
			case "scenario-unsafe":
				writeTest(t, d, "flood-installed.json", []byte(`{"x":9007199254740992}`))
			case "scenario-depth":
				writeTest(t, d, "flood-installed.json", []byte(strings.Repeat("[", 100)+"0"+strings.Repeat("]", 100)))
			case "vector-hash", "vector-unknown":
				var v map[string]any
				json.Unmarshal(readTest(t, d, "hash-vectors.json"), &v)
				if mode == "vector-hash" {
					v["vectors"].([]any)[0].(map[string]any)["sha256"] = strings.Repeat("0", 64)
				} else {
					v["extra"] = true
				}
				writeTest(t, d, "hash-vectors.json", jsonTest(v))
			}
			h := rebind(t, d, m)
			if mode == "duplicate-manifest" {
				raw := readTest(t, d, "manifest.json")
				raw = append([]byte(`{"id":"corrtest_operational",`), raw[1:]...)
				h = hashTest(raw)
				writeTest(t, d, "manifest.json", raw)
				writeTest(t, d, "manifest.sha256", []byte(h+"\n"))
			}
			refused(t, d, h)
		})
	}
}
func replaceTar(t *testing.T, d string, mutate func(*tar.Header, []byte) (*tar.Header, []byte)) {
	t.Helper()
	raw := readTest(t, d, "corrtest_operational-0.1.0.tar.gz")
	g, e := gzip.NewReader(bytes.NewReader(raw))
	if e != nil {
		t.Fatal(e)
	}
	r := tar.NewReader(g)
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	w := tar.NewWriter(gz)
	for {
		h, e := r.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			t.Fatal(e)
		}
		data, e := io.ReadAll(r)
		if e != nil {
			t.Fatal(e)
		}
		h, data = mutate(h, data)
		if e = w.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
		if _, e = w.Write(data); e != nil {
			t.Fatal(e)
		}
	}
	w.Close()
	gz.Close()
	g.Close()
	writeTest(t, d, "corrtest_operational-0.1.0.tar.gz", b.Bytes())
	writeTest(t, d, "corrtest_operational-0.1.0.tar.gz.sha256", []byte(hashTest(b.Bytes())+"  corrtest_operational-0.1.0.tar.gz\n"))
}
func TestLoadRejectsNestedArchiveChanges(t *testing.T) {
	for _, mode := range []string{"tar-path", "tar-link", "tar-profile", "tar-pack", "tar-trailing", "zip-path", "zip-rule", "zip-duplicate", "zip-alias", "zip-unknown", "zip-multidoc", "zip-bomb"} {
		t.Run(mode, func(t *testing.T) {
			d, _ := fixture(t)
			m := manifestTest(t, d)
			if strings.HasPrefix(mode, "tar-") {
				replaceTar(t, d, func(h *tar.Header, b []byte) (*tar.Header, []byte) {
					if h.Typeflag == tar.TypeReg {
						switch mode {
						case "tar-path":
							h.Name = "../outside"
						case "tar-link":
							h.Typeflag = tar.TypeSymlink
							h.Linkname = "outside"
							h.Size = 0
							b = nil
						case "tar-profile":
							if strings.HasSuffix(h.Name, ".json") {
								b = bytes.ReplaceAll(b, []byte("body:/startsAt"), []byte("body:/endsAt"))
								h.Size = int64(len(b))
							}
						case "tar-pack":
							if strings.HasSuffix(h.Name, "pack.yml") {
								b = append(b, []byte("unknown: true\n")...)
								h.Size = int64(len(b))
							}
						}
					}
					return h, b
				})
				if mode == "tar-trailing" {
					extra := append(readTest(t, d, "corrtest_operational-0.1.0.tar.gz"), []byte("extra")...)
					writeTest(t, d, "corrtest_operational-0.1.0.tar.gz", extra)
					writeTest(t, d, "corrtest_operational-0.1.0.tar.gz.sha256", []byte(hashTest(extra)+"  corrtest_operational-0.1.0.tar.gz\n"))
				}
			} else {
				raw := readTest(t, d, "correlation-rules.zip")
				z, _ := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
				rc, _ := z.File[0].Open()
				rule, _ := io.ReadAll(rc)
				rc.Close()
				name := z.File[0].Name
				switch mode {
				case "zip-path":
					name = "../outside"
				case "zip-rule":
					rule = bytes.ReplaceAll(rule, []byte("min_count: 5"), []byte("min_count: 6"))
				case "zip-alias":
					rule = append(rule, []byte("extra: &a [*a]\n")...)
				case "zip-unknown":
					rule = append(rule, []byte("extra: true\n")...)
				case "zip-multidoc":
					rule = append(rule, []byte("---\na: b\n")...)
				case "zip-bomb":
					rule = bytes.Repeat([]byte("x"), (1<<20)+1)
				}
				var b bytes.Buffer
				w := zip.NewWriter(&b)
				f, _ := w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
				f.Write(rule)
				if mode == "zip-duplicate" {
					f, _ = w.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
					f.Write(rule)
				}
				w.Close()
				writeTest(t, d, "correlation-rules.zip", b.Bytes())
			}
			refused(t, d, rebind(t, d, m))
		})
	}
}

func TestLoadRejectsRootSymlinkWithDirectorySuffix(t *testing.T) {
	for _, suffix := range []string{"/", "/."} {
		t.Run(suffix, func(t *testing.T) {
			d, h := fixture(t)
			link := filepath.Join(t.TempDir(), "link")
			if e := os.Symlink(d, link); e != nil {
				t.Fatal(e)
			}
			refused(t, link+suffix, h)
		})
	}
}

func TestInstallerSemanticHashVectors(t *testing.T) {
	d, h := fixture(t)
	if h != "44381fc9bdf13c1f04ea1244bc992b88ec23eedb45169163651d70a7dd01ca59" {
		t.Fatal("genuine fixture provenance changed")
	}
	k, e := Load(d, h)
	if e != nil {
		t.Fatal(e)
	}
	b := k.Bindings()
	// Independently evaluated by the genuine Python installer Bundle, not computed
	// from the Go implementation under test. Backend semantic hashes omit LF.
	if b.PackContentHash != "sha256:b31dfc425370e21947dfdceeebca2520f2e6fdfe480baaa0c118b534f196ae4a" || b.PackManifestSHA256 != "0ef1cb671571b62ecb965c64660eb532431b87a9db8fe4b4314b80de32f59bc8" || b.MappingStateSHA256 != "c19967aa3192ae940a6cdb7e046536464c6b98dab51f799bb8f9ea3bbe269443" || b.MappingRowKey != "mapping:3d289e24e96e93ae2cbfcdc67a13a51b508ffd5afc4dd2b243206e9173884d57" || b.MappingSourceFile != "conf/mappings/corrtest_operational_v1.json" {
		t.Fatalf("Python installer mismatch: %+v", b)
	}
	raw := k.MappingStateJSON()
	before := string(raw)
	raw[0] = 'X'
	if string(k.MappingStateJSON()) != before {
		t.Fatal("mutable normalized state")
	}
	s := k.Scenario()
	s.Groups[0].Count = 900
	if k.Scenario().InputCount() != 15 {
		t.Fatal("mutable scenario")
	}
}

func TestLoadRejectsHiddenZIPEnvelope(t *testing.T) {
	for _, mode := range []string{"prefix", "trailer", "foreign-local-name"} {
		t.Run(mode, func(t *testing.T) {
			d, _ := fixture(t)
			m := manifestTest(t, d)
			raw := readTest(t, d, "correlation-rules.zip")
			switch mode {
			case "prefix":
				raw = append([]byte("hidden payload"), raw...)
			case "trailer":
				raw = append(raw, []byte("hidden payload")...)
			case "foreign-local-name":
				name := []byte("corrtest-operational-v1-flood.yml")
				copy(raw[30:30+len(name)], []byte("../"+strings.Repeat("x", len(name)-3)))
			}
			writeTest(t, d, "correlation-rules.zip", raw)
			refused(t, d, rebind(t, d, m))
		})
	}
}

func TestZeroKitDoesNotClaimValidatedScenario(t *testing.T) {
	var k Kit
	if k.Scenario().InputCount() != 0 || k.Scenario().ID != "" {
		t.Fatal("zero-value Kit claimed validated input contract")
	}
}
