# Proposal: Improved filter properties panel

Status: Implemented (initial pass) — see Wave 8 #47.5 in the
CHANGELOG. Followups (modified-options pin, AVOption-`unit` grouping,
generated-CLI footer, "Reset to defaults", typed metadata key
autocomplete) remain open.
Scope: GUI Inspector — applies to every node whose `type` is
`filter`, `filter_source`, or `filter_sink` (i.e. every libavfilter
node). **Encoders are out of scope** — they keep their existing
`EncoderForm` (which has its own conventions around codec selection,
two-pass, and per-stream overrides).

## 1. Motivation

The current filter Inspector (`FilterForm` rendered inside
`NodeForm`) exposes the full backing `NodeDef` shape as four free-text
inputs at the top of the panel:

| Field    | Today                                | Problem                                                                 |
|----------|--------------------------------------|-------------------------------------------------------------------------|
| `ID`     | editable text input                  | Renaming a live node breaks every edge that targets it. Almost never the right action. |
| `Type`   | editable text input                  | Changing `filter` → `encoder` mid-edit produces a malformed node — there is no UI flow that benefits from this being editable. |
| `Filter` | editable text input                  | Replacing `scale` with `crop` in place silently invalidates every previously-set option. The user almost always wants "delete + add new node from the Palette". |
| Options  | one row per AVOption, all stacked    | Label, control, type-meta, range-meta, and help all get equal visual weight; the actual editable surface is hard to find. |

The screenshots in the original report (`acompressor`, `bwdif`)
illustrate the consequence: the user sees three identical-looking
text inputs (`ID` / `Type` / `Filter`) before they reach a single
real option, and below each option they read four separate dim lines
(`int · default 0 · ≤ 1` then `set mode`) just to learn what the
field does and what its default is.

## 2. Goals

1. **Hide structural fields by default.** `ID`, `Type`, and `Filter`
   identify the node — they are not properties of it. They belong in a
   compact non-editable header, with an explicit "Advanced" affordance
   for the rare power-user who needs them.
2. **One option = one obvious editable widget.** The control should
   be the dominant visual element of each row.
3. **Show the default in the control itself**, not as separate prose.
   The user already reads the value field; "default" repetition is
   noise.
4. **Move the allowed range to a quiet trailing badge** — visible
   but subordinate to the control. The range alone is enough to
   convey the type in almost every case (`0.015625–64` reads as
   floating-point, `0–255` reads as integer, `≤ 1` paired with a
   bool select reads as boolean). Drop the badge entirely for enums
   (the `<select>` already conveys both type and domain) and for
   string-typed options (no meaningful range to show).
5. **Surface the help text as the row's secondary label**, not as a
   third dim line below the meta. The help is what the user actually
   needs to choose a value.
6. **Keep behaviour reversible.** Empty value = remove the key from
   `def.params` (libavfilter falls back to its compiled-in default) —
   this stays.

## 3. Proposed layout

### 3.1 Header (replaces the ID / Type / Filter block)

```
┌──────────────────────────────────────────────────┐
│  acompressor                            [Delete] │   ← node title (filter display name)
│  Audio filter · id: acompressor                  │   ← subtitle: kind + monospace id
│  Compress audio dynamic range.                   │   ← filter description, dim
│                                          ▸ Advanced │ ← collapsible: id rename, type, filter swap
└──────────────────────────────────────────────────┘
```

* **Title** = filter display name (`info.description` short form, falls
  back to `info.name`). Bold, same size as today.
* **Subtitle** = "`<kind>` filter · id: `<id>`" where `<kind>` ∈
  {Audio, Video, Subtitle} derived from the filter's media types
  reported by `/api/filters`. Monospace `id` so it reads as an
  identifier, not prose.
* **Description** = full `info.description`.
* **▸ Advanced** = collapsed by default. When opened, exposes:
  * **Rename node id** — editable `Field` *with a confirm step*. Edits
    the node id and updates every edge in `graph.edges` that
    references the old id (existing rename infrastructure already
    used by the canvas drag-drop add path can be reused). Validation:
    must be non-empty and unique within the current pipeline.
  * **Filter type** — read-only badge (`filter`, `filter_source`,
    `filter_sink`). Not editable from the Inspector — type is
    determined when the node is created from the Palette.
  * **Filter name** — read-only with a small "Replace…" link that
    opens a confirmation prompt explaining "This will discard all
    current options" before swapping `def.filter`.

