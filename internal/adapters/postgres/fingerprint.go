package postgres

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// Fingerprint returns the SHA-256 of a canonicalised JSON request body.
func Fingerprint(body []byte) ([32]byte, error) {
	canonical, err := canonicalJSON(body)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(canonical), nil
}

// canonicalJSON re-encodes a JSON document in a stable form.
func canonicalJSON(body []byte) ([]byte, error) {
	if len(bytes.TrimSpace(body)) == 0 {
		// An empty body is a legitimate request shape for endpoints whose input
		// is entirely in the path. Canonicalise it to a stable literal rather
		// than failing.
		return []byte("null"), nil
	}

	decoder := json.NewDecoder(bytes.NewReader(body))

	// UseNumber is important so large integer amounts are not rounded through float64.
	decoder.UseNumber()

	var document any
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("idempotency: request body is not valid JSON: %w", err)
	}
	// Reject trailing content, so that `{"a":1}{"a":2}` is an error rather than
	// silently fingerprinting only the first document.
	if decoder.More() {
		return nil, fmt.Errorf("idempotency: request body has trailing content after the JSON document")
	}

	canonical, err := json.Marshal(document)
	if err != nil {
		return nil, fmt.Errorf("idempotency: re-encoding request body: %w", err)
	}
	return canonical, nil
}
