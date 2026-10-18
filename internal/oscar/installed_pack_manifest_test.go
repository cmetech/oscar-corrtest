package oscar

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/testpack"
)

func inputVectorDigest(t *testing.T, prepare func(int) (InstalledPackInputEvidence, []byte, error)) string {
	t.Helper()
	hash := sha256.New()
	for n := 0; n < 15; n++ {
		evidence, body, err := prepare(n)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(evidence)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = hash.Write(raw)
		_, _ = hash.Write([]byte{'\n'})
		_, _ = hash.Write(body)
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func TestInstalledPackInputExistingRequestVectors(t *testing.T) {
	for _, prefix := range []string{"", "/webhookreceiver"} {
		t.Run(prefix, func(t *testing.T) {
			kit, target, options := installedInputFixture(t, "https://receiver.example"+prefix)
			input, err := NewInstalledPackInput(kit, strings.Repeat("a", 32), time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC), target, options)
			if err != nil {
				t.Fatal(err)
			}
			got := inputVectorDigest(t, input.Prepare)
			want := map[string]string{"": "81858a65a4a55aa11508409c39eb73dede54caf0fdf36aaafdac4292e07ca8b5", "/webhookreceiver": "aabb660ca02215bd1383d087678d0ddae528fe15cc16013b1614f0105d85c3f2"}[prefix]
			if got != want {
				t.Fatalf("fixed pre-refactor request/evidence vector: %s", got)
			}
		})
	}
}

func installedPureSpec() InstalledPackInputSpec {
	return InstalledPackInputSpec{APIVersion: "corrtest.oscar/installed-input-v1", ManifestSHA256: strings.Repeat("b", 64), QualificationRun: strings.Repeat("a", 32), StartsAt: time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC), ReceiverTargetID: "receiver", ReceiverBaseURL: "https://receiver.example"}
}

func TestInstalledPackManifestPureVectorsAndDetachedValues(t *testing.T) {
	for _, prefix := range []string{"", "/webhookreceiver"} {
		spec := installedPureSpec()
		spec.ManifestSHA256 = "44381fc9bdf13c1f04ea1244bc992b88ec23eedb45169163651d70a7dd01ca59"
		spec.ReceiverBaseURL += prefix
		manifest, err := FreezeInstalledPackInput(spec)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"": "81858a65a4a55aa11508409c39eb73dede54caf0fdf36aaafdac4292e07ca8b5", "/webhookreceiver": "aabb660ca02215bd1383d087678d0ddae528fe15cc16013b1614f0105d85c3f2"}[prefix]
		if got := inputVectorDigest(t, manifest.Prepare); got != want {
			t.Fatalf("changed fixed bytes: %s", got)
		}
		evidence, body, _ := manifest.Prepare(0)
		evidence.Expected.Labels["site"] = "changed"
		body[0] = '!'
		saved := manifest.Spec()
		saved.ReceiverBaseURL = "https://foreign.example"
		raw, err := json.Marshal(manifest.Spec())
		if err != nil {
			t.Fatal(err)
		}
		var reopened InstalledPackInputSpec
		if err = json.Unmarshal(raw, &reopened); err != nil {
			t.Fatal(err)
		}
		restored, err := FreezeInstalledPackInput(reopened)
		if err != nil {
			t.Fatal(err)
		}
		if inputVectorDigest(t, manifest.Prepare) != want || inputVectorDigest(t, restored.Prepare) != want {
			t.Fatal("detached mutation or pure serialization changed requests")
		}
	}
}

func TestInstalledPackManifestRefusesInvalidSpecs(t *testing.T) {
	tests := map[string]func(*InstalledPackInputSpec){
		"version":         func(s *InstalledPackInputSpec) { s.APIVersion = "v2" },
		"manifest":        func(s *InstalledPackInputSpec) { s.ManifestSHA256 = strings.Repeat("B", 64) },
		"short manifest":  func(s *InstalledPackInputSpec) { s.ManifestSHA256 = "" },
		"domain run id":   func(s *InstalledPackInputSpec) { s.QualificationRun = "crt_01234567890123456789012345" },
		"uppercase token": func(s *InstalledPackInputSpec) { s.QualificationRun = strings.Repeat("A", 32) },
		"fraction":        func(s *InstalledPackInputSpec) { s.StartsAt = s.StartsAt.Add(time.Nanosecond) },
		"offset":          func(s *InstalledPackInputSpec) { s.StartsAt = s.StartsAt.In(time.FixedZone("offset", 3600)) },
		"zero":            func(s *InstalledPackInputSpec) { s.StartsAt = time.Time{} },
		"epoch":           func(s *InstalledPackInputSpec) { s.StartsAt = time.Unix(0, 0).UTC() },
		"late year":       func(s *InstalledPackInputSpec) { s.StartsAt = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC) },
		"missing target":  func(s *InstalledPackInputSpec) { s.ReceiverTargetID = "" },
		"target control":  func(s *InstalledPackInputSpec) { s.ReceiverTargetID = "receiver\nforged" },
		"target bound":    func(s *InstalledPackInputSpec) { s.ReceiverTargetID = strings.Repeat("r", 257) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			s := installedPureSpec()
			mutate(&s)
			if _, err := FreezeInstalledPackInput(s); err == nil {
				t.Fatal("invalid spec accepted")
			}
		})
	}
	for _, url := range []string{"", "https://user@receiver.example", "https://receiver.example/", "https://receiver.example/ext/mw", "https://receiver.example/webhookreceiver/", "https://receiver.example/%77ebhookreceiver", "https://receiver.example/webhookreceiver/../else", "https://receiver.example?", "https://receiver.example#", "https://receiver.example\\evil"} {
		t.Run(url, func(t *testing.T) {
			s := installedPureSpec()
			s.ReceiverBaseURL = url
			if _, err := FreezeInstalledPackInput(s); err == nil {
				t.Fatal("invalid base accepted")
			}
		})
	}
	var zero InstalledPackManifest
	if _, _, err := zero.Prepare(0); err == nil {
		t.Fatal("zero manifest accepted")
	}
	valid, err := FreezeInstalledPackInput(installedPureSpec())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{-1, 15, 1000} {
		if _, _, err := valid.Prepare(n); err == nil {
			t.Fatal("invalid index accepted")
		}
	}
}

func TestInstalledPackManifestFixedKitComparison(t *testing.T) {
	kit, _, _ := installedInputFixture(t, "https://receiver.example")
	original, scenario := kit.Bindings(), kit.Scenario()
	if !installedInputContractMatches(original, scenario) {
		t.Fatal("actual verified historical kit does not match")
	}
	for _, field := range []string{"manifest", "profile", "revision", "groups", "budget", "scope"} {
		t.Run(field, func(t *testing.T) {
			binding, changed := original, scenario
			switch field {
			case "manifest":
				binding.ManifestSHA256 = ""
			case "profile":
				binding.ProfileName = "another_profile"
			case "revision":
				binding.ProfileEffectiveSHA256 = strings.Repeat("c", 64)
			case "groups":
				changed.Groups[0].Count++
			case "budget":
				changed.Budgets.WindowSeconds++
			case "scope":
				changed.Coverage = "parent-delivery"
			}
			if installedInputContractMatches(binding, changed) {
				t.Fatal("changed verified-kit contract accepted")
			}
		})
	}
	if installedInputContractMatches(testpack.Bindings{}, testpack.Scenario{}) {
		t.Fatal("empty bindings gained authority")
	}
}