This collapses the three-input wall to one line in the common case
without losing any capability.

### 3.2 Option row

```
LEVEL_IN
set input gain                                 ┌─────────────┐
                                               │ 1           │  0.015625–64.0
                                               └─────────────┘

MODE
set mode                                       ┌─────────────┐
                                               │ downward  ▾ │
                                               └─────────────┘

THRESHOLD
set threshold                                  ┌─────────────┐
                                               │ 0.125       │  0.000976563–1.0
                                               └─────────────┘
```

Concretely each row has:

1. **Name** (uppercase, label weight) on its own line — unchanged.
2. **Help text** (the AVOption `help` string) immediately below the
   name as the row's *primary* descriptive line. Same size as today's
   meta line, but in `--text` colour rather than `--text-dim` so it
   reads as the row's purpose.
3. **Control** — the editable widget. The control's *placeholder*
   shows the libavfilter default; once the user enters a value it
   replaces the placeholder. If the user clears the field the
   placeholder reappears (because empty value = "use default" already
   in our save model). This eliminates the "default 0.125" prose
   entirely.
4. **Range badge** — a single trailing dim line aligned to the
   right of the control, showing only the allowed numeric range
   (`0.015625–64`, `≤ 1`, `≥ 0`). The `default <n>` token is
   **dropped** because the placeholder shows it; the type token is
   **dropped** because the range already implies it (decimals ⇒
   floating-point, whole numbers ⇒ integer). The badge is suppressed
   entirely for enums (the `<select>` conveys the type), strings
   (no meaningful range), and any option whose AVOption min/max are
   sentinels (`INT_MIN`/`INT_MAX`, `±FLT_MAX`).

   To make the "range implies type" cue reliable, the badge formatter
   normalises range endpoints by AVOption type:

   * **Float / double / rational** — both endpoints rendered with at
     least one decimal place. `0–1` becomes `0.0–1.0`, `1–20` becomes
     `1.0–20.0`. Endpoints that already carry a fractional part are
     left untouched (`0.015625–64` becomes `0.015625–64.0`).
   * **Integer types** (`int`, `int64`, `uint64`, `flags`,
     `pixel_fmt`, `sample_fmt`, etc.) — endpoints rendered as plain
     integers, no decimal point. `0.0–1.0` (rare but possible if the
     AVOption author wrote `1.0` literally) collapses to `0–1`.

   The same type-awareness governs **input coercion**: when an
   integer-typed option receives a fractional value (paste, manual
   typing, or a stale `def.params` entry from an older schema), the
   control rounds to the nearest valid integer on commit
   (`Math.round`, then clamp to `[min, max]`). The committed string
   in `def.params` is the rounded value — what the user sees is what
   gets serialised. Float/double options accept integers verbatim
   (`5` is a valid `double`).

### 3.3 Type-aware controls

Mostly already present in `controls/OptionControl.tsx`. The proposal
formalises the rules:

