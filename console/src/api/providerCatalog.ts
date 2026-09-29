/**
 * 프로바이더 카탈로그 — 콘솔이 소유하는 정적 데이터.
 *
 * omniroute에는 "지원 프로바이더 목록" API가 없다(2026-09-11 Playwright +
 * 소스 조사로 확정). 그래서 이 카탈로그는 omniroute의 `/omni/dashboard/
 * providers` 화면을 실측해 등록 방식별로 분류한, 콘솔 쪽의 정적 진실이다 —
 * 서버가 이 목록을 내려주지 않으므로 새 프로바이더가 omniroute에 추가되면
 * 이 파일도 사람이 갱신해야 한다(자동 동기화 없음, 정직하게 남긴다).
 *
 * method는 등록 흐름을 가른다(ProviderRegisterModal이 이 값으로 2단계
 * 폼을 분기한다):
 *   redirect — authUrl 새 탭 + 콜백 URL/코드 붙여넣기 (authorize → exchange)
 *   device   — 디바이스 코드 표시 + 자동 폴링 (device-code → poll)
 *   apikey   — 이름 + API 키 입력 (POST /providers)
 *   import   — 기존 토큰 붙여넣기 (import-token) — 페이로드 미확정분 다수,
 *              지원 확정된 것만 `importSupported: true`로 표시하고 나머지는
 *              모달에서 "지원 예정"으로 정직하게 안내한다.
 */
export type ProviderMethod = 'redirect' | 'device' | 'apikey' | 'import'

export interface ProviderCatalogEntry {
  id: string
  label: string
  method: ProviderMethod
  group: string
  /** import 방식 중 페이로드가 확정되지 않아 아직 못 붙인 것 표시용. */
  importSupported?: boolean
}

export const PROVIDER_CATALOG: ProviderCatalogEntry[] = [
  // ── redirect: authUrl 열기 → 승인 → 콜백 URL/코드 붙여넣기 ──────────────
  { id: 'claude', label: 'Claude', method: 'redirect', group: 'Anthropic' },
  { id: 'codex', label: 'Codex', method: 'redirect', group: 'OpenAI' },
  { id: 'gitlab-duo', label: 'GitLab Duo', method: 'redirect', group: 'GitLab' },
  { id: 'zed-hosted', label: 'Zed (Hosted)', method: 'redirect', group: 'Zed' },
  { id: 'cline', label: 'Cline', method: 'redirect', group: 'Cline' },
  { id: 'antigravity', label: 'Antigravity', method: 'redirect', group: 'Antigravity' },
  { id: 'agy', label: 'AGY', method: 'redirect', group: 'AGY' },
  { id: 'qoder', label: 'Qoder', method: 'redirect', group: 'Qoder' },

  // ── device: 디바이스 코드 표시 → 자동 폴링 ──────────────────────────────
  { id: 'github', label: 'GitHub Copilot', method: 'device', group: 'GitHub' },
  { id: 'qwen', label: 'Qwen', method: 'device', group: 'Alibaba' },
  { id: 'kiro', label: 'Kiro', method: 'device', group: 'Kiro' },
  { id: 'kilocode', label: 'Kilo Code', method: 'device', group: 'Kilo Code' },
  { id: 'kimi-coding', label: 'Kimi Coding', method: 'device', group: 'Moonshot' },
  { id: 'codebuddy-cn', label: 'CodeBuddy (CN)', method: 'device', group: 'Tencent' },
  { id: 'amazon-q', label: 'Amazon Q', method: 'device', group: 'AWS' },

  // ── apikey: 이름 + API 키 입력 ──────────────────────────────────────────
  { id: 'openai', label: 'OpenAI', method: 'apikey', group: 'OpenAI' },
  { id: 'anthropic', label: 'Anthropic', method: 'apikey', group: 'Anthropic' },
  { id: 'google', label: 'Google', method: 'apikey', group: 'Google' },
  { id: 'openrouter', label: 'OpenRouter', method: 'apikey', group: 'OpenRouter' },
  { id: 'groq', label: 'Groq', method: 'apikey', group: 'Groq' },
  { id: 'mistral', label: 'Mistral', method: 'apikey', group: 'Mistral' },

  // ── import: 기존 토큰 붙여넣기 (페이로드 대부분 미확정 — 정직하게 표시) ──
  { id: 'cursor', label: 'Cursor', method: 'import', group: 'Cursor', importSupported: false },
  { id: 'trae', label: 'Trae', method: 'import', group: 'Trae', importSupported: false },
  { id: 'grok-cli', label: 'Grok CLI', method: 'import', group: 'xAI', importSupported: false },
  { id: 'windsurf', label: 'Windsurf', method: 'import', group: 'Windsurf', importSupported: false },
  { id: 'devin-cli', label: 'Devin CLI', method: 'import', group: 'Cognition', importSupported: false },
  { id: 'zed', label: 'Zed (Import)', method: 'import', group: 'Zed', importSupported: false },
]

export const METHOD_LABEL: Record<ProviderMethod, string> = {
  redirect: '리다이렉트 + 코드 붙여넣기',
  device: '디바이스 코드',
  apikey: 'API 키',
  import: '토큰 임포트',
}

export function findCatalogEntry(providerId: string): ProviderCatalogEntry | undefined {
  return PROVIDER_CATALOG.find((p) => p.id === providerId)
}
