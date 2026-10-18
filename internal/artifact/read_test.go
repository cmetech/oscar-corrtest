package artifact

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReadVerifiedBoundedCapturesExactBytes(t *testing.T) {
	s, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.Write(context.Background(), "crt_01234567000000000000000000", "installed/original/attempt.json", "application/json", bytes.NewBufferString(`{"original":true}`))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := s.ReadVerifiedBounded(context.Background(), m, 64)
	if err != nil || string(raw) != `{"original":true}` {
		t.Fatalf("%q %v", raw, err)
	}
	for _, kind := range []string{"bound", "size", "hash", "traversal", "cancelled", "growth", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			copy := m
			ctx := context.Background()
			limit := int64(64)
			switch kind {
			case "bound":
				limit = 1
			case "size":
				copy.ByteSize++
			case "hash":
				copy.SHA256 = "wrong"
			case "traversal":
				copy.RelativePath = "../outside"
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "growth":
				path := filepath.Join(s.root, filepath.FromSlash(m.RelativePath))
				if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 65), 0600); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.WriteFile(path, raw, 0600) })
			case "symlink":
				path := filepath.Join(s.root, filepath.FromSlash(m.RelativePath))
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(t.TempDir(), "missing"), path); err != nil {
					t.Skip(err)
				}
				t.Cleanup(func() { _ = os.Remove(path); _ = os.WriteFile(path, raw, 0600) })
			}
			if b, err := s.ReadVerifiedBounded(ctx, copy, limit); err == nil || b != nil {
				t.Fatalf("unsafe/partial read returned %q %v", b, err)
			}
		})
	}
}
