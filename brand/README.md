# OSiris 브랜드 자산 — 호루스의 눈 (Eye of Horus)

OSiris(오시리스)의 상징으로 **호루스의 눈**을 사용한다. 이집트 신화에서
오시리스는 호루스의 아버지이며, 호루스의 눈은 *보호·관측·회복*을 뜻한다 —
조직 전체를 관측하고 헬스체크하며 복구하는 이 프레임워크의 성격과 맞는다.

## 파일

| 파일 | 용도 |
|---|---|
| `source/eye-of-horus-original.png` | 원본 비트맵(650×500 RGBA). **지우지 말 것** — 모든 자산의 출처 |
| `traced-path.txt` | 원본을 벡터로 트레이스한 결과(`viewBox`·path 데이터). 빌드 입력 |
| `osiris-mark.svg` | **투명 배경 + `currentColor`** 마크. 부모의 글자색을 상속한다 — **인라인 `<svg>` 로 넣을 때만** 쓴다 (아래 주의 참고) |
| `osiris-mark-auto.svg` | **테마 자동** — SVG 자신이 `prefers-color-scheme` 을 평가해 다크=금 / 라이트=잉크. **`<img>` 로 넣는 자리의 기본 선택지** |
| `osiris-mark-gold.svg` | 금 `#E8B54B` 고정. 배경이 어둡다고 **확신**할 때만 |
| `osiris-mark-ink.svg` | 잉크 `#0f172a`(gray-900) 고정. 배경이 밝다고 확신할 때만 |
| `osiris-mark-512.png` | 위 마크의 래스터판(금색, 투명 배경) |
| `favicon.svg` | 배지형 파비콘(벡터). 최신 브라우저가 이걸 우선 사용한다 |
| `favicon.ico` | 멀티해상도 ICO — 16·32·48px **각각 개별 글리프** 포함 |
| `favicon-16/32/48/180/512.png` | 개별 크기 래스터. 180은 `apple-touch-icon` |
| `favicon-maskable-512.png` | PWA maskable(안전영역 규격). 런처가 모서리를 깎으므로 마크를 작게, 모서리는 플랫 |

## ⚠️ `currentColor` 마크를 `<img>` 로 넣지 말 것

**실제로 사고가 났던 지점이다.** `<img src="osiris-mark.svg">` 로 넣으면 SVG 가
별도 문서로 렌더되어 **페이지의 CSS 를 상속하지 않는다** — `currentColor` 가
기본값(검정)으로 굳어, 어두운 배경 위에서 마크가 사라진다.

| 넣는 방식 | 써야 할 파일 |
|---|---|
| 인라인 `<svg>` (또는 React 컴포넌트로 임포트) | `osiris-mark.svg` — 부모 `color` 를 상속 |
| `<img>` · `background-image` · 배경을 모르는 자리 | **`osiris-mark-auto.svg`** |
| 배경색을 코드에서 확정한 자리 | `osiris-mark-gold.svg` / `osiris-mark-ink.svg` |

## 색

| 역할 | 값 |
|---|---|
| 배지 배경 | `#0F1419` (근사 블랙 슬레이트) |
| 마크 (어두운 색면 위) | `#E8B54B` (이집트 금) |
| 마크 (밝은 캔버스 위) | `#0f172a` (gray-900) |

**금은 어두운 색면 위에서만 쓴다** — `#0F1419`(9.82:1) 또는 lapis-900
`#0F223E`(8.45:1). 흰 배경 위 금은 1.88:1 로 AA 에 크게 미달하므로 라이트
캔버스에는 절대 놓지 않는다. 근거와 실측값은
`docs/design/41-console-design-system.md` §2.1.2.

`theme-color` 메타태그도 `#0F1419` 을 쓴다.

## 설계 판단 (바꾸기 전에 읽을 것)

**1. 배지형(어두운 라운드 사각 + 금색 눈)을 파비콘으로 쓴다.**
원본은 검정 단색이라 투명 배경으로 두면 **어두운 브라우저 크롬에서 사라진다.**
배경을 고정한 배지는 밝은·어두운 크롬 양쪽에서 읽히므로, 파비콘에는
`prefers-color-scheme` 분기보다 이 방식이 안전하다. (`.ico`·`.png` 은 애초에
미디어쿼리를 지원하지 않는다.)

