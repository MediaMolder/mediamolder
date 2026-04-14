// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

// corpus_test.go — Expanded test corpus for Parse (P4.3).
// Table-driven tests covering diverse FFmpeg command patterns.

import "testing"

// corpusTests exercises a broad range of FFmpeg command-line patterns.
// Each entry checks structural counts (inputs, outputs, graph nodes, graph edges)
// and selected output field values.
var corpusTests = []struct {
	name    string
	cmd     string
	wantIn  int
	wantOut int
	wantN   int // graph nodes (filters)
	wantE   int // graph edges
	wantErr bool
	// Optional field checks on first output (empty strings = skip check).
	codecV string
	codecA string
	codecS string
	bsfV   string
	bsfA   string
	format string
}{
	// ---- Basic codec flags ----
	{name: "cv-libx264", cmd: "ffmpeg -i in.mp4 -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx264"},
	{name: "cv-libx265", cmd: "ffmpeg -i in.mp4 -c:v libx265 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx265"},
	{name: "cv-libvpx", cmd: "ffmpeg -i in.mp4 -c:v libvpx out.webm", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libvpx"},
	{name: "cv-libvpx-vp9", cmd: "ffmpeg -i in.mp4 -c:v libvpx-vp9 out.webm", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libvpx-vp9"},
	{name: "cv-libaom-av1", cmd: "ffmpeg -i in.mp4 -c:v libaom-av1 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libaom-av1"},
	{name: "cv-libsvtav1", cmd: "ffmpeg -i in.mp4 -c:v libsvtav1 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libsvtav1"},
	{name: "cv-mpeg4", cmd: "ffmpeg -i in.mp4 -c:v mpeg4 out.avi", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "mpeg4"},
	{name: "cv-mpeg2video", cmd: "ffmpeg -i in.mp4 -c:v mpeg2video out.ts", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "mpeg2video"},
	{name: "cv-copy", cmd: "ffmpeg -i in.mp4 -c:v copy out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "copy"},
	{name: "cv-prores", cmd: "ffmpeg -i in.mp4 -c:v prores_ks out.mov", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "prores_ks"},
	{name: "cv-dnxhd", cmd: "ffmpeg -i in.mp4 -c:v dnxhd out.mxf", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "dnxhd"},
	{name: "cv-h264-nvenc", cmd: "ffmpeg -i in.mp4 -c:v h264_nvenc out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "h264_nvenc"},
	{name: "cv-h264-videotoolbox", cmd: "ffmpeg -i in.mp4 -c:v h264_videotoolbox out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "h264_videotoolbox"},
	{name: "cv-hevc-nvenc", cmd: "ffmpeg -i in.mp4 -c:v hevc_nvenc out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "hevc_nvenc"},
	{name: "cv-mjpeg", cmd: "ffmpeg -i in.mp4 -c:v mjpeg out.avi", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "mjpeg"},
	{name: "cv-gif", cmd: "ffmpeg -i in.mp4 -c:v gif out.gif", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "gif"},
	{name: "cv-rawvideo", cmd: "ffmpeg -i in.mp4 -c:v rawvideo out.avi", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "rawvideo"},
	{name: "cv-png", cmd: "ffmpeg -i in.mp4 -c:v png out.png", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "png"},
	{name: "cv-qsv-h264", cmd: "ffmpeg -i in.mp4 -c:v h264_qsv out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "h264_qsv"},
	{name: "cv-av1-nvenc", cmd: "ffmpeg -i in.mp4 -c:v av1_nvenc out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "av1_nvenc"},

	// ---- Audio codec flags ----
	{name: "ca-aac", cmd: "ffmpeg -i in.mp4 -c:a aac out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecA: "aac"},
	{name: "ca-libmp3lame", cmd: "ffmpeg -i in.mp4 -c:a libmp3lame out.mp3", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecA: "libmp3lame"},
	{name: "ca-libfdk-aac", cmd: "ffmpeg -i in.mp4 -c:a libfdk_aac out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecA: "libfdk_aac"},
	{name: "ca-opus", cmd: "ffmpeg -i in.mp4 -c:a libopus out.webm", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecA: "libopus"},
	{name: "ca-vorbis", cmd: "ffmpeg -i in.mp4 -c:a libvorbis out.ogg", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecA: "libvorbis"},
	{name: "ca-flac", cmd: "ffmpeg -i in.mp4 -c:a flac out.flac", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecA: "flac"},
	{name: "ca-pcm-s16le", cmd: "ffmpeg -i in.mp4 -c:a pcm_s16le out.wav", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecA: "pcm_s16le"},
	{name: "ca-pcm-s24le", cmd: "ffmpeg -i in.mp4 -c:a pcm_s24le out.wav", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecA: "pcm_s24le"},
	{name: "ca-ac3", cmd: "ffmpeg -i in.mp4 -c:a ac3 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecA: "ac3"},
	{name: "ca-eac3", cmd: "ffmpeg -i in.mp4 -c:a eac3 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecA: "eac3"},
	{name: "ca-copy", cmd: "ffmpeg -i in.mp4 -c:a copy out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecA: "copy"},
	{name: "ca-alac", cmd: "ffmpeg -i in.mp4 -c:a alac out.m4a", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecA: "alac"},
	{name: "ca-truehd", cmd: "ffmpeg -i in.mkv -c:a truehd out.mkv", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecA: "truehd"},

	// ---- Combined video+audio codecs ----
	{name: "cva-x264-aac", cmd: "ffmpeg -i in.mp4 -c:v libx264 -c:a aac out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx264", codecA: "aac"},
	{name: "cva-x265-opus", cmd: "ffmpeg -i in.mp4 -c:v libx265 -c:a libopus out.mkv", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx265", codecA: "libopus"},
	{name: "cva-vpx-vorbis", cmd: "ffmpeg -i in.mp4 -c:v libvpx -c:a libvorbis out.webm", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libvpx", codecA: "libvorbis"},
	{name: "cva-av1-opus", cmd: "ffmpeg -i in.mp4 -c:v libaom-av1 -c:a libopus out.mkv", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libaom-av1", codecA: "libopus"},
	{name: "cva-prores-pcm", cmd: "ffmpeg -i in.mp4 -c:v prores_ks -c:a pcm_s16le out.mov", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "prores_ks", codecA: "pcm_s16le"},
	{name: "cva-copy-copy", cmd: "ffmpeg -i in.mp4 -c copy out.mkv", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "copy", codecA: "copy"},
	{name: "cva-copy-explicit", cmd: "ffmpeg -i in.mp4 -c:v copy -c:a copy out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "copy", codecA: "copy"},

	// ---- Codec alias flags ----
	{name: "vcodec-alias", cmd: "ffmpeg -i in.mp4 -vcodec libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx264"},
	{name: "acodec-alias", cmd: "ffmpeg -i in.mp4 -acodec aac out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecA: "aac"},
	{name: "codec-generic-copy", cmd: "ffmpeg -i in.mp4 -codec copy out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "copy", codecA: "copy"},
	{name: "c-generic-x264", cmd: "ffmpeg -i in.mp4 -c libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx264", codecA: "libx264"},

	// ---- Stream disable flags ----
	{name: "an-disable-audio", cmd: "ffmpeg -i in.mp4 -an -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 1},
	{name: "vn-disable-video", cmd: "ffmpeg -i in.mp4 -vn -c:a aac out.mp3", wantIn: 1, wantOut: 1, wantN: 0, wantE: 1},
	{name: "sn-disable-subtitle", cmd: "ffmpeg -i in.mp4 -sn -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "an-vn-error", cmd: "ffmpeg -i in.mp4 -an -vn out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 0},

	// ---- Video filter flags ----
	{name: "vf-scale", cmd: "ffmpeg -i in.mp4 -vf scale=1280:720 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-fps", cmd: "ffmpeg -i in.mp4 -vf fps=30 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-crop", cmd: "ffmpeg -i in.mp4 -vf crop=640:480:0:0 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-pad", cmd: "ffmpeg -i in.mp4 -vf pad=1920:1080:240:0 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-transpose", cmd: "ffmpeg -i in.mp4 -vf transpose=1 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-rotate", cmd: "ffmpeg -i in.mp4 -vf rotate=PI/2 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-hflip", cmd: "ffmpeg -i in.mp4 -vf hflip out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-vflip", cmd: "ffmpeg -i in.mp4 -vf vflip out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-null", cmd: "ffmpeg -i in.mp4 -vf null out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-deinterlace", cmd: "ffmpeg -i in.mp4 -vf yadif out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-denoise", cmd: "ffmpeg -i in.mp4 -vf hqdn3d out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-sharpen", cmd: "ffmpeg -i in.mp4 -vf unsharp out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-eq", cmd: "ffmpeg -i in.mp4 -vf eq=brightness=0.1:contrast=1.2 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-overlay", cmd: "ffmpeg -i in.mp4 -vf overlay=10:10 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-drawtext", cmd: "ffmpeg -i in.mp4 -vf drawtext=text=Hello out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-colorbalance", cmd: "ffmpeg -i in.mp4 -vf colorbalance=rs=0.3 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-format", cmd: "ffmpeg -i in.mp4 -vf format=yuv420p out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-setpts", cmd: "ffmpeg -i in.mp4 -vf setpts=0.5*PTS out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-setdar", cmd: "ffmpeg -i in.mp4 -vf setdar=16/9 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-setsar", cmd: "ffmpeg -i in.mp4 -vf setsar=1 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-deband", cmd: "ffmpeg -i in.mp4 -vf deband out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-deshake", cmd: "ffmpeg -i in.mp4 -vf deshake out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-lut", cmd: "ffmpeg -i in.mp4 -vf lut=r=negval out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-select", cmd: "ffmpeg -i in.mp4 -vf select=eq(n) out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-thumbnail", cmd: "ffmpeg -i in.mp4 -vf thumbnail out.png", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-tile", cmd: "ffmpeg -i in.mp4 -vf tile=3x3 out.png", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-palettegen", cmd: "ffmpeg -i in.mp4 -vf palettegen out.png", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-subtitles", cmd: "ffmpeg -i in.mp4 -vf subtitles=subs.srt out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-fade-in", cmd: "ffmpeg -i in.mp4 -vf fade=t=in:st=0:d=2 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-fade-out", cmd: "ffmpeg -i in.mp4 -vf fade=t=out:st=8:d=2 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-boxblur", cmd: "ffmpeg -i in.mp4 -vf boxblur=5:1 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-colorchannelmixer", cmd: "ffmpeg -i in.mp4 -vf colorchannelmixer=.393:.769:.189:0 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-hwupload", cmd: "ffmpeg -i in.mp4 -vf hwupload out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-hwdownload", cmd: "ffmpeg -i in.mp4 -vf hwdownload out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-scale-npp", cmd: "ffmpeg -i in.mp4 -vf scale_npp=1280:720 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "vf-zscale", cmd: "ffmpeg -i in.mp4 -vf zscale=w=1280:h=720 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "filter-v-alias", cmd: "ffmpeg -i in.mp4 -filter:v scale=640:480 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},

	// ---- Video filter chains (2 filters) ----
	{name: "vf2-scale-fps", cmd: "ffmpeg -i in.mp4 -vf scale=1280:720,fps=30 out.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4},
	{name: "vf2-crop-pad", cmd: "ffmpeg -i in.mp4 -vf crop=640:480,pad=1280:720:320:120 out.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4},
	{name: "vf2-yadif-scale", cmd: "ffmpeg -i in.mp4 -vf yadif,scale=1280:720 out.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4},
	{name: "vf2-transpose-scale", cmd: "ffmpeg -i in.mp4 -vf transpose=1,scale=1280:720 out.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4},
	{name: "vf2-format-scale", cmd: "ffmpeg -i in.mp4 -vf format=yuv420p,scale=1920:1080 out.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4},
	{name: "vf2-eq-unsharp", cmd: "ffmpeg -i in.mp4 -vf eq=contrast=1.5,unsharp out.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4},
	{name: "vf2-hflip-vflip", cmd: "ffmpeg -i in.mp4 -vf hflip,vflip out.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4},
	{name: "vf2-setpts-fps", cmd: "ffmpeg -i in.mp4 -vf setpts=2*PTS,fps=15 out.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4},
	{name: "vf2-denoise-sharpen", cmd: "ffmpeg -i in.mp4 -vf hqdn3d,unsharp out.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4},
	{name: "vf2-fade-drawtext", cmd: "ffmpeg -i in.mp4 -vf fade=t=in:st=0:d=1,drawtext=text=Title out.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4},

	// ---- Video filter chains (3+ filters) ----
	{name: "vf3-scale-fps-format", cmd: "ffmpeg -i in.mp4 -vf scale=1280:720,fps=30,format=yuv420p out.mp4", wantIn: 1, wantOut: 1, wantN: 3, wantE: 5},
	{name: "vf3-yadif-scale-fps", cmd: "ffmpeg -i in.mp4 -vf yadif,scale=1920:1080,fps=24 out.mp4", wantIn: 1, wantOut: 1, wantN: 3, wantE: 5},
	{name: "vf3-crop-scale-pad", cmd: "ffmpeg -i in.mp4 -vf crop=640:360,scale=1280:720,pad=1920:1080:320:180 out.mp4", wantIn: 1, wantOut: 1, wantN: 3, wantE: 5},
	{name: "vf3-eq-lut-format", cmd: "ffmpeg -i in.mp4 -vf eq=brightness=0.1,lut=r=negval,format=yuv420p out.mp4", wantIn: 1, wantOut: 1, wantN: 3, wantE: 5},
	{name: "vf4-chain", cmd: "ffmpeg -i in.mp4 -vf yadif,scale=1280:720,fps=30,format=yuv420p out.mp4", wantIn: 1, wantOut: 1, wantN: 4, wantE: 6},
	{name: "vf5-chain", cmd: "ffmpeg -i in.mp4 -vf yadif,hqdn3d,scale=1280:720,fps=30,unsharp out.mp4", wantIn: 1, wantOut: 1, wantN: 5, wantE: 7},

	// ---- Audio filter flags ----
	{name: "af-volume", cmd: "ffmpeg -i in.mp4 -af volume=2.0 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "af-loudnorm", cmd: "ffmpeg -i in.mp4 -af loudnorm out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "af-aresample", cmd: "ffmpeg -i in.mp4 -af aresample=48000 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "af-dynaudnorm", cmd: "ffmpeg -i in.mp4 -af dynaudnorm out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "af-equalizer", cmd: "ffmpeg -i in.mp4 -af equalizer=f=1000:t=q:w=1:g=2 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "af-highpass", cmd: "ffmpeg -i in.mp4 -af highpass=f=200 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "af-lowpass", cmd: "ffmpeg -i in.mp4 -af lowpass=f=3000 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "af-atempo", cmd: "ffmpeg -i in.mp4 -af atempo=2.0 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "af-aecho", cmd: "ffmpeg -i in.mp4 -af aecho=0.8:0.9:1000:0.3 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "af-compand", cmd: "ffmpeg -i in.mp4 -af compand out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "af-silenceremove", cmd: "ffmpeg -i in.mp4 -af silenceremove=1:0:-50dB out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "af-adelay", cmd: "ffmpeg -i in.mp4 -af adelay=1000 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "af-afade", cmd: "ffmpeg -i in.mp4 -af afade=t=in:st=0:d=5 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "af-pan", cmd: "ffmpeg -i in.mp4 -af pan=stereo out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "af-anull", cmd: "ffmpeg -i in.mp4 -af anull out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "filter-a-alias", cmd: "ffmpeg -i in.mp4 -filter:a volume=0.5 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},

	// ---- Audio filter chains ----
	{name: "af2-volume-loudnorm", cmd: "ffmpeg -i in.mp4 -af volume=1.5,loudnorm out.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4},
	{name: "af2-highpass-lowpass", cmd: "ffmpeg -i in.mp4 -af highpass=f=200,lowpass=f=3000 out.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4},
	{name: "af3-chain", cmd: "ffmpeg -i in.mp4 -af volume=2.0,highpass=f=200,loudnorm out.mp4", wantIn: 1, wantOut: 1, wantN: 3, wantE: 5},

	// ---- Both video and audio filters ----
	{name: "vf-af-combined", cmd: "ffmpeg -i in.mp4 -vf scale=1920:1080 -af loudnorm out.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4},
	{name: "vf-af-x264-aac", cmd: "ffmpeg -i in.mp4 -vf scale=1280:720 -af volume=1.5 -c:v libx264 -c:a aac out.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4, codecV: "libx264", codecA: "aac"},
	{name: "vf2-af1", cmd: "ffmpeg -i in.mp4 -vf yadif,scale=1280:720 -af loudnorm -c:v libx264 -c:a aac out.mp4", wantIn: 1, wantOut: 1, wantN: 3, wantE: 5, codecV: "libx264", codecA: "aac"},
	{name: "vf1-af2", cmd: "ffmpeg -i in.mp4 -vf scale=1920:1080 -af highpass=f=200,loudnorm -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 3, wantE: 5, codecV: "libx264"},

	// ---- Format flag ----
	{name: "f-mp4", cmd: "ffmpeg -i in.mkv -f mp4 -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, format: "mp4"},
	{name: "f-matroska", cmd: "ffmpeg -i in.mp4 -f matroska -c:v libx264 out.mkv", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, format: "matroska"},
	{name: "f-webm", cmd: "ffmpeg -i in.mp4 -f webm -c:v libvpx out.webm", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, format: "webm"},
	{name: "f-avi", cmd: "ffmpeg -i in.mp4 -f avi -c:v mpeg4 out.avi", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, format: "avi"},
	{name: "f-mpegts", cmd: "ffmpeg -i in.mp4 -f mpegts -c:v libx264 out.ts", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, format: "mpegts"},
	{name: "f-flv", cmd: "ffmpeg -i in.mp4 -f flv -c:v libx264 out.flv", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, format: "flv"},
	{name: "f-wav", cmd: "ffmpeg -i in.mp4 -f wav -c:a pcm_s16le out.wav", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, format: "wav"},
	{name: "f-ogg", cmd: "ffmpeg -i in.mp4 -f ogg -c:a libvorbis out.ogg", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, format: "ogg"},
	{name: "f-null", cmd: "ffmpeg -i in.mp4 -f null /dev/null", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, format: "null"},
	{name: "f-rawvideo", cmd: "ffmpeg -i in.mp4 -f rawvideo -c:v rawvideo out.raw", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, format: "rawvideo"},

	// ---- Bitrate / framerate flags ----
	{name: "bv-2M", cmd: "ffmpeg -i in.mp4 -b:v 2M -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx264"},
	{name: "ba-128k", cmd: "ffmpeg -i in.mp4 -b:a 128k -c:a aac out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecA: "aac"},
	{name: "bv-ba-combined", cmd: "ffmpeg -i in.mp4 -b:v 5M -b:a 320k -c:v libx264 -c:a aac out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx264", codecA: "aac"},
	{name: "r-24", cmd: "ffmpeg -i in.mp4 -r 24 -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "r-30", cmd: "ffmpeg -i in.mp4 -r 30 -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "r-60", cmd: "ffmpeg -i in.mp4 -r 60 -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},

	// ---- Overwrite / force flags ----
	{name: "y-overwrite", cmd: "ffmpeg -y -i in.mp4 -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "n-no-overwrite", cmd: "ffmpeg -n -i in.mp4 -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},

	// ---- Quoting and paths ----
	{name: "double-quote-in", cmd: `ffmpeg -i "my input.mp4" -c:v libx264 out.mp4`, wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "double-quote-out", cmd: `ffmpeg -i in.mp4 -c:v libx264 "my output.mp4"`, wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "single-quote-in", cmd: "ffmpeg -i 'my input.mp4' -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "single-quote-both", cmd: "ffmpeg -i 'my in.mp4' -c:v libx264 'my out.mp4'", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "abs-path-in", cmd: "ffmpeg -i /home/user/video.mp4 -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "abs-path-out", cmd: "ffmpeg -i in.mp4 -c:v libx264 /tmp/out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "ffmpeg-abs-path", cmd: "/usr/local/bin/ffmpeg -i in.mp4 -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "extra-spaces", cmd: "  ffmpeg   -i  in.mp4   -c:v  libx264   out.mp4  ", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},

	// ---- Subtitle flags ----
	{name: "cs-mov-text", cmd: "ffmpeg -i in.mp4 -c:s mov_text out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 3, codecS: "mov_text"},
	{name: "cs-srt", cmd: "ffmpeg -i in.mp4 -c:s srt out.mkv", wantIn: 1, wantOut: 1, wantN: 0, wantE: 3, codecS: "srt"},
	{name: "cs-ass", cmd: "ffmpeg -i in.mp4 -c:s ass out.mkv", wantIn: 1, wantOut: 1, wantN: 0, wantE: 3, codecS: "ass"},
	{name: "cs-copy", cmd: "ffmpeg -i in.mp4 -c:s copy out.mkv", wantIn: 1, wantOut: 1, wantN: 0, wantE: 3, codecS: "copy"},
	{name: "cs-webvtt", cmd: "ffmpeg -i in.mp4 -c:s webvtt out.mkv", wantIn: 1, wantOut: 1, wantN: 0, wantE: 3, codecS: "webvtt"},
	{name: "scodec-alias", cmd: "ffmpeg -i in.mp4 -scodec mov_text out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 3, codecS: "mov_text"},
	{name: "cs-dvd-subtitle", cmd: "ffmpeg -i in.mkv -c:s dvd_subtitle out.mkv", wantIn: 1, wantOut: 1, wantN: 0, wantE: 3, codecS: "dvd_subtitle"},

	// ---- Bitstream filters ----
	{name: "bsf-v-h264-annexb", cmd: "ffmpeg -i in.mp4 -bsf:v h264_mp4toannexb -c:v copy out.ts", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, bsfV: "h264_mp4toannexb", codecV: "copy"},
	{name: "bsf-v-hevc-annexb", cmd: "ffmpeg -i in.mp4 -bsf:v hevc_mp4toannexb -c:v copy out.ts", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, bsfV: "hevc_mp4toannexb"},
	{name: "bsf-a-aac-adts", cmd: "ffmpeg -i in.ts -bsf:a aac_adtstoasc -c:a copy out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, bsfA: "aac_adtstoasc"},
	{name: "bsf-v-dump-extra", cmd: "ffmpeg -i in.mp4 -bsf:v dump_extra out.ts", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, bsfV: "dump_extra"},
	{name: "bsf-v-extract-extradata", cmd: "ffmpeg -i in.mp4 -bsf:v extract_extradata out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, bsfV: "extract_extradata"},
	{name: "bsf-va-combined", cmd: "ffmpeg -i in.ts -bsf:v h264_mp4toannexb -bsf:a aac_adtstoasc -c copy out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, bsfV: "h264_mp4toannexb", bsfA: "aac_adtstoasc"},

	// ---- Hardware acceleration ----
	{name: "hwaccel-cuda", cmd: "ffmpeg -hwaccel cuda -i in.mp4 -c:v h264_nvenc out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "h264_nvenc"},
	{name: "hwaccel-vaapi", cmd: "ffmpeg -hwaccel vaapi -hwaccel_device /dev/dri/renderD128 -i in.mp4 -c:v h264_vaapi out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "h264_vaapi"},
	{name: "hwaccel-qsv", cmd: "ffmpeg -hwaccel qsv -i in.mp4 -c:v h264_qsv out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "h264_qsv"},
	{name: "hwaccel-videotoolbox", cmd: "ffmpeg -hwaccel videotoolbox -i in.mp4 -c:v h264_videotoolbox out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "h264_videotoolbox"},
	{name: "hwaccel-auto", cmd: "ffmpeg -hwaccel auto -i in.mp4 -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "hwaccel-cuda-outfmt", cmd: "ffmpeg -hwaccel cuda -hwaccel_output_format cuda -i in.mp4 -c:v h264_nvenc out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "hwaccel-device-0", cmd: "ffmpeg -hwaccel cuda -hwaccel_device 0 -i in.mp4 -c:v h264_nvenc out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},

	// ---- Mixed complex commands ----
	{name: "complex-x264-aac-scale-volume", cmd: "ffmpeg -i in.mp4 -vf scale=1920:1080 -af volume=1.5 -c:v libx264 -c:a aac -b:v 5M -b:a 128k out.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4, codecV: "libx264", codecA: "aac"},
	{name: "complex-x265-filter-chain", cmd: "ffmpeg -i in.mp4 -vf yadif,scale=1280:720,fps=30 -c:v libx265 out.mp4", wantIn: 1, wantOut: 1, wantN: 3, wantE: 5, codecV: "libx265"},
	{name: "complex-prores-ingest", cmd: "ffmpeg -i in.mxf -vf scale=1920:1080 -c:v prores_ks -c:a pcm_s24le out.mov", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3, codecV: "prores_ks", codecA: "pcm_s24le"},
	{name: "complex-web-transcode", cmd: "ffmpeg -i in.mp4 -vf scale=1280:720,fps=30 -c:v libvpx-vp9 -c:a libopus -b:v 2M out.webm", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4, codecV: "libvpx-vp9", codecA: "libopus"},
	{name: "complex-social-media", cmd: "ffmpeg -i in.mp4 -vf scale=1080:1920,fps=30,format=yuv420p -c:v libx264 -c:a aac -b:v 8M out.mp4", wantIn: 1, wantOut: 1, wantN: 3, wantE: 5, codecV: "libx264", codecA: "aac"},
	{name: "complex-gif-pipeline", cmd: "ffmpeg -i in.mp4 -vf fps=10,scale=320:-1 -c:v gif out.gif", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4, codecV: "gif"},
	{name: "complex-hls-segment", cmd: "ffmpeg -i in.mp4 -c:v libx264 -c:a aac -f mpegts out.ts", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx264", codecA: "aac", format: "mpegts"},
	{name: "complex-audio-extract", cmd: "ffmpeg -i in.mp4 -vn -c:a flac -f flac out.flac", wantIn: 1, wantOut: 1, wantN: 0, wantE: 1, codecA: "flac", format: "flac"},
	{name: "complex-subtitle-embed", cmd: "ffmpeg -i in.mp4 -c:v libx264 -c:a aac -c:s mov_text out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 3, codecV: "libx264", codecA: "aac", codecS: "mov_text"},
	{name: "complex-bsf-remux", cmd: "ffmpeg -i in.mp4 -c copy -bsf:v h264_mp4toannexb -f mpegts out.ts", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "copy", codecA: "copy", bsfV: "h264_mp4toannexb", format: "mpegts"},
	{name: "complex-hw-transcode", cmd: "ffmpeg -hwaccel cuda -i in.mp4 -vf scale=1920:1080 -c:v h264_nvenc -c:a aac out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3, codecV: "h264_nvenc", codecA: "aac"},
	{name: "complex-all-options", cmd: "ffmpeg -y -hwaccel cuda -i in.mp4 -vf scale=1280:720,fps=30 -af loudnorm -c:v libx264 -c:a aac -b:v 2M -b:a 128k -r 30 -f mp4 out.mp4", wantIn: 1, wantOut: 1, wantN: 3, wantE: 5, codecV: "libx264", codecA: "aac", format: "mp4"},

	// ---- File extension variety ----
	{name: "ext-mkv", cmd: "ffmpeg -i in.mp4 -c:v libx264 out.mkv", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "ext-avi", cmd: "ffmpeg -i in.mp4 -c:v mpeg4 out.avi", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "ext-ts", cmd: "ffmpeg -i in.mp4 -c:v libx264 out.ts", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "ext-webm", cmd: "ffmpeg -i in.mp4 -c:v libvpx out.webm", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "ext-mov", cmd: "ffmpeg -i in.mp4 -c:v prores_ks out.mov", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "ext-flv", cmd: "ffmpeg -i in.mp4 -c:v libx264 -c:a aac out.flv", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "ext-mp3", cmd: "ffmpeg -i in.mp4 -vn -c:a libmp3lame out.mp3", wantIn: 1, wantOut: 1, wantN: 0, wantE: 1},
	{name: "ext-ogg", cmd: "ffmpeg -i in.mp4 -vn -c:a libvorbis out.ogg", wantIn: 1, wantOut: 1, wantN: 0, wantE: 1},
	{name: "ext-wav", cmd: "ffmpeg -i in.mp4 -vn -c:a pcm_s16le out.wav", wantIn: 1, wantOut: 1, wantN: 0, wantE: 1},
	{name: "ext-flac", cmd: "ffmpeg -i in.mp4 -vn -c:a flac out.flac", wantIn: 1, wantOut: 1, wantN: 0, wantE: 1},
	{name: "ext-m4a", cmd: "ffmpeg -i in.mp4 -vn -c:a aac out.m4a", wantIn: 1, wantOut: 1, wantN: 0, wantE: 1},
	{name: "ext-opus", cmd: "ffmpeg -i in.mp4 -vn -c:a libopus out.opus", wantIn: 1, wantOut: 1, wantN: 0, wantE: 1},
	{name: "ext-mxf", cmd: "ffmpeg -i in.mp4 -c:v dnxhd out.mxf", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},

	// ---- Input format variety ----
	{name: "in-mkv", cmd: "ffmpeg -i in.mkv -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "in-avi", cmd: "ffmpeg -i in.avi -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "in-ts", cmd: "ffmpeg -i in.ts -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "in-webm", cmd: "ffmpeg -i in.webm -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "in-mov", cmd: "ffmpeg -i in.mov -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "in-flv", cmd: "ffmpeg -i in.flv -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "in-mxf", cmd: "ffmpeg -i in.mxf -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "in-m2ts", cmd: "ffmpeg -i in.m2ts -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "in-mp3", cmd: "ffmpeg -i in.mp3 -c:a aac out.m4a", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "in-wav", cmd: "ffmpeg -i in.wav -c:a aac out.m4a", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "in-flac", cmd: "ffmpeg -i in.flac -c:a aac out.m4a", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "in-srt", cmd: "ffmpeg -i in.srt -c:s mov_text out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 3, codecS: "mov_text"},
	{name: "in-gif", cmd: "ffmpeg -i in.gif -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "in-png", cmd: "ffmpeg -i in.png -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},

	// ---- Error cases ----
	{name: "err-no-input", cmd: "ffmpeg -c:v libx264 out.mp4", wantErr: true},
	{name: "err-no-output", cmd: "ffmpeg -i in.mp4 -c:v libx264", wantErr: true},
	{name: "err-empty-i", cmd: "ffmpeg -i", wantErr: true},
	{name: "err-empty-cv", cmd: "ffmpeg -i in.mp4 -c:v", wantErr: true},
	{name: "err-empty-ca", cmd: "ffmpeg -i in.mp4 -c:a", wantErr: true},
	{name: "err-empty-vf", cmd: "ffmpeg -i in.mp4 -vf", wantErr: true},
	{name: "err-empty-af", cmd: "ffmpeg -i in.mp4 -af", wantErr: true},
	{name: "err-empty-f", cmd: "ffmpeg -i in.mp4 -f", wantErr: true},
	{name: "err-empty-bv", cmd: "ffmpeg -i in.mp4 -b:v", wantErr: true},
	{name: "err-empty-ba", cmd: "ffmpeg -i in.mp4 -b:a", wantErr: true},
	{name: "err-empty-r", cmd: "ffmpeg -i in.mp4 -r", wantErr: true},
	{name: "err-empty-cs", cmd: "ffmpeg -i in.mp4 -c:s", wantErr: true},
	{name: "err-empty-scodec", cmd: "ffmpeg -i in.mp4 -scodec", wantErr: true},
	{name: "err-empty-bsfv", cmd: "ffmpeg -i in.mp4 -bsf:v", wantErr: true},
	{name: "err-empty-bsfa", cmd: "ffmpeg -i in.mp4 -bsf:a", wantErr: true},
	{name: "err-empty-hwaccel", cmd: "ffmpeg -i in.mp4 -hwaccel", wantErr: true},
	{name: "err-empty-hwaccel-device", cmd: "ffmpeg -i in.mp4 -hwaccel_device", wantErr: true},
	{name: "err-empty-hwaccel-outfmt", cmd: "ffmpeg -i in.mp4 -hwaccel_output_format", wantErr: true},
	{name: "err-empty-vcodec", cmd: "ffmpeg -i in.mp4 -vcodec", wantErr: true},
	{name: "err-empty-acodec", cmd: "ffmpeg -i in.mp4 -acodec", wantErr: true},
	{name: "err-empty-codec", cmd: "ffmpeg -i in.mp4 -codec", wantErr: true},
	{name: "err-empty-c", cmd: "ffmpeg -i in.mp4 -c", wantErr: true},
	{name: "err-empty-filterv", cmd: "ffmpeg -i in.mp4 -filter:v", wantErr: true},
	{name: "err-empty-filtera", cmd: "ffmpeg -i in.mp4 -filter:a", wantErr: true},
	{name: "err-just-ffmpeg", cmd: "ffmpeg", wantErr: true},

	// ---- Edge cases with no codec specified ----
	{name: "passthrough-default", cmd: "ffmpeg -i in.mp4 out.mkv", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "passthrough-filter-only", cmd: "ffmpeg -i in.mp4 -vf scale=1280:720 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},

	// ---- Tokenizer stress tests ----
	{name: "tok-multi-space", cmd: "ffmpeg    -i    in.mp4    -c:v    libx264    out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "tok-tab-like", cmd: "ffmpeg -i in.mp4 -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "tok-leading-trailing", cmd: "  ffmpeg -i in.mp4 -c:v libx264 out.mp4  ", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},
	{name: "tok-mixed-quotes", cmd: `ffmpeg -i "in file.mp4" -c:v libx264 'out file.mp4'`, wantIn: 1, wantOut: 1, wantN: 0, wantE: 2},

	// ---- Filter parameter variety ----
	{name: "fp-named-scale-wh", cmd: "ffmpeg -i in.mp4 -vf scale=w=1280:h=720 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "fp-positional-scale", cmd: "ffmpeg -i in.mp4 -vf scale=640:480 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "fp-no-params-null", cmd: "ffmpeg -i in.mp4 -vf null out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "fp-eq-multi", cmd: "ffmpeg -i in.mp4 -vf eq=brightness=0.2:contrast=1.5:saturation=0.8 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "fp-drawtext-complex", cmd: "ffmpeg -i in.mp4 -vf drawtext=fontsize=24:fontcolor=white:text=Hello out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "fp-fade-params", cmd: "ffmpeg -i in.mp4 -vf fade=t=in:st=0:d=3 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "fp-volume-decimal", cmd: "ffmpeg -i in.mp4 -af volume=0.5 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},
	{name: "fp-equalizer-complex", cmd: "ffmpeg -i in.mp4 -af equalizer=f=1000:t=h:width=200:g=-10 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3},

	// ---- Realistic workflow patterns ----
	{name: "wf-youtube-upload", cmd: "ffmpeg -i raw.mov -vf scale=1920:1080,fps=30,format=yuv420p -c:v libx264 -c:a aac -b:v 8M -b:a 128k youtube.mp4", wantIn: 1, wantOut: 1, wantN: 3, wantE: 5, codecV: "libx264", codecA: "aac"},
	{name: "wf-instagram-reel", cmd: "ffmpeg -i in.mp4 -vf scale=1080:1920,fps=30 -c:v libx264 -c:a aac reel.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4, codecV: "libx264", codecA: "aac"},
	{name: "wf-podcast-audio", cmd: "ffmpeg -i recording.wav -af loudnorm -c:a libmp3lame -b:a 192k podcast.mp3", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3, codecA: "libmp3lame"},
	{name: "wf-dvd-rip", cmd: "ffmpeg -i dvd.vob -vf yadif,scale=1280:720 -c:v libx264 -c:a aac dvd_rip.mkv", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4, codecV: "libx264", codecA: "aac"},
	{name: "wf-archive-to-proxy", cmd: "ffmpeg -i archive.mxf -vf scale=960:540 -c:v libx264 -c:a aac proxy.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3, codecV: "libx264", codecA: "aac"},
	{name: "wf-gif-from-clip", cmd: "ffmpeg -i clip.mp4 -vf fps=15,scale=480:-1 -c:v gif out.gif", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4, codecV: "gif"},
	{name: "wf-stream-relay", cmd: "ffmpeg -i in.ts -c copy -f flv out.flv", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, format: "flv"},
	{name: "wf-audio-normalize", cmd: "ffmpeg -i in.mp4 -af loudnorm,volume=1.2 -c:v copy -c:a aac out.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4, codecV: "copy", codecA: "aac"},
	{name: "wf-thumbnail", cmd: "ffmpeg -i in.mp4 -vf thumbnail,scale=320:240 -c:v png thumb.png", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4, codecV: "png"},
	{name: "wf-timelapse", cmd: "ffmpeg -i in.mp4 -vf setpts=0.25*PTS,fps=30 -c:v libx264 timelapse.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4, codecV: "libx264"},
	{name: "wf-slow-motion", cmd: "ffmpeg -i in.mp4 -vf setpts=4*PTS -c:v libx264 slow.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3, codecV: "libx264"},
	{name: "wf-denoise-sharpen", cmd: "ffmpeg -i noisy.mp4 -vf hqdn3d,unsharp -c:v libx264 clean.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4, codecV: "libx264"},
	{name: "wf-webcam-record", cmd: "ffmpeg -i /dev/video0 -vf scale=1280:720,fps=30 -c:v libx264 webcam.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4, codecV: "libx264"},
	{name: "wf-screen-record", cmd: "ffmpeg -i screen.mp4 -vf crop=1920:1080:0:0,fps=60 -c:v libx264 screen_out.mp4", wantIn: 1, wantOut: 1, wantN: 2, wantE: 4, codecV: "libx264"},
	{name: "wf-vhs-restore", cmd: "ffmpeg -i vhs.avi -vf yadif,hqdn3d,eq=contrast=1.3:brightness=0.05 -c:v libx264 restored.mp4", wantIn: 1, wantOut: 1, wantN: 3, wantE: 5, codecV: "libx264"},
	{name: "wf-watermark", cmd: "ffmpeg -i in.mp4 -vf drawtext=text=PREVIEW:fontsize=48:fontcolor=white -c:v libx264 watermark.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3, codecV: "libx264"},
	{name: "wf-color-correct", cmd: "ffmpeg -i in.mp4 -vf eq=brightness=0.1:contrast=1.2:saturation=1.3 -c:v libx264 corrected.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3, codecV: "libx264"},
	{name: "wf-extract-audio-only", cmd: "ffmpeg -i video.mp4 -vn -c:a copy audio.m4a", wantIn: 1, wantOut: 1, wantN: 0, wantE: 1, codecA: "copy"},
	{name: "wf-mux-sub", cmd: "ffmpeg -i in.mp4 -c:v copy -c:a copy -c:s mov_text out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 3, codecV: "copy", codecA: "copy", codecS: "mov_text"},
	{name: "wf-lossless-remux", cmd: "ffmpeg -i in.mkv -c copy out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "copy", codecA: "copy"},

	// ---- Additional codec+filter combos ----
	{name: "combo-x264-yadif", cmd: "ffmpeg -i in.mp4 -vf yadif -c:v libx264 -c:a copy out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3, codecV: "libx264", codecA: "copy"},
	{name: "combo-x265-hqdn3d", cmd: "ffmpeg -i in.mp4 -vf hqdn3d -c:v libx265 out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3, codecV: "libx265"},
	{name: "combo-vpx-crop", cmd: "ffmpeg -i in.mp4 -vf crop=640:480 -c:v libvpx out.webm", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3, codecV: "libvpx"},
	{name: "combo-av1-scale", cmd: "ffmpeg -i in.mp4 -vf scale=3840:2160 -c:v libaom-av1 out.mkv", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3, codecV: "libaom-av1"},
	{name: "combo-prores-lut", cmd: "ffmpeg -i in.mp4 -vf lut=r=negval -c:v prores_ks out.mov", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3, codecV: "prores_ks"},
	{name: "combo-aac-loudnorm", cmd: "ffmpeg -i in.mp4 -af loudnorm -c:a aac out.mp4", wantIn: 1, wantOut: 1, wantN: 1, wantE: 3, codecA: "aac"},
	{name: "combo-mp3-volume", cmd: "ffmpeg -i in.mp4 -vn -af volume=0.8 -c:a libmp3lame out.mp3", wantIn: 1, wantOut: 1, wantN: 1, wantE: 2, codecA: "libmp3lame"},
	{name: "combo-opus-highpass", cmd: "ffmpeg -i in.mp4 -vn -af highpass=f=100 -c:a libopus out.webm", wantIn: 1, wantOut: 1, wantN: 1, wantE: 2, codecA: "libopus"},

	// ---- Extreme filter chains ----
	{name: "extreme-6-vf", cmd: "ffmpeg -i in.mp4 -vf yadif,hqdn3d,eq=brightness=0.1,unsharp,scale=1280:720,fps=30 out.mp4", wantIn: 1, wantOut: 1, wantN: 6, wantE: 8},
	{name: "extreme-7-vf", cmd: "ffmpeg -i in.mp4 -vf yadif,hqdn3d,eq=brightness=0.05,unsharp,scale=1280:720,format=yuv420p,fps=30 out.mp4", wantIn: 1, wantOut: 1, wantN: 7, wantE: 9},
	{name: "extreme-4-af", cmd: "ffmpeg -i in.mp4 -af volume=1.5,highpass=f=200,lowpass=f=3000,loudnorm out.mp4", wantIn: 1, wantOut: 1, wantN: 4, wantE: 6},
	{name: "extreme-5-af", cmd: "ffmpeg -i in.mp4 -af volume=1.5,highpass=f=200,lowpass=f=3000,equalizer=f=1000:t=q:w=1:g=2,loudnorm out.mp4", wantIn: 1, wantOut: 1, wantN: 5, wantE: 7},
	{name: "extreme-vf-af-both-long", cmd: "ffmpeg -i in.mp4 -vf yadif,scale=1280:720,fps=30,format=yuv420p -af volume=1.5,loudnorm -c:v libx264 -c:a aac out.mp4", wantIn: 1, wantOut: 1, wantN: 6, wantE: 8, codecV: "libx264", codecA: "aac"},

	// ---- Unknown flag passthrough ----
	{name: "unknown-flag-with-val", cmd: "ffmpeg -i in.mp4 -preset fast -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx264"},
	{name: "unknown-flag-crf", cmd: "ffmpeg -i in.mp4 -crf 23 -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx264"},
	{name: "unknown-flag-tune", cmd: "ffmpeg -i in.mp4 -tune film -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx264"},
	{name: "unknown-flag-profile", cmd: "ffmpeg -i in.mp4 -profile:v high -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx264"},
	{name: "unknown-flag-level", cmd: "ffmpeg -i in.mp4 -level 4.1 -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx264"},
	{name: "unknown-flag-g", cmd: "ffmpeg -i in.mp4 -g 250 -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx264"},
	{name: "unknown-flag-bf", cmd: "ffmpeg -i in.mp4 -bf 2 -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx264"},
	{name: "unknown-flag-threads", cmd: "ffmpeg -i in.mp4 -threads 4 -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx264"},
	{name: "unknown-flag-pix-fmt", cmd: "ffmpeg -i in.mp4 -pix_fmt yuv420p -c:v libx264 out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx264"},
	{name: "unknown-flag-ar", cmd: "ffmpeg -i in.mp4 -ar 44100 -c:a aac out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecA: "aac"},
	{name: "unknown-flag-ac", cmd: "ffmpeg -i in.mp4 -ac 2 -c:a aac out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecA: "aac"},

	// ---- Multiple unknown flags combined ----
	{name: "multi-unknown-flags", cmd: "ffmpeg -i in.mp4 -preset medium -crf 18 -tune film -profile:v high -c:v libx264 -c:a aac out.mp4", wantIn: 1, wantOut: 1, wantN: 0, wantE: 2, codecV: "libx264", codecA: "aac"},
}

func TestCorpusFFmpegCommands(t *testing.T) {
	for _, tt := range corpusTests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse(tt.cmd)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.Inputs) != tt.wantIn {
				t.Errorf("inputs: got %d, want %d", len(cfg.Inputs), tt.wantIn)
			}
			if len(cfg.Outputs) != tt.wantOut {
				t.Errorf("outputs: got %d, want %d", len(cfg.Outputs), tt.wantOut)
			}
			if len(cfg.Graph.Nodes) != tt.wantN {
				t.Errorf("nodes: got %d, want %d", len(cfg.Graph.Nodes), tt.wantN)
			}
			if len(cfg.Graph.Edges) != tt.wantE {
				t.Errorf("edges: got %d, want %d", len(cfg.Graph.Edges), tt.wantE)
			}
			if tt.codecV != "" && cfg.Outputs[0].CodecVideo != tt.codecV {
				t.Errorf("codec_video: got %q, want %q", cfg.Outputs[0].CodecVideo, tt.codecV)
			}
			if tt.codecA != "" && cfg.Outputs[0].CodecAudio != tt.codecA {
				t.Errorf("codec_audio: got %q, want %q", cfg.Outputs[0].CodecAudio, tt.codecA)
			}
			if tt.codecS != "" {
				if cfg.Outputs[0].CodecSubtitle != tt.codecS {
					t.Errorf("codec_subtitle: got %q, want %q", cfg.Outputs[0].CodecSubtitle, tt.codecS)
				}
				// Subtitle edge should exist in graph
				found := false
				for _, e := range cfg.Graph.Edges {
					if e.Type == "subtitle" {
						found = true
						break
					}
				}
				if !found {
					t.Error("expected subtitle edge, found none")
				}
			}
			if tt.bsfV != "" && cfg.Outputs[0].BSFVideo != tt.bsfV {
				t.Errorf("bsf_video: got %q, want %q", cfg.Outputs[0].BSFVideo, tt.bsfV)
			}
			if tt.bsfA != "" && cfg.Outputs[0].BSFAudio != tt.bsfA {
				t.Errorf("bsf_audio: got %q, want %q", cfg.Outputs[0].BSFAudio, tt.bsfA)
			}
			if tt.format != "" && cfg.Outputs[0].Format != tt.format {
				t.Errorf("format: got %q, want %q", cfg.Outputs[0].Format, tt.format)
			}
		})
	}
}
