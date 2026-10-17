//go:build !linux && !darwin

package testpack

import (
	"errors"
	"strings"
	"testing"
)

func TestUnsupportedSnapshotIsExplicit(t *testing.T) {
	k, e := Load("unused-no-file-access", strings.Repeat("0", 64))
	if k != nil || !errors.Is(e, ErrUnsupportedPlatform) {
		t.Fatalf("kit=%v error=%v", k, e)
	}
}
