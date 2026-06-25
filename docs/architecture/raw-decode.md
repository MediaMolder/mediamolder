# Camera-RAW decode (LibRaw)

How MediaMolder develops camera RAW, why it is a separate path from the normal libav decode, and
the determinism boundaries that make it safe. User-facing usage lives in
[docs/raw-decode-guide.md](../raw-decode-guide.md).

## The problem

libav is a codec library. For camera RAW it has no demosaic of the colour-filter-array sensor
data and no white-balance stage, so it renders RAW to a **black/garbled frame**. Real RAW develop
needs a dedicated pipeline; the field standard is [LibRaw](https://www.libraw.org/) (digiKam,
Krita, ImageMagick). MediaMolder wraps LibRaw in the `raw` package behind a small, stable API.

## The three decode intents

RAW is read for different purposes with different determinism needs; only the **develop** intent
uses LibRaw:

| Intent                     | Path                          | Determinism need                          |
|----------------------------|-------------------------------|-------------------------------------------|
| Hash / dedup / thumbnail   | libav → embedded preview      | byte-identical cross-machine (load-bearing) |
| **Develop / export master**| **libav → LibRaw**            | quality first; reproducible per platform  |
| On-screen display          | host-native (out of scope)    | none (never feeds a hash)                 |

The load-bearing choice: **keep any pixel-hash/dedup on the stable embedded-preview raster**, and
use LibRaw only for the develop/export master. The preview's bytes are fixed in the file, so a
hash of them is already byte-identical everywhere. LibRaw's demosaic is *not* guaranteed
byte-identical across CPU architectures or LibRaw versions, so making it a dedup key would create
a re-hash/migration liability. Confining LibRaw to the develop intent puts its quality where it
matters and its non-determinism nowhere that would break dedup. MediaMolder's own engine has no
hash path — it just executes graphs — so in-engine this simply means: develop is **explicit**
(the `raw_decode` node / `raw-decode` CLI), never silently substituted for the libav decode.

## The `raw` package

```
raw/
  raw.go               // public API: IsRAW, IsUniform, ErrUnsupported (all builds)
  params.go            // pinned deterministic develop parameters (all builds)
  pins.go              // LibRaw version + source SHA-256 (all builds)
  decode_stub.go       //go:build !with_libraw — Capable()=false, Decode→ErrUnsupported
  decode_libraw.go     //go:build with_libraw  — cgo LibRaw decoder
  cgo_flags_libraw.go  //go:build with_libraw  — static link flags
```

Public API (frozen):

```go
func Capable() bool                            // true only in a with_libraw build
func IsRAW(path string) bool                   // pure extension check, BOTH builds
func Decode(path string) (image.Image, error)  // *image.RGBA, 8-bit sRGB, un-oriented, full-res
var ErrUnsupported = errors.New("raw: RAW decode not available in this build")
```

`Decode` runs LibRaw under fixed parameters (`init → open → unpack → set params → dcraw_process →
dcraw_make_mem_image`), copies the packed RGB8 into an `*image.RGBA` (A=255), and recovers any
cgo panic into an error so one corrupt file never crashes the host. The pinned parameters and the
rationale for each are tabulated in the [user guide](../raw-decode-guide.md#how-it-works);
`user_flip = 0` (no rotation) is load-bearing so a downstream orientation step is not
double-applied.

This mirrors the `face` package's boundary (`analyze_stub.go` / `analyze_onnx.go` /
`models.go`): one small package contains the optional native dependency; the default build links
a pure-Go stub so everything else compiles and runs unchanged.

## Linking: static cgo, not a runtime load

LibRaw is a normal cgo-linked C library, so it follows the **whisper / x264** pattern, not the
ONNX-Runtime one:

- **ONNX Runtime** (face/yolo) is `dlopen`'d at runtime and discovered via env/standard paths; its
  *model files* are SHA-256-verified at load.
- **LibRaw** is compiled in. `Capable()` therefore only reflects the build tag — there is no
  runtime file to find or verify. The SHA-256 pin (`raw/pins.go`) is checked by
  `scripts/bundle-libraw.sh` at **bundle time**: the pinned source tarball is verified before it
  is built. We ship no binary; the script builds a self-contained static `libraw.a`
  (jpeg/jasper/lcms/openmp off, zlib on) — on macOS a `lipo`'d arm64+x86_64 universal archive —
  and the `with_libraw` cgo flags link `-lraw -lc++ -lz`.

## Determinism, scoped

A develop is reproducible **for a given platform + pinned LibRaw version**: same file ⇒ same
bytes across runs (tested in `raw/integration_libraw_test.go`). Cross-architecture byte-identity
is *desirable and recorded* (the CI `libraw` leg exercises Linux/amd64; local runs cover macOS)
but **not load-bearing**, because dedup never uses LibRaw. Bumping the pinned version is a
documented "exports may differ" event with no effect on any preview-based hash.

## Phase 2 (future): transparent source develop

Today, develop is **explicit** — a plain `input` node still hands a RAW to libav (black). A future
enhancement could auto-develop at the source node: when `raw.IsRAW(path) && raw.Capable()`, decode
via LibRaw instead of libav transparently. That was deliberately deferred: it touches the core
source handler (timestamps / stream-info / EOF) and would need an `Input` schema flag — pulling in
the schema↔Go-struct, `jobTypes.ts`, and validation invariants. The explicit node keeps this
change self-contained (a dynamic `go_processor`, no schema churn) and preserves the determinism
boundary above.

## See also

- [Camera-RAW Decode Guide](../raw-decode-guide.md)
- [Go Processor Nodes](../go-processor-nodes.md)
- [Build & Packaging](../build-and-packaging.md)
