// Command consumer-audit runs the consumer audit: it walks meshery/schemas, joins
// it against handler implementations in meshery/meshery and meshery-cloud,
// and reports per-endpoint coverage and implementation drift.
//
// Usage:
//
//	go run ./cmd/consumer-audit
//	go run ./cmd/consumer-audit --meshery-repo=../meshery --cloud-repo=../meshery-cloud
//	go run ./cmd/consumer-audit --sheet-id=<id> --credentials=<path>      # reconcile and update the canonical sheet
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/meshery/schemas/validation"
	"github.com/rodaine/table"
)

func main() {
	mesheryRepo := flag.String("meshery-repo", "", "Path to a meshery/meshery checkout (Gorilla router)")
	cloudRepo := flag.String("cloud-repo", "", "Path to a meshery-cloud checkout (Echo router)")
	verbose := flag.Bool("verbose", false, "Print per-endpoint Schema-only and Consumer-only lists")
	sheetID := flag.String("sheet-id", "", "Google Sheet ID to reconcile against and update")
	credentials := flag.String("credentials", "", "Path to Google service-account JSON credentials (required with --sheet-id)")
	flag.Parse()

	rootDir, err := findRepoRoot()
	if err != nil {
		fmt.Fprintf(os.Stderr, "consumer-audit: could not find repository root: %v\n", err)
		os.Exit(1)
	}

	if (*sheetID == "") != (*credentials == "") {
		fmt.Fprintln(os.Stderr, "consumer-audit: --sheet-id and --credentials must be provided together")
		os.Exit(1)
	}

	opts := validation.ConsumerAuditOptions{
		RootDir:     rootDir,
		MesheryRepo: *mesheryRepo,
		CloudRepo:   *cloudRepo,
	}

	if *sheetID != "" {
		creds, err := os.ReadFile(resolvePath(rootDir, *credentials))
		if err != nil {
			fmt.Fprintf(os.Stderr, "consumer-audit: read credentials: %v\n", err)
			os.Exit(1)
		}
		opts.SheetID = *sheetID
		opts.SheetsCredentials = creds
	}

	result, err := validation.RunConsumerAudit(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "consumer-audit: %v\n", err)
		os.Exit(1)
	}

	out := io.Writer(os.Stdout)
	printAuditReport(out, result)
	fmt.Fprintln(out)
	printActionItems(out, result)

	if *verbose {
		printVerbose(out, result)
	}

	if len(result.Tracked) > 0 || len(result.NewDeletions) > 0 {
		fmt.Fprintln(out)
		printDiff(out, result.Tracked, result.NewDeletions)
	}
}

// findRepoRoot walks up from the current working directory looking for go.mod.
func findRepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found in any parent directory")
		}
		dir = parent
	}
}

func newTable(out io.Writer, title string, cols ...any) table.Table {
	fmt.Fprintln(out, title)
	t := table.New(cols...)
	t.WithWriter(out)
	return t
}

// printAuditReport renders the top-level summary (Table 1 in the spec): one
// row per audit dimension, one column per source.
func printAuditReport(out io.Writer, result *validation.ConsumerAuditResult) {
	s := result.Summary

	cell := func(n int, enabled bool) any {
		if !enabled {
			return "-"
		}
		return n
	}

	t := newTable(out, "Audit Report",
		"Category", "Schema", "Meshery", "Cloud")
	t.AddRow("Total Endpoints", s.SchemaEndpoints, s.MesheryEndpoints, s.CloudEndpoints)
	t.AddRow("Schema Backed", "-", s.Meshery.BackedTrue, s.Cloud.BackedTrue)
	t.AddRow("Schema Only (Not Implemented)",
		s.SchemaOnly,
		cell(s.SchemaOnlyMeshery, s.MesheryEndpoints > 0),
		cell(s.SchemaOnlyCloud, s.CloudEndpoints > 0))
	t.AddRow("Consumer Only", "-", s.ConsumerOnlyMeshery, s.ConsumerOnlyCloud)
	t.Print()
}

