// Package domain contains storage- and transport-independent harness models.
package domain

import (
	"fmt"
	"io"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// RunID is the full random authority identifier for one test run.
type RunID string

// NewRunID reads 128 bits of entropy and returns its fixed Crockford encoding.
func NewRunID(random io.Reader) (RunID, error) {
	var raw [16]byte
	if _, err := io.ReadFull(random, raw[:]); err != nil {
		return "", fmt.Errorf("generate run id: %w", err)
	}

	encoded := make([]byte, 0, 26)
	var accumulator uint32
	bits := 2 // Two leading zero bits make 128 bits fill 26 five-bit symbols.
	for _, value := range raw {
		accumulator = (accumulator << 8) | uint32(value)
		bits += 8
		for bits >= 5 {
			bits -= 5
			encoded = append(encoded, crockford[(accumulator>>bits)&31])
		}
	}
	return RunID("crt_" + string(encoded)), nil
}

// String returns the externally stored identifier.
func (id RunID) String() string { return string(id) }

// Short returns the display-only eight-character token.
func (id RunID) Short() string {
	value := string(id)
	if len(value) < 12 {
		return ""
	}
	return value[4:12]
}
