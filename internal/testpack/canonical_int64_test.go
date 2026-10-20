package testpack

import (
	"bytes"
	"testing"
)

func TestCanonicalJSONInt64PreservesPythonWireContract(t *testing.T) {
	raw := []byte(`{"z":9223372036854775807,"a":"é<>&\u2028\u2029😀","n":-9223372036854775808}`)
	want := []byte("{\"a\":\"é<>&\u2028\u2029😀\",\"n\":-9223372036854775808,\"z\":9223372036854775807}")
	got, err := CanonicalJSONInt64(raw)
	if err != nil || !bytes.Equal(got, want) {
		t.Fatalf("got %s error %v", got, err)
	}
	if _, err = CanonicalJSON(raw); err == nil {
		t.Fatal("legacy kit unexpectedly permits unsafe integer")
	}
	legacy, err := CanonicalJSON([]byte(`{"x":1}`))
	if err != nil || string(legacy) != "{\"x\":1}\n" {
		t.Fatal("legacy LF contract changed")
	}
	for _, raw := range []string{`{"a":1,"a":2}`, `{"a":9223372036854775808}`, `{"a":1.0}`, `{"a":"\ud800"}`, `{"a":1} null`} {
		if _, err := CanonicalJSONInt64([]byte(raw)); err == nil {
			t.Fatalf("accepted invalid %s", raw)
		}
	}
}
