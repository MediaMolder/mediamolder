# Frame index

`frameindex` builds a presentation-order table of one video stream's timestamps. A row is one decoded frame. The timestamp is an `int64` PTS in the stream timebase (`{num, den}` on the table). The package does not cache, does not page, and does not know about a library.

## Walks

`Estimate(path, stream)` guesses a frame count from the container's duration and its average frame rate. The tick rate is used only when the average is missing, because that rate counts a variable-rate clip high. `stream < 0` selects the first video stream. A guess of 0 means the container did not say. It is not the table.

`WalkPackets` reads every video packet (`ReadPacket`, not a helper that drops corrupt packets), takes PTS (DTS when PTS is missing), skips a packet with neither, and sorts into presentation order. It does not decode. That table is provisional: the decoder can drop a packet, so the verified table can be shorter. A decoded row keeps the source frame's timestamp, so it is one of those packet timestamps.

`WalkDecode` decodes the stream and records each output frame's timestamp (`best_effort_timestamp`, or `AVFrame.pts` when that is missing), then sorts into presentation order. When the two tables disagree, the decoded one is the verified clock. The caller decides that. This package returns both and does not merge them.

Both walks take a context and a progress callback. The callback receives how many rows have been accepted. Cancelling the context returns that error and no table. A walk that would record more than `MaxIndexFrames` (1,000,000) returns `ErrTooManyFrames` and no table. A cancelled or capped walk is started again from the beginning; a prefix is not a result.

## Interlaced streams

`DetectInterlace` decides the field order on its own open, so the walk's demuxer stays at the start of the file. A progressive stream, or one whose headers and frames do not say, is one row per decoder output. Either way the returned table is sorted by PTS.

An interlaced stream (`tff` or `bff`) is passed through `bwdif=mode=send_frame:parity=…:deint=all` before a row is recorded. `send_frame` emits one frame per frame, so a field is not a row. bwdif's output timebase is not the stream timebase — it halves it — so a row does not store the filter frame's PTS. The walk remembers the source frame's PTS at push time and stamps that on the frame the filter later releases. The table and a packet walk of the same file are in one timebase.
