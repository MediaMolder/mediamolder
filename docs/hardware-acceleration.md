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

### Hardware Filters — Auto-Mapping (Wave 10 #58)

Hardware filter auto-mapping is **opt-in per node** via the `auto_map_hw` field. When set to `true` on a filter node, the pipeline's `expandHWFilterMappings` pass:

1. **Promotes** the software filter name to its hardware equivalent based on the node's `device` type (e.g. `"scale"` on a CUDA device → `"scale_cuda"`).
2. **Inserts `hwupload`** nodes on incoming video edges from sources not on the same device (CPU frames → GPU surface).
3. **Inserts `hwdownload`** nodes on outgoing video edges to destinations not on the same device (GPU surface → CPU frames).

Audio and subtitle edges are never converted. The pass is a no-op when no `hardware_devices` are declared.

**Example — CUDA scale with auto-mapping:**

```json
{
  "hardware_devices": [{ "name": "gpu0", "type": "cuda", "device": "0" }],
  "graph": {
    "nodes": [
      { "id": "scale", "type": "filter", "filter": "scale",
        "params": { "w": "1920", "h": "1080" },
        "device": "gpu0", "auto_map_hw": true }
    ]
  }
}
```

The engine rewrites this to `scale_cuda`, inserts `hwupload` before it (when the source is a CPU decoder) and `hwdownload` after it (when the destination is a CPU encoder) — without requiring the user to name hardware filters explicitly.

**Validation rules:**
- `auto_map_hw` is only valid on `"filter"` nodes.
- `device` must be set when `auto_map_hw` is `true`.
- If the filter has no hardware alternative for the named device type, `validate()` rejects the config with a descriptive error listing the supported device types.

Nodes that already name a hardware filter directly (e.g. `"scale_cuda"`) must **not** set `auto_map_hw: true`.

**Supported filter/device combinations:**

| Software Filter | CUDA | VAAPI | QSV | VideoToolbox | Vulkan | OpenCL |
|----------------|------|-------|-----|--------------|--------|--------|
| `scale` | `scale_cuda` | `scale_vaapi` | `scale_qsv` | `scale_vt` | `scale_vulkan` | — |
| `yadif` | `yadif_cuda` | `deinterlace_vaapi` | — | — | — | — |
| `deinterlace` | — | `deinterlace_vaapi` | `deinterlace_qsv` | — | — | — |
| `overlay` | `overlay_cuda` | `overlay_vaapi` | `overlay_qsv` | — | — | — |
| `transpose` | — | `transpose_vaapi` | `transpose_qsv` | — | — | — |
| `thumbnail` | `thumbnail_cuda` | — | — | — | — | — |
| `tonemap` | — | `tonemap_vaapi` | — | — | — | `tonemap_opencl` |
| `flip` | — | — | — | — | `flip_vulkan` | — |
| `rotate` | — | — | — | — | `rotate_vulkan` | — |
| `avgblur` | — | — | — | — | `avgblur_vulkan` | `avgblur_opencl` |
| `unsharp` | — | — | — | — | — | `unsharp_opencl` |
| `bilateral` | — | — | — | — | — | `bilateral_opencl` |
| `nlmeans` | — | — | — | — | — | `nlmeans_opencl` |
| `convolution` | — | — | — | — | — | `convolution_opencl` |
| `boxblur` | — | — | — | — | — | `boxblur_opencl` |
| `sobel` | — | — | — | — | — | `sobel_opencl` |
| `deshake` | — | — | — | — | — | `deshake_opencl` |
| `colorkey` | `chromakey_cuda` | — | — | — | — | `colorkey_opencl` |
| `blend` | — | — | — | — | `blend_vulkan` | `blend_opencl` |
| `maskedmerge` | — | — | — | — | — | `maskedmerge_opencl` |
| `erosion` | — | — | — | — | — | `erosion_opencl` |
| `dilation` | — | — | — | — | — | `dilation_opencl` |
| `xfade` | — | — | — | — | — | `xfade_opencl` |
| `pad` | — | — | — | — | — | `pad_opencl` |

Use `pipeline.HWFilterAlts()` to query the full table at runtime (e.g. for GUI node-palette population).

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

