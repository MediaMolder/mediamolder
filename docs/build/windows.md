# Build MediaMolder on Windows

This guide covers building the `mediamolder.exe` binary (CLI + library) and
the embedded GUI on Windows via MSYS2, plus the day-to-day rebuild loops
after editing code.

See also: [Cross-platform overview](../build-and-packaging.md) ·
[macOS](macos.md) · [Linux](linux.md).

## 1. One-time setup

1. **Install [MSYS2](https://www.msys2.org/)** using the official installer.
   Accept the default install path of `C:\msys64`.
2. **Install [Go 1.25 or later](https://go.dev/dl/)** using the Windows MSI.
3. **Install [Node.js 20 or later](https://nodejs.org/)** using the Windows
   MSI (only required if you plan to build the GUI).
4. **Add `C:\msys64\mingw64\bin` to your Windows `PATH`** so the Go compiler
   can find `gcc` and `pkg-config`.

   In Windows 11: press <kbd>Win</kbd>, type
   "Edit the system environment variables", click **Environment Variables…**,
   select **Path** under "User variables", click **Edit…**, then **New**, and
   paste `C:\msys64\mingw64\bin`. Click OK on every dialog.

5. Open the **MSYS2 MinGW64** shortcut from the Start menu (blue icon
   labelled "MSYS2 MINGW64") and install the C toolchain and FFmpeg:

   ```bash
   pacman -S --needed \
       mingw-w64-x86_64-toolchain \
       mingw-w64-x86_64-ffmpeg \
       mingw-w64-x86_64-pkg-config
   ```

6. Close and reopen any terminals so the new `PATH` takes effect.

Verify in PowerShell:

```powershell
go version          # 1.25 or later
pkg-config --version
ffmpeg -version     # 8.1 or later
node --version      # 20 or later (GUI only)
```

## 2. Get the source

```powershell
git clone https://github.com/MediaMolder/mediamolder.git
Set-Location mediamolder
```

## 3. Build the CLI / library (default FFmpeg)

Uses the MSYS2 FFmpeg via `pkg-config`. From PowerShell:

```powershell
go build -o mediamolder.exe .\cmd\mediamolder\
.\mediamolder.exe version
```

If `go build` reports `'gcc' is not recognized` or `'pkg-config' not found`,
your `PATH` does not include `C:\msys64\mingw64\bin`. Revisit step 4 of the
one-time setup.

The resulting `mediamolder.exe` depends on MSYS2 DLLs at runtime (see §4b
for a self-contained build).

## 4. Build with a custom FFmpeg

### 4a. Custom shared FFmpeg (still uses pkg-config)

In PowerShell, point `pkg-config` at your custom build's `.pc` files:

```powershell
$env:PKG_CONFIG_PATH = "C:\path\to\ffmpeg\build\lib\pkgconfig;$env:PKG_CONFIG_PATH"
go build -o mediamolder.exe .\cmd\mediamolder\
```

### 4b. Static FFmpeg from source (`ffstatic` + `ffstatic_windows_msys2`)

Produces a single `.exe` that does **not** depend on any MSYS2 DLLs.

**Step A — lay out the folders.** Place the FFmpeg source tree as a sibling
of your mediamolder checkout:

```text
E:\projects\
├── ffmpeg\        ← clone https://github.com/FFmpeg/FFmpeg.git here
└── mediamolder\
```

**Step B — install codec libraries** (so the linker can find the transitive
deps that FFmpeg's encoders pull in). In the **MSYS2 MinGW64** shell:

```bash
pacman -S --needed \
    mingw-w64-x86_64-x264 \
    mingw-w64-x86_64-x265 \
    mingw-w64-x86_64-libvpx \
    mingw-w64-x86_64-opus \
    mingw-w64-x86_64-libass \
    mingw-w64-x86_64-freetype \
    mingw-w64-x86_64-harfbuzz
```

**Step C — compile FFmpeg as static archives.** Still in MSYS2 MinGW64:

```bash
cd /e/projects/ffmpeg
./configure \
    --disable-shared --enable-static --enable-gpl \
    --enable-libx264 --enable-libx265 \
    --enable-libvpx  --enable-libopus \
    --enable-libass  --enable-libfreetype \
    --disable-doc    --disable-programs
make -j$(nproc)
```

You should see `libavcodec/libavcodec.a` and friends in each `libav*`
subdirectory.

**Step D — build with both static tags.** In PowerShell from the
`mediamolder` folder:

```powershell
go build -tags "ffstatic,ffstatic_windows_msys2" -o mediamolder.exe .\cmd\mediamolder\
```

The two tags split the work:

| Tag | Provides | Source file |
| --- | --- | --- |
| `ffstatic` | The six FFmpeg `libav*` archives from `..\ffmpeg\libav*\*.a`. | [av/cgo_flags_static.go](../../av/cgo_flags_static.go) |
| `ffstatic_windows_msys2` | Transitive codec/system libs (libx264, libx265, libvpx, libopus, libass, zlib, …) from `C:\msys64\mingw64\lib`. | [av/cgo_flags_windows_static.go](../../av/cgo_flags_windows_static.go) |

#### Custom paths

- **FFmpeg lives somewhere other than `..\ffmpeg`** — set CGO flags
  inline:

  ```powershell
  $env:CGO_CFLAGS  = "-IC:/path/to/ffmpeg"
  $env:CGO_LDFLAGS = "-LC:/path/to/ffmpeg/libavcodec -LC:/path/to/ffmpeg/libavformat -LC:/path/to/ffmpeg/libavfilter -LC:/path/to/ffmpeg/libavutil -LC:/path/to/ffmpeg/libswscale -LC:/path/to/ffmpeg/libswresample"
  go build -tags "ffstatic,ffstatic_windows_msys2" -o mediamolder.exe .\cmd\mediamolder\
  ```

- **MSYS2 lives somewhere other than `C:\msys64`** — edit the
  `-LC:/msys64/mingw64/lib` directive on line 26 of
  [av/cgo_flags_windows_static.go](../../av/cgo_flags_windows_static.go) to
  match your installation prefix.

#### Verify the binary is self-contained

Run `dumpbin /dependents mediamolder.exe` from a Visual Studio command
prompt. The output should list only Windows system DLLs (`KERNEL32.dll`,
`USER32.dll`, `WS2_32.dll`, …). If you see `avcodec-*.dll`, `libx264-*.dll`,
or any `mingw*.dll`, the static link did not take effect — double-check
that you passed both build tags and that the `*.a` files exist in
`..\ffmpeg\libav*\`.

## 4. Build the Graphical User Interface (GUI)

The GUI is a React/Vite app embedded into the Go binary via `//go:embed`.
When you run the GUI, mediamolder launches a local web server that 
hosts the user interface in your default browser.

The project's `Makefile` is Unix-only, so on Windows you run the equivalent
steps manually in PowerShell:

```powershell
# Step 1 — install the JavaScript packages used by the web interface
Set-Location frontend
npm ci

# Step 2 — compile the web interface (HTML, CSS, JavaScript)
npm run build
Set-Location ..

# Step 3 — copy the compiled web files into the //go:embed source dir
Remove-Item -Recurse -Force internal\gui\dist -ErrorAction SilentlyContinue
New-Item  -ItemType Directory -Force internal\gui\dist | Out-Null
Copy-Item -Recurse frontend\dist\* internal\gui\dist\

# Step 4 — build the final single-file executable
# Use the same build command you used in §3:
go build -o mediamolder.exe .\cmd\mediamolder\                          # Option A
# go build -tags "ffstatic,ffstatic_windows_msys2" -o mediamolder.exe .\cmd\mediamolder\  # Option B2
```

Launch with:

```powershell
.\mediamolder.exe gui
```

Add `--no-open` to skip auto-opening the browser (useful for servers).

## 5. Rebuild loops after code changes

Pick the shortest sequence that covers your edit. The step numbers below
refer to the numbered steps inside §4 (Build the GUI) above.

| You changed … | Steps to repeat (from §4) |
| --- | --- |
| Go code only (anything outside `frontend/`) | Step 4 — `go build` only |
| Frontend code (`frontend\src\**`) | Steps 2, 3, **and** 4 |
| `frontend\package.json` (added/upgraded a JS package) | Steps 1, 2, 3, and 4 |
| Nothing — fresh binary | Step 4 |

Step 4 (`go build`) is always required after frontend edits because the
production assets are baked into the binary at compile time via `//go:embed`.

A complete "rebuild after a frontend edit" looks like this:

```powershell
Set-Location frontend
npm run build
Set-Location ..
Remove-Item -Recurse -Force internal\gui\dist -ErrorAction SilentlyContinue
New-Item  -ItemType Directory -Force internal\gui\dist | Out-Null
Copy-Item -Recurse frontend\dist\* internal\gui\dist\
go build -o mediamolder.exe .\cmd\mediamolder\   # replace with Option B2 command if applicable
```

### Faster GUI iteration: dev server

While actively editing the frontend, skip the embed-and-rebuild cycle:

```powershell
# Terminal 1 — Vite hot-reload server on http://localhost:5173
Set-Location frontend
npm run dev
```

```powershell
# Terminal 2 — Go backend in dev mode (no embedded assets, proxies to Vite)
.\mediamolder.exe gui --dev --no-open
```

Frontend edits reload instantly in your browser. Go code changes still
require a `go build` and a restart of terminal 2.

## 6. Run the tests

```powershell
go test .\...                             # Option A — default FFmpeg
go test -tags=ffstatic .\...             # Option B2 — static FFmpeg
```

## Optional built-in nodes

A few processors are **opt-in**: they sit behind a build tag (so they don't add
a cgo dependency for builds that don't need them) or need an external runtime
service. Install the prerequisites below **before** building or running.

| Node | Build tag | Needs | Runtime env / config |
| --- | --- | --- | --- |
| `whisper_stt` (speech-to-text) | `with_whisper` | whisper.cpp / `libwhisper` + a ggml model | `model` param → ggml model path |
| `yolo_v8` (object detection) | `with_onnx` | ONNX Runtime DLL + a `.onnx` model | `ONNXRUNTIME_SHARED_LIBRARY_PATH`; `model` + `labels_file` params |
| `face_detect` (face detection + embeddings) | `with_onnx` | ONNX Runtime DLL + the two face models | `ONNXRUNTIME_SHARED_LIBRARY_PATH`, `MEDIAMOLDER_FACE_MODELS` |
| `vidi_analyzer` (multimodal) | *(none)* | a running Vidi 2.5 service | `service_url` param |
| `twelvelabs_*` (cloud understanding) | *(none)* | TwelveLabs API key | `TWELVELABS_API_KEY` |

> `whisper_stt` binds **whisper.cpp** (`ggml-org/whisper.cpp`), **not** the
> OpenAI Python `whisper` package — the latter does not produce `libwhisper`.
>
> `with_onnx` enables **both** `yolo_v8` **and** `face_detect` (and the
> `mediamolder face-detect` CLI) — one tag, all ONNX nodes.

### Combining tags

Windows builds list tags directly in `-tags "…"` (comma-separated, no spaces) —
there is no `EXTRA_TAGS` (that's a Makefile convenience; Windows builds by hand).
Stack them as needed, e.g. static FFmpeg + whisper + the ONNX nodes:

```powershell
go build -tags "ffstatic,ffstatic_windows_msys2,with_whisper,with_onnx" -o mediamolder.exe .\cmd\mediamolder\
```

### Downloading models — how and when

Models are **not** shipped and are loaded at **run time**, not build time: build
with the node's tag, then download the model(s) and point an env var / param at
them before running.

| Node | Download what | How (run `.sh` in the MSYS2 shell) | Point at it with |
| --- | --- | --- | --- |
| `whisper_stt` | a ggml/gguf speech model | `whisper.cpp/models/download-ggml-model.sh base.en` | the node's `model` param |
| `face_detect` / `face-detect` | YOLOv8-face + SFace `.onnx` | `./scripts/fetch-face-models.sh` (SHA-256-verified) | `MEDIAMOLDER_FACE_MODELS` or `--models-dir` |
| `yolo_v8` | a YOLOv8 `.onnx` + labels | export from Ultralytics / your own | `model` + `labels_file` params |

Models can be large and (for the face detector) carry a **copyleft** licence, so
they are **never committed** — `fetch-face-models.sh` defaults to the git-ignored
`testdata/face_models/`.

### whisper_stt (whisper.cpp)

Build whisper.cpp in the **MSYS2 MinGW64** shell and install it into the MinGW64
prefix so `pkg-config` (and the cgo build) can find `whisper.pc`:

```bash
pacman -S mingw-w64-x86_64-cmake mingw-w64-x86_64-ninja

git clone https://github.com/ggml-org/whisper.cpp
cmake -S whisper.cpp -B whisper.cpp/build -G Ninja
cmake --build whisper.cpp/build -j
cmake --install whisper.cpp/build --prefix /mingw64   # puts whisper.pc on PKG_CONFIG_PATH
```

Then build MediaMolder with the `with_whisper` tag. It links libwhisper
dynamically via `pkg-config`, so it works with or without `ffstatic` — combine
it with the static-FFmpeg tags from §3 for a static-FFmpeg + dynamic-whisper
binary:

```powershell
go build -tags "ffstatic,ffstatic_windows_msys2,with_whisper" -o mediamolder.exe .\cmd\mediamolder\
```

At runtime, put `whisper.dll` (and the ggml DLLs) on `PATH` or next to
`mediamolder.exe` — Windows has no rpath. Fetch a model (you supply this —
MediaMolder ships none), point the node's `model` param at it, and set
`WHISPER_TEST_MODEL` to run the gated tests:

```powershell
$env:WHISPER_TEST_MODEL = "C:\path\to\ggml-base.en.bin"
go test -tags "with_whisper" .\av\... .\processors\...
```

> **Note:** `with_whisper` links libwhisper **dynamically** and is independent
> of `ffstatic` (FFmpeg). The separate `whisperstatic` tag (fully static
> libwhisper) is wired for macOS/Linux only
> ([av/cgo_flags_whisper_static.go](../../av/cgo_flags_whisper_static.go) has no
> Windows branch) — do not use it here. Usage, params, and output formats:
> [Whisper Speech-to-Text Guide](../whisper-stt-guide.md).

### yolo_v8 (ONNX Runtime)

Download the Windows zip from the
[ONNX Runtime releases](https://github.com/microsoft/onnxruntime/releases),
extract `onnxruntime.dll` next to `mediamolder.exe`, then:

```powershell
$env:ONNXRUNTIME_SHARED_LIBRARY_PATH = "C:\path\to\onnxruntime.dll"
go build -tags=with_onnx -o mediamolder.exe .\cmd\mediamolder\
```

You also need a `.onnx` model and a labels file — see the
[YOLOv8 Guide](../yolov8-guide.md).

### face_detect (ONNX Runtime + face models)

`face_detect` and the `mediamolder face-detect` CLI share the `with_onnx` tag
with `yolo_v8` (same `onnxruntime.dll`), plus two bundled face models. Fetch the
models in the MSYS2 shell, then build with the tag:

```bash
# In the MSYS2 MinGW64 shell, from the MediaMolder repo root:
./scripts/fetch-face-models.sh          # fetches + SHA-256-verifies into testdata/face_models/
```

```powershell
# Back in PowerShell:
$env:ONNXRUNTIME_SHARED_LIBRARY_PATH = "C:\path\to\onnxruntime.dll"
$env:MEDIAMOLDER_FACE_MODELS = "$PWD\testdata\face_models"
go build -tags=with_onnx -o mediamolder.exe .\cmd\mediamolder\
```

The detector (YOLOv8-face) is **AGPL-3.0** and the embedder (SFace) is
Apache-2.0; both are loaded as **data** at run time (never linked) and
SHA-256-verified on load. MediaMolder ships neither — keep them out of any
committed tree. See the [Face Detection Guide](../face-detection-guide.md).

### vidi_analyzer / twelvelabs_*

No build tag. `vidi_analyzer` needs a running
[Vidi 2.5](https://github.com/bytedance/vidi) service (pass its `service_url`);
the `twelvelabs_*` nodes need a [TwelveLabs](https://twelvelabs.io) API key via
`TWELVELABS_API_KEY` (`$env:TWELVELABS_API_KEY`), the `api_key` param, or
`%USERPROFILE%\.config\mediamolder\twelvelabs.json`. See the
[Vidi 2.5](../vidi-guide.md) and [TwelveLabs](../twelvelabs.md) guides.

### raw_decode (LibRaw)

Camera-RAW develop (the `raw_decode` node + `mediamolder raw-decode`). Build tag
`with_libraw`. LibRaw is bundled — `scripts/bundle-libraw.sh` builds a
SHA-256-pinned static lib from source. Run it from the **MSYS2 MinGW64 shell**
(the same environment §prereqs installs the toolchain into — the archive's CRT
must match the gcc that cgo links with; the plain MSYS shell's gcc builds a
cygwin-runtime archive that fails the go link, and the script warns if it sees
one). It builds the host `x86_64` arch; the universal `lipo` step is macOS-only:

```bash
scripts/bundle-libraw.sh            # → third_party/libraw (gitignored)
# The Makefile is Unix-only; build directly with the tag (add ffstatic as needed):
CGO_LDFLAGS_ALLOW='.*' go build -tags with_libraw -o mediamolder.exe ./cmd/mediamolder
```

LibRaw itself links statically — it adds **no** MinGW runtime DLLs (no libraw.dll, no
libstdc++-6.dll, no zlib1.dll) beyond the libav DLLs + libwinpthread every cgo build here
already imports. Confirm with `./mediamolder.exe raw-setup`.
See [Camera-RAW Decode Guide](../raw-decode-guide.md).

### Combining nodes in one binary

The build tags stack, so one binary can carry several nodes. The `Makefile` is
Unix-only, so combine the tags directly — static FFmpeg + `whisper_stt` +
`yolo_v8`:

```powershell
go build -tags "ffstatic,ffstatic_windows_msys2,with_whisper,with_onnx" -o mediamolder.exe .\cmd\mediamolder\
```

For a GUI single-binary, build and embed the frontend first (see §4), then run
the same command. Each enabled node keeps its own runtime requirement:
`whisper.dll` + ggml DLLs for `whisper_stt`, and `onnxruntime.dll` +
`ONNXRUNTIME_SHARED_LIBRARY_PATH` for `yolo_v8` — on `PATH` or beside the exe.

## Troubleshooting linker errors

Static linking can fail with `undefined reference to …` errors when FFmpeg
was compiled with a feature whose backing Windows system library is not on
the link line in
[av/cgo_flags_windows_static.go](../../av/cgo_flags_windows_static.go).

Map the error to the missing `-l<name>` flag, then add it to the
`#cgo LDFLAGS:` line in that file:

| Undefined symbols include … | Add to `cgo_flags_windows_static.go` |
| --- | --- |
| `NCryptOpenStorageProvider`, `NCryptImportKey`, `NCryptExportKey`, `NCryptDeleteKey`, `NCryptFreeObject` | `-lncrypt` |
| `AcquireCredentialsHandleA`, `InitializeSecurityContextA`, `AcceptSecurityContext`, `EncryptMessage`, `DecryptMessage`, `FreeContextBuffer`, `ApplyControlToken`, `DeleteSecurityContext`, `FreeCredentialsHandle`, `QueryContextAttributesA`, `SetContextAttributesA` | `-lsecur32` |
| `MFCreateAttributes`, `MFCreateMediaType` (Media Foundation) | `-lmfplat -lmf` |
| `D3D11CreateDevice`, `ID3D11Device*` | `-ld3d11 -ldxgi` |
| `IID_IMMDevice…` (WASAPI) | `-lksuser` |
| `getaddrinfo`, `freeaddrinfo` (winsock) | `-lws2_32 -liphlpapi` |

Both `-lncrypt` and `-lsecur32` are already on the default link line because
recent FFmpeg releases enable Schannel TLS by default. If you upgrade FFmpeg
and a *new* family of `undefined reference` errors appears, look up the
function names on
<https://learn.microsoft.com/en-us/windows/win32/api/> — the page header
shows which `.lib` (and therefore which `-l<name>`) provides the symbol.

After editing the file, re-run the `go build` command. There is no need to
rebuild FFmpeg.
