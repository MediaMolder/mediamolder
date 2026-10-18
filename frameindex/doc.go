// Package frameindex builds a presentation-order PTS table for one video stream.
//
// The table is the clock: one row is one decoded frame, and its PTS is in the
// stream timebase. WalkPackets does not decode. It reads packet timestamps and
// sorts them into presentation order. That table is provisional. WalkDecode
// replaces it with the timestamps of the frames the decoder actually outputs.
//
// An interlaced stream is deinterlaced with bwdif in send_frame mode before a
// row is recorded, so a field is not a row. The filter's own timestamps are
// not the table. bwdif halves its output timebase, and a row keeps the PTS of
// the source frame that was pushed, which is still the stream timebase.
//
// Both walks stop when their context is cancelled and return that error with
// no table. Both report how many rows they have accepted. A walk that grows
// past MaxIndexFrames returns ErrTooManyFrames and no table. A cancelled walk
// is started again by the caller; a partial table is not a result.
package frameindex
