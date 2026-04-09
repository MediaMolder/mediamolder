# Hardware Acceleration

MediaMolder supports hardware-accelerated video decoding, encoding, and filtering through the following backends:

| Backend | Devices | API |
|---------|---------|-----|
| **CUDA/NVENC** | NVIDIA GPUs | CUDA + NVENC/NVDEC |
| **VAAPI** | Intel/AMD GPUs (Linux) | Video Acceleration API |
| **QSV** | Intel GPUs | Intel Quick Sync Video |
| **VideoToolbox** | Apple Silicon / Intel Mac | macOS VideoToolbox |

## Setup Requirements

### CUDA (NVIDIA)
1. Install NVIDIA drivers (≥ 470.x recommended)
2. Install CUDA toolkit (≥ 11.0)
3. Ensure FFmpeg was built with `--enable-cuda-nvcc --enable-libnpp --enable-nvenc --enable-nvdec`

### VAAPI (Intel/AMD on Linux)
1. Install VA-API drivers:
   - Intel: `intel-media-va-driver` (Broadwell+) or `intel-media-va-driver-non-free`
   - AMD: `mesa-va-drivers`
2. Verify: `vainfo` should list supported profiles
3. Device path: typically `/dev/dri/renderD128`

### QSV (Intel)
1. Install Intel Media SDK or oneVPL
2. Install `intel-media-va-driver`
3. Ensure FFmpeg was built with `--enable-libmfx` or `--enable-libvpl`

### VideoToolbox (macOS)
1. Available by default on macOS with Apple Silicon or supported Intel
2. No additional setup required
3. Ensure FFmpeg was built with `--enable-videotoolbox`

## JSON Configuration

### Hardware-Accelerated Transcode

```json
{
  "schema_version": "1.0",
  "inputs": [
    {
      "id": "main",
      "url": "input.mp4",
      "streams": [
        {"input_index": 0, "type": "video", "track": 0},
        {"input_index": 0, "type": "audio", "track": 0}
      ]
    }
  ],
  "graph": {
    "nodes": [],
    "edges": [
      {"from": "main:v:0", "to": "out:v", "type": "video"},
      {"from": "main:a:0", "to": "out:a", "type": "audio"}
    ]
  },
  "outputs": [
    {
      "id": "out",
      "url": "output.mp4",
      "codec_video": "h264_nvenc",
      "codec_audio": "aac"
    }
  ],
  "global_options": {
    "hw_accel": "cuda",
    "hw_device": "0"
  }
}
```

### Zero-Copy Decode→Encode Path

When both the decoder and encoder use the same hardware device, frames stay in GPU memory — no host↔device transfer overhead:

```json
{
  "global_options": {
    "hw_accel": "cuda",
    "hw_device": "0"
  }
}
```

With `hw_accel` set, the pipeline:
1. Opens a hardware device context (`av.OpenHWDevice`)
2. Creates hardware-accelerated decoders where supported (`av.OpenHWDecoder`)
3. Routes frames through hardware filters (e.g., `scale_cuda` instead of `scale`)
4. Feeds frames to a hardware encoder (e.g., `h264_nvenc`)

### Hardware Filters

When a hardware device is active, software filters are automatically mapped to their hardware equivalents where available:

| Software Filter | CUDA | VAAPI | QSV | VideoToolbox |
|----------------|------|-------|-----|--------------|
| `scale` | `scale_cuda` | `scale_vaapi` | `scale_qsv` | `scale_vt` |
| `yadif` | `yadif_cuda` | — | — | — |
| `transpose` | `transpose_cuda` | `transpose_vaapi` | — | — |
| `overlay` | `overlay_cuda` | `overlay_vaapi` | `overlay_qsv` | — |
| `deinterlace` | — | `deinterlace_vaapi` | `deinterlace_qsv` | — |

### Fallback to Software

If hardware acceleration is unavailable (driver not installed, GPU not present), the pipeline transparently falls back to software decoding/encoding. Hardware tests gracefully skip on systems without GPU using `av.RequireHWDevice(t, deviceType)`.

## Go API Usage

```go
// Open a CUDA device.
dev, err := av.OpenHWDevice(av.HWDeviceCUDA, "")
if err != nil { /* fallback to software */ }
defer dev.Close()

// Hardware-accelerated decoding.
hwDec, err := av.OpenHWDecoder(input, streamIdx, dev, av.HWDecoderOptions{
    AutoTransfer: true, // auto hw→sw transfer for downstream sw filters
})

// Hardware-accelerated encoding.
hwEnc, err := av.OpenHWEncoder(av.HWEncoderOptions{
    EncoderOptions: av.EncoderOptions{
        CodecName: "h264_nvenc",
        Width: 1920, Height: 1080,
    },
    HWDevice: dev,
})

// Hardware filter graph.
fg, err := av.NewHWVideoFilterGraph(av.HWVideoFilterGraphConfig{
    FilterSpec: "scale_cuda=1280:720",
    HWDevice:   dev,
    // ... video params
})

// List available HW device types.
types := av.ListHWDeviceTypes()
```

## FFmpeg CLI Equivalents

| FFmpeg CLI | MediaMolder JSON |
|-----------|------------------|
| `-hwaccel cuda` | `"hw_accel": "cuda"` |
| `-hwaccel_device 0` | `"hw_device": "0"` |
| `-hwaccel_output_format cuda` | Automatic when device is set |
| `-c:v h264_nvenc` | `"codec_video": "h264_nvenc"` |
| `-c:v h264_vaapi` | `"codec_video": "h264_vaapi"` |
| `-c:v h264_qsv` | `"codec_video": "h264_qsv"` |

## Troubleshooting

### "av_hwdevice_ctx_create: Cannot allocate memory"
- GPU drivers not installed, or device type not supported on this system
- VAAPI: check `vainfo` output; ensure `/dev/dri/renderD128` exists
- CUDA: check `nvidia-smi` output

### "no decoder found" for hardware codec
- FFmpeg not built with hardware acceleration support
- Rebuild FFmpeg with appropriate `--enable-*` flags

### Poor performance with hardware encoding
- Ensure frames are not being transferred host↔device unnecessarily
- Use zero-copy path: set `hw_accel` in global options
- Check that the hardware encoder is receiving frames in the GPU pixel format
