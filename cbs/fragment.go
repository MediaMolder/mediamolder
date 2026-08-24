// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Port of cbs_read_fragment_content (libavcodec/cbs.c), with per-unit
// error isolation: FFmpeg aborts the fragment on the first failing unit,
// this port records the error on the unit and continues.

package cbs

import (
	"errors"
	"runtime"
	"runtime/debug"
)

// recoverUnit converts template unwinding (r.fail) into unit.Err, and any
// runtime panic into *InternalError so hostile input can never crash the
// process. Deferred at the top of each codec's readUnit.
func recoverUnit(unit *Unit) {
	switch p := recover().(type) {
	case nil:
	case readAbort:
		unit.Err = p.err
	default:
		if re, ok := p.(runtime.Error); ok {
			unit.Err = &InternalError{Panic: re, Stack: debug.Stack()}
		} else {
			panic(p)
		}
	}
}

// avErrString mirrors av_err2str for the errors the port produces, so the
// "Failed to read unit" diagnostic matches FFmpeg's text.
func avErrString(err error) string {
	switch {
	case errors.Is(err, ErrInvalidData):
		return "Invalid data found when processing input"
	case errors.Is(err, ErrPatchWelcome):
		return "Not yet implemented in FFmpeg, patches welcome"
	case errors.Is(err, ErrUnsupported):
		return "Function not implemented"
	}
	return err.Error()
}

// readFragmentUnits runs readUnit over each unit of the fragment,
// mirroring cbs_read_fragment_content's ENOSYS/EAGAIN/error handling.
func readFragmentUnits(frag *Fragment, tr Tracer, readUnit func(*Unit)) {
	diag := func(level Level, format string, args ...any) {
		if tr != nil {
			tr.Diag(level, sprintf(format, args...))
		}
	}

	for i := range frag.Units {
		unit := &frag.Units[i]
		if unit.Skip != "" {
			continue
		}

		readUnit(unit)

		switch {
		case unit.Err == nil:
		case errors.Is(unit.Err, ErrUnsupported) && unit.Skip == SkipUnimplemented:
			diag(LevelVerbose, "Decomposition unimplemented for unit %d (type %d).",
				i, unit.Type)
		case errors.Is(unit.Err, ErrSkipped):
			diag(LevelVerbose, "Skipping decomposition of unit %d (type %d).",
				i, unit.Type)
			unit.Skip = SkipDroppedOBU
			unit.Content = nil
			unit.Err = nil
		default:
			diag(LevelError, "Failed to read unit %d (type %d): %s.",
				i, unit.Type, avErrString(unit.Err))
		}
	}
}
