// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavdevice/avdevice.h"
import "C"

// init registers libavdevice's input and output devices so demuxers like
// "lavfi" (filtergraph virtual sources) and platform capture devices
// (avfoundation, gdigrab, decklink, …) can be selected via OpenInputWithFormat.
func init() {
	C.avdevice_register_all()
}
