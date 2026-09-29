/**
 * OSiris 콘솔 디자인 토큰 — 단일 소스.
 *
 * 출처: docs/design/41-console-design-system.md §2 (전부 hex 확정, 대비 실측값 포함).
 * 규칙(41 §2 서두): "antd 토큰 키가 존재하면 그것이 정본"이다. 이 파일이 내보내는
 * `osirisTheme.token`/`osirisTheme.components`가 antd ConfigProvider에 그대로
 * 공급되며, CSS에서는 antd가 노출하는 `var(--ant-*)`를 우선 참조한다.
 * `--osiris-*` CSS 변수는 antd가 커버하지 못하는 개념(차트 팔레트, 사이드바 세부,
 * 틴트, 모노스페이스 폰트 등)에만 정의한다. 이 파일 밖에서 리터럴 hex를 쓰지 않는다
 * (검증 3 — grep으로 확인).
 */
import type { ThemeConfig } from 'antd'

// ── 2.1 브랜드 — 라피스 블루 10단계 스케일 ────────────────────────────────
export const lapis = {
  50: '#EAF0FA',
  100: '#D1DFF5',
  200: '#A6C2ED',
  300: '#729FE4',
  400: '#266ED9',
  500: '#1F5BB5',
  600: '#1B4F9C', // anchor — colorPrimary
  700: '#143C78', // colorLink
  800: '#112E5A',
  900: '#0F223E',
} as const

// ── 2.1.2 금 — 마크 전용. 절대 UI 색(버튼/링크/배지/차트)으로 쓰지 않는다 ──
export const gold = {
  mark: '#E8B54B',
  badgeDark: '#0F1419',
} as const

// ── 2.2 뉴트럴 — slate 계열 (DESIGN-admin.md 계승, 캔버스만 사용자 지정값) ─
export const neutral = {
  white: '#ffffff',
  gray50: '#f8fafc',
  canvas: '#F7F9FC', // colorBgLayout
  gray100: '#f1f5f9',
  gray200: '#e2e8f0',
  border: '#e3e8ef', // colorBorder
  borderLight: '#edf0f4', // colorBorderSecondary
  gray300: '#cbd5e1',
  gray400: '#94a3b8', // colorTextQuaternary
  gray500: '#64748b', // colorTextTertiary
  gray600: '#475569', // colorTextSecondary
  gray700: '#334155',
  text: '#172033', // colorText
  gray900: '#0f172a',
} as const

// ── 2.3 시맨틱 — 본색 + 틴트 쌍 (대비 재계산, admin값에서 조정됨) ─────────
export const semantic = {
  success: { base: '#15793a', tint: '#e8f7ee' },
  warning: { base: '#9b5608', tint: '#fdf3e3' },
  error: { base: '#c4271c', tint: '#fdecec' },
  info: { base: '#722ed1', tint: '#f1ebf9' },
  neutral: { base: neutral.gray600, tint: neutral.gray100 },
} as const

// ── 2.4.1 차트 — 카테고리 팔레트 (dataviz 스킬 6체크 검증 완료) ───────────
// 순서 고정: 이 배열의 인덱스가 곧 "고정 순서"다 (§2.4.2 — 카테고리 인코딩
// 전용, 상태 인코딩에는 절대 쓰지 않는다).
export const chartCategorical = [
  '#2a78d6', // chart-1 blue
  '#eb6834', // chart-2 orange
  '#1baf7a', // chart-3 aqua (surface 대비 <3:1 — 직접 라벨 필수)
  '#eda100', // chart-4 yellow (surface 대비 <3:1 — 직접 라벨 필수)
  '#e87ba4', // chart-5 magenta (surface 대비 <3:1 — 직접 라벨 필수)
  '#008300', // chart-6 green
  '#4a3aa7', // chart-7 violet
  '#e34948', // chart-8 red
] as const

// 3:1 미만이라 표/직접 라벨 동반이 필수인 슬롯 (§2.4.1 실측 WARN)
export const chartLowContrastSurface = new Set([
  chartCategorical[2],
  chartCategorical[3],
  chartCategorical[4],
])

export const chartAux = {
  surfaceLight: neutral.canvas,
  surfaceCard: neutral.white,
  gridline: '#eceff3',
  axisLabel: neutral.gray500,
} as const

// ── 2.5 타이포그래피 ───────────────────────────────────────────────────
export const fontFamily =
  'Inter, Pretendard, "Noto Sans KR", -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif'
export const fontMono = '"SF Mono", "Fira Code", Menlo, monospace'

export const typography = {
  kpi: { size: 28, weight: 700, lineHeight: 1.2 },
  h1: { size: 20, weight: 700, lineHeight: 1.3 },
  h2: { size: 16, weight: 650, lineHeight: 1.4 },
  cardTitle: { size: 15, weight: 600, lineHeight: 1.4 },
  body: { size: 14, weight: 400, lineHeight: 1.55 },
  bodySm: { size: 13, weight: 400, lineHeight: 1.5 },
  caption: { size: 12, weight: 400, lineHeight: 1.4 },
  overline: { size: 11, weight: 700, lineHeight: 1.2, letterSpacing: '0.06em' },
} as const

