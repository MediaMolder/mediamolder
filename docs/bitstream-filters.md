# Bitstream Filters

Bitstream filters (BSFs) transform encoded packets without re-encoding. They are used for container format conversions, metadata injection, and packet-level transformations.

## Common Use Cases

| BSF Name | When Needed |
|----------|-------------|
| `h264_mp4toannexb` | Remuxing H.264 from MP4 to MPEG-TS / raw H.264 |
| `hevc_mp4toannexb` | Remuxing HEVC from MP4 to MPEG-TS / raw HEVC |
| `aac_adtstoasc` | Remuxing AAC from MPEG-TS to MP4 (ADTS→AudioSpecificConfig) |
| `h264_metadata` | Modifying H.264 SPS/PPS parameters (level, aspect ratio) |
| `hevc_metadata` | Modifying HEVC VPS/SPS/PPS parameters |
| `extract_extradata` | Extracting codec-specific data (SPS/PPS) from bitstream |
| `dump_extra` | Prepending extradata to every keyframe |
| `remove_extra` | Removing redundant extradata from non-keyframes |
| `vp9_superframe` | Combining VP9 frames into superframes for WebM |
| `vp9_superframe_split` | Splitting VP9 superframes |
| `av1_metadata` | Modifying AV1 OBU metadata |
| `filter_units` | Removing specific NAL units by type |
| `dts2pts` | Recomputing PTS from DTS |

## JSON Configuration

Bitstream filters are specified per-output on video and audio streams:

```json
{
  "outputs": [
    {
      "id": "out",
      "url": "output.ts",
      "codec_video": "copy",
      "codec_audio": "copy",
      "bsf_video": "h264_mp4toannexb",
      "bsf_audio": "aac_adtstoasc"
    }
  ]
}
```

### Chaining BSFs

Multiple bitstream filters can be chained with commas:

```json
{
  "bsf_video": "h264_mp4toannexb,dump_extra=freq=keyframe"
}
```

### BSF Options

Some BSFs accept parameters in `key=value` format:

```json
{
  "bsf_video": "h264_metadata=level=4.1:video_full_range_flag=1"
}
```

## Go API Usage

```go
// Open a bitstream filter.
bsf, err := av.OpenBitstreamFilter(av.BitstreamFilterOptions{
    Name: "h264_mp4toannexb",
})
if err != nil { return err }
defer bsf.Close()

// Filter a packet (send + receive in one call).
outPkt, err := bsf.FilterPacket(pkt)
if err != nil { return err }

// Or use the two-step API for more control.
if err := bsf.SendPacket(pkt); err != nil { return err }
for {
    outPkt, err := bsf.ReceivePacket()
    if errors.Is(err, av.ErrEAgain) { break }
    if err != nil { return err }
    // process outPkt
}

// Initialize from an encoder (copies codec parameters automatically).
bsf, err := av.OpenBitstreamFilterFromEncoder("h264_mp4toannexb", encoder)

// List all available bitstream filters.
filters := av.ListBitstreamFilters()
for _, f := range filters {
    fmt.Printf("%s\n", f.Name)
}
```

## FFmpeg CLI Equivalents

| FFmpeg CLI | MediaMolder JSON |
|-----------|------------------|
| `-bsf:v h264_mp4toannexb` | `"bsf_video": "h264_mp4toannexb"` |
| `-bsf:a aac_adtstoasc` | `"bsf_audio": "aac_adtstoasc"` |
| `-bsf:v h264_metadata=level=4.1` | `"bsf_video": "h264_metadata=level=4.1"` |

The `ffcli` compatibility layer maps `-bsf:v` and `-bsf:a` flags directly to the output's `bsf_video` and `bsf_audio` fields.

## Typical Workflows

### MP4 → MPEG-TS Remux
```json
{
  "outputs": [{
    "url": "output.ts",
    "codec_video": "copy",
    "codec_audio": "copy",
    "bsf_video": "h264_mp4toannexb",
    "bsf_audio": "aac_adtstoasc"
  }]
}
```

### MPEG-TS → MP4 Remux
```json
{
  "outputs": [{
    "url": "output.mp4",
    "codec_video": "copy",
    "codec_audio": "copy",
    "bsf_audio": "aac_adtstoasc"
  }]
}
```

### HLS Segment Preparation
```json
{
  "outputs": [{
    "url": "segment_%03d.ts",
    "codec_video": "copy",
    "bsf_video": "h264_mp4toannexb,dump_extra=freq=keyframe"
  }]
}
```
