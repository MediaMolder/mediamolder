// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// cmdJob implements the `mediamolder job` subcommand dispatcher.
func cmdJob(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		jobUsage()
		return nil
	}
	switch args[0] {
	case "submit":
		return cmdJobSubmit(args[1:])
	case "status":
		return cmdJobStatus(args[1:])
	case "cancel":
		return cmdJobCancel(args[1:])
	case "artifacts":
		return cmdJobArtifacts(args[1:])
	default:
		return fmt.Errorf("job: unknown subcommand %q\nRun 'mediamolder job help' for usage.", args[0])
	}
}

func jobUsage() {
	fmt.Print(`Usage: mediamolder job <subcommand> [flags]

Subcommands:
  submit     <config.json>   Submit a pipeline job to a remote server.
  status     <job-id>        Show status of a remote job.
  cancel     <job-id>        Cancel a running remote job.
  artifacts  <job-id>        List the output artifacts of a completed remote job.

Common flags (all subcommands):
  --backend=URL    Remote server base URL, e.g. https://myserver.example.com:8443
  --token=TOKEN    Bearer auth token (or set MEDIAMOLDER_TOKEN env var)
`)
}

// ---- submit ---------------------------------------------------------------

func cmdJobSubmit(args []string) error {
	fs := flag.NewFlagSet("job submit", flag.ContinueOnError)
	backend := fs.String("backend", "", "Remote server base URL (required)")
	token := fs.String("token", os.Getenv("MEDIAMOLDER_TOKEN"), "Bearer auth token")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *backend == "" {
		return errors.New("job submit: --backend is required")
	}
	if fs.NArg() < 1 {
		return errors.New("job submit: a config file path is required")
	}

	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("job submit: read config: %w", err)
	}

	resp, err := apiRequest(http.MethodPost, *backend+"/v1/jobs", *token, "application/json", data)
	if err != nil {
		return fmt.Errorf("job submit: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("job submit: server returned %d: %s", resp.StatusCode, body)
	}
	// Print the response JSON (id, status_url, events_url).
	var pretty any
	if err := json.Unmarshal(body, &pretty); err != nil {
		fmt.Println(string(body))
		return nil
	}
	out, _ := json.MarshalIndent(pretty, "", "  ")
	fmt.Println(string(out))
	return nil
}

// ---- status ---------------------------------------------------------------

func cmdJobStatus(args []string) error {
	fs := flag.NewFlagSet("job status", flag.ContinueOnError)
	backend := fs.String("backend", "", "Remote server base URL (required)")
	token := fs.String("token", os.Getenv("MEDIAMOLDER_TOKEN"), "Bearer auth token")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *backend == "" {
		return errors.New("job status: --backend is required")
	}
	if fs.NArg() < 1 {
		return errors.New("job status: a job-id is required")
	}
	jobID := fs.Arg(0)

	resp, err := apiRequest(http.MethodGet, *backend+"/v1/jobs/"+jobID, *token, "", nil)
	if err != nil {
		return fmt.Errorf("job status: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("job status: server returned %d: %s", resp.StatusCode, body)
	}
	var pretty any
	if err := json.Unmarshal(body, &pretty); err != nil {
		fmt.Println(string(body))
		return nil
	}
	out, _ := json.MarshalIndent(pretty, "", "  ")
	fmt.Println(string(out))
	return nil
}

// ---- cancel ---------------------------------------------------------------

func cmdJobCancel(args []string) error {
	fs := flag.NewFlagSet("job cancel", flag.ContinueOnError)
	backend := fs.String("backend", "", "Remote server base URL (required)")
	token := fs.String("token", os.Getenv("MEDIAMOLDER_TOKEN"), "Bearer auth token")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *backend == "" {
		return errors.New("job cancel: --backend is required")
	}
	if fs.NArg() < 1 {
		return errors.New("job cancel: a job-id is required")
	}
	jobID := fs.Arg(0)

	resp, err := apiRequest(http.MethodDelete, *backend+"/v1/jobs/"+jobID, *token, "", nil)
	if err != nil {
		return fmt.Errorf("job cancel: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("job cancel: server returned %d: %s", resp.StatusCode, body)
	}
	fmt.Println("canceled")
	return nil
}

// ---- artifacts ------------------------------------------------------------

func cmdJobArtifacts(args []string) error {
	fs := flag.NewFlagSet("job artifacts", flag.ContinueOnError)
	backend := fs.String("backend", "", "Remote server base URL (required)")
	token := fs.String("token", os.Getenv("MEDIAMOLDER_TOKEN"), "Bearer auth token")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if *backend == "" {
		return errors.New("job artifacts: --backend is required")
	}
	if fs.NArg() < 1 {
		return errors.New("job artifacts: a job-id is required")
	}
	jobID := fs.Arg(0)

	resp, err := apiRequest(http.MethodGet, *backend+"/v1/jobs/"+jobID+"/artifacts", *token, "", nil)
	if err != nil {
		return fmt.Errorf("job artifacts: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("job artifacts: server returned %d: %s", resp.StatusCode, body)
	}
	var result struct {
		Artifacts []string `json:"artifacts"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		fmt.Println(string(body))
		return nil
	}
	for _, a := range result.Artifacts {
		fmt.Println(a)
	}
	return nil
}

// ---- shared HTTP helper ---------------------------------------------------

// apiRequest performs a single HTTP request with optional Bearer auth.
func apiRequest(method, url, token, contentType string, body []byte) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		bodyReader = strings.NewReader(string(body))
	}
	req, err := http.NewRequest(method, url, bodyReader)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return http.DefaultClient.Do(req)
}
