# Build & Packaging

This guide walks you through building MediaMolder from source on macOS, Linux,
and Windows. Each section is a complete, copy-and-paste procedure — you do not
need to be a developer to follow it, but you will need to be comfortable
running commands in a terminal.

## What you will need

Before you start, install the following on your computer:

- **Go 1.25.0 or later** — the programming language MediaMolder is written in.
  Download from <https://go.dev/dl/>.
- **FFmpeg 8.1 or later** — the multimedia library MediaMolder uses for video
  and audio processing. Each platform section below shows how to install it.
- **`pkg-config`** — a small helper program that tells the Go compiler where
  to find FFmpeg. It is installed alongside FFmpeg on most platforms.
- **Node.js 20 or later** — *only* required if you want to build the
  graphical user interface (GUI). Download from <https://nodejs.org/>.

After installation, verify each tool is on your `PATH` by opening a new
terminal and running:

```bash
go version
pkg-config --version
ffmpeg -version
```

If any command reports "not found", finish that tool's installation before
continuing.

---

## Build on macOS

```bash
# 1. Install FFmpeg and its development headers using Homebrew
brew install ffmpeg pkg-config

# 2. Download the MediaMolder source code
git clone https://github.com/MediaMolder/mediamolder.git
cd mediamolder

# 3. Compile the command-line program
go build ./cmd/mediamolder/

# 4. (Optional) Install it to your Go bin directory so it is on your PATH
go install ./cmd/mediamolder/

# 5. Verify the build
./mediamolder version
```

## Build on Linux (Debian / Ubuntu)

```bash
# 1. Install FFmpeg development libraries and pkg-config
sudo apt-get update
sudo apt-get install -y \
    libavcodec-dev libavformat-dev libavfilter-dev \
    libavutil-dev libswscale-dev libswresample-dev \
    pkg-config

# 2. Download the MediaMolder source code
git clone https://github.com/MediaMolder/mediamolder.git
cd mediamolder

# 3. Compile and install
go build ./cmd/mediamolder/
go install ./cmd/mediamolder/
```

## Build on Linux (Fedora / RHEL)

