package testpack

import "testing"

func TestCanonicalPythonParity(t *testing.T) {
	for _, pair := range [][2]string{{`{"é":"\u2028\u2029<>&","😀":9007199254740991,"a":-9007199254740991}`, "{\"a\":-9007199254740991,\"é\":\"\u2028\u2029<>&\",\"😀\":9007199254740991}\n"}, {`{"v":"\u0000\b\t\n\f\r/\\\""}`, "{\"v\":\"\\u0000\\b\\t\\n\\f\\r/\\\\\\\"\"}\n"}} {
		got, e := canonicalJSON([]byte(pair[0]))
		if e != nil || string(got) != pair[1] {
			t.Fatalf("canonical %q %v expected %q", got, e, pair[1])
		}
	}
	for _, raw := range []string{`{"x":1,"x":2}`, `{"x":1e2}`, `{"x":1.0}`, `{"x":9007199254740992}`, `{"x":"\ud800"}`, `{"x":"\udc00"}`, "{\"x\":\"\xff\"}"} {
		if _, e := canonicalJSON([]byte(raw)); e == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}

func TestCanonicalLiteralBackslashIsNotUnicodeEscape(t *testing.T) {
	raw := []byte(`{"actual":"\u2028\u2029","literal":"\\u2028\\u2029","pair":"\ud83d\ude00"}`)
	got, e := CanonicalJSON(raw)
	want := "{\"actual\":\"\u2028\u2029\",\"literal\":\"\\\\u2028\\\\u2029\",\"pair\":\"😀\"}\n"
	if e != nil || string(got) != want {
		t.Fatalf("got %q error %v want %q", got, e, want)
	}
}
