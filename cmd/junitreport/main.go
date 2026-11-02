// Command junitreport parses JUnit XML and publishes a normalized summary to the Portfolio Hub.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"
)

func printOut(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stdout, format, args...) //nolint:errcheck // CLI stdout
}

func printOutLn(msg string) {
	_, _ = fmt.Fprintln(os.Stdout, msg) //nolint:errcheck // CLI stdout
}

func printErr(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format, args...) //nolint:errcheck // CLI stderr
}

// payload is the request body sent to the Portfolio Hub.
type payload struct {
	ProjectID       string    `json:"project_id"`
	CommitSHA       string    `json:"commit_sha"`
	Branch          string    `json:"branch"`
	Timestamp       time.Time `json:"timestamp"`
	Totals          Totals    `json:"totals"`
	DurationSeconds float64   `json:"duration_seconds"` //nolint:forbidigo // JUnit duration in seconds
	Failures        []Failure `json:"failures"`
}

func main() {
	projectID := flag.String("project", "01-ledger-core", "project id as known to the hub")
	hubURL := flag.String("hub", os.Getenv("HUB_URL"), "hub base URL")
	token := flag.String("token", os.Getenv("HUB_TOKEN"), "per-project ingest token")
	commit := flag.String("commit", os.Getenv("GITHUB_SHA"), "commit SHA")
	branch := flag.String("branch", os.Getenv("GITHUB_REF_NAME"), "branch name")
	dryRun := flag.Bool("dry-run", false, "print the payload instead of sending it")
	flag.Parse()

	files := flag.Args()
	if len(files) == 0 {
		printErr("junitreport: no JUnit files given\n")
		os.Exit(2)
	}

	summary, err := parseFiles(files)
	if err != nil {
		printErr("junitreport: %v\n", err)
		os.Exit(1)
	}

	body := payload{
		ProjectID:       *projectID,
		CommitSHA:       *commit,
		Branch:          *branch,
		Timestamp:       time.Now().UTC(),
		Totals:          summary.Totals,
		DurationSeconds: summary.DurationSeconds,
		Failures:        summary.Failures,
	}

	printOut("junitreport: %d passed, %d failed, %d skipped in %.2fs\n",
		body.Totals.Passed, body.Totals.Failed, body.Totals.Skipped, body.DurationSeconds)
	for _, f := range body.Failures {
		printOut("  FAIL %s.%s\n", f.Suite, f.Test)
	}

	if *dryRun || *hubURL == "" || *token == "" {
		if !*dryRun {
			printOutLn("junitreport: hub URL or token not set; skipping publish")
		}
		encoded, encErr := json.MarshalIndent(body, "", "  ")
		if encErr != nil {
			printErr("junitreport: encoding payload: %v\n", encErr)
			os.Exit(1)
		}
		printOutLn(string(encoded))
		return
	}

	if pubErr := publish(*hubURL, *token, body); pubErr != nil {
		printErr("junitreport: publishing failed (not failing the build): %v\n", pubErr)
		return
	}
	printOutLn("junitreport: published")
}

func publish(hubURL, token string, body payload) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encoding payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, //nolint:gosec // operator-configured hub URL
		hubURL+"/api/test-results", bytes.NewReader(encoded))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req) //nolint:gosec // operator-configured hub URL
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // best-effort body close

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("hub returned %s", resp.Status)
	}
	return nil
}
