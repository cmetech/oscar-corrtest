package scenario

import (
	"bytes"
	"reflect"
	"testing"
)

func TestEveryBuiltinCanonicalYAMLRoundTrips(t *testing.T) {
	for _, builtin := range AllBuiltins() {
		t.Run(builtin.Pattern, func(t *testing.T) {
			first, err := BuiltinSource(builtin.Pattern)
			if err != nil {
				t.Fatal(err)
			}
			decoded, err := Decode(bytes.NewReader(first))
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(decoded, builtin) {
				t.Fatalf("round trip differs\nsource:\n%s\ngot=%+v\nwant=%+v", first, decoded, builtin)
			}
			second, err := Encode(decoded)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(first, second) {
				t.Fatalf("encoding unstable\nfirst:\n%s\nsecond:\n%s", first, second)
			}
			if !bytes.HasSuffix(first, []byte("\n")) || bytes.Contains(first, []byte("\r")) || bytes.Contains(first, []byte("&id")) || bytes.Contains(first, []byte("*id")) {
				t.Fatalf("canonical formatting violated: %q", first)
			}
		})
	}
}

func TestBuiltinSourceRejectsUnknownPattern(t *testing.T) {
	if _, err := BuiltinSource("unknown"); err == nil {
		t.Fatal("unknown pattern accepted")
	}
}
