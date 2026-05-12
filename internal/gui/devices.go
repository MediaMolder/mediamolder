// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
)

// handleListDevices implements GET /api/devices?format=<fmt>.
//
// It enumerates capture devices for the given libavdevice input format.
// When format is omitted the handler picks the platform default:
//   - Windows → "dshow"
//   - macOS   → "avfoundation"
//   - Linux   → "v4l2"
//
// Enumeration runs under a 2-second context timeout because Windows dshow
// can block indefinitely on an in-use device.
func handleListDevices(w http.ResponseWriter, r *http.Request) {
	format := r.URL.Query().Get("format")
	if format == "" {
		format = defaultDeviceFormat()
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	type result struct {
		devices []av.DeviceInfo
		err     error
	}
	ch := make(chan result, 1)
	go func() {
		d, err := av.ListDevices(format)
		ch <- result{d, err}
	}()

	var devices []av.DeviceInfo
	select {
	case res := <-ch:
		if res.err != nil {
			http.Error(w, res.err.Error(), http.StatusInternalServerError)
			return
		}
		devices = res.devices
	case <-ctx.Done():
		http.Error(w, "device enumeration timed out", http.StatusGatewayTimeout)
		return
	}

	if devices == nil {
		devices = []av.DeviceInfo{} // return [] not null
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(devices)
}

// defaultDeviceFormat returns the conventional libavdevice input format name
// for the current operating system.
func defaultDeviceFormat() string {
	switch runtime.GOOS {
	case "windows":
		return "dshow"
	case "darwin":
		return "avfoundation"
	default:
		return "v4l2"
	}
}
