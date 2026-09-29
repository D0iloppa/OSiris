#!/usr/bin/env python3
"""OSiris 브랜드 자산 생성 — 호루스의 눈(Eye of Horus) 파비콘 세트.

트레이스된 벡터 폴리곤(traced.txt)에서:
  - brand/osiris-mark.svg   투명 배경 + currentColor (문서/UI 헤더용)
  - brand/favicon.svg       배지형(어두운 라운드 사각 + 금색 눈) — 브라우저 탭용
  - brand/favicon-{16,32,48,180,512}.png
  - brand/favicon-maskable-512.png  (PWA maskable, 안전영역 여유)
  - brand/favicon.ico       16/32/48 멀티해상도
"""
import os
import re
from PIL import Image, ImageDraw

SCRATCH = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.dirname(os.path.abspath(__file__))

# 브랜드 색 — 이집트 모티프. 어두운 슬레이트 + 금.
BG = (15, 20, 25)        # #0F1419  근사 블랙 슬레이트
FG = (232, 181, 75)      # #E8B54B  이집트 금
BG_HEX = '#0F1419'
FG_HEX = '#E8B54B'

SS = 8  # 슈퍼샘플링 배수


def load_paths():
    vw, vh, d = open(os.path.join(SCRATCH, 'traced-path.txt')).read().split('|', 2)
    subs = []
    for chunk in d.split('Z'):
        chunk = chunk.strip()
        if not chunk:
            continue
        pts = [(float(a), float(b)) for a, b in
               re.findall(r'[ML](-?[\d.]+) (-?[\d.]+)', chunk)]
        if len(pts) >= 3:
            subs.append(pts)
    # 면적순: 큰 2개 = 채움(본체·눈썹), 나머지 = 구멍(흰자)
    def area(p):
        n = len(p)
        return abs(sum(p[i][0] * p[(i + 1) % n][1] - p[(i + 1) % n][0] * p[i][1]
                       for i in range(n))) / 2
    order = sorted(range(len(subs)), key=lambda i: area(subs[i]), reverse=True)
    fills = [subs[i] for i in order[:2]]
    holes = [subs[i] for i in order[2:]]
    return float(vw), float(vh), fills, holes


VW, VH, FILLS, HOLES = load_paths()


# 작은 크기에서 나선 꼬리는 노이즈로 뭉개진다. 16/32px 은 나선을 뺀
# 폭(=CROP_SMALL)만 기준으로 배치해 눈·눈썹을 더 크게 담는다.
# 크기별로 디테일 단계를 두는 것은 파비콘의 표준 관행이다.
CROP_SMALL = 74.0


def mark_mask(box_w, box_h, frac, crop_w=None):
    """마크를 box 안에 frac 비율로 중앙 배치한 알파 마스크(L, 255=잉크).

    crop_w 를 주면 그 폭을 기준으로 스케일·중앙정렬한다(나선이 밖으로 나감).
    """
    W, H = box_w * SS, box_h * SS
    ref_w = crop_w if crop_w else VW
    target_w = W * frac
    s = target_w / ref_w
    if VH * s > H * frac:
        s = (H * frac) / VH
    ox = (W - ref_w * s) / 2
    oy = (H - VH * s) / 2
    m = Image.new('L', (W, H), 0)
    d = ImageDraw.Draw(m)
    for p in FILLS:
        d.polygon([(x * s + ox, y * s + oy) for x, y in p], fill=255)
    for p in HOLES:
        d.polygon([(x * s + ox, y * s + oy) for x, y in p], fill=0)
    return m.resize((box_w, box_h), Image.LANCZOS)


