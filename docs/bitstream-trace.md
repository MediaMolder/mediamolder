# Bitstream trace — NAL / OBU analysis to JSON

`bitstream_trace` scans an elementary video bitstream at the packet level —
**no decoding** — and reports every NAL unit (H.264 / H.265) or OBU (AV1)
to a JSON, JSON Lines, CSV, or text file: parameter sets, slice / frame
headers, SEI messages, and (optionally) every syntax element with its bit
position, width, raw bits and value.

It is an improved, machine-readable version of FFmpeg's `trace_headers`
bitstream filter, built on the `cbs` package — a read-only Go port of
FFmpeg's Coded Bitstream framework (`libavcodec/cbs*`) that is validated
byte-for-byte against `ffmpeg -bsf:v trace_headers` output (see
[Validation](#validation)).

Typical uses:

- **Ingest QC** — assert "every IDR is preceded by SPS+PPS", "HDR10
  static metadata present", "no B-frames", "`max_dec_frame_buffering ≤ 4`"
  with a script over the JSON.
- **Stream comparison** — diff two encodes' parameter sets and SEI.
- **GOP / reference structure** — slice types, `frame_num`, AV1
  `refresh_frame_flags` per packet.
- **Bug reports** — attach a structured header dump instead of a log.

## CLI

```sh
mediamolder trace-headers in.mp4                          # JSON to stdout
mediamolder trace-headers --format text in.mp4            # trace_headers-format text
mediamolder trace-headers --detail headers --units sps,pps,sei \
    --output report.json in.mkv
mediamolder trace-headers --stream v:1 --range 0:100 in.ts
```

| Flag | Default | Meaning |
|---|---|---|
| `--stream` | `v:0` | `v:N` (Nth video stream) or an absolute stream index |
| `--output` | `-` | output path (`-` = stdout) |
| `--format` | `json` | `json`, `jsonl`, `csv`, `text` |
| `--detail` | `elements` | `summary`, `headers`, `elements` (see below) |
| `--units` | all | comma-separated filter: family names (`sps,pps,vps,sei,idr,aud,slice,metadata`) or numeric types; names cover every codec's spelling (`sei` also matches HEVC `SEI_PREFIX`/`SEI_SUFFIX`, `sps` the AV1 sequence header) |
| `--max-packets` | all | stop after N packets |
| `--range` | all | only packets with index in `a:b` (0-based, inclusive at both ends; `0:0` = first packet only) |

`--format text` reproduces FFmpeg's `trace_headers` output (without the
`[trace_headers @ 0x…]` prefix), so it can be diffed directly against an
ffmpeg run.

## Graph node

```json
{
  "schema_version": "1.1",
  "inputs": [ { "id": "in0", "url": "in.mp4" } ],
  "graph": {
    "nodes": [
      { "id": "trace", "type": "go_processor", "processor": "bitstream_trace",
        "params": {
          "input_id": "in0",
          "stream": "v:0",
          "output_file": "/abs/path/trace.json",
          "output_format": "json",
          "detail": "headers"
        } }
    ],
    "edges": []
  },
  "outputs": []
}
```

The node opens the referenced input itself (a `FrameSource` that emits no
frames), so nothing in the graph decodes — tracing a two-hour file reads
it at I/O speed. It can run alone (as above) or alongside any other
processing of the same input.

| Param | Default | Meaning |
|---|---|---|
| `input_id` / `url` | required | input to scan (`input_id` is resolved from the job's `inputs`) |
| `stream` | `v:0` | stream selector |
| `output_file` | required | **absolute** report path |
| `output_format` | `json` | `json`, `jsonl`, `csv`, `text` |
| `detail` | `headers` | `summary`, `headers`, `elements` |
| `unit_types` | all | e.g. `["sps", "pps", "sei"]` or numeric types (family names match across codecs) |
| `max_packets`, `packet_range` | all | limit scope (`packet_range`: `[first, last]`, 0-based inclusive) |
| `emit_events` | `false` | per-packet unit summaries on the events bus (GUI, `metadata_file_writer`) |

Supported codecs: H.264/AVC, H.265/HEVC, AV1 — in any container
libavformat opens (MP4/MOV, MKV/WebM, MPEG-TS, IVF, raw `.h264` /
`.hevc` / `.obu`). Both Annex B and length-prefixed (avcC / hvcC / av1C)
packaging are handled; codec extradata is parsed first, exactly as
`trace_headers` does.

## Detail levels

| `detail` | Parameter sets, SEI, metadata OBUs | Slice / frame headers |
|---|---|---|
| `summary` | summary only | summary only |
| `headers` | full element trace | summary only |
| `elements` | full element trace | full element trace |

Extradata is always reported in full regardless of packet filters — it is
init-time state, exactly as `trace_headers` prints it once up front.
`headers` is the "what is in this stream" mode and keeps reports small;
`elements` is full FFmpeg parity (a feature film at `elements` produces a
very large report — combine with `unit_types` or `packet_range`).

## Report format

```jsonc
{
  "schema": "mediamolder.bitstream_trace/2",
  "source": { "url": "in.mp4", "stream_index": 0, "codec": "h264",
              "profile": "High", "format": "avcc", "nal_length_size": 4,
              "time_base": [1, 12800] },
  "extradata": { "size": 47, "units": [ /* unit objects */ ] },
  "packets": [
    {
      "index": 0, "pts": 0, "dts": 0, "time": 0.0, "duration": 512, "pos": 48,
      "size": 1587, "key_frame": true,
      "units": [
        {
          "index": 0, "offset": 4, "prefix": 4, "size": 27, "rbsp_size": 25,
          "type": 7, "name": "SPS", "class": "ps",
          "header": { "nal_ref_idc": 3, "type": 7 },
          "summary": { "sps_id": 0, "profile_idc": 100, "level_idc": 31,
                       "width": 1920, "height": 1080, "bit_depth_luma": 8,
                       "vui": { "timing": [1, 50], "hrd": false } },
          "sections": [
            { "name": "Sequence Parameter Set",
              "elements": [
                { "pos": 0, "bits": 1, "name": "forbidden_zero_bit", "raw": "0", "value": 0 }
              ] }
          ],
          "decomposed": true
        },
        { "index": 3, "offset": 745, "prefix": 4, "size": 838, "rbsp_size": 838,
          "type": 5, "name": "IDR", "class": "vcl",
          "header": { "nal_ref_idc": 3, "type": 5 },
          "picture": { "type": "I", "type_value": 7, "poc": 0, "frame_num": 0,
                       "lsb": 0, "first_mb": 0, "pps": 0, "qp_delta": 4,
                       "idr_pic_id": 0 },
          "decomposed": true
        }
      ]
    }
  ],
  "stats": { "packets": 20, "units": 42, "by_type": { "1": 19, "5": 1, "7": 1 },
             "skipped": 0, "errors": 0 }
}
```

Field notes:

- `pos` (element) is the **bit offset within the unit's RBSP** — identical
  to FFmpeg's first trace column. `offset` / `size` (unit) are byte
  positions in the packet including emulation-prevention bytes; `epb`
  (at `detail=elements`) lists where `0x03` bytes were removed.
- `class` buckets every unit for filtering and rate analysis: `vcl`
  (coded picture data — slices, AV1 frame/tile-group OBUs), `ps`
  (parameter sets), `sei` (SEI messages, AV1 metadata OBUs), `other`
  (AUD, filler, delimiters, reserved). It is present even for units that
  failed to decompose. `time` is the packet pts in seconds (stream time
  base applied).
- `header` carries the raw NAL/OBU header fields even for units that are
  not decomposed (reserved types, HEVC layered NALs FFmpeg drops —
  reported here with a `skip` reason).
- **Coded pictures** (H.264/H.265 slices) carry a typed `picture` record
  with a fixed schema instead of a free-form summary: slice `type` /
  `type_value`, the **derived Picture Order Count** (`poc`, per
  Rec. H.264 §8.2.1 / H.265 §8.3.1 — all three H.264 POC modes, lsb wrap,
  IDR/BLA resets, continuous across mid-stream CRAs), `frame_num` /
  `segment_address`, `lsb`, `pps`, `qp_delta`, `ref_l0`/`ref_l1`,
  `idr_pic_id`, `field`. Fixed keys keep long streams compact and map 1:1
  onto the CSV picture columns.
- `summary` is derived for the other unit types: SPS/sequence header →
  dimensions, profile/level, bit depth, VUI timing/colour; PPS → entropy
  mode, weighted prediction, `init_qp`; AV1 frame headers → frame type,
  order hint, reference state; SEI → message inventory with decoded
  fields for the common types (user data, mastering display, CLL,
  recovery point, pic timing, …).
- `jsonl` emits the same objects one per line (header, packets, stats) so
  arbitrarily long streams can be processed without loading a document.
- `csv` emits **one row per unit** for spreadsheet / SQL workflows:
  `kind` (`extradata` | `packet`), the packet context (index, pts/dts,
  `time` in seconds, duration, position, size, key frame), the unit
  identity (index, offset, prefix, sizes, type, name, `class`,
  decomposed/skip/error), then the
  **coded-picture columns** (`pic_type`, `pic_type_value`, `poc`,
  `frame_num`, `pic_lsb`, `first_mb`, `pps_id`, `qp_delta`, `ref_l0`,
  `ref_l1`) filled for H.264/H.265 slice rows, and a compact-JSON
  `summary` column for the other unit types (slice rows leave it empty —
  the columns are the report). Element-level detail is not representable
  in CSV (`detail` is ignored); the `unit_types` filter drops
  non-matching rows entirely.
- A malformed unit gets an `"error"` and parsing **continues** with the
  next unit — unlike `trace_headers`, which aborts on the first error.

## Plotting bit rate

Every unit row carries its byte `size`, the packet `time` in seconds, and
a `class` — so bit rate over time, split into picture data vs. overhead,
is a three-line aggregation over the CSV:

```sh
mediamolder trace-headers --format csv in.mp4 > units.csv
```

```python
import pandas as pd
df = pd.read_csv("units.csv")
df = df[df.kind == "packet"]
rate = (df.assign(bits=df["size"] * 8, second=df.time.astype(float).astype(int))
          .pivot_table(index="second", columns="class", values="bits", aggfunc="sum")
          .fillna(0))
rate.plot(ylabel="bits/s")            # vcl vs sei vs ps per second
```

Filtering the optional units away (or selecting only them) is the same
column: `df[df["class"] == "vcl"]` for pure picture payload,
`df[df["class"] == "sei"]` for SEI overhead. In the JSON formats the
same fields appear as `packets[].time` and `units[].class`.

## Validation

The report always carries a top-level `violations` list (layer 0): every
unit parse failure the syntax templates detect — out-of-range elements,
truncation, missing parameter-set references — as

```jsonc
{ "severity": "error", "kind": "syntax", "spec": "H.264",
  "packet": 12, "unit": 0, "message": "PPS id 3 not available." }
```

Opt-in **structure checks** (layer 1, `kind: "structure"`) walk the same
decode-order state the POC tracker keeps:

| Check id | Severity | Finding |
|---|---|---|
| `vcl_before_ps` | error | picture data before any parameter sets (extradata or in-band) |
| `frame_num_gap` | error | H.264 `frame_num` gap while `gaps_in_frame_num_allowed_flag` is 0 |
| `no_aud` | warning | packet carries picture data but no access unit delimiter |
| `sei_after_vcl` | warning | prefix SEI after the first VCL unit of the access unit |

Presets: `default` = `vcl_before_ps,frame_num_gap` (flags only broken
streams); `strict` adds the convention warnings — many perfectly legal
streams fail those (no AUD, parameter sets only in extradata), which is
why nothing beyond layer 0 is always-on.

```sh
mediamolder trace-headers --validate in.mp4            # exit non-zero on any
                                                       # error-severity finding
mediamolder trace-headers --checks strict in.mp4       # report-only
```

`--validate` implies `--checks default` when `--checks` is not given and
makes the exit status reflect error-severity findings (syntax + failed
checks). The graph node takes the same `checks` param plus
`validate: true`, which fails the node (and so the job) instead. In CSV
the findings append as `kind=violation` rows (check id in `name`,
severity in `class`, message in `error`). Range and unit filters never
hide violations — excluded packets are still parsed and checked.

This is header-level validation only: semantics that need a decoder —
DPB conformance, level limits, HRD/CPB timing, residual decoding — are
out of scope. Structure checks need a structured format (`json`,
`jsonl`, `csv`); `text` stays byte-for-byte FFmpeg parity.

## Library

```go
import "github.com/MediaMolder/MediaMolder/cbs"

c, _ := cbs.New(cbs.CodecH264, nil)          // nil tracer: structs only
frag, _ := c.ReadExtradata(stream.ExtraData) // avcC/hvcC/av1C or Annex B
frag, _ = c.ReadPacket(pkt.Data())
for _, u := range frag.Units {
    if sps, ok := u.Content.(*cbs.H264RawSPS); ok { … }
}
```

The `cbs` package is cgo-free and safe on hostile input (fuzzed; a parse
error is reported on the unit, never a panic). `cbs/report` renders the
JSON/JSONL/CSV/text reports; `processors.RunBitstreamTrace` is the shared
driver.

## Validation

The port is tested byte-for-byte against FFmpeg: checked-in golden files
under `cbs/testdata/golden/` capture `ffmpeg -bsf:v trace_headers` output
from the reference FFmpeg build for tiny H.264 / H.265 / AV1 fixtures
(`scripts/gen-cbs-fixtures.sh`, `scripts/gen-cbs-golden.sh`), and the
test suite renders the Go parser's trace in the same text format and
diffs it. Fuzz targets (`FuzzH264ReadPacket`, `FuzzH265ReadPacket`,
`FuzzAV1ReadPacket`) assert that no input can crash the parser.

## Limitations

- H.264 SVC / MVC / 3DAVC NALs (types 14, 15, 20, 21) are listed but not
  decomposed — same coverage as FFmpeg's CBS.
- AV1 Annex B (length-delimited) files are handled by libavformat's
  demuxer conversion, as in FFmpeg; `tile_list` / large-scale-tile OBUs
  are parsed but rarely exercised.
- Mid-stream parameter-set changes delivered as packet side data
  (`AV_PKT_DATA_NEW_EXTRADATA`) are not yet reported.
