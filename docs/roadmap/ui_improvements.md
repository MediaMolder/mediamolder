## Plan: make the GUI palette friendlier for non-experts

### A. "Common" vs "All" toggle — one switch, two curated views

**UX shape**
- Add a segmented control at the top of Palette.tsx, next to the search box: `[ Common · All ]` (default Common).
- A second toggle `[ Friendly names · Library names ]` (default `Friendly`).
- Both states persist in `localStorage` (`mm.palette.scope`, `mm.palette.naming`).
- When Common is active, the palette renders only entries flagged as common; the per-subcategory `Show N more…` link is replaced by a single inline `Show all in this section` link, and a sticky footer hint says *"Showing common nodes — switch to All to see every codec/filter."*

**Data model**
Add two fields to `NodeCatalogEntry` in api.go:
```go
Common    bool   `json:"common,omitempty"`     // surfaces in default "Common" view
FriendlyName string `json:"friendly_name,omitempty"` // "x264", "AAC (Fraunhofer)", "Opus"
Aliases   []string `json:"aliases,omitempty"`     // search-only synonyms ("h264", "avc", "mp4")
```
No frontend type changes needed beyond extending `PaletteEntry`.

**Curation source — single registry, not scattered**
Create `internal/gui/curation.go` with one declarative table. Keeps the policy in one auditable file:
```go
var commonNodes = map[string]NodeMeta{
  // encoders
  "libx264":    {Friendly: "x264", Aliases: []string{"h264","avc","mp4"}},
  "libx265":    {Friendly: "x265",  Aliases: []string{"hevc","h265"}},
  "libsvtav1":  {Friendly: "SVT-AV1",      Aliases: []string{"av1"}},
  "libvpx-vp9": {Friendly: "VP9",          Aliases: []string{"webm"}},
  "aac":        {Friendly: "AAC",          Aliases: []string{"m4a","mp4"}},
  "libopus":    {Friendly: "Opus",         Aliases: []string{"webm","ogg"}},
  "libmp3lame": {Friendly: "MP3",          Aliases: []string{}},
  "libfdk_aac": {Friendly: "AAC (Fraunhofer)"},
  "flac":       {Friendly: "FLAC"},
  "pcm_s16le":  {Friendly: "PCM 16-bit"},
  // hardware variants — common but grouped together
  "h264_videotoolbox": {Friendly: "H.264 (Apple VideoToolbox)", Aliases: []string{"hwaccel","mac"}},
  "hevc_videotoolbox": {Friendly: "H.265 (Apple VideoToolbox)"},
  "h264_nvenc":        {Friendly: "H.264 (NVIDIA NVENC)"},
  "hevc_nvenc":        {Friendly: "H.265 (NVIDIA NVENC)"},
  // filters — the ~30 a non-expert actually reaches for
  "scale": {Friendly: "Resize"},
  "crop":  {Friendly: "Crop"},
  "pad":   {Friendly: "Pad / letterbox"},
  "rotate":{Friendly: "Rotate"},
  "fps":   {Friendly: "Frame rate"},
  "eq":    {Friendly: "Brightness / contrast"},
  "drawtext":{Friendly: "Text overlay"},
  "subtitles":{Friendly: "Burn-in subtitles"},
  "volume":{Friendly: "Volume"},
  "loudnorm":{Friendly: "Loudness normalise (EBU R128)"},
  "atrim": {Friendly: "Trim audio"},
  "trim":  {Friendly: "Trim video"},
  "fade":  {Friendly: "Fade in/out"},
  "afade": {Friendly: "Audio fade"},
  // ... ~30-40 entries total
}
```
`handleListNodes` looks each emitted entry up by `Name`; if present, sets `Common=true` and copies `FriendlyName`/`Aliases`.

A `TestCommonNodesAreLinked` unit test asserts every key resolves to an actual `av.ListCodecs()` / `av.ListFilters()` entry — catches typos when FFmpeg renames something.

### B. Friendly-name resolution (one source of truth, both views)

Same `commonNodes` table powers the **Friendly names** toggle for the Common view *and* the All view. When toggle = `Friendly`:
- Palette label = `entry.friendly_name` if present, else `prettyEncoderName(name, longName)` falls back as today.
- `MMNode` heading mirrors the same lookup so dropping a node onto the canvas keeps the friendly heading.
- Inspector adds a sub-text under the friendly name showing the canonical library name in monospace (`libx264`) so power users can still see what's actually invoked.