// printActionItems renders the high-priority drift counters (Table 2).
func printActionItems(out io.Writer, result *validation.ConsumerAuditResult) {
	s := result.Summary
	t := newTable(out, "Action Items", "Item", "Meshery", "Cloud")
	t.AddRow("Not Schema Driven", s.Meshery.DriftTrue, s.Cloud.DriftTrue)
	t.AddRow("x-annotation mismatch",
		s.AnnotationMismatchMeshery, s.AnnotationMismatchCloud)
	t.AddRow("Schema-Driven = Not Audited",
		s.Meshery.DrivenNotAud, s.Cloud.DrivenNotAud)
	t.Print()
}

func printVerbose(out io.Writer, result *validation.ConsumerAuditResult) {
	if result == nil || result.Match == nil {
		return
	}
	if len(result.Match.SchemaOnly) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Schema-only endpoints (defined but no handler):")
		for _, ep := range result.Match.SchemaOnly {
			fmt.Fprintf(out, "  %-7s %s   (%s)\n", ep.Method, ep.Path, ep.SourceFile)
		}
	}
	if len(result.Match.ConsumerOnly) > 0 {
		fmt.Fprintln(out)
		fmt.Fprintln(out, "Consumer-only endpoints (registered but no schema):")
		for _, c := range result.Match.ConsumerOnly {
			fmt.Fprintf(out, "  %-7s %s   (%s, %s)\n", c.Method, c.Path, c.Repo, c.HandlerName)
		}
	}
}

// printDiff prints a per-endpoint reconciliation log. For changed rows it
// shows each affected column as `column: "old" -> "new"`. The Notes column
// is intentionally skipped — it is derived, not signal.
func printDiff(out io.Writer, tracked []validation.TrackedEndpoint, deletions []validation.DeletionRecord) {
	var added, changed []validation.TrackedEndpoint
	for _, t := range tracked {
		switch t.State {
		case validation.StateNew:
			added = append(added, t)
		case validation.StateChanged:
			changed = append(changed, t)
		}
	}

	if len(added) == 0 && len(changed) == 0 && len(deletions) == 0 {
		fmt.Fprintln(out, "Reconciliation: no changes since last run")
		return
	}

	fmt.Fprintln(out, "Reconciliation: diff against previous state")

	sortTracked := func(rows []validation.TrackedEndpoint) {
		sort.Slice(rows, func(i, j int) bool {
			if rows[i].Row.Endpoint != rows[j].Row.Endpoint {
				return rows[i].Row.Endpoint < rows[j].Row.Endpoint
			}
			return rows[i].Row.Method < rows[j].Row.Method
		})
	}

	if len(added) > 0 {
		sortTracked(added)
		fmt.Fprintf(out, "\n  Added (%d):\n", len(added))
		for _, t := range added {
			fmt.Fprintf(out, "    %-7s %s\n", t.Row.Method, t.Row.Endpoint)
		}
	}

	if len(changed) > 0 {
		sortTracked(changed)
		fmt.Fprintf(out, "\n  Changed (%d):\n", len(changed))
		for _, t := range changed {
			fmt.Fprintf(out, "    %-7s %s\n", t.Row.Method, t.Row.Endpoint)
			for _, col := range t.Row.Metadata.ChangedColumns {
				if col == "Notes" {
					continue
				}
				prev := ""
				if t.Prev != nil {
					prev = validation.AuditedColumnValue(*t.Prev, col)
				}
				cur := validation.AuditedColumnValue(t.Row, col)
				fmt.Fprintf(out, "      %s: %q -> %q\n", col, prev, cur)
			}
		}
	}

	if len(deletions) > 0 {
		sorted := append([]validation.DeletionRecord(nil), deletions...)
		sort.Slice(sorted, func(i, j int) bool {
			if sorted[i].Endpoint != sorted[j].Endpoint {
				return sorted[i].Endpoint < sorted[j].Endpoint
			}
			return sorted[i].Method < sorted[j].Method
		})
		fmt.Fprintf(out, "\n  Removed (%d):\n", len(sorted))
		for _, d := range sorted {
			fmt.Fprintf(out, "    %-7s %s  %s\n", d.Method, d.Endpoint, d.RemovedAt)
		}
	}
}

func resolvePath(rootDir, path string) string {
	if path == "" || path == "-" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(rootDir, path)
}
