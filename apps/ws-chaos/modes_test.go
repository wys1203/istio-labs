package main

import (
	"encoding/json"
	"testing"
)

func TestParseMode_Defaults(t *testing.T) {
	m, err := parseMode([]byte(`{"mode":"normal"}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Mode != ModeNormal {
		t.Fatalf("expected normal, got %s", m.Mode)
	}
}

func TestParseMode_RandomRSTRatioBounds(t *testing.T) {
	cases := []struct {
		body  string
		ok    bool
		ratio float64
	}{
		{`{"mode":"random-rst","params":{"ratio":0.3}}`, true, 0.3},
		{`{"mode":"random-rst","params":{"ratio":2.0}}`, false, 0},
		{`{"mode":"random-rst","params":{"ratio":-0.1}}`, false, 0},
	}
	for _, c := range cases {
		m, err := parseMode([]byte(c.body))
		if c.ok {
			if err != nil {
				t.Errorf("body %q: unexpected err %v", c.body, err)
			}
			if m.Params.Ratio != c.ratio {
				t.Errorf("body %q: ratio = %v, want %v", c.body, m.Params.Ratio, c.ratio)
			}
		} else if err == nil {
			t.Errorf("body %q: expected err, got nil", c.body)
		}
	}
}

func TestParseMode_DropAfterSeconds(t *testing.T) {
	m, err := parseMode([]byte(`{"mode":"drop-after","params":{"seconds":30}}`))
	if err != nil {
		t.Fatal(err)
	}
	if m.Params.Seconds != 30 {
		t.Fatalf("seconds = %d", m.Params.Seconds)
	}
}

func TestParseMode_UnknownMode(t *testing.T) {
	_, err := parseMode([]byte(`{"mode":"banana"}`))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestModeJSONRoundtrip(t *testing.T) {
	m := Mode{Mode: ModeIdleHang}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := parseMode(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != ModeIdleHang {
		t.Fatalf("roundtrip mismatch: %s", got.Mode)
	}
}
