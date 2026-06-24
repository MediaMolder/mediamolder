// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package raw

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// LibRaw version + source pin. LibRaw is bundled and statically linked (we ship no binaries —
// see Design Principles); scripts/bundle-libraw.sh downloads this exact source, verifies it
// against [LibRawSourceSHA256], and builds the static library the `with_libraw` cgo binding
// links. The version is recorded so a bump is a documented "exports may differ" event:
// the develop is reproducible only for a given pinned version.
//
// This is the manifest of truth, mirroring face/models.go's SHA-256 pins. Verification happens
// at bundle time (a tampered or wrong tarball never builds), not at Go load — unlike face's
// model files, the library is compiled in, so [Capable] need only report whether the binding
// was built (see decode_libraw.go / decode_stub.go).
const (
	LibRawVersion      = "0.21.3"
	LibRawSourceURL    = "https://www.libraw.org/data/LibRaw-" + LibRawVersion + ".tar.gz"
	LibRawSourceSHA256 = "dba34b7fc1143503942fa32ad9db43e94f714e62a4a856e91617f8f3e1e0aa5c"
)

// VerifySource returns an error unless data hashes to [LibRawSourceSHA256] — the same check
// scripts/bundle-libraw.sh performs in shell, exposed in Go for the bundle tooling and tests.
func VerifySource(data []byte) error {
	sum := sha256.Sum256(data)
	got := hex.EncodeToString(sum[:])
	if !strings.EqualFold(got, LibRawSourceSHA256) {
		return fmt.Errorf("raw: LibRaw source SHA-256 mismatch: got %s, want %s", got, LibRawSourceSHA256)
	}
	return nil
}
