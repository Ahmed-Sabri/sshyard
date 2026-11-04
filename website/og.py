"""Draws the 1200×630 Open Graph images in Source Han Sans (思源黑体),
which the SIL Open Font License lets anyone embed in images.

Each language uses its own regional subset (CN, TW, JP, KR), so Han
characters take the glyph forms readers expect. The fonts are downloaded
once into website/.cache/fonts from a pinned commit of
adobe-fonts/source-han-sans and checked against their SHA-256.
"""

from __future__ import annotations

import hashlib
import re
import tempfile
import urllib.request
from pathlib import Path

from PIL import Image, ImageDraw, ImageFilter, ImageFont

W, H = 1200, 630
S = 2  # drawn at twice the size, then scaled down for smooth edges

FONT_COMMIT = "a4f7cf94edfb9d7ffbdfc4841de276358bd7e0f2"  # 2.005R
FONT_SHA256 = {
    "SourceHanSansCN-Bold.otf": "62383707c086a32f3afd5e293f34c7eff64c7fea31f579fdc6cbe34d920519a6",
    "SourceHanSansCN-Regular.otf": "e2bc8a2e7f37474b774fff8db758681ece40bb6947a90d571bce9dd60671a8e4",
    "SourceHanSansTW-Bold.otf": "a586990078d8fe4e97202e38fa3c2e0a61ce54009affdd9e2cfe59351ad3f5c7",
    "SourceHanSansTW-Regular.otf": "5034bc32aa64bc93ce673fe05752724bb31fc6757c6bc1d23e925052c54fd2ac",
    "SourceHanSansJP-Bold.otf": "3a2722f94c97a53b172579a10ef8fc34b3fa8a6bb4f7947a2ec709ab647fb755",
    "SourceHanSansJP-Regular.otf": "40d1b760d1135539f6b6e0ee2b9f415de6d97576f7676840b06306c7c190c074",
    "SourceHanSansKR-Bold.otf": "f678def86be3d973e85fb0e34513b93b9ec7d6a478380d93fe79d43d1ad9f797",
    "SourceHanSansKR-Regular.otf": "d8299926a5284f7461b46fa0617cc05d4f1fce8f034ffff78084225ce8b38071",
}
CACHE = Path(__file__).resolve().parent / ".cache" / "fonts"

BRAND = (25, 132, 252)  # #1984FC
BRAND_DEEP = (21, 112, 214)  # #1570D6, the pressed accent
WHITE = (255, 255, 255)

# A word that must not break: Latin, digits and Hangul runs (Korean
# breaks between words, like English); every other character may break.
TOKEN = re.compile(r"[A-Za-z0-9À-ɏ가-힣ㄱ-ㆎ][A-Za-z0-9À-ɏ가-힣ㄱ-ㆎ.,'’&+/:@#%_\-]*|\s+|.", re.S)
NO_LINE_START = set("，。、：；！？）」』》〉】,.;:!?)]}…·ー")


