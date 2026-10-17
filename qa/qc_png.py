"""Bounded decoder for non-interlaced, 8-bit RGB/RGBA Chromium PNGs.

Unsupported PNG formats are rejected, never treated as verified screenshots.
All chunk CRCs, critical structure, the complete zlib stream and every decoded
scanline are checked. No third-party decoder or host browser is required.
"""
import hashlib
import json
import os
import pathlib
import stat
import struct
import sys
import zlib

MAX_FILE = 32 * 1024 * 1024
MAX_DECODED = 64 * 1024 * 1024
SIGNATURE = b'\x89PNG\r\n\x1a\n'


def decode_png(raw):
    if len(raw) > MAX_FILE or not raw.startswith(SIGNATURE):
        raise ValueError('Invalid or oversized PNG')
    offset, width, height, channels = 8, 0, 0, 0
    saw_idat = ended_idat = saw_iend = saw_palette = False
    compressed = bytearray()
    while offset < len(raw):
        if offset + 12 > len(raw):
            raise ValueError('Truncated PNG chunk')
        length, kind = struct.unpack('>I4s', raw[offset:offset + 8])
        end = offset + 12 + length
        if end > len(raw) or not all(65 <= c <= 90 or 97 <= c <= 122 for c in kind) or kind[2] & 32:
            raise ValueError('Invalid PNG chunk')
        data = raw[offset + 8:end - 4]
        crc = struct.unpack('>I', raw[end - 4:end])[0]
        if zlib.crc32(kind + data) & 0xffffffff != crc:
            raise ValueError('PNG CRC mismatch')
        if offset == 8 and kind != b'IHDR':
            raise ValueError('PNG must begin with IHDR')
        if kind == b'IHDR':
            if width or offset != 8 or length != 13:
                raise ValueError('Invalid or repeated PNG IHDR')
            width, height, depth, colour, compression, filtering, interlace = struct.unpack('>IIBBBBB', data)
            if not (0 < width <= 8192 and 0 < height <= 8192) or depth != 8 or colour not in (2, 6) or (compression, filtering, interlace) != (0, 0, 0):
                raise ValueError('Unsupported screenshot PNG format/dimensions')
            channels = 3 if colour == 2 else 4
            if (width * channels + 1) * height > MAX_DECODED:
                raise ValueError('Decoded PNG exceeds screenshot limit')
        elif kind == b'IDAT':
            if ended_idat:
                raise ValueError('Non-consecutive PNG IDAT chunks')
            saw_idat = True
            compressed.extend(data)
        elif kind == b'IEND':
            if length or not saw_idat or end != len(raw):
                raise ValueError('Invalid PNG IEND or trailing data')
            saw_iend = True
        else:
            if saw_idat:
                ended_idat = True
            if kind == b'PLTE':
                if saw_idat or saw_palette or not length or length % 3 or length > 768:
                    raise ValueError('Invalid PNG palette')
                saw_palette = True
            elif not kind[0] & 32:
                raise ValueError('Unsupported PNG critical chunk')
        offset = end
    if not width or not saw_idat or not saw_iend:
        raise ValueError('Incomplete PNG')
    stride = width * channels
    expected = (stride + 1) * height
    decoder = zlib.decompressobj()
    try:
        decoded = decoder.decompress(compressed, expected + 1)
    except zlib.error as error:
        raise ValueError('Invalid PNG zlib stream') from error
    if len(decoded) != expected or not decoder.eof or decoder.unused_data or decoder.unconsumed_tail:
        raise ValueError('PNG pixel stream is truncated, oversized, or has trailing data')
    previous = bytearray(stride)
    pixels_hash = hashlib.sha256()
    for y in range(height):
        start = y * (stride + 1)
        filter_type = decoded[start]
        if filter_type > 4:
            raise ValueError('Invalid PNG scanline filter')
        row = bytearray(decoded[start + 1:start + 1 + stride])
        for x in range(stride):
            left = row[x - channels] if x >= channels else 0
            above = previous[x]
            upper_left = previous[x - channels] if x >= channels else 0
            if filter_type == 1:
                predictor = left
            elif filter_type == 2:
                predictor = above
            elif filter_type == 3:
                predictor = (left + above) // 2
            elif filter_type == 4:
                p = left + above - upper_left
                distances = (abs(p - left), abs(p - above), abs(p - upper_left))
                predictor = (left, above, upper_left)[distances.index(min(distances))]
            else:
                predictor = 0
            row[x] = (row[x] + predictor) & 255
        pixels_hash.update(row)
        previous = row
    return dict(width=width, height=height, pixel_sha256=pixels_hash.hexdigest())


def inspect_png_file(path):
    path = pathlib.Path(path)
    if path.resolve() != path:
        raise ValueError('Screenshot path contains a symlink')
    with os.fdopen(os.open(path, os.O_RDONLY | os.O_NOFOLLOW), 'rb') as source:
        before = os.fstat(source.fileno())
        if not stat.S_ISREG(before.st_mode) or before.st_size > MAX_FILE:
            raise ValueError('Screenshot must be a bounded regular PNG')
        raw = source.read(MAX_FILE + 1)
        after = os.fstat(source.fileno())
    if (before.st_ino, before.st_size, before.st_mtime_ns) != (after.st_ino, after.st_size, after.st_mtime_ns) or len(raw) != after.st_size:
        raise ValueError('Screenshot changed during inspection')
    return dict(sha256=hashlib.sha256(raw).hexdigest(), size=len(raw),
                mtime_ns=after.st_mtime_ns, **decode_png(raw))


if __name__ == '__main__':
    print(json.dumps(inspect_png_file(sys.argv[1])))