Centralise the lookup in `frontend/src/lib/friendlyNames.ts` keyed by `entry.name`; React components read through one helper `displayName(entry, mode)`. Avoids divergence between palette / canvas / inspector.

### C. Hardware-encoder grouping

Today every `*_nvenc` / `*_videotoolbox` / `*_qsv` / `*_vaapi` / `*_amf` shows up flat under `Video encoders`. Replace with a third subcategory level:
- `Encoders › Video › Software` (libx264, libx265, libsvtav1, libvpx-vp9, …)
- `Encoders › Video › Hardware` (auto-grouped by suffix)
- A small chip on each hardware entry — `[NVIDIA]`, `[Apple]`, `[Intel QSV]`, `[AMD AMF]`, `[VAAPI]` — derived from the suffix in api.go.
- "Hardware" subcategory is collapsed by default in Common view.

### D. "Recipes" panel — the strongest leverage for non-experts

Above the palette, add a small **Recipes** dropdown:
- *MP4 for web (x264 + AAC)*
- *MP4 for archive (x265 + AAC, slow preset)*
- *WebM for web (VP9 + Opus)*
- *Audio-only MP3*
- *GIF preview*
- *Lossless trim (stream copy)*
- *Burn-in subtitles*

Each recipe is a tiny JSON snippet under `internal/gui/recipes/*.json` served from a new `GET /api/recipes` endpoint — picking one inserts the connected sub-graph at the cursor. Reuses the existing `loadJob` path (zero new runtime). Recipes naturally reference friendly names. This is what most non-technical users actually want.

### E. Search improvements — friendly-first

Current search matches `name` + `description`. Extend to also match `friendly_name` + `aliases` (FE side, no API change beyond the new fields). So typing `h264` finds `libx264`; `mp3` finds `libmp3lame`; `loudness` finds `loudnorm`.

Highlight the matched substring in the palette row to make the relationship obvious.

### F. Encoder Inspector — preset chips instead of CRF/preset/tune from scratch

For the most common video encoders (`libx264`, `libx265`, `libsvtav1`), add a "Quality preset" row at the top of the encoder form with three chips: `Web (CRF 23, preset medium)`, `Archive (CRF 18, preset slow)`, `Fast preview (CRF 28, preset veryfast)`. Picking one fills `params.crf` / `params.preset`; the existing AVOption form remains below for power users.

### G. Implementation order (smallest landable slices)

1. **Schema + curation table** — Common / `FriendlyName` / `Aliases` on `NodeCatalogEntry` + `internal/gui/curation.go`. Test that every key resolves. ~half-day. ✅ landed
2. **Palette toggle** — segmented control + filter logic in Palette.tsx; persistence in `localStorage`. ~half-day. ✅ landed
3. **Friendly-name helper** — `displayName()` used by Palette, MMNode, Inspector heading. ~half-day. ✅ landed
4. **Hardware sub-grouping + chips** — pure `classifyEncoder()` logic in api.go + a small CSS chip. ~half-day.
5. **Recipes endpoint + dropdown** — 6–8 starter JSONs. ~1 day.
6. **Encoder preset chips** — additive, only for the three common video encoders. ~half-day.
7. **Search across `friendly_name` / `aliases` + match highlighting**. ~half-day.

Each slice is independently shippable, individually doc-able in the CHANGELOG, and adds no new build dependencies. None changes pipeline semantics, runtime, or schema — purely GUI surface.

### Doc updates per slice
- gui.md Quickstart: mention the Common/All and Friendly/Library toggles, the Recipes menu.
- ffmpeg-coverage-roadmap.md §6.8: spawn this as a new item (e.g. **#54a Friendly palette + recipes**) so it appears alongside #44.

### What this does *not* try to do
- No removal or hiding of features from the underlying schema — every codec / filter remains reachable via the All toggle. Power users keep parity.
- No translation / i18n layer (out of scope until a localised release is on the roadmap).
- No telemetry-driven "popular" promotion — the curation list is hand-maintained; review on FFmpeg version bumps.