// Probe which device types are actually usable on this host.
probes := av.ProbeHWDevices()
for _, p := range probes {
    fmt.Printf("%s: available=%v\n", p.Type, p.Available)
}
```

## Per-Input Hardware-Accelerated Decoding (Wave 10 #59)

Three fields on each `Input` mirror FFmpeg's per-input `-hwaccel`, `-hwaccel_device`, and `-hwaccel_output_format` flags:

| Field | Type | Description |
|-------|------|-------------|
| `hwaccel` | string | Hardware acceleration backend: `"cuda"`, `"vaapi"`, `"qsv"`, `"videotoolbox"`, `"d3d11va"`, `"dxva2"`, `"vulkan"`, `"opencl"`, `"auto"`, etc. |
| `hwaccel_device` | string | Name of a pre-declared `hardware_devices` entry. The pipeline reuses its `AVHWDeviceContext` instead of opening a transient one. Omit to let the pipeline open a transient context. |
| `hwaccel_output_format` | string | Pixel format for decoder output. Use a software format (`"nv12"`, `"yuv420p"`, …) for automatic CPU transfer, or a hardware surface name (`"cuda"`, `"vaapi"`, `"qsv"`, …) to keep frames on the GPU for zero-copy filter chains. |

**Validation rules:**
- `hwaccel_device` and `hwaccel_output_format` require `hwaccel` to be non-empty.
- `hwaccel_device` must match a declared `hardware_devices[].name` entry.

**Example — per-input CUDA decode with explicit GPU surface output:**

```json
{
  "schema_version": "1.1",
  "hardware_devices": [
    { "name": "gpu0", "type": "cuda", "device": "0" }
  ],
  "inputs": [
    {
      "id": "src",
      "url": "input.mp4",
      "hwaccel": "cuda",
      "hwaccel_device": "gpu0",
      "hwaccel_output_format": "cuda",
      "streams": [
        {"input_index": 0, "type": "video", "track": 0},
        {"input_index": 0, "type": "audio", "track": 0}
      ]
    }
  ],
  "graph": { "nodes": [], "edges": [
    {"from": "src:v:0", "to": "out:v", "type": "video"},
    {"from": "src:a:0", "to": "out:a", "type": "audio"}
  ]},
  "outputs": [{ "id": "out", "url": "output.mp4", "codec_video": "h264_nvenc", "codec_audio": "aac" }]
}
```

Setting `hwaccel_output_format` to `"cuda"` keeps decoded frames in GPU memory so a downstream `scale_cuda` or `h264_nvenc` encoder receives them without a host↔device round-trip.

**Example — VAAPI decode with auto CPU transfer (for software filters):**

```json
{
  "inputs": [{
    "id": "src",
    "url": "input.mp4",
    "hwaccel": "vaapi",
    "hwaccel_device": "vaapi0",
    "hwaccel_output_format": "nv12"
  }]
}
```

`"nv12"` is a software format, so the pipeline sets `AutoTransfer: true` on the decoder, which instructs libav to copy frames from the GPU surface to system RAM automatically. This enables downstream software filters.

**Multiple inputs, mixed acceleration:**

```json
{
  "inputs": [
    { "id": "hd", "url": "hd.mp4", "hwaccel": "cuda", "hwaccel_device": "gpu0", "hwaccel_output_format": "cuda", "streams": [{"input_index": 0, "type": "video", "track": 0}] },
    { "id": "bg", "url": "bg.mp4", "streams": [{"input_index": 0, "type": "video", "track": 0}] }
  ]
}
```

`"hd"` decodes on the GPU; `"bg"` uses the software decoder. Each input is independent.

## FFmpeg CLI Equivalents

| FFmpeg CLI | MediaMolder JSON |
|-----------|------------------|
| `-hwaccel cuda -i input.mp4` | `"inputs": [{"hwaccel": "cuda", "url": "input.mp4"}]` |
| `-hwaccel_device 0 -i input.mp4` | `"inputs": [{"hwaccel_device": "gpu0", "url": "input.mp4"}]` |
| `-hwaccel_output_format cuda -i input.mp4` | `"inputs": [{"hwaccel_output_format": "cuda", "url": "input.mp4"}]` |
| `-c:v h264_nvenc` | `"codec_video": "h264_nvenc"` |
| `-c:v h264_vaapi` | `"codec_video": "h264_vaapi"` |
| `-c:v h264_qsv` | `"codec_video": "h264_qsv"` |

## Detecting available hardware

Use the `list-hw-devices` subcommand to probe which accelerators are usable on the current host:

```sh
# Text table — available devices only (default)
mediamolder list-hw-devices

