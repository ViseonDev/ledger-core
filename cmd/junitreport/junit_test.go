package main

import (
	"strings"
	"testing"
)

const gotestsumSample = `<?xml version="1.0" encoding="UTF-8"?>
<testsuites tests="4" failures="1" errors="0" time="1.284">
  <testsuite name="internal/domain/money" tests="3" failures="1" errors="0" skipped="0" time="0.812">
    <testcase classname="internal/domain/money" name="TestAdd" time="0.001"></testcase>
    <testcase classname="internal/domain/money" name="TestAllocate" time="0.002">
      <failure message="Failed" type="">allocate_test.go:88: parts sum to 99, want 100 - allocation lost money</failure>
    </testcase>
    <testcase classname="internal/domain/money" name="TestSkipped" time="0.000">
      <skipped message="short mode"></skipped>
    </testcase>
  </testsuite>
  <testsuite name="internal/domain/ledger" tests="1" failures="0" errors="0" skipped="0" time="0.472">
    <testcase classname="internal/domain/ledger" name="TestNewTransaction" time="0.003"></testcase>
  </testsuite>
</testsuites>`

func TestParseReader(t *testing.T) {
	summary, err := parseReader(strings.NewReader(gotestsumSample), "unit.xml")
	if err != nil {
		t.Fatalf("parseReader: %v", err)
	}

	if summary.Totals.Passed != 2 {
		t.Errorf("Passed = %d, want 2", summary.Totals.Passed)
	}
	if summary.Totals.Failed != 1 {
		t.Errorf("Failed = %d, want 1", summary.Totals.Failed)
	}
	if summary.Totals.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1", summary.Totals.Skipped)
	}
	if summary.DurationSeconds < 1.28 || summary.DurationSeconds > 1.29 {
		t.Errorf("DurationSeconds = %v, want ~1.284", summary.DurationSeconds)
	}

	if len(summary.Failures) != 1 {
		t.Fatalf("Failures has %d entries, want 1", len(summary.Failures))
	}
	got := summary.Failures[0]
	if got.Suite != "internal/domain/money" {
		t.Errorf("Suite = %q", got.Suite)
	}
	if got.Test != "TestAllocate" {
		t.Errorf("Test = %q", got.Test)
	}
	if !strings.Contains(got.Message, "allocation lost money") {
		t.Errorf("Message does not carry the assertion output: %q", got.Message)
	}
}

// Counts are recomputed from the cases rather than trusting attributes.
func TestParseIgnoresSuiteAttributeCounts(t *testing.T) {
	const lying = `<testsuites tests="999" failures="999">
  <testsuite name="x" tests="999" failures="999" time="0.1">
    <testcase classname="x" name="A" time="0.05"></testcase>
    <testcase classname="x" name="B" time="0.05"></testcase>
  </testsuite>
</testsuites>`

	summary, err := parseReader(strings.NewReader(lying), "x.xml")
	if err != nil {
		t.Fatalf("parseReader: %v", err)
	}
	if summary.Totals.Passed != 2 || summary.Totals.Failed != 0 {
		t.Errorf("totals = %+v, want 2 passed and 0 failed", summary.Totals)
	}
}

// An error element counts as a failure to prevent panics from masquerading as passes.
func TestParseTreatsErrorAsFailure(t *testing.T) {
	const panicked = `<testsuites>
  <testsuite name="x" time="0.1">
    <testcase classname="x" name="TestPanics" time="0.05">
      <error message="panic: runtime error: index out of range">goroutine 21 [running]:</error>
    </testcase>
  </testsuite>
</testsuites>`

	summary, err := parseReader(strings.NewReader(panicked), "x.xml")
	if err != nil {
		t.Fatalf("parseReader: %v", err)
	}
	if summary.Totals.Failed != 1 {
		t.Fatalf("Failed = %d, want 1: a panic must not report as a pass", summary.Totals.Failed)
	}
	if summary.Totals.Passed != 0 {
		t.Errorf("Passed = %d, want 0", summary.Totals.Passed)
	}
}

func TestTruncateBoundsTheMessage(t *testing.T) {
	long := strings.Repeat("x", maxFailureMessage+500)

	got := truncate(long, maxFailureMessage)
	if len(got) <= maxFailureMessage {
		t.Fatalf("truncate returned %d bytes, expected the limit plus a notice", len(got))
	}
	if !strings.Contains(got, "truncated") {
		t.Error("truncated output does not say so")
	}

	short := "fine"
	if truncate(short, maxFailureMessage) != short {
		t.Error("truncate modified a short message")
	}
}

func TestParseEmptySuite(t *testing.T) {
	summary, err := parseReader(strings.NewReader(`<testsuites></testsuites>`), "empty.xml")
	if err != nil {
		t.Fatalf("parseReader: %v", err)
	}
	if summary.Totals != (Totals{}) {
		t.Errorf("totals = %+v, want all zero", summary.Totals)
	}
}