def badge(size, frac=0.68, radius_frac=0.22, crop_w=None):
    """어두운 라운드 사각 배지 + 금색 눈."""
    W = size * SS
    img = Image.new('RGBA', (W, W), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    r = int(W * radius_frac)
    d.rounded_rectangle([0, 0, W - 1, W - 1], radius=r, fill=BG + (255,))
    img = img.resize((size, size), Image.LANCZOS)
    eye = mark_mask(size, size, frac, crop_w)
    fg = Image.new('RGBA', (size, size), FG + (255,))
    img.paste(fg, (0, 0), eye)
    return img


def transparent(size, frac=0.92, color=FG):
    img = Image.new('RGBA', (size, size), (0, 0, 0, 0))
    eye = mark_mask(size, size, frac)
    fg = Image.new('RGBA', (size, size), color + (255,))
    img.paste(fg, (0, 0), eye)
    return img


def write_ico(path, images):
    """멀티해상도 ICO 를 직접 작성한다(각 프레임은 PNG 로 임베드).

    ICONDIR(6B) + ICONDIRENTRY(16B x n) + PNG payload 들.
    """
    import io
    import struct

    payloads = []
    for im in images:
        buf = io.BytesIO()
        im.save(buf, format='PNG', optimize=True)
        payloads.append(buf.getvalue())

    n = len(images)
    offset = 6 + 16 * n
    out = bytearray()
    out += struct.pack('<HHH', 0, 1, n)          # reserved, type=icon, count
    for im, data in zip(images, payloads):
        w, h = im.size
        out += struct.pack(
            '<BBBBHHII',
            0 if w >= 256 else w,                # width (0 = 256)
            0 if h >= 256 else h,                # height
            0,                                   # palette count
            0,                                   # reserved
            1,                                   # color planes
            32,                                  # bits per pixel
            len(data),                           # bytes in resource
            offset,                              # offset
        )
        offset += len(data)
    for data in payloads:
        out += data
    with open(path, 'wb') as f:
        f.write(bytes(out))


def svg_path_data():
    parts = []
    for p in FILLS + HOLES:
        seg = ''.join(('M' if i == 0 else 'L') +
                      f'{x:g} {y:g}' for i, (x, y) in enumerate(p))
        parts.append(seg + 'Z')
    return ' '.join(parts)


def main():
    os.makedirs(OUT, exist_ok=True)
    os.makedirs(os.path.join(OUT, 'source'), exist_ok=True)
    d = svg_path_data()

    # 1) 투명 마크 — currentColor 로 상속받아 어디에나 얹힌다
    with open(f'{OUT}/osiris-mark.svg', 'w') as f:
        f.write(
            f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {VW:g} {VH:g}" '
            f'role="img" aria-label="OSiris — Eye of Horus">'
            f'<title>OSiris</title>'
            f'<path fill="currentColor" fill-rule="evenodd" d="{d}"/>'
            f'</svg>\n')

    # 1b) 색 고정 변형 + 테마 자동 변형
    #
    # 왜 필요한가: currentColor 마크는 **인라인 <svg> 로 넣을 때만** 동작한다.
    # <img src="...svg"> 로 넣으면 SVG 가 별도 문서로 렌더되어 페이지의 CSS 를
    # 상속하지 않으므로 currentColor 가 기본값(검정)으로 굳는다 — 어두운 배경
    # 위에서 마크가 사라지는 실제 사고가 있었다.
    #
    # 색 선택 근거는 docs/design/41-console-design-system.md §2.1.2:
    #   · 금(#E8B54B)은 **어두운 색면 위에서만** — badge-dark(9.82:1),
    #     lapis-900(8.45:1). 라이트 캔버스에는 절대 놓지 않는다.
    #   · 라이트 캔버스용은 뉴트럴 최심단 gray-900(#0f172a, 백색 대비 17.85:1).
    INK_HEX = '#0f172a'   # gray-900 — 라이트 캔버스용

    def mark_svg(body):
        return (f'<svg xmlns="http://www.w3.org/2000/svg" '
                f'viewBox="0 0 {VW:g} {VH:g}" '
                f'role="img" aria-label="OSiris — Eye of Horus">'
                f'<title>OSiris</title>{body}</svg>\n')

    # 고정색 — 배경을 확실히 아는 자리에서 강제할 때
    with open(f'{OUT}/osiris-mark-gold.svg', 'w') as f:
        f.write(mark_svg(f'<path fill="{FG_HEX}" fill-rule="evenodd" d="{d}"/>'))
    with open(f'{OUT}/osiris-mark-ink.svg', 'w') as f:
        f.write(mark_svg(f'<path fill="{INK_HEX}" fill-rule="evenodd" d="{d}"/>'))

    # 테마 자동 — SVG 자신의 <style> 안에서 prefers-color-scheme 을 본다.
    # <img> 로 넣어도 동작한다(SVG 가 자기 렌더링 컨텍스트에서 미디어쿼리를
    # 평가하므로). 배경을 모르는 자리의 기본 선택지.
    auto_body = (
        '<style>'
        f'.m{{fill:{INK_HEX}}}'
        f'@media (prefers-color-scheme:dark){{.m{{fill:{FG_HEX}}}}}'
        '</style>'
        f'<path class="m" fill-rule="evenodd" d="{d}"/>'
    )
    with open(f'{OUT}/osiris-mark-auto.svg', 'w') as f:
        f.write(mark_svg(auto_body))

    # 2) 배지형 파비콘 — 어두운 배경 고정. 밝은/어두운 브라우저 크롬 양쪽에서 읽힌다.
    pad = (100 - 68) / 2
    s = 68 / VW
    ox, oy = pad, (VH * s and (100 * VH / VW - VH * s) / 2)
    # 배지는 정사각 100x100 기준으로 마크를 중앙 배치
    box = 100.0
    sc = (box * 0.68) / VW
    tx = (box - VW * sc) / 2
    ty = (box - VH * sc) / 2
    with open(f'{OUT}/favicon.svg', 'w') as f:
        f.write(
            f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" '
            f'role="img" aria-label="OSiris">'
            f'<title>OSiris</title>'
            f'<rect width="100" height="100" rx="22" fill="{BG_HEX}"/>'
            f'<g transform="translate({tx:.3f} {ty:.3f}) scale({sc:.5f})">'
            f'<path fill="{FG_HEX}" fill-rule="evenodd" d="{d}"/>'
            f'</g></svg>\n')

    # 3) 래스터 파비콘 — 크기별 디테일 단계
    #    16/32: 나선 제거(크롭) 글리프. 48 이상: 전체 마크.
    small = {16: 0.74, 32: 0.72}
    for n, f in small.items():
        badge(n, frac=f, crop_w=CROP_SMALL).save(f'{OUT}/favicon-{n}.png')
    for n in (48, 180, 512):
        badge(n).save(f'{OUT}/favicon-{n}.png')
    # maskable: 안전영역 80% 규격 → 마크를 더 작게, 모서리는 플랫(런처가 깎는다)
    badge(512, frac=0.52, radius_frac=0.0).save(f'{OUT}/favicon-maskable-512.png')
    # 투명 마크 PNG (README/문서용)
    transparent(512).save(f'{OUT}/osiris-mark-512.png')

    # 4) ICO 멀티해상도 — 크기별로 개별 렌더한 글리프를 담는다.
    #    Pillow 의 ICO 저장은 단일 원본을 축소하므로(16px 이 뭉개진다)
    #    ICO 컨테이너를 직접 작성한다. PNG 임베드는 Vista 이후 표준.
    write_ico(f'{OUT}/favicon.ico', [
        badge(16, frac=0.74, crop_w=CROP_SMALL),
        badge(32, frac=0.72, crop_w=CROP_SMALL),
        badge(48),
    ])

    print('생성 완료:')
    for fn in sorted(os.listdir(OUT)):
        p = os.path.join(OUT, fn)
        if os.path.isfile(p):
            print(f'  {fn:34s} {os.path.getsize(p):>7,d} B')


if __name__ == '__main__':
    main()
