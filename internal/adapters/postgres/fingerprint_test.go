package postgres

import (
	"encoding/json"
	"testing"
)

func TestFingerprintIgnoresKeyOrderAndWhitespace(t *testing.T) {
	variants := [][]byte{
		[]byte(`{"currency":"EUR","reference":"order_4471","amount":25000}`),
		[]byte(`{"amount":25000,"currency":"EUR","reference":"order_4471"}`),
		[]byte("{\n  \"reference\": \"order_4471\",\n  \"amount\": 25000,\n  \"currency\": \"EUR\"\n}"),
		[]byte(`   {"amount":25000,"reference":"order_4471","currency":"EUR"}   `),
	}

	want, err := Fingerprint(variants[0])
	if err != nil {
		t.Fatalf("Fingerprint: %v", err)
	}

	for i, variant := range variants[1:] {
		got, err := Fingerprint(variant)
		if err != nil {
			t.Fatalf("Fingerprint(variant %d): %v", i+1, err)
		}
		if got != want {
			t.Errorf("variant %d fingerprinted differently; retries would be rejected as conflicts", i+1)
		}
	}
}

func TestFingerprintIsNestedOrderInsensitive(t *testing.T) {
	a := []byte(`{"entries":[{"direction":"DEBIT","amount":100},{"amount":100,"direction":"CREDIT"}]}`)
	b := []byte(`{"entries":[{"amount":100,"direction":"DEBIT"},{"direction":"CREDIT","amount":100}]}`)

	fa, err := Fingerprint(a)
	if err != nil {
		t.Fatalf("Fingerprint(a): %v", err)
	}
	fb, err := Fingerprint(b)
	if err != nil {
		t.Fatalf("Fingerprint(b): %v", err)
	}
	if fa != fb {
		t.Error("nested object key order changed the fingerprint")
	}
}

// Array order is significant and must NOT be canonicalised away: swapping two
// entries changes which account is debited.
func TestFingerprintRespectsArrayOrder(t *testing.T) {
	a := []byte(`{"entries":[{"account":"a"},{"account":"b"}]}`)
	b := []byte(`{"entries":[{"account":"b"},{"account":"a"}]}`)

	fa, err := Fingerprint(a)
	if err != nil {
		t.Fatalf("Fingerprint(a): %v", err)
	}
	fb, err := Fingerprint(b)
	if err != nil {
		t.Fatalf("Fingerprint(b): %v", err)
	}
	if fa == fb {
		t.Error("reordering an array did not change the fingerprint")
	}
}

// The test that justifies decoder.UseNumber(). These two amounts differ by one
// minor unit and are both beyond float64's exact integer range.
func TestFingerprintPreservesLargeIntegers(t *testing.T) {
	a := []byte(`{"amount":9007199254740993}`)
	b := []byte(`{"amount":9007199254740992}`)

	fa, err := Fingerprint(a)
	if err != nil {
		t.Fatalf("Fingerprint(a): %v", err)
	}
	fb, err := Fingerprint(b)
	if err != nil {
		t.Fatalf("Fingerprint(b): %v", err)
	}
	if fa == fb {
		t.Error("amounts differing by one minor unit fingerprinted identically: float64 rounding")
	}

	// And the canonical form keeps the digits rather than switching to
	// scientific notation, which is what float64 marshalling would do.
	canonical, err := canonicalJSON(a)
	if err != nil {
		t.Fatalf("canonicalJSON: %v", err)
	}
	if string(canonical) != `{"amount":9007199254740993}` {
		t.Errorf("canonical form = %s, want the exact integer preserved", canonical)
	}
}

func TestFingerprintDiffersForDifferentBodies(t *testing.T) {
	a := []byte(`{"amount":25000}`)
	b := []byte(`{"amount":25001}`)

	fa, err := Fingerprint(a)
	if err != nil {
		t.Fatalf("Fingerprint(a): %v", err)
	}
	fb, err := Fingerprint(b)
	if err != nil {
		t.Fatalf("Fingerprint(b): %v", err)
	}
	if fa == fb {
		t.Error("different amounts fingerprinted identically")
	}
}

func TestFingerprintRejectsInvalidJSON(t *testing.T) {
	for _, body := range [][]byte{
		[]byte(`{"amount":`),
		[]byte(`{"a":1}{"a":2}`),
		[]byte(`not json at all`),
	} {
		if _, err := Fingerprint(body); err == nil {
			t.Errorf("Fingerprint(%q) succeeded, want an error", body)
		}
	}
}

func TestFingerprintAcceptsEmptyBody(t *testing.T) {
	got, err := Fingerprint(nil)
	if err != nil {
		t.Fatalf("Fingerprint(nil): %v", err)
	}
	empty, err := Fingerprint([]byte("   "))
	if err != nil {
		t.Fatalf("Fingerprint(spaces): %v", err)
	}
	if got != empty {
		t.Error("nil and whitespace-only bodies fingerprinted differently")
	}
}

// Guard the assumption the whole design rests on: encoding/json sorts map keys.
func TestEncodingJSONSortsMapKeys(t *testing.T) {
	encoded, err := json.Marshal(map[string]int{"zebra": 1, "alpha": 2, "monkey": 3})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := `{"alpha":2,"monkey":3,"zebra":1}`; string(encoded) != want {
		t.Errorf("Marshal = %s, want %s: canonicalisation depends on sorted map keys", encoded, want)
	}
}
