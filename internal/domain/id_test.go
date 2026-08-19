package domain

import (
	"bytes"
	"errors"
	"regexp"
	"testing"
)

func TestNewRunIDUsesFixedCrockfordEncoding(t *testing.T) {
	input := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	first, err := NewRunID(bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	second, err := NewRunID(bytes.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("determinism: %q != %q", first, second)
	}
	if !regexp.MustCompile(`^crt_[0-9A-HJKMNP-TV-Z]{26}$`).MatchString(first.String()) {
		t.Fatalf("id=%q", first)
	}
	if !regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{8}$`).MatchString(first.Short()) {
		t.Fatalf("short=%q", first.Short())
	}
	if first.Short() != first.String()[4:12] {
		t.Fatalf("short=%q id=%q", first.Short(), first)
	}
}

func TestNewRunIDReadsAllEntropy(t *testing.T) {
	if _, err := NewRunID(errorReader{}); err == nil {
		t.Fatal("NewRunID() error=nil")
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errors.New("entropy unavailable") }