# Include devices that failed to open (shows error reason)
mediamolder list-hw-devices --all

# Machine-readable JSON array
mediamolder list-hw-devices --json
```

Example JSON output:
```json
[
  {"type": "cuda",    "available": true},
  {"type": "d3d11va", "available": true},
  {"type": "qsv",     "available": false, "error": "av_hwdevice_ctx_create(qsv): Unknown error occurred"}
]
```

From Go code, use `av.ProbeHWDevices()`:
```go
for _, p := range av.ProbeHWDevices() {
    if p.Available {
        fmt.Println(p.Type, "is available")
    }
}
```

## Listing capture devices (`GET /api/devices`) (Wave 11 #61)

The REST API exposes `GET /api/devices?format=<fmt>` to enumerate the
capture devices available for a given libavdevice input format.

| Platform | Default format | Examples |
|----------|---------------|---------|
| Windows  | `dshow`        | `video="Integrated Camera"`, `audio="Microphone (Realtek)"` |
| macOS    | `avfoundation` | `0` (first camera), `1` (second camera), `:0` (default mic) |
| Linux    | `v4l2`         | `/dev/video0`, `/dev/video1` |

**Request:**
```
GET /api/devices?format=dshow
GET /api/devices?format=avfoundation
GET /api/devices?format=v4l2
GET /api/devices            ← uses the platform default
```

**Response** — JSON array of `{name, description}` objects:
```json
[
  {"name": "video=Integrated Camera", "description": "Integrated Camera"},
  {"name": "audio=Microphone (Realtek HD Audio)", "description": "Microphone (Realtek HD Audio)"}
]
```

The endpoint applies a **2-second timeout**. On Windows, dshow COM enumeration can block indefinitely when a device is locked by another process; the timeout returns HTTP 504 rather than hanging.

From Go code, use `av.ListDevices(format)`:
```go
devices, err := av.ListDevices("dshow")
if err != nil {
    log.Fatal(err)
}
for _, d := range devices {
    fmt.Printf("%s — %s\n", d.Name, d.Description)
}
```

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

## GUI: Device Picker and HW Indicator Badges (Wave 10 #60)

When using the visual graph editor, hardware device assignment is surfaced directly on the canvas and in the Inspector panel.

### Device picker in the Inspector

Selecting a **filter** or **encoder** node opens the Inspector, which shows a **Hardware device** dropdown listing every `hardware_devices` entry defined in the job config (e.g. `gpu0 [cuda]`). Choosing an entry sets `NodeDef.device` on that node. Selecting `(none — software)` clears the assignment.

For filter nodes, an **Auto-map to hardware filter** checkbox appears below the picker (enabled only when a device is selected). Checking it sets `auto_map_hw: true`, which promotes the software filter name to its hardware equivalent (e.g. `scale` → `scale_cuda`) and automatically inserts `hwupload`/`hwdownload` nodes at device boundaries.

If no `hardware_devices` entries have been declared, the picker renders with only the `(none — software)` option and a hint to add entries to the job config.

### Canvas badges

Each graph node that has `NodeDef.device` set shows a small **purple chip** bearing the device name (e.g. `⊞ gpu0`). Hovering the chip shows the full tooltip `Hardware device: <name>`.

A **yellow ⚠ sw/hw** warning badge is shown on software filter nodes (no `device`) that are adjacent — via the graph's edges — to at least one hardware-accelerated node. This indicates that the pipeline will be forced to perform an implicit `hwdownload` + `hwupload` round-trip at that boundary, which costs memory bandwidth and can negate the performance benefit of HW acceleration. To eliminate the warning, either:
1. Assign the same device to the filter and enable `auto_map_hw`, or
2. Reorder the graph so software filters are grouped together away from the HW chain.
