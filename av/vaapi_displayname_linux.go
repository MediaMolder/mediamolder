// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build linux

package av

// queryVAAPIDisplayName returns a human-readable name for the VAAPI render
// device at the given DRI path (e.g. "/dev/dri/renderD128").  It reads the
// PCI vendor and device IDs from sysfs and looks them up against a compact
// embedded table covering the most common desktop and laptop GPUs.
// Falls back to the DRI path string when the PCI IDs are unknown.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// pciVendorDevice is a (vendor_id, device_id) pair used as a map key.
type pciVendorDevice struct{ vendor, device uint16 }

// vapiKnownDevices is a concise lookup table of common iGPU and dGPU PCI IDs.
// Vendor IDs: 0x8086 Intel, 0x1002 AMD, 0x10de NVIDIA, 0x1af4 virtio-gpu.
// Device IDs are the most-deployed values; the fallback (vendor-only) path
// covers unknowns within a known vendor family.
var vaapiKnownDevices = map[pciVendorDevice]string{
	// ── Intel Arc / Xe (discrete) ─────────────────────────────────────────
	{0x8086, 0x56a0}: "Intel Arc A770",
	{0x8086, 0x56a1}: "Intel Arc A750",
	{0x8086, 0x56a5}: "Intel Arc A380",
	{0x8086, 0x56a6}: "Intel Arc A310",
	{0x8086, 0x5690}: "Intel Arc A770M",
	{0x8086, 0x5691}: "Intel Arc A730M",
	{0x8086, 0x5693}: "Intel Arc A550M",
	{0x8086, 0x5694}: "Intel Arc A370M",
	{0x8086, 0x5695}: "Intel Arc A350M",
	// ── Intel Gen12 (Tiger Lake / Iris Xe) ───────────────────────────────
	{0x8086, 0x9a49}: "Intel Iris Xe Graphics (Tiger Lake)",
	{0x8086, 0x9a60}: "Intel UHD Graphics (Tiger Lake)",
	{0x8086, 0x9a68}: "Intel UHD Graphics (Tiger Lake)",
	{0x8086, 0x9a78}: "Intel UHD Graphics (Tiger Lake)",
	// ── Intel Gen12.5 (Alder Lake) ───────────────────────────────────────
	{0x8086, 0x46a6}: "Intel UHD Graphics 770 (Alder Lake)",
	{0x8086, 0x4626}: "Intel Iris Xe Graphics (Alder Lake)",
	// ── Intel Gen12.7 (Raptor Lake) ──────────────────────────────────────
	{0x8086, 0xa780}: "Intel UHD Graphics 770 (Raptor Lake)",
	{0x8086, 0xa781}: "Intel UHD Graphics 730 (Raptor Lake)",
	// ── Intel Gen13 (Meteor Lake) ────────────────────────────────────────
	{0x8086, 0x7d45}: "Intel Arc Graphics (Meteor Lake)",
	{0x8086, 0x7d55}: "Intel Arc Graphics (Meteor Lake)",
	// ── AMD RDNA 3 (RX 7000) ─────────────────────────────────────────────
	{0x1002, 0x744c}: "AMD Radeon RX 7900 XTX",
	{0x1002, 0x745e}: "AMD Radeon RX 7900 XT",
	{0x1002, 0x7480}: "AMD Radeon RX 7600",
	{0x1002, 0x7483}: "AMD Radeon RX 7700 XT",
	{0x1002, 0x7489}: "AMD Radeon RX 7800 XT",
	// ── AMD RDNA 2 (RX 6000) ─────────────────────────────────────────────
	{0x1002, 0x73bf}: "AMD Radeon RX 6900 XT",
	{0x1002, 0x73a5}: "AMD Radeon RX 6950 XT",
	{0x1002, 0x73df}: "AMD Radeon RX 6700 XT",
	{0x1002, 0x73ef}: "AMD Radeon RX 6800 XT",
	{0x1002, 0x73ff}: "AMD Radeon RX 6800",
	{0x1002, 0x7421}: "AMD Radeon RX 6500 XT",
	{0x1002, 0x7422}: "AMD Radeon RX 6400",
	{0x1002, 0x7424}: "AMD Radeon RX 6300M",
	// ── AMD RDNA 1 (RX 5000) ─────────────────────────────────────────────
	{0x1002, 0x731f}: "AMD Radeon RX 5700 XT",
	{0x1002, 0x7340}: "AMD Radeon RX 5500 XT",
	// ── AMD Vega ─────────────────────────────────────────────────────────
	{0x1002, 0x687f}: "AMD Radeon RX Vega 64",
	{0x1002, 0x6863}: "AMD Radeon RX Vega 56",
	// ── AMD iGPU (Rembrandt / Phoenix) ───────────────────────────────────
	{0x1002, 0x1681}: "AMD Radeon 780M (Phoenix)",
	{0x1002, 0x164e}: "AMD Radeon 680M (Rembrandt)",
	// ── NVIDIA (rare VAAPI via nouveau, included for completeness) ────────
	{0x10de, 0x0}: "NVIDIA GPU", // device 0 = placeholder; fallback used
	// ── virtio-gpu ────────────────────────────────────────────────────────
	{0x1af4, 0x1050}: "VirtIO GPU",
}

// vaapiVendorFallback maps a PCI vendor ID to a generic family name used when
// the specific device ID is not in the table above.
var vaapiVendorFallback = map[uint16]string{
	0x8086: "Intel GPU",
	0x1002: "AMD GPU",
	0x10de: "NVIDIA GPU",
	0x1af4: "VirtIO GPU",
}

// queryVAAPIDisplayName returns a human-readable name for the VAAPI device
// associated with the given DRI render node path (e.g. "/dev/dri/renderD128").
// It is safe to call with an empty path; "VAAPI GPU" is returned as the last
// resort fallback.
func queryVAAPIDisplayName(driPath string) string {
	if driPath == "" {
		return "VAAPI GPU"
	}

	// /dev/dri/renderD128 → sysfs: /sys/class/drm/renderD128/device/vendor
	node := filepath.Base(driPath)                         // "renderD128"
	base := filepath.Join("/sys/class/drm", node, "device") // "/sys/class/drm/renderD128/device"

	vendor := readHexU16(filepath.Join(base, "vendor"))
	device := readHexU16(filepath.Join(base, "device"))

	if vendor != 0 {
		if name, ok := vaapiKnownDevices[pciVendorDevice{vendor, device}]; ok {
			return name
		}
		if name, ok := vaapiVendorFallback[vendor]; ok {
			return fmt.Sprintf("%s (PCI %04x:%04x)", name, vendor, device)
		}
	}
	return driPath // last resort: return the DRI path itself
}

// readHexU16 reads a file containing a "0x…" hex string and returns it as
// uint16. Returns 0 on any error.
func readHexU16(path string) uint16 {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	s := strings.TrimSpace(string(data))
	s = strings.TrimPrefix(s, "0x")
	s = strings.TrimPrefix(s, "0X")
	var v uint64
	for _, c := range s {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= uint64(c - '0')
		case c >= 'a' && c <= 'f':
			v |= uint64(c-'a') + 10
		case c >= 'A' && c <= 'F':
			v |= uint64(c-'A') + 10
		default:
			return 0
		}
	}
	return uint16(v)
}
