// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "mm_packet_guard.h"
import "C"

import "sync/atomic"

// scrubbedPackets counts packets whose buffers had to be abandoned (leaked)
// because their struct was structurally inconsistent — see mm_packet_guard.c.
var scrubbedPackets atomic.Int64

// ScrubbedPacketCount reports how many packets have been scrubbed rather than
// freed since process start. A non-zero value means a demuxer produced (or was
// driven into producing) a structurally corrupt packet that would previously
// have crashed the process; each scrub leaks that packet's buffer by design.
// Hosts can use it to flag the offending media for quarantine or re-probe.
func ScrubbedPacketCount() int64 { return scrubbedPackets.Load() }

// poisonPacketForTest stamps pkt with the exact corruption pattern observed in
// a real crash (all-ones buffer pointer, negative size) so tests can prove the
// guarded unref/free paths survive it.
func poisonPacketForTest(pkt *Packet) { C.mm_packet_poison_for_test(pkt.p) }