FFmpeg is not in the default Fedora repositories — enable
[RPM Fusion](https://rpmfusion.org/) first, then:

```bash
# 1. Install FFmpeg development libraries
sudo dnf install -y ffmpeg-devel pkg-config

# 2. Download and build
git clone https://github.com/MediaMolder/mediamolder.git
cd mediamolder
go build ./cmd/mediamolder/
go install ./cmd/mediamolder/
```

---

## Build on Windows

Windows builds need MSYS2, which provides the C compiler and FFmpeg libraries
that the Go compiler links against. Plan to follow these three sections in
order:

1. **One-time setup** — install MSYS2, Go, and (for the GUI) Node.js.
2. **Build the command-line program** — produces `mediamolder.exe`.
3. **Build the GUI** — wraps the command-line program plus the web interface
   into a single `.exe`.

### 1. One-time setup

1. Install [MSYS2](https://www.msys2.org/) using the official installer.
   Accept the default install path of `C:\msys64`.
2. Install [Go 1.25 or later](https://go.dev/dl/) using the Windows MSI.
3. Install [Node.js 20 or later](https://nodejs.org/) using the Windows MSI
   (only required if you plan to build the GUI).
4. Add `C:\msys64\mingw64\bin` to your Windows `PATH` environment variable.
   This is what lets the Go compiler find `gcc` and `pkg-config`.

   To do this in Windows 11: press <kbd>Win</kbd>, type
   "Edit the system environment variables", click **Environment Variables…**,
   select **Path** under "User variables", click **Edit…**, then **New**, and
   paste `C:\msys64\mingw64\bin`. Click OK on every dialog.

5. Open the **MSYS2 MinGW64** shortcut from the Start menu (it is labelled
   "MSYS2 MINGW64" and has a blue icon). Use this shell — *not* the plain
   "MSYS2" or "MSYS2 UCRT64" shell — for every MSYS2 command in this guide.

6. Inside the MSYS2 MinGW64 shell, install the C toolchain and FFmpeg
   libraries:

   ```bash
   pacman -S --needed \
       mingw-w64-x86_64-toolchain \
       mingw-w64-x86_64-ffmpeg \
       mingw-w64-x86_64-pkg-config
   ```

   Press <kbd>Enter</kbd> when prompted to accept the defaults.

7. Close and reopen any terminals so the new `PATH` takes effect.

### 2. Build the command-line program

Open **PowerShell** (or Windows Terminal) and run:

```powershell
# Download the source code
git clone https://github.com/MediaMolder/mediamolder.git
Set-Location mediamolder

# Compile
go build -o mediamolder.exe .\cmd\mediamolder\

# Verify
.\mediamolder.exe version
```

If the `go build` command reports `'gcc' is not recognized` or
`'pkg-config' not found`, your `PATH` does not include
`C:\msys64\mingw64\bin`. Revisit step 4 of the one-time setup.

### 3. Build the GUI

The GUI is a web interface that gets compiled into a single `.exe` alongside
the command-line program. The project's `Makefile` is written for Unix
systems, so on Windows you run the equivalent steps manually in PowerShell:

```powershell
# Step 1 — install the JavaScript packages used by the web interface
Set-Location frontend
npm ci

# Step 2 — compile the web interface (HTML, CSS, JavaScript)
npm run build
Set-Location ..

# Step 3 — copy the compiled web files into the location the Go compiler
#          will embed them from
Remove-Item -Recurse -Force internal\gui\dist -ErrorAction SilentlyContinue
New-Item  -ItemType Directory -Force internal\gui\dist | Out-Null
Copy-Item -Recurse frontend\dist\* internal\gui\dist\

# Step 4 — build the final single-file executable
go build -o mediamolder.exe .\cmd\mediamolder\
```

To launch the GUI, run:

```powershell
.\mediamolder.exe gui
```

This opens MediaMolder in your default web browser. To run the program
without opening a browser (useful for servers), add `--no-open`.

### Rebuilding after code changes

You do not need to repeat the full procedure every time you edit the source.
Once the one-time setup is done and `node_modules` is populated, use the
shortest sequence that covers your edit:

| You changed … | Steps you need to repeat |
| --- | --- |
| Only Go code (anything outside `frontend/`) | Step 4 — `go build` |
| Anything in `frontend/src` (TSX, CSS, etc.) | Steps 2, 3, **and** 4 — re-build the web UI, refresh the embed directory, then re-build the binary. Step 4 is required because the production assets are baked into the binary via `//go:embed`. |
| `frontend/package.json` (added or upgraded a JS package) | Steps 1, 2, 3, and 4 |
| Nothing — you just want a fresh binary | Step 4 |

A complete "rebuild after a frontend edit" looks like this from the
mediamolder folder:

```powershell
# Re-build the web UI
Set-Location frontend
npm run build
Set-Location ..

# Refresh the embedded copy that the Go binary serves
Remove-Item -Recurse -Force internal\gui\dist -ErrorAction SilentlyContinue
New-Item  -ItemType Directory -Force internal\gui\dist | Out-Null
Copy-Item -Recurse frontend\dist\* internal\gui\dist\

# Re-build the executable (use whichever -tags line matches your setup)
go build -o mediamolder.exe .\cmd\mediamolder\
```

If you built statically earlier, keep using your static tag line instead of
the bare `go build` above:

```powershell
go build -tags "ffstatic,ffstatic_windows_msys2" -o mediamolder.exe .\cmd\mediamolder\
```

#### Faster iteration with the dev server

While actively editing the frontend, skip the embed-and-rebuild cycle
entirely by running the Vite dev server alongside the Go backend:

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

---

## Advanced: Build with a custom or static FFmpeg

The procedures above link against a *shared* FFmpeg installation (the
`.so` / `.dylib` / `.dll` files installed by your package manager). If you
prefer a fully self-contained binary that does not depend on any FFmpeg
files at runtime, you can statically link against an FFmpeg source tree you
have compiled yourself by adding the `ffstatic` build tag.

### How the `ffstatic` tag finds FFmpeg

When you pass `-tags=ffstatic`, the file
[av/cgo_flags_static.go](../av/cgo_flags_static.go) is compiled in. It tells
the C compiler and linker to look for FFmpeg in a **sibling directory of the
mediamolder checkout**, using the relative path `../ffmpeg` (i.e.
`${SRCDIR}/../../ffmpeg` from inside the `av` package).

This means your folder layout must look like this:

```text
some-parent-folder/
├── ffmpeg/        ← FFmpeg source tree, already compiled with `./configure && make`
│   ├── libavcodec/libavcodec.a
│   ├── libavformat/libavformat.a
│   └── …
└── mediamolder/   ← this repository
    └── cmd/mediamolder/
```

The compiler picks up FFmpeg headers from `../ffmpeg`, and the linker pulls
the static archives (`libavcodec.a`, `libavformat.a`, `libavfilter.a`,
`libavutil.a`, `libswscale.a`, `libswresample.a`) from each `../ffmpeg/libav*`
subdirectory.

### Override the FFmpeg location (macOS / Linux)

If your FFmpeg source tree lives somewhere other than `../ffmpeg`, override
the cgo flags on the command line. The values you set on `CGO_CFLAGS` /
`CGO_LDFLAGS` are *added to* the ones baked into the file above, and the
linker honours whichever `-L` directory contains the library first:

```bash
# Tell the compiler where your FFmpeg source lives
export FFMPEG_SRC=/path/to/ffmpeg

CGO_CFLAGS="-I${FFMPEG_SRC}" \
CGO_LDFLAGS="\
    -L${FFMPEG_SRC}/libavcodec    \
    -L${FFMPEG_SRC}/libavformat   \
    -L${FFMPEG_SRC}/libavfilter   \
    -L${FFMPEG_SRC}/libavutil     \
    -L${FFMPEG_SRC}/libswscale    \
    -L${FFMPEG_SRC}/libswresample" \
go build -tags ffstatic ./cmd/mediamolder/
```

The `cgo_flags.go` / `cgo_flags_static.go` build-tag pair controls which
FFmpeg is linked:

| Build command | FFmpeg source |
|---|---|
| `make build` | System FFmpeg via `pkg-config` |
| `make build-gui` | System FFmpeg via `pkg-config` |
| `make build-static` | Local source tree (`-tags=ffstatic`) |
| `make build-gui-static` | Local source tree (`-tags=ffstatic`) |

### Static GUI build on Windows

To produce a single `mediamolder.exe` that does *not* depend on any MSYS2
DLLs, you combine **two** build tags:

| Tag | Provides | Source file |
| --- | --- | --- |
| `ffstatic` | The six FFmpeg `libav*` static archives from `../ffmpeg/libav*/*.a`. | [av/cgo_flags_static.go](../av/cgo_flags_static.go) |
| `ffstatic_windows_msys2` | The transitive codec and system libraries (libx264, libx265, libvpx, libopus, libass, zlib, …) from `C:/msys64/mingw64/lib`. | [av/cgo_flags_windows_static.go](../av/cgo_flags_windows_static.go) |

Because the two tags split the work, you need **both** an FFmpeg source tree
*and* a working MSYS2 installation.

**Step A — lay out the folders.** Place the FFmpeg source tree as a sibling
of your mediamolder checkout:

```text
E:\projects\
├── ffmpeg\        ← clone https://github.com/FFmpeg/FFmpeg.git here
└── mediamolder\
```

**Step B — compile FFmpeg as static archives.** In the MSYS2 MinGW64 shell:

```bash
cd /e/projects/ffmpeg

./configure \
    --disable-shared \
    --enable-static \
    --enable-gpl \
    --enable-libx264 --enable-libx265 \
    --enable-libvpx  --enable-libopus \
    --enable-libass  --enable-libfreetype \
    --disable-doc    --disable-programs

make -j$(nproc)
```

When this finishes you should see `libavcodec/libavcodec.a` and similar
files inside each `libav*` subdirectory — these are what the linker will
pull in.

**Step C — install the transitive codec libraries.** Still in the MSYS2
MinGW64 shell, install the codecs that FFmpeg depends on. Without these,
the linker will fail with `cannot find -lx264`, `-lass`, etc.:

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

**Step D — build the web interface.** In PowerShell:

```powershell
Set-Location E:\projects\mediamolder
Set-Location frontend
npm ci
npm run build
Set-Location ..
Remove-Item -Recurse -Force internal\gui\dist -ErrorAction SilentlyContinue
New-Item  -ItemType Directory -Force internal\gui\dist | Out-Null
Copy-Item -Recurse frontend\dist\* internal\gui\dist\
```

**Step E — build the statically linked executable.** In PowerShell, from the
mediamolder folder:

```powershell
go build -tags "ffstatic,ffstatic_windows_msys2" -o mediamolder.exe .\cmd\mediamolder\
```

#### Customising the paths

You may need to adjust the hard-coded paths if your layout differs from the
defaults:

- **FFmpeg lives somewhere other than `..\ffmpeg`** — set `CGO_CFLAGS` and
  `CGO_LDFLAGS` on the command line before `go build`, the same way as the
  macOS / Linux example above. PowerShell syntax:

  ```powershell
  $env:CGO_CFLAGS  = "-IC:/path/to/ffmpeg"
  $env:CGO_LDFLAGS = "-LC:/path/to/ffmpeg/libavcodec -LC:/path/to/ffmpeg/libavformat -LC:/path/to/ffmpeg/libavfilter -LC:/path/to/ffmpeg/libavutil -LC:/path/to/ffmpeg/libswscale -LC:/path/to/ffmpeg/libswresample"
  go build -tags "ffstatic,ffstatic_windows_msys2" -o mediamolder.exe .\cmd\mediamolder\
  ```

- **MSYS2 lives somewhere other than `C:\msys64`** — open
  [av/cgo_flags_windows_static.go](../av/cgo_flags_windows_static.go) and
  edit the `-LC:/msys64/mingw64/lib` directive on line 26 to match your
  installation prefix (for example `-LD:/dev/msys64/mingw64/lib`).

#### Verifying the build is self-contained

Open the produced `mediamolder.exe` in a tool like **Dependency Walker** or
run `dumpbin /dependents mediamolder.exe` from a Visual Studio command
prompt. The output should list only Windows system DLLs (`KERNEL32.dll`,
`USER32.dll`, `WS2_32.dll`, …). If you see `avcodec-*.dll`, `libx264-*.dll`,
or any `mingw*.dll`, the static link did not take effect — double-check
that you passed both build tags and that the `*.a` files exist in
`..\ffmpeg\libav*\`.

#### Troubleshooting linker errors

Static linking can fail with `undefined reference to …` errors when FFmpeg
was compiled with a feature whose backing Windows system library is not on
the link line in
[av/cgo_flags_windows_static.go](../av/cgo_flags_windows_static.go).

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

---

## Run the tests

To run the project's automated tests:

```bash
go test ./...
```

To run the tests against a static FFmpeg build instead:

```bash
go test -tags=ffstatic ./...
```

---

## Notes and licensing

- The MediaMolder project does **not** distribute pre-compiled binaries
  because of patent licensing concerns surrounding video codecs (H.264,
  H.265, etc.). When you build from source, you are responsible for
  understanding and obtaining a license for any patents that apply to the
  codecs you enable.
- The minimum supported FFmpeg version is **8.1**. Continuous integration
  runs against the latest 8.1.x patch release.
- The default build uses `pkg-config`, so any FFmpeg installation that
  publishes `.pc` files (Homebrew, apt, dnf, MSYS2) will work without
  additional configuration.
