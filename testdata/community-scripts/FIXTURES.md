# Community Scripts – Required Fixtures

Each JSON job in this directory maps one NapoleonWils0n/ffmpeg-scripts script to a
MediaMolder pipeline job.  The test harness (`TestCommunityScriptsRun`) skips any
job whose fixture files are absent, with an explanatory message.

## Already present in testdata/

| File | Used by |
|------|---------|
| `testdata/BBB_10sec.mp4` | all jobs (`{{input}}` / `{{input2}}`) |
| `testdata/BBB_30sec.mp4` | fallback when BBB_10sec.mp4 is missing |
| `testdata/subs.srt` | `18_subtitle_add.json` |

## Additional fixtures needed

| File | Used by | How to generate |
|------|---------|-----------------|
| `testdata/sample.jpg` | 15_img2video, 16_zoompan, 17_pan_scan | `ffmpeg -i testdata/BBB_10sec.mp4 -ss 2 -vframes 1 testdata/sample.jpg` |
| `testdata/sample.aac` | 03_combine_clips | `ffmpeg -i testdata/BBB_10sec.mp4 -vn -c:a aac -t 10 testdata/sample.aac` |

Generate both with:

```sh
ffmpeg -i testdata/BBB_10sec.mp4 -ss 2 -vframes 1 testdata/sample.jpg
ffmpeg -i testdata/BBB_10sec.mp4 -vn -c:a aac -t 10 testdata/sample.aac
```

## Scripts NOT converted (not pipeline-expressible)

| Script | Reason |
|--------|--------|
| `audio-silence` | Needs a lavfi virtual audio source (`anullsrc`); MediaMolder has no virtual-source input type |
| `chapter-add` | Metadata mux — no metadata-injection node in the pipeline schema |
| `chapter-csv` | Pure timestamp-arithmetic utility; no media transformation |
| `chapter-extract` | Metadata demux — no metadata-extraction node |
| `clip-time` | Pure timestamp-arithmetic utility |
| `ebu-meter` | ffplay loudness meter; no output file produced |
| `extract-frame` | Needs per-output `-vframes 1` / `-frames:v 1` control, not yet exposed in the pipeline schema |
| `scene-cut` | Requires an external cut-file; produces multiple output files (batch trim) |
| `scene-cut-to` | Same as scene-cut |
| `scene-images` | Requires an external cut-file for batch frame extraction |
| `scene-time` | Pure timestamp-arithmetic utility |
| `scopes` | ffplay video-scope display; no output file produced |
| `sexagesimal-time` | Pure timestamp-arithmetic utility |
| `tile-thumbnails` | Needs per-output `-frames:v 1` control (tile filter produces one image per N frames); not yet exposed |
| `waveform` | `showwavespic` is an audio→video type-crossing filter; MediaMolder's simple 1→1 filter path does not yet support mixed-media-type graphs |