**2. 크기별로 디테일 단계를 둔다.**
원본 마크는 가로:세로 ≈ 1.37:1 로 넓어서 정사각 파비콘에 넣으면 여백이 크고,
16px 에서는 나선 꼬리와 얇은 눈썹이 뭉개져 판독 불가가 된다. 그래서:

- **16·32px** — 나선 꼬리를 프레임 밖으로 보내고(`CROP_SMALL = 74`) 눈·눈썹을
  더 크게 담는다. 실루엣이 정사각에 가까워져 같은 픽셀에서 훨씬 크게 들어간다.
- **48px 이상** — 나선까지 포함한 전체 마크.

크기가 다른 파비콘이 나란히 보이는 일은 없으므로 이 불일치는 문제되지 않는다.
파비콘의 표준 관행이다.

**3. `.ico` 는 직접 작성한다.**
Pillow 의 ICO 저장은 단일 원본을 축소해 여러 크기를 만들기 때문에 위의
크기별 글리프를 담을 수 없다(16px 이 뭉개진다). `build-brand.py` 의
`write_ico()` 가 ICO 컨테이너(ICONDIR + ICONDIRENTRY + PNG payload)를
직접 쓴다. PNG 임베드는 Windows Vista 이후 표준이다.

**4. 벡터 트레이싱을 자체 구현했다.**
이 호스트에 `potrace`·ImageMagick·Inkscape 가 없다. `trace-source.py` 가
경계추적(Moore neighborhood) + RDP 단순화로 폴리곤을 뽑는다.
원본 대비 **IoU 97.0%**, 정점 178개 — 육안으로 구분되지 않는 수준이다.
구멍(눈동자 옆 흰 부분)은 `fill-rule="evenodd"` 로 표현된다.

## 재생성

```bash
# 원본 PNG 가 바뀐 경우에만 (벡터 재트레이스)
python3 brand/trace-source.py 1.2 > brand/traced-path.txt

# 자산 재생성 (traced-path.txt 만 있으면 됨)
python3 brand/build-brand.py

# 게이트웨이가 서빙하는 사본 갱신
cp brand/favicon.ico brand/favicon.svg brand/favicon-180.png \
   brand/favicon-512.png gw/html/
bash gw/render-status.sh
```

`trace-source.py` 의 인자는 RDP 허용오차다. 값을 키우면 정점이 줄고 형태가
뭉툭해진다(1.2 가 충실도/크기 균형점).

## 서빙 경로

서빙 경로가 **두 갈래**다 — 헷갈리기 쉬우니 구분해 둔다.

**1. 최상위 브랜드 자산** — `brand/` 가 원본이고 `gw/html/` 로 **복사**되어
`osiris-gw` 가 바인드 마운트로 서빙한다(`/favicon.ico`, `/osiris-mark-auto.svg`
등). 게이트웨이는 이걸 **파일명 열거가 아니라 `favicon*`·`osiris-mark*` 두
접두사로 예약**한다 — 열거식이었을 때 나중에 추가한 마크 변형 3종이 목록에
없어 404 났던 사고가 실제로 있었다(`gw/conf.d/default.conf` 의 해당 주석 참고).
자산을 새로 추가할 때 이 두 접두사를 벗어나는 이름을 쓰면 다시 깨진다.

`gw/html/pre-status.html` 은 `gw/render-status.sh` 가 생성하므로 그 페이지의
favicon `<link>` 태그는 **스크립트의 템플릿** 안에 있다 — 생성된 HTML 을 직접
고치지 말 것.

**2. 콘솔 SPA 쪽** — `console/public/brand/` 가 vite 빌드를 거쳐 이미지 레이어에
함께 구워지고(`gw/Dockerfile`), `/brand/` 접두사로 서빙된다. 여기 있는
`osiris-mark.svg` 는 `currentColor` 판이다 — 콘솔은 마크를 **인라인 컴포넌트로**
넣으므로 색 상속이 동작한다(위 `<img>` 경고가 적용되지 않는 경우).

## 인계 사항

- 콘솔이 PWA 매니페스트를 갖게 되면 `favicon-maskable-512.png` 을
  `purpose: "maskable"` 로 등록할 것. 현재는 매니페스트가 없다.
