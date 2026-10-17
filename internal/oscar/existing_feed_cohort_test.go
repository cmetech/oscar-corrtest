package oscar

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/cmetech/oscar-corrtest/internal/domain"
)

type existingFeedCohortConfig struct {
	Manifest            ExistingFeedManifestSpec `json:"manifest"`
	Receiver            domain.Target            `json:"receiver"`
	MappingSnapshotPath string                   `json:"mappingSnapshotPath"`
	StateDir            string                   `json:"stateDir"`
	Phase               string                   `json:"phase"`
}

type existingFeedCohortResult struct {
	BodySHA256 string          `json:"bodySha256"`
	Result     InjectionResult `json:"result"`
	Error      string          `json:"error,omitempty"`
}

// This test-only driver needs externally verified owned runtime/snapshot provenance.
func runExistingFeedOwnedCohort(ctx context.Context, config existingFeedCohortConfig) error {
	first, last := 0, 0
	switch config.Phase {
	case "first":
	case "remaining":
		first, last = 1, 4
	case "no-match":
		first, last = 5, 5
	default:
		return fmt.Errorf("unsupported owned-cohort phase")
	}
	manifest, err := FreezeExistingFeedManifest(config.Manifest)
	if err != nil {
		return err
	}
	input, err := NewExistingFeedLabInput(manifest, config.Receiver, Options{})
	if err != nil {
		return err
	}
	info, err := os.Lstat(config.StateDir)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(config.StateDir) || !info.IsDir() || info.Mode().Perm() != 0700 {
		return fmt.Errorf("owned-cohort state must be an existing private absolute directory")
	}
	verifySnapshot := func() error {
		actual, err := readExistingFeedEvidence(config.MappingSnapshotPath)
		if err != nil {
			return err
		}
		expected, _ := base64.StdEncoding.DecodeString(config.Manifest.Mapping.BodyBase64)
		if !bytes.Equal(actual, expected) {
			return fmt.Errorf("mounted Mapping snapshot differs from frozen manifest")
		}
		return nil
	}
	if err := verifySnapshot(); err != nil {
		return err
	}
	frozenConfig := config
	frozenConfig.Phase = ""
	frozenBytes, _ := json.Marshal(frozenConfig)
	frozenBytes = append(frozenBytes, '\n')
	manifestPath := filepath.Join(config.StateDir, "manifest.json")
	if err := writeExistingFeedEvidence(manifestPath, frozenConfig); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		saved, readErr := readExistingFeedEvidence(manifestPath)
		if readErr != nil || !bytes.Equal(saved, frozenBytes) {
			return fmt.Errorf("owned-cohort saved configuration is partial, corrupt or changed")
		}
	}
	for index := 0; index < 6; index++ {
		prefix := filepath.Join(config.StateDir, strconv.Itoa(index))
		attempt, attemptErr := readExistingFeedEvidence(prefix + ".attempt.json")
		result, resultErr := readExistingFeedEvidence(prefix + ".result.json")
		if errors.Is(attemptErr, os.ErrNotExist) && errors.Is(resultErr, os.ErrNotExist) {
			if index < first {
				return fmt.Errorf("owned-cohort preceding phase is incomplete")
			}
			continue
		}
		if attemptErr != nil || resultErr != nil {
			return fmt.Errorf("owned-cohort attempt is unresolved or journal is unreadable")
		}
		expected, _, _ := manifest.Prepare(index)
		encoded, _ := json.Marshal(expected)
		var saved existingFeedCohortResult
		if !bytes.Equal(attempt, append(encoded, '\n')) || json.Unmarshal(result, &saved) != nil || saved.BodySHA256 != expected.Request.BodySHA256 || saved.Result.Class != InjectionForwarded || saved.Result.StatusCode != 200 || saved.Error != "" {
			return fmt.Errorf("owned-cohort attempt is corrupt or not known forwarded")
		}
		var response struct {
			Status string `json:"status"`
		}
		canonicalResult, _ := json.Marshal(saved)
		if !bytes.Equal(result, append(canonicalResult, '\n')) || json.Unmarshal(saved.Result.Body, &response) != nil || response.Status != "ok" {
			return fmt.Errorf("owned-cohort result is corrupt or conflicts with forwarding")
		}
		if index >= first {
			return fmt.Errorf("owned-cohort phase already attempted or out of order")
		}
	}
	for index := first; index <= last; index++ {
		if err := verifySnapshot(); err != nil {
			return err
		}
		prefix := filepath.Join(config.StateDir, strconv.Itoa(index))
		var recorded ExistingFeedRequestEvidence
		result, sendErr := input.SendRecorded(ctx, index, func(e ExistingFeedRequestEvidence) error {
			if err := writeExistingFeedEvidence(prefix+".attempt.json", e); err != nil {
				return err
			}
			recorded = e
			return nil
		})
		if recorded.Request.BodySHA256 == "" {
			return sendErr
		}
		saved := existingFeedCohortResult{BodySHA256: recorded.Request.BodySHA256, Result: result}
		if sendErr != nil {
			saved.Error = sendErr.Error()
		}
		if err := writeExistingFeedEvidence(prefix+".result.json", saved); err != nil {
			return errors.Join(sendErr, err)
		}
		if sendErr != nil {
			return sendErr
		}
		if result.Class != InjectionForwarded {
			return fmt.Errorf("owned-cohort forwarding is unconfirmed")
		}
	}
	return nil
}

func readExistingFeedEvidence(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("owned-cohort evidence must be a regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, 2*maxRecordedInjectionBytes+1))
	if err == nil && len(body) > 2*maxRecordedInjectionBytes {
		return nil, fmt.Errorf("owned-cohort evidence exceeds 2 MiB")
	}
	return body, err
}

// Exclusive partial files are intentionally retained: they prohibit replay after interruption.
func writeExistingFeedEvidence(path string, value any) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	body = append(body, '\n')
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.Write(body); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func TestExistingFeedOwnedCohort(t *testing.T) {
	path := os.Getenv("CORRTEST_EXISTING_FEED_LAB_CONFIG")
	if path == "" {
		t.Skip("requires explicitly owned cohort configuration")
	}
	body, err := readExistingFeedEvidence(path)
	if err != nil {
		t.Fatal(err)
	}
	var config existingFeedCohortConfig
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatal("trailing cohort configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := runExistingFeedOwnedCohort(ctx, config); err != nil {
		t.Fatal(err)
	}
}
