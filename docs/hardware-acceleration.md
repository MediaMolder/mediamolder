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

## Device probe + seek guard (Wave 11 #62)

### Probing a device input (`POST /api/probe`)

`POST /api/probe` accepts an optional `format` field in the JSON body.
When supplied, `avformat_open_input` is forced to use that input-format
demuxer (e.g. `"dshow"` or `"v4l2"`) so the URL is interpreted as a
device specifier rather than a filename:

```json
{
  "url": "video=Integrated Camera",
  "format": "dshow",
  "options": {"video_size": "1280x720", "framerate": "30"}
}
```

The probe call runs under a **2-second timeout** (same goroutine pattern as
`GET /api/devices`). HTTP 504 is returned when the device is unavailable.

### Seek guard for device inputs

Device demuxers (`dshow`, `avfoundation`, `v4l2`, `gdigrab`, `x11grab`,
`decklink`) do not support `avformat_seek_file`. Attempting to seek a
live device input returns an error or blocks indefinitely.

`isDeviceFormat(name string) bool` in `pipeline/handlers_source.go`
identifies these demuxers:

```go
func isDeviceFormat(name string) bool {
    switch name {
    case "dshow", "avfoundation", "v4l2", "gdigrab", "x11grab", "decklink":
        return true
    }
    return false
}
```

The seek step in `openSource` — which honours `-ss` / `-t` / `-to` by
calling `input.SeekFile(targetUS)` — is now skipped for both `lavfi` and
all device formats. Any `-ss` value on a device input is ignored at open
time and instead converted to the per-packet stop-check (the same path
already used for lavfi). This matches FFmpeg's own behaviour.

### Device palette entries (`GET /api/nodes`)

`handleListNodes` emits one or more `device_input` catalog entries using
`runtime.GOOS` dispatch so only the platform-appropriate capture formats
appear in the GUI palette:

| OS      | Entries |
|---------|---------|
| Windows | `dshow` (camera/mic), `gdigrab` (screen capture) |
| macOS   | `avfoundation` (camera/mic/screen) |
| Linux   | `v4l2` (camera) |

Each entry carries `Type: "device_input"` so the frontend can render a
dedicated device inspector form (Wave 11 #63).

## Device picker + Inspector form (Wave 11 #63)

### Spawning a device input node

Dragging a `device_input` palette entry onto the graph canvas creates an
`Input` node with `format` pre-set to the demuxer name (`dshow`, `v4l2`,
etc.). The Inspector detects device inputs by checking `Input.format`
against the set of known device formats and routes them to
`DeviceInputForm` instead of the standard `InputForm`.

### DeviceInputForm

`DeviceInputForm` (in `frontend/src/components/Inspector.tsx`) provides:

**Device type dropdown** — shown when the format supports more than one
stream type (`dshow`, `avfoundation`, `decklink`):
- `video` — selects a video capture device
- `audio` — selects an audio capture device
- `screen` — for `gdigrab` (always selected; no dropdown shown)

**Device name combobox** — asynchronously fetches
`GET /api/devices?format=<fmt>` on mount and populates a `<datalist>`.
Selecting or typing a device name builds the URL automatically:

| Format | URL form |
|--------|----------|
| `dshow` | `video="<name>"` or `audio="<name>"` |
| `avfoundation` | `<video_index>` or `none:<audio_index>` |
| `v4l2` | `/dev/videoN` (raw device path) |
| `gdigrab` | `desktop` or window title |
| `decklink` | device name string |

**Device URL field** — the auto-built URL shown verbatim; the user can
override it manually (useful for `gdigrab` window titles or
`avfoundation` combined `<v>:<a>` indices).

**Test connection button** — issues `POST /api/probe` with
`{url, format, options}` under the existing 2-second timeout. On
success, stream metadata appears below (codec, resolution, frame rate,
etc.) just like the standard input probe.

**Capture options** — four typed fields mapped to AVDict entries:

| Field | AVOption | Example |
|-------|----------|---------|
| Frame rate | `framerate` | `30` |
| Video size | `video_size` | `1280x720` |
| Pixel format | `pixel_format` | `yuyv422` |
| Sample rate | `sample_rate` | `44100` |

These go into `Input.options` (the AVDict passed to
`avformat_open_input`) so they reach the device demuxer directly.

### Roundtrip from JSON

When a job config is loaded from JSON, inputs with a `format` matching a
known device demuxer name are automatically shown in `DeviceInputForm`
regardless of how they were created — no `kind: "device_input"` tag is
required in the JSON.

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

## Hardware Capabilities Dialog

The palette's **Hardware** button (at the very top of the left sidebar, above
the search box) opens a modal dialog that shows the result of the startup
hardware probe for every backend MediaMolder knows about.

