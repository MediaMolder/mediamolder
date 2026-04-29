// Comma-separated lowercase file extensions accepted by FFmpeg's
// demuxers and muxers, used as the default filter for the file
// browser when picking input/output URLs. Aim is "if FFmpeg can read
// or write it, the user can pick it" — being too restrictive surprised
// users who tried to load common formats (e.g. .y4m raw video).
//
// This list intentionally errs on the side of inclusion. It covers
// the common containers, raw streams, image sequences, and audio-only
// formats. It does NOT try to be exhaustive (FFmpeg supports
// hundreds of niche formats); add to this list as the need arises.

const VIDEO_CONTAINERS = [
  'mp4', 'm4v', 'mov', 'mkv', 'webm', 'avi', 'flv', 'wmv', '3gp', '3g2',
  'mpg', 'mpeg', 'm2v', 'mxf', 'gxf', 'asf', 'rm', 'rmvb', 'ogv', 'mts',
  'm2ts', 'ts', 'tsv', 'vob', 'dv', 'nut', 'ivf', 'mj2', 'mjpg', 'mjpeg',
  'f4v', 'qt',
];

const RAW_VIDEO = [
  'y4m',  // YUV4MPEG2 — common encoder input
  'yuv',  // raw YUV
  'h264', '264', 'avc',
  'h265', '265', 'hevc',
  'av1',
  'vp8', 'vp9',
  'mpv', 'm1v', 'm4s',
];

const AUDIO = [
  'mp3', 'mp2', 'm4a', 'aac', 'ac3', 'eac3', 'dts', 'ec3', 'thd',
  'wav', 'w64', 'aiff', 'aif', 'flac', 'alac', 'ape', 'opus', 'ogg', 'oga',
  'wma', 'amr', 'au', 'caf', 'mka', 'tta', 'tak', 'wv',
];

const SUBTITLES = [
  'srt', 'ass', 'ssa', 'vtt', 'sub', 'sup', 'ttml', 'sbv', 'mks',
];

const IMAGES = [
  // Single images and image-sequence demuxer inputs.
  'jpg', 'jpeg', 'jp2', 'png', 'apng', 'bmp', 'gif', 'tif', 'tiff',
  'webp', 'avif', 'heic', 'heif', 'tga', 'pgm', 'ppm', 'pbm', 'pam',
  'exr', 'xpm', 'xwd', 'pcx', 'sgi', 'sun', 'dds', 'dpx',
];

const PLAYLISTS = ['m3u', 'm3u8', 'mpd', 'concat'];

/** Comma-separated list suitable for the FileBrowser `filter` prop. */
export const MEDIA_FILE_EXTENSIONS = [
  ...VIDEO_CONTAINERS,
  ...RAW_VIDEO,
  ...AUDIO,
  ...SUBTITLES,
  ...IMAGES,
  ...PLAYLISTS,
].join(',');