// ── 2.6 Spacing (4px 기수) ─────────────────────────────────────────────
export const space = [4, 8, 12, 16, 20, 24, 32, 40] as const

// ── 2.7 Radius ──────────────────────────────────────────────────────────
export const radius = { sm: 6, md: 8, lg: 10, xl: 12, pill: 999 } as const

// ── 2.8 Elevation (그림자는 보조 — 보더 우선) ───────────────────────────
export const elevation = {
  e0: 'none',
  e1: '0 1px 2px rgba(15,23,42,.04)',
  e2: '0 4px 16px rgba(15,23,42,.08)',
  e3: '0 16px 40px rgba(15,23,42,.14)',
} as const

// ── 2.9 z-index ──────────────────────────────────────────────────────
export const zIndex = { sider: 100, topbar: 110 } as const

// ── 2.10 ConfigProvider 목표 형상 ────────────────────────────────────────
export const osirisTheme: ThemeConfig = {
  cssVar: true,
  token: {
    colorPrimary: lapis[600],
    colorLink: lapis[700],
    colorInfo: semantic.info.base,
    colorInfoBg: semantic.info.tint,
    colorSuccess: semantic.success.base,
    colorSuccessBg: semantic.success.tint,
    colorWarning: semantic.warning.base,
    colorWarningBg: semantic.warning.tint,
    colorError: semantic.error.base,
    colorErrorBg: semantic.error.tint,
    colorBgLayout: neutral.canvas,
    colorBorder: neutral.border,
    colorBorderSecondary: neutral.borderLight,
    colorText: neutral.text,
    colorTextSecondary: neutral.gray600,
    colorTextTertiary: neutral.gray500,
    colorTextQuaternary: neutral.gray400,
    borderRadius: radius.lg,
    fontSize: typography.body.size,
    fontFamily,
  },
  components: {
    Layout: { siderBg: neutral.white, headerBg: neutral.white },
    Menu: {
      itemBorderRadius: radius.md,
      itemSelectedBg: lapis[50],
      itemSelectedColor: lapis[600],
    },
    Table: {
      headerBg: neutral.gray50,
      headerColor: neutral.gray500,
      rowHoverBg: '#eef4fc', // lapis 틴트 hover (lapis-50과 별개, 살짝 진하게)
    },
    Card: { borderRadiusLG: radius.xl },
    Button: { borderRadius: radius.md, fontWeight: 600 },
    Tag: { borderRadiusSM: radius.pill },
  },
}

// ── --osiris-* CSS 변수 주입값 (antd가 커버하지 않는 개념만) ─────────────
// src/theme/cssVars.ts가 이 객체를 :root에 주입한다.
export const osirisCssVars: Record<string, string> = {
  '--osiris-lapis-50': lapis[50],
  '--osiris-lapis-100': lapis[100],
  '--osiris-lapis-200': lapis[200],
  '--osiris-lapis-300': lapis[300],
  '--osiris-lapis-400': lapis[400],
  '--osiris-lapis-800': lapis[800],
  '--osiris-lapis-900': lapis[900],
  '--osiris-gold-mark': gold.mark,
  '--osiris-badge-dark': gold.badgeDark,
  '--osiris-gray-50': neutral.gray50,
  '--osiris-gray-100': neutral.gray100,
  '--osiris-gray-200': neutral.gray200,
  '--osiris-gray-300': neutral.gray300,
  '--osiris-gray-700': neutral.gray700,
  '--osiris-gray-900': neutral.gray900,
  '--osiris-success': semantic.success.base,
  '--osiris-success-bg': semantic.success.tint,
  '--osiris-warning': semantic.warning.base,
  '--osiris-warning-bg': semantic.warning.tint,
  '--osiris-error': semantic.error.base,
  '--osiris-error-bg': semantic.error.tint,
  '--osiris-info': semantic.info.base,
  '--osiris-info-bg': semantic.info.tint,
  '--osiris-neutral-state': semantic.neutral.base,
  '--osiris-neutral-state-bg': semantic.neutral.tint,
  '--osiris-chart-grid': chartAux.gridline,
  '--osiris-font-mono': fontMono,
  '--osiris-font-kpi-size': `${typography.kpi.size}px`,
  '--osiris-font-overline-size': `${typography.overline.size}px`,
  '--osiris-space-1': `${space[0]}px`,
  '--osiris-space-2': `${space[1]}px`,
  '--osiris-space-3': `${space[2]}px`,
  '--osiris-space-4': `${space[3]}px`,
  '--osiris-space-5': `${space[4]}px`,
  '--osiris-space-6': `${space[5]}px`,
  '--osiris-space-7': `${space[6]}px`,
  '--osiris-space-8': `${space[7]}px`,
  '--osiris-radius-pill': `${radius.pill}px`,
  '--osiris-shadow-1': elevation.e1,
  '--osiris-shadow-2': elevation.e2,
  '--osiris-shadow-3': elevation.e3,
  '--osiris-z-sider': String(zIndex.sider),
  '--osiris-z-topbar': String(zIndex.topbar),
}
chartCategorical.forEach((hex, i) => {
  osirisCssVars[`--osiris-chart-${i + 1}`] = hex
})