| AVOption shape                                            | Widget                                                                 |
|-----------------------------------------------------------|------------------------------------------------------------------------|
| Has named constants (enum / flag-set)                     | `<select>` (single) or chip toggles (flag-set, e.g. `scale`'s `flags`)  |
| Boolean (`bool`, or `int` with min=0 max=1 named consts)  | segmented toggle: `true` / `false` (plus a `clear` ✕ to revert to the libavfilter default). Never rendered as `0` / `1` or as a number input — even when the underlying AVOption is `int` with `min=0 max=1`, the displayed and committed values are the words `true` / `false`. |
| Numeric (`int`, `int64`, `double`, `float`, `rational`)   | `<input type="number">` with `min` / `max` / `step` from AVOption       |
| Duration (`AV_OPT_TYPE_DURATION`)                         | text input with `HH:MM:SS[.ms]` placeholder showing parsed default      |
| Color (`AV_OPT_TYPE_COLOR`)                               | text input + native color picker swatch                                 |
| Image size / video rate / pix_fmt / sample_fmt / ch_layout| text input + datalist of canonical values from `/api/probe-defaults`    |
| Everything else (`string`, `binary`)                      | text input                                                              |

Booleans get extra wording because the report's screenshots show
several int-typed AVOptions whose semantics are actually boolean
(e.g. a `mode` field with `min=0 max=1` and named consts `off=0` /
`on=1`). For these, the toggle commits the **word** (`true` /
`false`) into `def.params` — `av_opt_set` accepts both the words and
the digits, so this round-trips cleanly through libavutil and reads
better in the saved JSON. The placeholder shows the libavfilter
default in the same form (`true` / `false`), never `0` / `1`.

For the placeholder-as-default rule to work for **enums**, the
`(default)` option must remain present and selected when the user has
not overridden the value (current behaviour) — the badge is just
suppressed for enums.

### 3.4 Search & grouping

Keep the existing search box (already gated on `> 6` options). Add
two refinements:

* **Pin overridden options to the top.** Any option whose key is
  present in `def.params` floats above the rest, with a small
  `● modified` dot to the left of the name. This makes it trivial to
  audit "what did I change?" on filters with 30+ options
  (`scale`, `format`, …).
* **Optional grouping by AVOption `unit`.** libavfilter groups related
  enum/flag options under the same `unit` string (e.g. `scale`'s
  `interp_algo` and `flags` share semantic context). When two or more
  visible options share a `unit`, render a thin group header
  (`Scaling algorithm`) above them. Suppress when the search box is
  active (search results stay flat).

### 3.5 Footer

```
   ───────────────────────────────────────────
   Reset to defaults     ▸ Generated CLI args
```

* **Reset to defaults** — clears `def.params` (drops every overridden
  key). Confirms first if any value is set.
* **▸ Generated CLI args** — collapsible, monospace, shows the
  libavfilter argument string MediaMolder would emit
  (`acompressor=threshold=0.05:ratio=4`). Read-only; useful for
  copy-pasting into ffmpeg invocations and for verifying the exact
  bytes the runtime sees. This is also the natural surface for the
  later "Export FFmpeg CLI" roadmap item (#53) at filter scope.

## 4. Out-of-scope decisions

* **Filter swap** is intentionally guarded behind a confirmation —
  changing `def.filter` invalidates every entry in `def.params` and
  there is no general-purpose way to remap option names between
  filters. Users who need to "try `bwdif` instead of `yadif`" are
  better served by deleting the node and adding a fresh one from the
  palette (option defaults are correct for the new filter).
* **Multi-filter graph editing inside one node** (`split[a][b];[a]hflip`
  style filtergraph strings) is *not* in scope here. MediaMolder's
  data model is one node per filter, and filtergraph composition
  happens at the graph level — the option panel only ever edits one
  AVFilter's options.
* **Encoder forms** are unchanged — `EncoderForm` already has its own
  pattern (codec selector + per-codec AVOption surface) and the
  per-stream overrides landed in Wave 8 #45. Aligning that form with
  this proposal is a separate follow-up.

## 5. Migration plan

1. Build a new `FilterHeader` component (subtitle + collapsible
   Advanced section). Render in place of the three top `Field` calls
   in `NodeForm` whenever `def.type` is one of the three filter
   variants.
2. Refactor `OptionRow` in `FilterForm.tsx` per §3.2: move help to
   row-2, drop `default <n>` from the meta line, push the control to
   widget-prominence, suppress the meta badge for enums.
3. Wire placeholders via `OptionControl`: each control receives the
   default string from `defaultDisplay(option)` and renders it as the
   `placeholder` attribute on its underlying input (no change for
   `<select>` — the `(default)` entry already covers this).
4. Add the modified-options pin, grouping, reset, and generated-CLI
   panels as small additive changes — none of them require schema
   changes.
5. Documentation: update [docs/gui.md](../gui.md) "Filter nodes"
   section once the new layout lands; mark the proposal as
   *Implemented* (with commit hash) and leave the file as the design
   record.

## 6. Validation

* `tsc -b --noEmit` clean.
* Visual check against the original report's filters
  (`acompressor`, `bwdif`) plus a filter with many options
  (`scale`), a flag-set filter (`scale`'s `flags`), an enum-only
  filter (`format`), and a sources/sinks node (`movie`,
  `nullsink`).
* No backend changes required — the existing `/api/filters/{name}`
  AVOption schema is sufficient.
