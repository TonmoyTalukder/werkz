#!/usr/bin/env python3
"""Draw the werkz icon (saffron → red → blue "w" on a dark squircle) and write PNGs + a macOS .icns."""
import struct, sys, io, os
from PIL import Image, ImageDraw, ImageFilter

S = 4096  # supersampled canvas
SAFFRON, RED, BLUE = (255, 153, 51), (232, 52, 78), (59, 108, 246)

def lerp(a, b, t): return tuple(int(a[i] + (b[i] - a[i]) * t) for i in range(3))

def brand(t):
    return lerp(SAFFRON, RED, t * 2) if t < 0.5 else lerp(RED, BLUE, (t - 0.5) * 2)

def diagonal_gradient(size):
    small = Image.new("RGB", (256, 256))
    px = small.load()
    for y in range(256):
        for x in range(256):
            px[x, y] = brand(min(1, max(0, ((x * 0.85 + y * 0.15) / 255 - 0.2) / 0.6)))
    return small.resize((size, size), Image.BICUBIC)

def draw():
    # background squircle with a soft vertical gradient
    bg = Image.new("RGB", (S, S))
    d = ImageDraw.Draw(bg)
    for y in range(S):
        t = y / S
        d.line([(0, y), (S, y)], fill=lerp((30, 31, 44), (12, 13, 20), t))
    mask = Image.new("L", (S, S), 0)
    m = 0.06 * S
    ImageDraw.Draw(mask).rounded_rectangle([m, m, S - m, S - m], radius=0.2 * S, fill=255)
    icon = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    icon.paste(bg, (0, 0), mask)

    # thin inner highlight border
    border = Image.new("L", (S, S), 0)
    bd = ImageDraw.Draw(border)
    bd.rounded_rectangle([m, m, S - m, S - m], radius=0.2 * S, outline=255, width=int(0.006 * S))
    icon.paste(Image.new("RGBA", (S, S), (255, 255, 255, 40)), (0, 0), border)

    grad = diagonal_gradient(S)

    # the "w"
    pts = [(0.20, 0.30), (0.335, 0.68), (0.50, 0.40), (0.665, 0.68), (0.80, 0.30)]
    pts = [(x * S, y * S) for x, y in pts]
    wmask = Image.new("L", (S, S), 0)
    wd = ImageDraw.Draw(wmask)
    w = int(0.105 * S)
    wd.line(pts, fill=255, width=w, joint="curve")
    r = w / 2
    for x, y in (pts[0], pts[-1]):
        wd.ellipse([x - r, y - r, x + r, y + r], fill=255)

    # glow under the w
    glow = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    glow.paste(grad, (0, 0), wmask)
    glow = glow.filter(ImageFilter.GaussianBlur(0.03 * S))
    a = glow.getchannel("A").point(lambda v: int(v * 0.55))
    glow.putalpha(a)
    clip = Image.new("RGBA", (S, S), (0, 0, 0, 0))
    clip.paste(glow, (0, 0), mask)
    icon = Image.alpha_composite(icon, clip)

    icon.paste(grad, (0, 0), wmask)

    # terminal cursor bar
    cmask = Image.new("L", (S, S), 0)
    ImageDraw.Draw(cmask).rounded_rectangle([0.43 * S, 0.775 * S, 0.57 * S, 0.815 * S], radius=0.02 * S, fill=255)
    icon.paste(Image.new("RGB", (S, S), SAFFRON), (0, 0), cmask)
    return icon

def write_icns(images, path):
    # PNG-backed icns entries (macOS 10.7+)
    types = [("icp4", 16), ("icp5", 32), ("icp6", 64), ("ic07", 128), ("ic08", 256),
             ("ic09", 512), ("ic10", 1024), ("ic11", 32), ("ic12", 64), ("ic13", 256), ("ic14", 512)]
    body = b""
    for t, sz in types:
        buf = io.BytesIO()
        images[sz].save(buf, "PNG")
        data = buf.getvalue()
        body += t.encode() + struct.pack(">I", len(data) + 8) + data
    with open(path, "wb") as f:
        f.write(b"icns" + struct.pack(">I", len(body) + 8) + body)

if __name__ == "__main__":
    out = sys.argv[1] if len(sys.argv) > 1 else "dist/icons"
    os.makedirs(out, exist_ok=True)
    big = draw()
    sizes = [16, 24, 32, 48, 64, 128, 256, 512, 1024]
    imgs = {s: big.resize((s, s), Image.LANCZOS) for s in sizes}
    for s, im in imgs.items():
        im.save(os.path.join(out, f"werkz-{s}.png"))
    write_icns(imgs, os.path.join(out, "werkz.icns"))
    print("icons written to", out)
