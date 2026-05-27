// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/MediaMolder/MediaMolder/internal/twelvelabs"
)

// twelvelabsClientFactory builds a *twelvelabs.Client from the request.
// Tests override this to redirect to an httptest server.
var twelvelabsClientFactory = defaultTwelvelabsClient

func defaultTwelvelabsClient(_ *http.Request) (*twelvelabs.Client, error) {
	key, err := twelvelabs.ResolveAPIKey("")
	if err != nil {
		return nil, err
	}
	return twelvelabs.New(key), nil
}

func registerTwelveLabsRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/twelvelabs/ping", handleTwelveLabsPing)
	mux.HandleFunc("GET /api/twelvelabs/indexes", handleTwelveLabsListIndexes)
	mux.HandleFunc("POST /api/twelvelabs/indexes", handleTwelveLabsCreateIndex)
	mux.HandleFunc("DELETE /api/twelvelabs/indexes/{id}", handleTwelveLabsDeleteIndex)
	mux.HandleFunc("POST /api/twelvelabs/search", handleTwelveLabsSearch)
}

// twelvelabsCtx returns a request-scoped context with a sane default deadline.
func twelvelabsCtx(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 30*time.Second)
}

func twelvelabsAuthError(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	writeJSONError(w, http.StatusUnauthorized, fmt.Errorf("twelvelabs auth: %w", err))
	return true
}

func handleTwelveLabsPing(w http.ResponseWriter, r *http.Request) {
	c, err := twelvelabsClientFactory(r)
	if twelvelabsAuthError(w, err) {
		return
	}
	ctx, cancel := twelvelabsCtx(r)
	defer cancel()
	if _, err := c.ListIndexes(ctx); err != nil {
		writeJSONError(w, http.StatusBadGateway, fmt.Errorf("twelvelabs ping: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"ok":true}`))
}

func handleTwelveLabsListIndexes(w http.ResponseWriter, r *http.Request) {
	c, err := twelvelabsClientFactory(r)
	if twelvelabsAuthError(w, err) {
		return
	}
	ctx, cancel := twelvelabsCtx(r)
	defer cancel()
	idxs, err := c.ListIndexes(ctx)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"indexes": idxs})
}

type createIndexRequest struct {
	Name   string                 `json:"name"`
	Models []twelvelabs.ModelSpec `json:"models"`
}

func handleTwelveLabsCreateIndex(w http.ResponseWriter, r *http.Request) {
	var body createIndexRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}
	if body.Name == "" {
		writeJSONError(w, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	if len(body.Models) == 0 {
		body.Models = []twelvelabs.ModelSpec{{Name: "marengo3.0"}}
	}
	c, err := twelvelabsClientFactory(r)
	if twelvelabsAuthError(w, err) {
		return
	}
	ctx, cancel := twelvelabsCtx(r)
	defer cancel()
	idx, err := c.CreateIndex(ctx, body.Name, body.Models)
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, idx)
}

func handleTwelveLabsDeleteIndex(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeJSONError(w, http.StatusBadRequest, errors.New("id is required"))
		return
	}
	c, err := twelvelabsClientFactory(r)
	if twelvelabsAuthError(w, err) {
		return
	}
	ctx, cancel := twelvelabsCtx(r)
	defer cancel()
	if err := c.DeleteIndex(ctx, id); err != nil {
		writeJSONError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": id})
}

type searchRequestBody struct {
	IndexID       string   `json:"index_id"`
	Query         string   `json:"query"`
	QueryMediaURL string   `json:"query_media_url"`
	SearchOptions []string `json:"search_options"`
	Threshold     string   `json:"threshold"`
	PageLimit     int      `json:"page_limit"`
}

func handleTwelveLabsSearch(w http.ResponseWriter, r *http.Request) {
	var body searchRequestBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("decode body: %w", err))
		return
	}
	if body.IndexID == "" {
		writeJSONError(w, http.StatusBadRequest, errors.New("index_id is required"))
		return
	}
	if body.Query == "" && body.QueryMediaURL == "" {
		writeJSONError(w, http.StatusBadRequest, errors.New("query or query_media_url is required"))
		return
	}
	if len(body.SearchOptions) == 0 {
		body.SearchOptions = []string{"visual", "audio"}
	}
	if body.Threshold == "" {
		body.Threshold = "medium"
	}
	c, err := twelvelabsClientFactory(r)
	if twelvelabsAuthError(w, err) {
		return
	}
	ctx, cancel := twelvelabsCtx(r)
	defer cancel()
	results, err := c.Search(ctx, twelvelabs.SearchRequest{
		IndexID:       body.IndexID,
		Query:         body.Query,
		QueryMediaURL: body.QueryMediaURL,
		SearchOptions: body.SearchOptions,
		Threshold:     body.Threshold,
		PageLimit:     body.PageLimit,
	})
	if err != nil {
		writeJSONError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"matches": results,
		"count":   len(results),
	})
}

// writeJSON is a small helper used by the twelvelabs handlers.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
