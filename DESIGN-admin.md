# DESIGN-admin.md — 원본 앱 관리자 콘솔 디자인 시스템 스펙

> **목적**: `/admin` SPA(admin-frontend) 비주얼 전면개편의 기준 문서.
> **적용범위**: `admin-frontend/` 관리자 콘솔 전체 (~15개 페이지). 앱 프론트(`frontend/`)·랜딩(`landing/`)·구 어드민(`/admin-legacy`)은 대상 아님.
> **전제**: React 18.3 + Vite 5 + **antd 5.21** + @tanstack/react-query + recharts 3. **새 스타일링 툴체인(Tailwind 등) 도입 없음** — antd `ConfigProvider theme` + CSS 변수 이관이 기본 경로.
> **레퍼런스**: `_tmp/ref/admin/` PNG 8장 (QueryMind 어드민 콘솔, 라이트 테마 기반). 관찰 요약은 [부록 A](#부록-a--레퍼런스-이미지-관찰-요약).

### 현행 대비 핵심 변경 요약

| # | 항목 | 현행 | 개편 |
|---|---|---|---|
| 1 | **사이드바** | 다크 네이비 `#111b2d` (앱과 이질적 톤) | **라이트(백색) 사이드바** + 틴트 선택 pill — 레퍼런스 8장 공통 골격 |
| 2 | **토큰 소스** | `App.tsx` 인라인 theme 객체 + `admin.css` 리터럴 hex ~20곳 **이원 하드코딩** | `src/theme/tokens.ts` **단일 소스** → ConfigProvider + CSS 변수(`cssVar: true`) 동시 공급 |
| 3 | **공용 컴포넌트** | 4개 (Layout·StatusTag·모달 2) — 테이블/KPI 보일러플레이트 페이지마다 반복 | `AdminTable`·`StatCard`·`PageHeader`·`FilterBar` 추출, StatusTag 틴트 스타일 확장 |
| 4 | **브랜드색** | teal `#0f8f8b` (유지) | **teal 유지** + 10단계 스케일화, 틴트(연한 배경색) 체계 도입 |
| 5 | **다크모드** | 없음 (사이드바만 다크) | **라이트 우선**, 토큰 통합 후 antd `darkAlgorithm` 확장 여지 확보 |

---

## 1. 디자인 원칙 (레퍼런스에서 도출)

레퍼런스 8장의 공통 디자인 언어를 다음 5개 원칙으로 압축한다.

1. **밝고 낮은 대비의 캔버스, 색은 의미에만.** 배경은 아주 연한 블루그레이, 카드는 백색 + 헤어라인 보더. 채도 높은 색(브랜드·시맨틱)은 버튼/배지/차트/선택 상태에만 등장한다. 장식적 색 사용 금지.
2. **틴트(tint)로 상태를 말한다.** 선택된 메뉴, 상태 pill, 아이콘 타일, 콜아웃 — 전부 "본색의 8~12% 연한 배경 + 본색 텍스트" 패턴. 진한 단색 채움은 primary 버튼과 브랜드 마크 정도로 제한.
3. **보더 우선, 그림자는 보조.** 구획은 1px 헤어라인 보더가 만들고, 그림자는 거의 없거나(카드) 오버레이(드롭다운·모달)에만 뚜렷하게.
4. **넉넉한 밀도.** 행 높이 52~60px, 카드 패딩 20~24px, 섹션 간 24~32px. 관리자 도구지만 빽빽한 엑셀형이 아니라 SaaS 대시보드형 밀도.
5. **위계는 크기·굵기·회색 단계로.** 페이지 타이틀(20/700) → 카드 타이틀(15/600) → 본문(13~14/400) → 캡션(12, gray-500) → 오버라인(11/700 대문자). 색으로 위계를 만들지 않는다.

---

## 2. 디자인 토큰

토큰은 **`src/theme/tokens.ts` 단일 소스**로 정의하고, 두 경로로 소비한다:
- **antd**: `ConfigProvider theme={{ token, components, cssVar: true }}` — antd 컴포넌트 전부.
- **CSS**: antd `cssVar: true`가 노출하는 `--ant-*` 변수 + 아래 표의 `--admin-*` 커스텀 변수(antd 토큰에 대응 없는 것만: 사이드바·차트·틴트) — `admin.css` 및 recharts.

> 규칙: **antd 토큰 키가 존재하면 그것이 정본**이고 CSS에서는 `var(--ant-…)`를 참조한다. `--admin-*`는 antd가 커버하지 못하는 개념에만 만든다. 같은 값을 두 이름으로 중복 정의하지 않는다.

### 2.1 컬러 — 브랜드 (teal)

현행 `#0f8f8b`을 600으로 앵커링한 10단계 스케일. (근거·대안은 §3)

| 토큰 | 값 | antd 키 / CSS 변수 | 용도 |
|---|---|---|---|
| teal-50 | `#effbfa` | `--admin-teal-50` | 선택 pill 배경, hover 틴트, 콜아웃 배경 |
| teal-100 | `#d7f4f2` | `--admin-teal-100` | 아이콘 타일 배경, 아바타 배경(현행 `#d8f3f1` 대체) |
| teal-200 | `#b0e8e5` | `--admin-teal-200` | 선택 pill 보더, 포커스 링 보조 |
| teal-300 | `#7fd6d1` | `--admin-teal-300` | 차트 보조, 다크모드 primary 후보 |
| teal-400 | `#45bcb7` | `--admin-teal-400` | 그래프 강조, hover 상태 |
| teal-500 | `#16a29d` | `--admin-teal-500` | `colorPrimaryHover` 근사(현행 브랜드마크 `#13a6a2` 흡수) |
| **teal-600** | **`#0f8f8b`** | **`colorPrimary`** → `--ant-color-primary` | **primary 버튼·선택 상태·링크 강조 (정본)** |
| teal-700 | `#0d7672` | `colorLink`(현행 `#0f766e` 대체) | 링크, primary active |
| teal-800 | `#0f5e5b` | `--admin-teal-800` | 진한 강조 텍스트 |
| teal-900 | `#114b49` | `--admin-teal-900` | 다크 배경 위 잔상용(로그인 그라데이션 등) |

### 2.2 컬러 — 뉴트럴 (slate 계열, 현행 admin.css 값과 연속성 유지)

| 토큰 | 값 | antd 키 / CSS 변수 | 용도 |
|---|---|---|---|
| white | `#ffffff` | `colorBgContainer` | 카드·사이드바·탑바·테이블 |
| gray-50 | `#f8fafc` | `--admin-gray-50` | 테이블 헤더, Descriptions 라벨 배경 |
| layout-bg | `#f5f7fa` | `colorBgLayout` | 컨텐츠 영역 배경 (현행 유지) |
| gray-100 | `#f1f5f9` | `--admin-gray-100` | filled input 배경, 비활성 배경 |
| gray-200 | `#e2e8f0` | `--admin-gray-200` | 분리선 진한 단계 |
| border | `#e3e8ef` | `colorBorder`(현행 `#dfe5ec` 대체 — admin.css 카드 보더와 통일) | 카드·테이블·입력 보더 |
| border-light | `#edf0f4` | `colorBorderSecondary` | 테이블 행 구분선, 카드 헤더 하단선 |
| gray-300 | `#cbd5e1` | `--admin-gray-300` | 비활성 아이콘 |
| gray-400 | `#94a3b8` | `colorTextQuaternary` | placeholder, 비활성 텍스트 |
| gray-500 | `#64748b` | `colorTextTertiary` | 캡션·보조 텍스트·테이블 헤더 텍스트 |
| gray-600 | `#475569` | `colorTextSecondary` | 서브타이틀 |
| gray-700 | `#334155` | `--admin-gray-700` | 테이블 본문 텍스트 |
| text | `#172033` | `colorText` | 기본 텍스트 (현행 유지) |
| gray-900 | `#0f172a` | `--admin-gray-900` | 헤드라인, KPI 숫자 |

### 2.3 컬러 — 시맨틱 (본색 + 틴트 쌍)

레퍼런스의 상태 표현은 항상 "틴트 배경 + 본색 텍스트" pill. 본색과 배경틴트를 쌍으로 정의한다.

| 의미 | 본색 | 틴트 배경 | antd 키 | CSS 변수 |
|---|---|---|---|---|
| success | `#16a34a` | `#e8f7ee` | `colorSuccess` | `--admin-success` / `--admin-success-bg` |
| warning | `#d97706` | `#fdf3e3` | `colorWarning` | `--admin-warning` / `--admin-warning-bg` |
| error | `#dc2626` | `#fdecec` | `colorError` | `--admin-error` / `--admin-error-bg` |
| info | `#2563eb` | `#e9f1fe` | `colorInfo`(현행 teal 겸용에서 **분리** — info는 파랑) | `--admin-info` / `--admin-info-bg` |
| neutral(기각·완료 등 종결) | `#64748b` | `#f1f5f9` | — | `--admin-neutral` / `--admin-neutral-bg` |

> 현행 `colorInfo: '#0f8f8b'`(primary와 동일)은 info 시맨틱을 죽이는 설정 — 파랑으로 분리한다. Alert/Message의 info 표현이 브랜드색과 섞이지 않게 된다.

### 2.4 컬러 — 차트 팔레트 (recharts 정합)

레퍼런스 도넛/바 차트의 블루·퍼플·그린·골드 4색 축을 따르되, 브랜드 teal을 시리즈에 편입한다. `DashboardPage.tsx` 로컬 `SERIES` 상수를 이 토큰으로 대체.

| 토큰 | 값 | CSS 변수 | 현행 SERIES 매핑 |
|---|---|---|---|
| chart-1 | `#4f7df0` (blue) | `--admin-chart-1` | `newUsers #2a78d6` → 대체 |
| chart-2 | `#16a29d` (teal-500) | `--admin-chart-2` | `tickets #199e70` → 대체 |
| chart-3 | `#a06ee8` (purple) | `--admin-chart-3` | (신규) |
| chart-4 | `#d9a13b` (gold) | `--admin-chart-4` | `reports #c98500` → 대체 |
| chart-5 | `#2bbd8e` (green) | `--admin-chart-5` | `newListings #008300` → 대체 |
| chart-6 | `#e0637f` (rose) | `--admin-chart-6` | `trades #d55181` → 대체 |
| chart-grid | `#eceff3` | `--admin-chart-grid` | `CartesianGrid #e1e0d9` → 대체 (베이지끼 제거) |

규칙: 라인/바 stroke 2px, dot 없음(현행 유지), 그리드는 수평선만(`vertical={false}` 유지), 범례는 차트 하단 점+라벨.

### 2.5 타이포그래피

폰트 체인 유지: `Inter, Pretendard, "Noto Sans KR", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`.
**웹폰트가 현재 로드되지 않음(시스템 폴백 의존)** — 마이그레이션 시 Pretendard Variable woff2 self-host를 권장(§7 Phase 0). 숫자 컬럼·KPI에는 `font-variant-numeric: tabular-nums`.

| 토큰 | 크기/굵기/행간 | antd 키 / CSS 변수 | 용도 |
|---|---|---|---|
| kpi | 28px / 700 / 1.2 | `--admin-font-kpi` | StatCard 수치 (레퍼런스는 36~40이나 우리 KPI 6종 그리드엔 28 적정) |
| h1 | 20px / 700 / 1.3 | `fontSizeHeading4=20` | 페이지 타이틀 (현행 21px에서 정돈) |
| h2 | 16px / 650 / 1.4 | `fontSizeHeading5=16` | 섹션 타이틀 |
| card-title | 15px / 600 / 1.4 | `--admin-font-card-title` | 카드 헤더 |
| body | 14px / 400 / 1.55 | `fontSize=14` | 기본 본문·폼 |
| body-sm | 13px / 400 / 1.5 | `--admin-font-sm` | 테이블 셀 (현행 유지) |
| caption | 12px / 400 / 1.4 | `--admin-font-caption` | 보조 설명·타임스탬프 |
| overline | 11px / 700 / 1.2, letter-spacing .06em, uppercase | `--admin-font-overline` | 사이드바 그룹라벨, 테이블 헤더 |

### 2.6 Spacing (4px 기수)

`--admin-space-1..8` = 4 / 8 / 12 / 16 / 20 / 24 / 32 / 40.
관례: 카드 패딩 20(현행 유지), 카드 간 간격 16, 섹션 간 24~32, 페이지 좌우 패딩 38(현행 유지), 필터바-테이블 간 16.

### 2.7 Radius

| 토큰 | 값 | antd 키 / CSS 변수 | 용도 |
|---|---|---|---|
| radius-sm | 6px | `borderRadiusSM` | Tag(사각형 변형), 코드칩 |
| radius-md | 8px | `--admin-radius-md` | 버튼·입력(현행 admin.css 8px 유지) |
| radius-lg | 10px | `borderRadius=10` | antd 기본(현행 유지), Select·모달 내부 |
| radius-xl | 12px | `--admin-radius-xl` | 카드·테이블 래퍼(현행 유지), 모달 |
| radius-pill | 999px | `--admin-radius-pill` | 상태 pill·필터 칩(현행 Tag 999 유지) |

### 2.8 Elevation

| 토큰 | 값 | CSS 변수 | 용도 |
|---|---|---|---|
| e0 | `none` (보더만) | — | 카드·테이블 기본 (현행 `box-shadow:none` 철학 유지 — 레퍼런스도 보더 우선) |
| e1 | `0 1px 2px rgba(15,23,42,.04)` | `--admin-shadow-1` | hover 카드, sticky 헤더 이탈 시 |
| e2 | `0 4px 16px rgba(15,23,42,.08)` | `--admin-shadow-2` / `boxShadowSecondary` | 드롭다운·팝오버 |
| e3 | `0 16px 40px rgba(15,23,42,.14)` | `--admin-shadow-3` / modal | 모달·드로어 |

### 2.9 z-index

antd 기본(popup 1000+)을 존중하고, 레이아웃 자체 것만 정의: 사이드바 `--admin-z-sider: 100`, 탑바 `--admin-z-topbar: 110`. 그 외 커스텀 z-index 신설 금지.

### 2.10 ConfigProvider 목표 형상 (참고 코드)

```ts
// src/theme/tokens.ts — 단일 소스 (발췌)
export const adminTheme: ThemeConfig = {
  cssVar: true,                       // --ant-* CSS 변수 노출 (antd 5.12+)
  token: {
    colorPrimary: '#0f8f8b',
    colorLink: '#0d7672',
    colorInfo: '#2563eb',
    colorSuccess: '#16a34a',
    colorWarning: '#d97706',
    colorError: '#dc2626',
    colorBgLayout: '#f5f7fa',
    colorBorder: '#e3e8ef',
    colorBorderSecondary: '#edf0f4',
    colorText: '#172033',
    colorTextSecondary: '#475569',
    colorTextTertiary: '#64748b',
    borderRadius: 10,
    fontSize: 14,
    fontFamily: `Inter, Pretendard, "Noto Sans KR", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif`,
  },
  components: {
    Layout: { siderBg: '#ffffff', headerBg: '#ffffff' },   // ← 다크 사이드바 폐기
    Menu: { itemBorderRadius: 8, itemSelectedBg: '#effbfa', itemSelectedColor: '#0f8f8b' },
    Table: { headerBg: '#f8fafc', headerColor: '#64748b', rowHoverBg: '#f0fafa' },
    Card: { borderRadiusLG: 12 },
    Button: { borderRadius: 8, fontWeight: 600 },
    Tag: { borderRadiusSM: 999 },
  },
}
```

---

## 3. 브랜드 컬러 결정 — teal 유지 (권장) vs 레퍼런스 블루 (대안)

**권장: 현행 teal `#0f8f8b` 유지.**

- 레퍼런스의 스카이블루(≈`#1e9ce6`)는 *그 제품(QueryMind)의 브랜드색*이지 디자인 언어의 본질이 아니다. 8장에서 이식할 가치가 있는 것은 **톤 운용 체계**(라이트 사이드바, 틴트 선택 상태, pill 배지, 보더 우선 카드)이며 이는 hue와 무관하게 성립한다.
- teal은 원본 앱 브랜드와의 연속성(브랜드마크 `#13a6a2`)을 유지하고, 관리자 콘솔임을 앱과 동일한 정체성으로 표시한다.
- teal-600은 백색 위 대비 4.6:1로 AA 충족 — 접근성 문제 없음.
- 단, 현행처럼 `colorInfo`까지 teal로 쓰는 겸용은 폐기한다(§2.3). teal은 브랜드/인터랙션, 파랑은 정보 시맨틱.

**대안 (비권장)**: 레퍼런스 그대로 스카이블루 `#1e9ce6`(600 `#1483c9`) 채택. 레퍼런스 재현 충실도는 높지만 앱 브랜드와 단절되고, 기존 관리자 사용자의 색 학습(teal=우리 도구)을 버리는 비용이 있다. 채택 시 §2.1 스케일만 블루 기준으로 치환하면 나머지 스펙은 동일하게 적용된다.

---

## 4. 라이트/다크 결정

- **라이트 우선.** 레퍼런스 8장 전부 라이트 기반(다크는 토글로 존재). 개편 1차 목표는 라이트 단일 테마의 완성도.
- **다크는 확장 여지만 확보**: 토큰 단일화(Phase 0)가 끝나면 antd `theme.algorithm: darkAlgorithm` + `--admin-*` 다크 오버라이드 한 겹으로 도달 가능하다. 지금 다크 팔레트를 미리 확정하지 않는다(과설계 금지) — 단, **admin.css의 하드코딩 hex를 전부 변수화하는 것이 다크의 전제조건**임을 마이그레이션 항목으로 못박는다.
- 현행 "사이드바만 다크" 하이브리드는 폐기 — 레퍼런스 공통 골격(전면 라이트)으로 통일한다. 로그인 페이지의 다크 그라데이션 배경은 별개 화면이므로 유지 가능(teal-900 계열로 토큰화만).

---

## 5. 레이아웃 스펙

### 5.1 사이드바 (AdminLayout.tsx `Sider`)
*레퍼런스 근거: 8장 공통 — 백색 사이드바, 그룹라벨, 틴트 선택 pill, 하단 계정 카드.*

| 속성 | 스펙 |
|---|---|
| 배경/보더 | `#ffffff`, 우측 1px `colorBorder`. sticky 100vh (현행 유지) |
| 폭 | 256px (현행 유지, 레퍼런스 ≈268px과 동급) |
| 브랜드 영역 | 높이 64px(탑바와 정렬). 브랜드 마크: 32px 라운드 사각(`radius 10`) teal-600 채움 + 백색 글리프. 텍스트 `#172033` 15/700 + 캡션 gray-500 |
| 그룹 라벨 | overline 토큰(11/700 uppercase), `gray-400`. 현행 5그룹(OVERVIEW / TRUST & SAFETY / CUSTOMER CARE / CONTENT & POLICY / 동네지도) + root 전용 SYSTEM 유지 |
| 메뉴 아이템 | 높이 40px, radius 8, 기본 `gray-600` + 아이콘 `gray-400`. hover: `gray-100` 배경. **선택: teal-50 배경 + teal-600 텍스트/아이콘 + 600 굵기** (다크 pill `#1e4d59` 폐기) |
| 하단 | 계정 카드(아바타 teal-100 배경 + teal-700 이니셜, 이름 13/600, 역할 캡션) + 로그아웃. 레퍼런스의 ⌘K 퀵서치는 **도입하지 않음**(페이지 15개 규모에 과함 — 필요 시 후속) |
| 반응형 | `<900px` 숨김 (현행 유지) |

antd 매핑: `Menu theme="dark"` → `theme="light"` + `components.Menu` 토큰. **커스터마이즈 필요**: 그룹라벨 타이포는 antd 토큰만으로 부족 — admin.css에서 `.ant-menu-item-group-title` 오버라이드 유지(변수 참조로 전환).

### 5.2 탑바 (Header)
*레퍼런스 근거: #1·#2 — 백색, 하단 헤어라인, 좌 타이틀 / 우 상태·유틸.*

- 높이 76px → **64px** (레퍼런스 ≈56~64px, 현행이 과대).
- 배경 `#ffffff`(현행 `rgba(255,255,255,.94)` 반투명 폐기 — sticky 아님), 하단 1px `colorBorder`.
- 좌: 브레드크럼(12px, gray-500) + 페이지 타이틀은 탑바가 아닌 **컨텐츠 내 PageHeader로 이동**(레퍼런스 패턴 — 탑바에는 현재 위치 라벨만 14/600).
- 우: 계정 표시(아바타+이름) + 필요 시 환경 배지. 레퍼런스의 read-only pill·데이터소스 선택기·알림벨은 해당 기능이 없으므로 **도입하지 않음**.

### 5.3 컨텐츠 + 페이지 헤더 (공용 `PageHeader` 신설)
*레퍼런스 근거: #2·#5 — 컨텐츠 상단에 타이틀(굵게)+서브텍스트, 우측 액션 버튼 무리.*

- 컨텐츠: max-width 1500 중앙 정렬, 패딩 28px 38px 44px (현행 유지).
- `PageHeader` props: `title`, `description?`, `extra?`(우측 액션). 타이틀 h1(20/700), description 캡션(gray-500), 하단 마진 20px. 모든 목록/상세 페이지 최상단에 배치해 현행 페이지별 제각각인 타이틀 마크업을 통일.

---

## 6. 컴포넌트 스펙

### 6.1 데이터 테이블 — 공용 `AdminTable` 추출 (권장)
*레퍼런스 근거: #7 사용자 목록 — 카드형 래퍼, 연회색 헤더, 60px 행, 행 구분선만, 우측 행 액션 아이콘.*

현행: 목록 페이지 ~10곳이 antd `Table` 컬럼/페이지네이션/로딩을 개별 작성. **`AdminTable<T>` 래퍼로 추출**한다.

| 속성 | 스펙 |
|---|---|
| 래퍼 | 백색 카드, 보더 `colorBorder`, radius 12, overflow hidden (현행 css 유지) |
| 헤더 행 | 높이 46px, 배경 gray-50, 텍스트 overline(11/700 uppercase) gray-500 (현행 유지 — 레퍼런스와 이미 일치) |
| 본문 행 | 높이 52px, 텍스트 13px gray-700, 구분선 `colorBorderSecondary`. hover `#f0fafa`(teal 틴트, 현행 유지) |
| 행 액션 | 우측 정렬 아이콘 버튼(`Button type="text" size="small"`) 그룹 — 레퍼런스 #7의 행말 아이콘 3종 패턴. 파괴적 액션은 아이콘 error색 |
| 페이지네이션 | 우하단, margin 16px 20px (현행 유지) |
| 기본 내장 | `loading`(스켈레톤 행 5개), 빈 상태(§6.9), 페이지 사이즈 20 기본, `rowKey` 필수 prop |

레퍼런스 #7의 아바타 셀(파스텔 배경 원형 + 이니셜)은 유저 목록에 적용: 배경 teal-100/chart 틴트 로테이션, 이니셜 700.

### 6.2 필터바 — 공용 `FilterBar` 추출
*레퍼런스 근거: #5 인사이트 피드 — 라벨 + pill 칩 토글 필터열 / #3·#7 — 검색 인풋 + 액션 버튼 행.*

- 테이블 위 한 줄: 좌측 검색 `Input`(prefix 돋보기, **`variant="filled"`** — 레퍼런스의 회색 filled 인풋, antd 5 네이티브 지원) + 상태 필터, 우측 primary 액션.
- 상태 필터는 두 형태 허용: 값 4개 이하 → **pill 칩 토글**(`Tag.CheckableTag` 기반: 기본 백색+보더, 선택 teal-50 배경+teal-600 텍스트 — **antd 커스터마이즈 필요**: CheckableTag 선택색이 기본 solid primary라 admin.css 오버라이드 1건), 5개 이상 → `Select`.
- 높이 36px 통일, 요소 간 8px, 필터바-테이블 간 16px.

### 6.3 통계 KPI — 공용 `StatCard` 추출
*레퍼런스 근거: #2 — 백색 카드 내 라벨(소형 gray) + 대형 숫자, 인접 카드 그리드.*

현행 대시보드의 `Card`+`Statistic`+로컬 `PairStat`을 대체하는 `StatCard`:

| 속성 | 스펙 |
|---|---|
| 구조 | 라벨(12/600 gray-500) → 수치(kpi 토큰 28/700 gray-900, tabular-nums) → 보조행(캡션: "오늘 n · 7일 n" 등 PairStat 의미 흡수) |
| 카드 | 백색, 보더, radius 12, 패딩 20, 그리드 등고(현행 `height:100%` 유지) |
| 강조 변형 | `tone?: 'default' | 'warning' | 'error'` — 처리 대기 신고/문의 등 우선순위 카드는 현행 상단 3px 컬러 보더(`#e15a47`·`#d49429`) 대신 **좌측 아이콘 타일**(radius 10, 시맨틱 틴트 배경 + 본색 아이콘 — 레퍼런스 #5의 파스텔 아이콘 타일 패턴)로 교체 |
| 클릭 | 우선순위 카드는 클릭 시 해당 목록 이동(현행 동작 유지), hover에 e1 그림자 |

### 6.4 차트 (recharts)
*레퍼런스 근거: #2 — 도넛+하단 범례리스트, 수평 바, 카드 내 차트.*

- 팔레트는 §2.4 토큰만 사용. `SERIES`/`ALERT_RED`/`ALERT_ORANGE` 로컬 상수를 `theme/tokens.ts` export로 대체 (ALERT 계열은 시맨틱 error/warning 토큰 사용).
- 라인차트(현행 대시보드): stroke 2px, dot false, 그리드 `chart-grid` 수평선만, 축 라벨 12px gray-500, 툴팁은 백색 카드 + e2 그림자 + radius 10.
- 카드 헤더에 차트 제목(card-title 토큰) — 레퍼런스 #2의 "질문형 제목 + … 메뉴" 중 … 오버플로 메뉴는 기능이 없으므로 도입하지 않음.

### 6.5 상태 배지/태그 — `StatusTag` 확장
*레퍼런스 근거: #5·#7·#8 — 전부 "틴트 배경 + 본색 텍스트" pill. antd 프리셋 Tag의 보더형과 다름.*

- 현행 `StatusTag`(kind: report/user/listing/support)의 **매핑 구조는 유지**, 스타일만 틴트 pill로: radius 999(현행), 배경 `--admin-{semantic}-bg`, 텍스트 본색, **보더 없음**, 11/600, 패딩 2px 10px.
- 색 재매핑(시맨틱 토큰 기준): 대기/접수 → warning, 검토중/처리중/예약중 → info, 처리완료/정상/판매중/해결 → success, 정지/숨김 → warning, 영구정지/삭제됨 → error, 기각/판매완료 → neutral.
- **antd 커스터마이즈 필요**: antd `Tag color` 프리셋은 보더가 남는다 — StatusTag 내부에서 프리셋 문자열 대신 `style`(또는 클래스)로 틴트 쌍을 직접 지정하는 방식으로 전환. kind 추가(POI 제보심사 상태 등)는 매핑 테이블 확장으로 흡수.

### 6.6 버튼 위계
*레퍼런스 근거: #2·#3 — primary solid + 아이콘, 옆에 백색 보더 secondary. #4 — 풀폭 primary.*

| 위계 | antd | 스펙 |
|---|---|---|
| Primary | `type="primary"` | teal-600 채움, 백색 텍스트, radius 8, 600 굵기, 높이 36(기본)/40(폼 제출). 페이지당 1~2개 원칙 |
| Secondary | `type="default"` | 백색 + `colorBorder`, gray-700 텍스트. 아이콘 병용 가능 |
| Tertiary | `type="text"` | 행 액션·카드 헤더 액션. hover gray-100 |
| Link | `type="link"` | teal-700. "상세 →" 류 인라인 이동 (레퍼런스 #2 "상세 분석 →") |
| Danger | `danger` | 파괴적 확정 액션(제재·삭제)에만. 모달 confirm과 병용 |

### 6.7 모달·드로어
*레퍼런스 근거: 직접 노출 없음 — 카드·콜아웃 스타일에서 외삽.*

- 모달(`ModerateModal`·`SanctionModal` 등): radius 12, 패딩 24, 타이틀 16/650, e3 그림자, footer 우측 정렬(Secondary + Primary/Danger). 파괴적 액션 모달은 상단에 error 틴트 콜아웃(아이콘+요약)으로 결과 고지 — 레퍼런스 #4 콜아웃 패턴 적용.
- 상세 빠른 열람(신고 상세의 DmViewer 등 폭이 필요한 보조 문맥)은 우측 `Drawer`(폭 480~640) 사용 가능 — 신규 강제 아님, 스타일 토큰만 모달과 동일.

### 6.8 폼·필터
*레퍼런스 근거: #4 온보딩 폼 — 라벨 상단, filled 회색 인풋, 정보 콜아웃, 풀폭 제출.*

- 라벨 상단 배치(13/600 gray-700), 필드 간 16px. `Form layout="vertical"` 표준화.
- 입력: `variant="filled"`(gray-100 배경, 보더 없음, focus 시 teal 링) 또는 outlined 중 **페이지 단위 통일** — 작성/수정 폼(공지·POI)은 filled, 테이블 인라인·필터는 filled 소형. 혼용 금지.
- 안내 콜아웃: `Alert` → info 틴트 배경 + 본색 아이콘 + radius 10, 보더 없음 (**antd 커스터마이즈 필요**: 기본 Alert 보더 제거 오버라이드 1건).
- 제출: 폼 하단 우측 정렬(모달) 또는 풀폭 primary(단일 목적 폼 — 레퍼런스 #4).

### 6.9 빈 상태 / 로딩 / 에러

- **빈 상태**: 중앙 정렬, 아이콘 타일(48px, gray-100 배경 + gray-400 아이콘) + 한 줄 설명(gray-500) + 필요 시 primary 액션. antd `Empty` 커스텀 image 대신 이 패턴으로 `AdminTable`·목록 카드에 내장.
- **로딩**: 목록 = 스켈레톤 행(antd `Skeleton` active), 전면 = 중앙 `Spin`(현행 AuthGate 패턴 유지), 버튼 = `loading` prop.
- **에러**: 쿼리 실패 시 error 틴트 콜아웃 + "다시 시도" secondary 버튼. 토스트(`message.error`)는 뮤테이션 실패에만.

### 6.10 아이콘

`@ant-design/icons` 유지(신규 의존성 없음). **레퍼런스와의 편차 명시**: 레퍼런스는 lucide류 1.5px 스트로크 라인 아이콘으로 더 가늘고 균질하다 — antd outlined 아이콘은 이보다 굵지만, 아이콘 교체는 전 페이지 수정을 수반하므로 개편 범위에서 제외하고 "후속 검토(lucide-react 치환)"로만 남긴다. 크기 표준: 메뉴 16, 행 액션 16, 아이콘 타일 내 20.

---

## 7. 마이그레이션 로드맵 (이번에 코드는 건드리지 않음 — 실행 시 이 순서로)

### Phase 0 — 토큰 단일화 (모든 것의 전제)
1. `src/theme/tokens.ts` 신설: §2 전체를 TS 상수 + `ThemeConfig`로 정의. `App.tsx:61` 인라인 theme 객체를 이 파일 import로 교체, `cssVar: true` 활성화.
2. `admin.css`의 리터럴 hex(~20곳)를 `var(--ant-*)` / `var(--admin-*)`로 전환. `--admin-*`는 `:root` 블록 하나로 tokens.ts와 값 일치시켜 수기 정의(빌드 파이프라인 신설 금지 — 26줄 CSS에 코드젠은 과설계).
3. `DashboardPage.tsx`의 `SERIES`·`ALERT_*` 상수를 tokens.ts export로 이동.
4. (선택) Pretendard Variable woff2 self-host — `index.html` preload 1건.
   - **검증**: 시각 회귀 없이 빌드 통과 — 이 단계는 값 이동만, 색 변경 없음.

### Phase 1 — 레이아웃 리스킨 (첫 시각 변화)
1. `AdminLayout.tsx`: Sider 라이트 전환(§5.1), 탑바 64px(§5.2), `Menu theme="light"`.
2. `PageHeader` 컴포넌트 신설, 전 페이지 상단 적용.
   - **검증**: 전 페이지 스크린샷 순회 — 다크 잔재(`#111b2d`·`#1e4d59`·`#223047` 등) grep 0건.

### Phase 2 — 공용 컴포넌트 추출
1. `AdminTable` → 목록 페이지 중 2곳(신고·유저)에 먼저 적용해 API 확정 후 나머지 확산.
2. `StatCard` + `FilterBar` → 대시보드·목록 상단.
3. `StatusTag` 틴트 스타일 전환(§6.5) — 매핑 테이블은 무변경.
   - **검증**: 페이지별 antd `Table` 직접 사용 잔존 여부 grep, 대시보드 `PairStat` 제거.

### Phase 3 — 페이지별 적용 우선순위
1. **대시보드** (노출 빈도 최고, StatCard·차트 토큰의 쇼케이스)
2. **목록 공통 4종**: 신고센터 → 유저 → 매물 → 고객센터 (AdminTable 확산)
3. 상세 페이지(신고/유저/매물/고객센터 상세, Descriptions·모달 스타일)
4. CMS(공지·FAQ·금칙어)·감사로그·지도(POI·제보심사 3종) — 폼 filled 표준 적용
   - **검증**: 단계마다 어드민배포(`docker compose --env-file .env up --build -d admin_frontend`) 후 실화면 확인.

### Phase 4 — (후속, 별도 결정) 다크모드
`darkAlgorithm` + `--admin-*` 다크 오버라이드. Phase 0 완료가 선행조건. 이번 개편 범위 아님.

---

## 부록 A — 레퍼런스 이미지 관찰 요약

| # | 파일 (orca-paste-…) | 관찰 |
|---|---|---|
| 1 | `…478467` (Ask 홈) | 백색 사이드바 + 틴트 선택 pill·그룹라벨·하단 ⌘K 검색/계정 카드의 골격 확립. 중앙 히어로형 입력 카드, 회색 pill 예시 칩, 최근 항목 리스트(아이콘+텍스트+우측 타임스탬프). 탑바 우측에 상태 pill(read-only)·다크모드 토글·알림. |
| 2 | `…558764` (대시보드) | 페이지 헤더(타이틀+캡션 / 우측 새로고침·primary 버튼). 풀폭 인사이트 배너(3분할, 아이콘+설명+"상세 분석 →" 링크). 3열 차트 카드 그리드 — 도넛+하단 점 범례(값 우정렬), 대형 KPI 숫자, 수평 퍼플 바차트. 차트 팔레트 블루/퍼플/그린/골드. |
| 3 | `…599762` (저장된 질의) | 좌측 보조 패널(폴더+카운트) + 카드 그리드. 카드: 제목/출처 캡션/태그 칩/날짜, 하단 3등분 아이콘 액션바, 우상단 pill 버튼. filled 검색 인풋 + primary "새 저장". |
| 4 | `…614072` (온보딩 설정) | 중앙 정렬 원형 스테퍼(활성=브랜드 아웃라인). 폼 카드: 상단 라벨 + filled 회색 인풋(보더리스, radius ~10), info 틴트 콜아웃(아이콘+본문), 풀폭 primary 버튼, 성공 틴트 콜아웃 내 중첩 버튼. |
| 5 | `…649497` (인사이트 피드) | 라벨+pill 칩 토글 필터열(선택=틴트 배경). 리스트형 풀폭 카드: 파스텔 아이콘 타일(핑크/블루/오렌지) + 제목 + 아웃라인 타입 배지 + 심각도 점 + 우측 날짜/셰브런. 모노스페이스 코드 칩(회색 배경). |
| 6 | `…661036` (설정 일반·표시) | 2단 설정 내비(아이콘+제목+캡션, 선택=틴트 배경+우측 인디케이터 바). 섹션 오버라인 라벨. 설정 행: 회색 아이콘 타일 + 제목/캡션 + 우측 컨트롤(세그먼트 토글·스위치·드롭다운). 다크/라이트 테마 전환 UI가 제품 기능으로 존재. |
| 7 | `…671514` (사용자·권한·RLS) | 세그먼트 탭 + filled 검색. 카드형 테이블: 연회색 헤더 소문자 라벨, 행 60px, 파스텔 아바타 원형+이니셜, 인라인 역할 Select, 민트 틴트 상태 pill, 우측 행 액션 아이콘 3종, 행 구분선만(세로선 없음). 우상단 primary "+ 사용자 초대". |
| 8 | `…680748` (진단·시스템 상태) | 정의 리스트형 상태 행(아이콘 타일+제목/캡션, 우측 성공색 체크 "정상"). 모노스페이스 값 표기. 얇은 라운드 진행바(토큰 사용량). 아웃라인 컬러 pill(핑크/오렌지)로 오류 통계. 섹션 오버라인 구분. |

---

*작성: 2026-07-21 · 근거 코드: `admin-frontend/src/App.tsx`(ConfigProvider theme), `src/styles/admin.css`, `src/components/StatusTag.tsx`, `src/pages/DashboardPage.tsx`(SERIES), `src/components/AdminLayout.tsx` · 레퍼런스: `_tmp/ref/admin/*.png` 8장*