def sha256(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for chunk in iter(lambda: f.read(1 << 20), b""):
            h.update(chunk)
    return h.hexdigest()


def font_path(region: str, weight: str) -> Path:
    name = f"SourceHanSans{region}-{weight}.otf"
    want = FONT_SHA256[name]
    path = CACHE / name
    if path.exists() and sha256(path) == want:
        return path
    url = f"https://raw.githubusercontent.com/adobe-fonts/source-han-sans/{FONT_COMMIT}/SubsetOTF/{region}/{name}"
    CACHE.mkdir(parents=True, exist_ok=True)
    print(f"Downloading {name}")
    with tempfile.NamedTemporaryFile(dir=CACHE, delete=False) as tmp:
        with urllib.request.urlopen(url, timeout=120) as r:
            while chunk := r.read(1 << 20):
                tmp.write(chunk)
    got = sha256(Path(tmp.name))
    if got != want:
        Path(tmp.name).unlink()
        raise SystemExit(f"og.py: {name} has SHA-256 {got}, expected {want}")
    Path(tmp.name).replace(path)
    return path


def font(region: str, weight: str, size: float) -> ImageFont.FreeTypeFont:
    return ImageFont.truetype(str(font_path(region, weight)), round(size * S))


def wrap(text: str, f: ImageFont.FreeTypeFont, width: float) -> list[str]:
    lines, cur = [], ""
    for tok in TOKEN.findall(text):
        if not cur.strip() or tok in NO_LINE_START or f.getlength(cur + tok) <= width:
            cur += tok
        else:
            lines.append(cur.rstrip())
            cur = tok.lstrip()
    if cur.strip():
        lines.append(cur.rstrip())
    return lines


def clamp_lines(lines: list[str], f: ImageFont.FreeTypeFont, width: float, n: int) -> list[str]:
    if len(lines) <= n:
        return lines
    last = lines[n - 1]
    while last and f.getlength(last + "…") > width:
        last = last[:-1]
    return lines[: n - 1] + [last.rstrip(" ,，、") + "…"]


def rounded_mask(size: tuple[int, int], radius: int) -> Image.Image:
    m = Image.new("L", size, 0)
    ImageDraw.Draw(m).rounded_rectangle((0, 0, size[0] - 1, size[1] - 1), radius, fill=255)
    return m


def render(out: Path, *, region: str, title: list[str], subtitle: str, tags: list[str],
           logo: Path, shot: Path) -> None:
    w, h = W * S, H * S
    px = lambda v: round(v * S)

    # Brand blue, deepening toward the bottom right.
    grad = Image.blend(Image.linear_gradient("L").resize((w, h)),
                       Image.linear_gradient("L").rotate(90).transpose(Image.FLIP_LEFT_RIGHT).resize((w, h)), 0.5)
    img = Image.composite(Image.new("RGBA", (w, h), BRAND_DEEP + (255,)),
                          Image.new("RGBA", (w, h), BRAND + (255,)), grad)

    # The arch of the logo, large and faint, behind the screenshot.
    deco = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    ImageDraw.Draw(deco).rounded_rectangle((px(610), px(36), px(1330), px(1200)), px(360),
                                           outline=WHITE + (22,), width=px(56))
    img.alpha_composite(deco)

    # The screenshot, bleeding off the right and bottom edges.
    src = Image.open(shot).convert("RGBA")
    sw = px(820)
    sh = round(src.height * sw / src.width)
    src = src.resize((sw, sh), Image.LANCZOS)
    sx, sy = px(618), px(178)
    shadow = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    ImageDraw.Draw(shadow).rounded_rectangle((sx, sy + px(18), sx + sw, sy + sh + px(18)), px(20),
                                             fill=(6, 30, 70, 110))
    img.alpha_composite(shadow.filter(ImageFilter.GaussianBlur(px(28))))
    img.paste(src, (sx, sy), rounded_mask((sw, sh), px(18)))
    ImageDraw.Draw(img).rounded_rectangle((sx, sy, sx + sw - 1, sy + sh - 1), px(18),
                                          outline=WHITE + (70,), width=px(1.5))

    draw = ImageDraw.Draw(img)
    x, text_w = px(72), px(500)

    # Logo and name.
    mark = Image.open(logo).convert("RGBA").resize((px(84), px(84)), Image.LANCZOS)
    img.alpha_composite(mark, (x - px(6), px(60)))
    draw.text((x + px(92), px(102)), "SSHYard", font=font(region, "Bold", 40), fill=WHITE, anchor="lm")

    # Title: as large as keeps each of its lines whole, else wrapped.
    for size in range(64, 42, -2):
        tf = font(region, "Bold", size)
        if all(tf.getlength(part) <= text_w for part in title):
            lines = title
            break
    else:
        tf = font(region, "Bold", 52)
        lines = clamp_lines([line for part in title for line in wrap(part, tf, text_w)], tf, text_w, 3)
    y = px(196)
    step = round(tf.size * 1.2)
    for line in lines:
        draw.text((x, y), line, font=tf, fill=WHITE, anchor="la")
        y += step

    # Subtitle.
    sf = font(region, "Regular", 25)
    y += px(26)
    for line in clamp_lines(wrap(subtitle, sf, text_w), sf, text_w, 3):
        draw.text((x, y), line, font=sf, fill=WHITE + (225,), anchor="la")
        y += round(sf.size * 1.5)

    # Tags along the bottom, as many as fit.
    tag_font = font(region, "Regular", 20)
    tx, ty, th = x, px(H - 66 - 44), px(44)
    pills = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    pd = ImageDraw.Draw(pills)
    for tag in tags:
        tw = round(tag_font.getlength(tag)) + px(36)
        if tx + tw > x + px(540):
            break
        pd.rounded_rectangle((tx, ty, tx + tw, ty + th), th // 2, fill=WHITE + (38,), outline=WHITE + (64,), width=px(1))
        pd.text((tx + tw // 2, ty + th // 2), tag, font=tag_font, fill=WHITE, anchor="mm")
        tx += tw + px(10)
    img.alpha_composite(pills)

    out.parent.mkdir(parents=True, exist_ok=True)
    img.resize((W, H), Image.LANCZOS).convert("RGB").save(out, "PNG", optimize=True)
