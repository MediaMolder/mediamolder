# Subtitles

MediaMolder supports subtitle stream selection, passthrough, and burn-in (hardcoding) for common text and bitmap subtitle formats.

## Supported Formats

| Format | Type | Extension / Container | Burn-In |
|--------|------|-----------------------|---------|
| SRT (SubRip) | Text | `.srt`, MKV, MP4 | Yes |
| ASS/SSA | Text (styled) | `.ass`, `.ssa`, MKV | Yes |
| DVB Subtitle | Bitmap | DVB-T/S/C transport streams | No |
| PGS (Blu-ray) | Bitmap | `.sup`, MKV, Blu-ray | No |
| WebVTT | Text | `.vtt`, DASH/HLS | No |
| MOV_TEXT | Text | MP4 | No |

## Subtitle Types

MediaMolder classifies subtitles into three categories:

- **Text** (`SubtitleText`) — Plain text subtitles (SRT, WebVTT, MOV_TEXT). Can be burned in with the `subtitles` filter.
- **ASS** (`SubtitleASS`) — Styled text with font, color, and positioning. Burned in with the `ass` filter to preserve styling.
- **Bitmap** (`SubtitleBitmap`) — Pre-rendered image subtitles (PGS, DVB). Can be overlaid but not directly burned in as text.

## JSON Configuration

### Subtitle Stream Selection

```json
{
  "inputs": [
    {
      "id": "main",
      "url": "movie.mkv",
      "streams": [
        {"input_index": 0, "type": "video", "track": 0},
        {"input_index": 0, "type": "audio", "track": 0},
        {"input_index": 0, "type": "subtitle", "track": 0}
      ]
    }
  ],
  "graph": {
    "edges": [
      {"from": "main:v:0", "to": "out:v", "type": "video"},
      {"from": "main:a:0", "to": "out:a", "type": "audio"},
      {"from": "main:s:0", "to": "out:s", "type": "subtitle"}
    ]
  },
  "outputs": [
    {
      "id": "out",
      "url": "output.mkv",
      "codec_video": "libx264",
      "codec_audio": "aac",
      "codec_subtitle": "srt"
    }
  ]
}
```

### Burn-In (Hardcoding) Subtitles

For text subtitles (SRT), use the `subtitles` video filter:

```json
{
  "graph": {
    "nodes": [
      {
        "id": "burn",
        "filter": "subtitles=filename='subs.srt':charenc=UTF-8",
        "type": "video"
      }
    ],
    "edges": [
      {"from": "main:v:0", "to": "burn:in", "type": "video"},
      {"from": "burn:out", "to": "out:v", "type": "video"}
    ]
  }
}
```

For ASS/SSA styled subtitles:

```json
{
  "graph": {
    "nodes": [
      {
        "id": "burn",
        "filter": "ass=filename='subs.ass'",
        "type": "video"
      }
    ],
    "edges": [
      {"from": "main:v:0", "to": "burn:in", "type": "video"},
      {"from": "burn:out", "to": "out:v", "type": "video"}
    ]
  }
}
```

### Disabling Subtitles

To strip all subtitle streams, omit subtitle entries from `streams` and `edges`.

## Go API Usage

```go
// Open subtitle decoder.
subDec, err := av.OpenSubtitleDecoder(input, subtitleStreamIndex)
if err != nil { return err }
defer subDec.Close()

// Decode a subtitle packet.
sub, gotOutput, err := subDec.Decode(pkt)
if err != nil { return err }
if gotOutput {
    defer sub.Free()
    fmt.Println(sub.StartDisplayTime(), sub.EndDisplayTime())
}

// Encode subtitles.
subEnc, err := av.OpenSubtitleEncoder(av.SubtitleEncoderOptions{
    CodecName: "srt",
    Width:     1920,
    Height:    1080,
})
if err != nil { return err }
defer subEnc.Close()

data, err := subEnc.Encode(sub)

// Generate burn-in filter specs.
spec := av.SubtitleBurnInFilter("/path/to/subs.srt", "UTF-8")
// => "subtitles=filename='/path/to/subs.srt':charenc=UTF-8"

assSpec := av.ASSBurnInFilter("/path/to/subs.ass")
// => "ass=filename='/path/to/subs.ass'"
```

## FFmpeg CLI Equivalents

| FFmpeg CLI | MediaMolder |
|-----------|-------------|
| `-c:s srt` | `"codec_subtitle": "srt"` in output |
| `-c:s copy` | subtitle edge with no encoding node |
| `-sn` | Omit subtitle streams from config |
| `-vf "subtitles=subs.srt"` | Burn-in filter node in graph |
| `-vf "ass=subs.ass"` | ASS burn-in filter node in graph |

The `ffcli` compatibility layer maps:
- `-c:s <codec>` / `-scodec <codec>` → subtitle codec selection
- `-sn` → disables subtitle streams

## Known Limitations

- **Bitmap subtitle burn-in**: DVB and PGS subtitles cannot be burned in using the text-based `subtitles` or `ass` filters. They must be overlaid with the `overlay` filter, which requires additional configuration.
- **Container compatibility**: Not all containers support all subtitle formats. MP4 supports MOV_TEXT; MKV supports SRT, ASS, PGS, and most others.
- **Hardware filters and subtitles**: Burning in subtitles is a CPU operation. When using hardware-accelerated pipelines, subtitle burn-in requires a hw→sw→hw transfer unless the entire filter graph runs in software.
