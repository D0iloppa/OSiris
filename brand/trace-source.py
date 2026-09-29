#!/usr/bin/env python3
"""호루스의 눈 PNG -> 벡터 SVG 트레이서 (potrace 부재 대체).

경계추적(Moore neighborhood) + RDP 단순화로 폴리곤 path 를 뽑고
even-odd fill 로 구멍(눈동자 흰자 등)을 표현한다.
"""
import os
import sys
from PIL import Image

SRC = os.path.join(os.path.dirname(os.path.abspath(__file__)), 'source', 'eye-of-horus-original.png')


def load_binary(path, thresh=128):
    im = Image.open(path).convert('RGBA')
    w, h = im.size
    px = im.load()
    grid = [[0] * w for _ in range(h)]
    for y in range(h):
        for x in range(w):
            r, g, b, a = px[x, y]
            grid[y][x] = 1 if (a > 128 and (r + g + b) / 3 < thresh) else 0
    return grid, w, h


def label_regions(grid, w, h, target):
    """4-이웃 연결요소 라벨링. target 값(0 또는 1)인 픽셀만."""
    lab = [[0] * w for _ in range(h)]
    regions = []
    for sy in range(h):
        for sx in range(w):
            if grid[sy][sx] != target or lab[sy][sx]:
                continue
            rid = len(regions) + 1
            stack = [(sx, sy)]
            lab[sy][sx] = rid
            pixels = []
            touches_border = False
            while stack:
                x, y = stack.pop()
                pixels.append((x, y))
                if x == 0 or y == 0 or x == w - 1 or y == h - 1:
                    touches_border = True
                for dx, dy in ((1, 0), (-1, 0), (0, 1), (0, -1)):
                    nx, ny = x + dx, y + dy
                    if 0 <= nx < w and 0 <= ny < h and not lab[ny][nx] \
                            and grid[ny][nx] == target:
                        lab[ny][nx] = rid
                        stack.append((nx, ny))
            regions.append({'id': rid, 'pixels': pixels,
                            'border': touches_border})
    return lab, regions


def trace_boundary(mask, w, h, start):
    """Moore-neighborhood 경계추적. mask[y][x] 가 True 인 영역의 외곽선.
    픽셀 코너 좌표계로 반환(계단형)."""
    # 8-이웃 시계방향
    nbrs = [(1, 0), (1, 1), (0, 1), (-1, 1), (-1, 0), (-1, -1), (0, -1), (1, -1)]

    def inside(x, y):
        return 0 <= x < w and 0 <= y < h and mask[y][x]

    contour = [start]
    cur = start
    # 시작 방향: 왼쪽에서 진입했다고 가정
    bdir = 4
    first = True
    while True:
        found = False
        for k in range(8):
            d = (bdir + 1 + k) % 8
            dx, dy = nbrs[d]
            nx, ny = cur[0] + dx, cur[1] + dy
            if inside(nx, ny):
                # 되돌아온 방향 기준 재설정
                bdir = (d + 4 + 1) % 8
                cur = (nx, ny)
                found = True
                break
        if not found:
            break
        if cur == start and not first:
            break
        if not first:
            contour.append(cur)
        first = False
        if len(contour) > 4 * w * h:
            break
    return contour


def pixel_contour_to_path(contour):
    """픽셀 중심 경로를 픽셀 코너 기준 계단 폴리곤으로 변환하지 않고,
    픽셀 중심 + 0.5 오프셋으로 근사(RDP 로 어차피 완화됨)."""
    return [(x + 0.5, y + 0.5) for (x, y) in contour]


def rdp(points, eps):
    """Ramer-Douglas-Peucker 단순화 (닫힌 경로용: 시작/끝 고정)."""
    if len(points) < 3:
        return points

    def perp(p, a, b):
        (px, py), (ax, ay), (bx, by) = p, a, b
        dx, dy = bx - ax, by - ay
        if dx == 0 and dy == 0:
            return ((px - ax) ** 2 + (py - ay) ** 2) ** 0.5
        t = ((px - ax) * dx + (py - ay) * dy) / (dx * dx + dy * dy)
        t = max(0.0, min(1.0, t))
        cx, cy = ax + t * dx, ay + t * dy
        return ((px - cx) ** 2 + (py - cy) ** 2) ** 0.5

    stack = [(0, len(points) - 1)]
    keep = [False] * len(points)
    keep[0] = keep[-1] = True
    while stack:
        i, j = stack.pop()
        if j <= i + 1:
            continue
        dmax, idx = 0.0, i
        for k in range(i + 1, j):
            d = perp(points[k], points[i], points[j])
            if d > dmax:
                dmax, idx = d, k
        if dmax > eps:
            keep[idx] = True
            stack.append((i, idx))
            stack.append((idx, j))
    return [p for p, k in zip(points, keep) if k]


def path_d(pts, sx, sy, ox, oy, prec=2):
    def fmt(v):
        s = f'{v:.{prec}f}'.rstrip('0').rstrip('.')
        return s if s else '0'
    out = []
    for i, (x, y) in enumerate(pts):
        X = (x - ox) * sx
        Y = (y - oy) * sy
        out.append(('M' if i == 0 else 'L') + fmt(X) + ' ' + fmt(Y))
    out.append('Z')
    return ''.join(out)


def main():
    eps = float(sys.argv[1]) if len(sys.argv) > 1 else 1.2
    grid, w, h = load_binary(SRC)

    # 잉크 바운딩박스
    minx, miny, maxx, maxy = w, h, 0, 0
    for y in range(h):
        for x in range(w):
            if grid[y][x]:
                minx = min(minx, x); maxx = max(maxx, x)
                miny = min(miny, y); maxy = max(maxy, y)
    bw, bh = maxx - minx + 1, maxy - miny + 1

    # viewBox 를 정수 100 기준으로 정규화 (가로 100, 세로는 비율)
    VW = 100.0
    VH = round(VW * bh / bw, 2)
    sx = VW / bw
    sy = VH / bh

    # 잉크(1) 영역들
    _, ink_regions = label_regions(grid, w, h, 1)
    ink_regions = [r for r in ink_regions if len(r['pixels']) >= 12]

    # 배경(0) 영역 중 테두리에 닿지 않는 것 = 구멍
    _, bg_regions = label_regions(grid, w, h, 0)
    holes = [r for r in bg_regions if not r['border'] and len(r['pixels']) >= 12]

    paths = []
    for reg in ink_regions + holes:
        mask = [[False] * w for _ in range(h)]
        for (x, y) in reg['pixels']:
            mask[y][x] = True
        start = min(reg['pixels'], key=lambda p: (p[1], p[0]))
        c = trace_boundary(mask, w, h, start)
        if len(c) < 3:
            continue
        pts = rdp(pixel_contour_to_path(c), eps)
        if len(pts) < 3:
            continue
        paths.append(path_d(pts, sx, sy, minx, miny))

    d = ' '.join(paths)
    print(f'# viewBox 0 0 {VW:g} {VH:g}', file=sys.stderr)
    print(f'# ink_regions={len(ink_regions)} holes={len(holes)} '
          f'paths={len(paths)} chars={len(d)}', file=sys.stderr)
    sys.stdout.write(f'{VW:g}|{VH:g}|{d}')


if __name__ == '__main__':
    main()