### Backend cards

Each backend that was successfully opened shows a card with:

| Element | Content |
|---------|---------|
| **Device name** | Human-readable GPU/accelerator label (e.g. `NVIDIA GeForce RTX 4090`, `Apple VideoToolbox`). |
| **Backend label** | Canonical type string: `NVIDIA CUDA`, `Apple VideoToolbox`, `Intel QSV`, `AMD/Intel VAAPI`, etc. |
| **Video encode / Video decode** | Chips for every codec the backend can encode or decode, grouped separately. Codecs without a known `media_type` default to video. |
| **Audio encode / Audio decode** | Chips for audio codecs (e.g. `aac_at` on VideoToolbox). Shown only when the backend reports at least one audio codec; chip rows are prefixed `V` (video) / `A` (audio). |
| **Advanced** | Expandable `<details>` section showing the supported software pixel formats and, when reported, the maximum encode resolution. |

Chip colour: white for supported codecs, amber with a `⚠` icon for codecs
the backend nominally lists but whose hardware support could not be confirmed
(e.g. a CUDA codec on an older SM generation that lacks the hardware block).

### Unavailable backends

Any backend the probe attempted but could not open appears in a separate
**Unavailable backends** section below the cards, showing the error message
returned by `av_hwdevice_ctx_create`. Common causes:

- Driver not installed / GPU not present
- VAAPI device file (`/dev/dri/renderD128`) not accessible
- FFmpeg not built with support for that backend

### Button state

| Condition | Button label |
|-----------|-------------|
| Probe not yet complete | `Hardware …` (loading) |
| ≥ 1 usable backend | `Hardware  N available` (badge with count) |
| No usable backend | `Hardware  Software only` |

Clicking the button at any time opens the dialog. Clicking the overlay or
pressing Escape closes it.

### /api/hwaccel response shape

`GET /api/hwaccel` returns an array of `HWAccelProbe` objects:

```jsonc
[
  {
    "type": "videotoolbox",
    "available": true,
    "display_name": "Apple VideoToolbox",
    "filters": ["scale_vt", "vpp_qsv"],
    "codecs": [
      { "name": "h264_videotoolbox", "role": "encoder", "media_type": "video" },
      { "name": "hevc_videotoolbox", "role": "encoder", "media_type": "video" },
      { "name": "prores_videotoolbox", "role": "encoder", "media_type": "video" },
      { "name": "h264", "role": "decoder", "media_type": "video" }
    ],
    "max_width": -1,
    "max_height": -1,
    "sw_formats": ["nv12", "yuv420p", "p010le"]
  }
]
```

`max_width`/`max_height` of `-1` means the backend imposes no resolution
limit (`INT_MAX` sentinel suppressed).

### Codec detection coverage

MediaMolder uses `av_codec_iterate` to enumerate the codecs compiled into
LibAV and tests each one for hardware support via both
`AV_CODEC_HW_CONFIG_METHOD_HW_DEVICE_CTX` (decoders: VAAPI, CUDA/cuvid, VT
decode) **and** `AV_CODEC_HW_CONFIG_METHOD_HW_FRAMES_CTX` (encoders:
VideoToolbox, NVENC, QSV).  The latter flag was missing from an earlier
version of the scan, which caused VideoToolbox encoders to be invisible.

Because LibAV's codec registry is static (compiled in at build time —
`avcodec_register` was removed in FFmpeg 5.0), any codec not compiled into
your FFmpeg build will not appear.  In particular, **ProRes RAW encode and
hardware-accelerated ProRes RAW decode** are not representable in the LibAV
codec registry at all and will never appear in this dialog regardless of the
hardware present.  A future MediaMolder-native VideoToolbox path for ProRes
RAW is planned (see
[roadmap/hardware.md](roadmap/) for status).

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
