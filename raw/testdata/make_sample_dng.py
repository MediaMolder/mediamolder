#!/usr/bin/env python3
"""Generate a tiny, valid RGGB-Bayer DNG with no external deps.

Self-authored => provenance is trivially clean (CC0). The CFA data is a
deterministic gradient/checker so a correct demosaic yields a NON-uniform sRGB
raster (what the integration test asserts). Output: a single-IFD DNG LibRaw reads.
"""
import struct, sys

W, H = 64, 48  # even dims for a 2x2 Bayer tile; tiny

# --- CFA raw samples (16-bit LE). RGGB: row-even = R,G,G,...; row-odd = G,B,G,B
# Value depends on x and y so demosaicked output varies across the frame.
raw = bytearray()
for y in range(H):
    for x in range(W):
        # gradient 0..~60000 across the diagonal, plus channel offset
        v = int(((x / (W - 1)) * 0.6 + (y / (H - 1)) * 0.3) * 60000) + 2000
        v = max(0, min(65535, v))
        raw += struct.pack('<H', v)
IMG = bytes(raw)

# ---- TIFF/DNG writer -------------------------------------------------------
BYTE, ASCII, SHORT, LONG, RATIONAL, SBYTE, SRATIONAL = 1, 2, 3, 4, 5, 6, 10
TYPESIZE = {BYTE: 1, ASCII: 1, SHORT: 2, LONG: 4, RATIONAL: 8, SBYTE: 1, SRATIONAL: 8}

def pack_vals(t, vals):
    if t in (BYTE, SBYTE):
        return bytes(vals)
    if t == ASCII:
        return vals if isinstance(vals, bytes) else (vals.encode() + b'\x00')
    if t == SHORT:
        return b''.join(struct.pack('<H', v) for v in vals)
    if t == LONG:
        return b''.join(struct.pack('<I', v) for v in vals)
    if t == RATIONAL:
        return b''.join(struct.pack('<II', n, d) for (n, d) in vals)
    if t == SRATIONAL:
        return b''.join(struct.pack('<ii', n, d) for (n, d) in vals)
    raise ValueError(t)

# Each entry: (tag, type, count, values). StripOffsets patched after layout.
entries = [
    (254, LONG, 1, [0]),                # NewSubfileType: full-res primary
    (256, LONG, 1, [W]),                # ImageWidth
    (257, LONG, 1, [H]),                # ImageLength
    (258, SHORT, 1, [16]),              # BitsPerSample
    (259, SHORT, 1, [1]),               # Compression: none
    (262, SHORT, 1, [32803]),           # Photometric: CFA
    (271, ASCII, 0, b'MediaMolder\x00'),# Make
    (272, ASCII, 0, b'Synthetic\x00'),  # Model
    (273, LONG, 1, [0]),                # StripOffsets (patched)
    (277, SHORT, 1, [1]),               # SamplesPerPixel
    (278, LONG, 1, [H]),                # RowsPerStrip
    (279, LONG, 1, [len(IMG)]),         # StripByteCounts
    (284, SHORT, 1, [1]),               # PlanarConfig
    (33421, SHORT, 2, [2, 2]),          # CFARepeatPatternDim
    (33422, BYTE, 4, [0, 1, 1, 2]),     # CFAPattern: RGGB
    (50706, BYTE, 4, [1, 4, 0, 0]),     # DNGVersion 1.4.0.0
    (50707, BYTE, 4, [1, 1, 0, 0]),     # DNGBackwardVersion 1.1
    (50708, ASCII, 0, b'MediaMolder Synthetic\x00'),  # UniqueCameraModel
    # ColorMatrix1 (XYZ->camera), a plausible non-singular matrix (x10000 SRATIONAL)
    (50721, SRATIONAL, 9, [(8000, 10000), (-1000, 10000), (-500, 10000),
                           (-2000, 10000), (11000, 10000), (1000, 10000),
                           (0, 10000), (1000, 10000), (6000, 10000)]),
    (50728, RATIONAL, 3, [(1, 1), (1, 1), (1, 1)]),   # AsShotNeutral (neutral)
    (50778, SHORT, 1, [21]),            # CalibrationIlluminant1: D65
]
entries.sort(key=lambda e: e[0])

n = len(entries)
ifd_size = 2 + n * 12 + 4
header_size = 8
# Layout: header | IFD | external-values | image
ext_blobs, ext_off = [], header_size + ifd_size
ext_layout = {}
for (tag, t, count, vals) in entries:
    cnt = len(vals) if count == 0 else count
    blob = pack_vals(t, vals)
    if len(blob) > 4:
        ext_layout[tag] = ext_off
        ext_blobs.append(blob)
        ext_off += len(blob) + (len(blob) & 1)  # word-align
img_off = ext_off

out = bytearray()
out += struct.pack('<2sHI', b'II', 42, header_size)  # TIFF header, IFD at 8
out += struct.pack('<H', n)
for (tag, t, count, vals) in entries:
    cnt = len(vals) if count == 0 else count
    blob = pack_vals(t, vals)
    val_field = struct.pack('<I', img_off) if tag == 273 else None
    if tag != 273:
        if len(blob) <= 4:
            val_field = blob + b'\x00' * (4 - len(blob))
        else:
            val_field = struct.pack('<I', ext_layout[tag])
    out += struct.pack('<HHI', tag, t, cnt) + val_field
out += struct.pack('<I', 0)  # next IFD = 0
for blob in ext_blobs:
    out += blob + (b'\x00' if (len(blob) & 1) else b'')
assert len(out) == img_off, (len(out), img_off)
out += IMG

with open(sys.argv[1], 'wb') as f:
    f.write(out)
print(f"wrote {sys.argv[1]} ({len(out)} bytes, {W}x{H})")
