// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

// cmdValidate implements the `mediamolder validate` subcommand.
//
// Usage:
//
//	mediamolder validate [--json] [--strict] <config.json>
//
// Flags:
//
//	--json    Emit the full ValidationReport as JSON instead of human-readable text.
//	--strict  Treat WARNING-level issues as errors (exit code 1 instead of 2).
//
// Exit codes:
//
//	0  No issues found.
//	1  One or more ERROR-level issues found (or WARNING with --strict).
//	2  One or more WARNING-level issues found (no errors).
func cmdValidate(args []string) error {
	var jsonOut bool
	var strict bool
	var configPath string

	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		case "--strict":
			strict = true
		default:
			if configPath != "" {
				return fmt.Errorf("unexpected argument %q\nusage: mediamolder validate [--json] [--strict] <config.json>", a)
			}
			configPath = a
		}
	}

	if configPath == "" {
		return fmt.Errorf("usage: mediamolder validate [--json] [--strict] <config.json>")
	}

	cfg, err := pipeline.ParseConfigFile(configPath)
	if err != nil {
		return fmt.Errorf("parse config: %w", err)
	}

	report := pipeline.ValidateConfigStatic(cfg, nil)

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(report); err != nil {
			return err
		}
	} else {
		printReport(report)
	}

	// Determine exit code.
	if report.HasErrors {
		os.Exit(1)
	}
	if report.HasWarnings && strict {
		os.Exit(1)
	}
	if report.HasWarnings {
		os.Exit(2)
	}
	return nil
}

// printReport writes a human-readable summary to stdout/stderr.
func printReport(report *pipeline.ValidationReport) {
	if len(report.Issues) == 0 {
		fmt.Println("OK: no issues found")
		return
	}

	for _, iss := range report.Issues {
		var loc string
		if iss.Location != "" {
			loc = " [" + iss.Location + "]"
		}
		fmt.Printf("%s%s %s: %s\n", string(iss.Severity), loc, iss.Code, iss.Message)
		if iss.Suggestion != "" {
			fmt.Printf("  suggestion: %s\n", iss.Suggestion)
		}
	}

	fmt.Println()
	var errCount, warnCount int
	for _, iss := range report.Issues {
		switch iss.Severity {
		case pipeline.SeverityError:
			errCount++
		case pipeline.SeverityWarning:
			warnCount++
		}
	}
	fmt.Printf("summary: %d error(s), %d warning(s)\n", errCount, warnCount)
}
