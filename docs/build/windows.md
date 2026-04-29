# Build MediaMolder on Windows

This guide covers building the `mediamolder.exe` binary (CLI + library) and
the embedded GUI on Windows via MSYS2, plus the day-to-day rebuild loops
after editing code.

See also: [Cross-platform overview](../build_and_packaging.md) ·
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

## 5. Build the GUI

The GUI is a React/Vite app embedded into the Go binary via `//go:embed`.
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

# Step 4 — build the final single-file executable (use whichever -tags
#          line matches your setup)
go build -o mediamolder.exe .\cmd\mediamolder\
# or for static:
# go build -tags "ffstatic,ffstatic_windows_msys2" -o mediamolder.exe .\cmd\mediamolder\
```

Launch with:

```powershell
.\mediamolder.exe gui
```

Add `--no-open` to skip auto-opening the browser (useful for servers).

## 6. Rebuild loops after code changes

Pick the shortest sequence that covers your edit:

| You changed … | Steps to repeat |
| --- | --- |
| Go code only (anything outside `frontend/`) | Step 4 — `go build` |
| Frontend code (`frontend\src\**`) | Steps 2, 3, **and** 4 |
| `frontend\package.json` (added/upgraded a JS package) | Steps 1, 2, 3, and 4 |
| Nothing — fresh binary | Step 4 |

Step 4 is required after frontend edits because the production assets are
baked into the binary at compile time via `//go:embed`.

A complete "rebuild after a frontend edit" looks like this:

```powershell
Set-Location frontend
npm run build
Set-Location ..
Remove-Item -Recurse -Force internal\gui\dist -ErrorAction SilentlyContinue
New-Item  -ItemType Directory -Force internal\gui\dist | Out-Null
Copy-Item -Recurse frontend\dist\* internal\gui\dist\
go build -o mediamolder.exe .\cmd\mediamolder\
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

## 7. Run the tests

```powershell
go test .\...
# or for static:
go test -tags=ffstatic .\...
```

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
