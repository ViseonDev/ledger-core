package main

import (
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// JUnit XML data structures for parsing gotestsum test outputs.

type testSuites struct {
	XMLName xml.Name    `xml:"testsuites"`
	Suites  []testSuite `xml:"testsuite"`
}

type testSuite struct {
	Name  string     `xml:"name,attr"`
	Time  float64    `xml:"time,attr"` //nolint:forbidigo // JUnit duration in seconds
	Cases []testCase `xml:"testcase"`
}

type testCase struct {
	ClassName string   `xml:"classname,attr"`
	Name      string   `xml:"name,attr"`
	Time      float64  `xml:"time,attr"` //nolint:forbidigo // JUnit duration in seconds
	Failure   *failure `xml:"failure"`
	Error     *failure `xml:"error"`
	Skipped   *skipped `xml:"skipped"`
}

type failure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Body    string `xml:",chardata"`
}

type skipped struct {
	Message string `xml:"message,attr"`
}

// Failure describes one failing test in the published payload.
type Failure struct {
	Suite   string `json:"suite"`
	Test    string `json:"test"`
	Message string `json:"message"`
}

// Totals counts test outcomes.
type Totals struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

// Summary is the normalized result of one or more JUnit files.
type Summary struct {
	Totals          Totals    `json:"totals"`
	DurationSeconds float64   `json:"duration_seconds"` //nolint:forbidigo // JUnit duration in seconds
	Failures        []Failure `json:"failures"`
}

// maxFailureMessage bounds how much of a failure message is published.
const maxFailureMessage = 2000

// maxPublishedFailures bounds how many failures are published.
const maxPublishedFailures = 50

// parseFiles reads every JUnit file and merges them into one summary.
func parseFiles(paths []string) (Summary, error) {
	var summary Summary

	for _, path := range paths {
		file, err := os.Open(filepath.Clean(path)) //nolint:gosec // CLI inputs
		if err != nil {
			return Summary{}, fmt.Errorf("opening %s: %w", path, err)
		}

		parsed, parseErr := parseReader(file, filepath.Base(path))
		closeErr := file.Close()
		if parseErr != nil {
			return Summary{}, fmt.Errorf("parsing %s: %w", path, parseErr)
		}
		if closeErr != nil {
			return Summary{}, fmt.Errorf("closing %s: %w", path, closeErr)
		}

		summary.Totals.Passed += parsed.Totals.Passed
		summary.Totals.Failed += parsed.Totals.Failed
		summary.Totals.Skipped += parsed.Totals.Skipped
		summary.DurationSeconds += parsed.DurationSeconds
		summary.Failures = append(summary.Failures, parsed.Failures...)
	}

	if len(summary.Failures) > maxPublishedFailures {
		summary.Failures = summary.Failures[:maxPublishedFailures]
	}
	return summary, nil
}

func parseReader(r io.Reader, sourceName string) (Summary, error) {
	var document testSuites

	if err := xml.NewDecoder(r).Decode(&document); err != nil {
		return Summary{}, err
	}

	var summary Summary
	for _, suite := range document.Suites {
		summary.DurationSeconds += suite.Time

		suiteName := suite.Name
		if suiteName == "" {
			suiteName = sourceName
		}

		for _, testcase := range suite.Cases {
			switch {
			case testcase.Failure != nil || testcase.Error != nil:
				summary.Totals.Failed++

				detail := testcase.Failure
				if detail == nil {
					detail = testcase.Error
				}
				summary.Failures = append(summary.Failures, Failure{
					Suite:   firstNonEmpty(testcase.ClassName, suiteName),
					Test:    testcase.Name,
					Message: truncate(failureText(detail), maxFailureMessage),
				})

			case testcase.Skipped != nil:
				summary.Totals.Skipped++

			default:
				summary.Totals.Passed++
			}
		}
	}
	return summary, nil
}

// failureText prefers the element body over the attribute for more complete assertion details.
func failureText(f *failure) string {
	body := strings.TrimSpace(f.Body)
	if body != "" {
		return body
	}
	if f.Message != "" {
		return f.Message
	}
	return f.Type
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func truncate(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "\n... truncated; see the uploaded artifact for full output"
}
