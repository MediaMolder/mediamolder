# FFmpeg Migration Guide

Convert FFmpeg CLI commands to MediaMolder JSON configs using `convert-cmd`:

```sh
mediamolder convert-cmd "ffmpeg -i input.mp4 -c:v libx264 out.mp4"
```

## Common conversions

| # | FFmpeg CLI | Notes | Config |
|---|-----------|-------|--------|
| 1 | `ffmpeg -i in.mp4 -c:v libx264 -c:a aac out.mp4` | Simple transcode | [JSON](../testdata/examples/01_simple_transcode.json) |
| 2 | `ffmpeg -i in.mp4 -c copy out.mkv` | Stream copy | [JSON](../testdata/examples/02_stream_copy.json) |
| 3 | `ffmpeg -i in.mp4 -vf scale=1280:720 -c:v libx264 out.mp4` | Scale filter | [JSON](../testdata/examples/03_scale_filter.json) |
| 4 | `ffmpeg -i in.mp4 -vf "scale=640:480,fps=30" -c:v libx264 out.mp4` | Filter chain | [JSON](../testdata/examples/04_filter_chain.json) |
| 5 | `ffmpeg -i in.mp4 -af "volume=2.0" -c:a aac out.mp4` | Audio filter | [JSON](../testdata/examples/05_audio_filter.json) |
| 6 | `ffmpeg -i in.mp4 -an -c:v libx264 out.mp4` | Strip audio | [JSON](../testdata/examples/06_strip_audio.json) |
| 7 | `ffmpeg -i in.mp4 -vn -c:a aac out.mp3` | Audio only | [JSON](../testdata/examples/07_audio_only.json) |
| 8 | `ffmpeg -i in.mp4 -f matroska -c:v libx264 out.mkv` | Set format | [JSON](../testdata/examples/08_set_format.json) |
| 9 | `ffmpeg -i in.mp4 -b:v 2M -b:a 128k -c:v libx264 out.mp4` | Set bitrate | [JSON](../testdata/examples/09_set_bitrate.json) |
| 10 | `ffmpeg -i in.mp4 -r 30 -c:v libx264 out.mp4` | Set framerate | [JSON](../testdata/examples/10_set_framerate.json) |
| 11 | `ffmpeg -i in.mp4 -c:v libx265 -c:a aac out.mp4` | HEVC | [JSON](../testdata/examples/11_hevc.json) |
| 12 | `ffmpeg -i in.mp4 -c:v libvpx-vp9 -c:a libopus out.webm` | VP9+Opus | [JSON](../testdata/examples/12_vp9_opus.json) |
| 13 | `ffmpeg -i in.mp4 -vf "drawtext=text='Hello'" -c:v libx264 out.mp4` | Text overlay | [JSON](../testdata/examples/13_text_overlay.json) |
| 14 | `ffmpeg -i in.mp4 -vf "crop=640:480,scale=320:240" -c:v libx264 out.mp4` | Crop+scale | [JSON](../testdata/examples/14_crop_scale.json) |
| 15 | `ffmpeg -i in.mp4 -af loudnorm -c:a aac out.mp4` | Audio normalize | [JSON](../testdata/examples/15_audio_normalize.json) |
| 16 | `ffmpeg -i in.mp4 -vf scale=1920:1080 -af loudnorm -c:v libx264 -c:a aac out.mp4` | V+A filters | [JSON](../testdata/examples/16_video_audio_filters.json) |
| 17 | `ffmpeg -y -i in.mp4 -c:v libx264 out.mp4` | Overwrite (ignored) | [JSON](../testdata/examples/17_overwrite.json) |
| 18 | `ffmpeg -i in.mp4 -vf "scale=1280:720,pad=1920:1080" -c:v libx264 out.mp4` | Letterbox | [JSON](../testdata/examples/18_letterbox.json) |
| 19 | `ffmpeg -i in.mp4 -vf "scale=640:480,pad=640:480,fps=24" -c:v libx264 out.mp4` | 3-filter chain | [JSON](../testdata/examples/19_three_filter_chain.json) |
| 20 | `ffmpeg -i "my file.mp4" -c:v libx264 "output.mp4"` | Quoted paths | [JSON](../testdata/examples/20_quoted_paths.json) |
| 21 | `ffmpeg -hwaccel cuda -i in.mp4 -c:v h264_nvenc out.mp4` | CUDA HW accel | [JSON](../testdata/examples/21_cuda_hwaccel.json) |
| 22 | `ffmpeg -hwaccel vaapi -hwaccel_device /dev/dri/renderD128 -i in.mp4 -c:v h264_vaapi out.mp4` | VAAPI HW accel | [JSON](../testdata/examples/22_vaapi_hwaccel.json) |
| 23 | `ffmpeg -i in.mp4 -vf "subtitles=subs.srt" -c:v libx264 out.mp4` | Burn-in SRT subs | [JSON](../testdata/examples/23_burn_in_srt.json) |
| 24 | `ffmpeg -i in.mp4 -vf "ass=subs.ass" -c:v libx264 out.mp4` | Burn-in ASS subs | [JSON](../testdata/examples/24_burn_in_ass.json) |
| 25 | `ffmpeg -i in.mkv -c:v copy -c:a copy -c:s srt out.mkv` | Subtitle passthrough | [JSON](../testdata/examples/25_subtitle_passthrough.json) |
| 26 | `ffmpeg -i in.mkv -sn -c:v libx264 out.mp4` | Strip subtitles | [JSON](../testdata/examples/26_strip_subtitles.json) |
| 27 | `ffmpeg -i in.mp4 -c copy -bsf:v h264_mp4toannexb out.ts` | BSF: MP4→TS remux | [JSON](../testdata/examples/27_bsf_remux.json) |
| 28 | `ffmpeg -hwaccel qsv -i in.mp4 -c:v h264_qsv -bsf:v h264_metadata=level=4.1 out.mp4` | QSV + BSF | [JSON](../testdata/examples/28_qsv_bsf.json) |

## Key differences from FFmpeg CLI

| Feature | FFmpeg CLI | MediaMolder |
|---------|-----------|-------------|
| Config format | CLI arguments | JSON (structured, versionable) |
| Filter graphs | String-based | DAG with typed edges |
| Stream selection | `-map 0:v:0` | `"streams"` array in input |
| Error handling | Global flags | Per-node `error_policy` |
| Observability | stderr progress | Event bus + metrics API |
| State control | None (run to end) | Start/Pause/Resume/Seek |
