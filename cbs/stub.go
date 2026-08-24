// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package cbs

// stubCodec keeps the package usable while a codec port is in progress.
type stubCodec struct{ codec CodecID }

func (s stubCodec) ReadExtradata(data []byte) (*Fragment, error) {
	return &Fragment{Data: data}, ErrUnsupported
}

func (s stubCodec) ReadPacket(data []byte) (*Fragment, error) {
	return &Fragment{Data: data}, ErrUnsupported
}

func (s stubCodec) Flush() {}
