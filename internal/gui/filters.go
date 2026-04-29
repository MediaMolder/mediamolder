// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/MediaMolder/MediaMolder/av"
)

// Filter option enumeration is a cgo + AVOption walk; cache the answer
// for the process lifetime since it only depends on the linked
// libavfilter build.
var (
	filterOptionsCache   = map[string]av.FilterOptionsInfo{}
	filterOptionsCacheMu sync.RWMutex
)

// handleFilterOptions serves GET /api/filters/{name}/options.
//
// Response body is the av.FilterOptionsInfo JSON: filter name +
// description + every AVOption on the filter's private class, with
// type, range, default, and named constants. The frontend uses this
// to render typed, validated controls in the Inspector.
func handleFilterOptions(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("filter name is required"))
		return
	}

	filterOptionsCacheMu.RLock()
	cached, ok := filterOptionsCache[name]
	filterOptionsCacheMu.RUnlock()
	if ok {
		writeFilterOptions(w, cached)
		return
	}

	info, err := av.FilterOptionsByName(name)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err)
		return
	}
	annotateExpressionOptions(&info)

	filterOptionsCacheMu.Lock()
	filterOptionsCache[name] = info
	filterOptionsCacheMu.Unlock()

	writeFilterOptions(w, info)
}

// annotateExpressionOptions stamps the curated `Expression` /
// `Variables` fields onto every option that the filterExprOptions
// registry marks as expression-typed (Wave 5 #19). The variable list
// is the same one served by the eval-expression endpoint, so the
// frontend's syntax-highlight + cookbook + live-validation flow has
// a single source of truth.
func annotateExpressionOptions(info *av.FilterOptionsInfo) {
	vars := FilterExprVariables(info.Name)
	for i := range info.Options {
		opt := &info.Options[i]
		if IsExpressionOption(info.Name, opt.Name) {
			opt.Expression = true
			opt.Variables = vars
		}
	}
}

func writeFilterOptions(w http.ResponseWriter, info av.FilterOptionsInfo) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}
