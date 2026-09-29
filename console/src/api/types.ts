/**
 * 온보딩 0단계 API 계약 타입.
 *
 * 출처: docs/design/30-onboarding-provisioning.md §1.0.3(단계 순서)·§1.0.8
 * (엔드포인트 계약, JSON 예시)·§1.0.4(자격증명 교환). 이 문서 자체가
 * "엔드포인트명·페이로드 스키마는 예시이며 확정 사양이 아니다"라고 명시한
 * 부분(40-console-ia.md F절 서두와 동일한 성질)이 있어, 아래 타입 중 예시
 * JSON에 명시되지 않은 필드는 합리적으로 추정하고 `[계약 미확정]`으로
 * 표시했었다.
 *
 * **2026-07-27 대조 완료 (콘솔 게이트웨이 배선 워커)** — `api/internal/
 * onboarding/handlers.go` 소스를 직접 읽어 6건 전부 확정했다. 아래 각
 * 타입의 주석에 실제 소스 근거를 남긴다. 그 전 워커가 추정한 필드명 중
 * `VerifyGatewayResponse`가 **실제와 달랐다**(healthPathOk/
 * orchestratorRestarted는 API에 없음 — 실제는 gatewayHealthOk/
 * propagatedToOrchestrator) — API 쪽이 실제로 도는 쪽이므로 콘솔을
 * API에 맞췄다(VerifyStep.tsx도 함께 수정).
 */

// ── GET /onboarding/status (§1.0.8 JSON 예시, step0을 최상위 필드로 흡수) ──

export interface ProviderCredentialSummary {
  name: string
  provider: string
}

export interface AliasSyncStatus {
  name: string
  inSync: boolean
  providerRegistered: boolean
}

export interface OnboardingStep0Status {
  omniroute: { reachable: boolean; manageKeyExchanged: boolean }
  providers: ProviderCredentialSummary[]
  aliases: AliasSyncStatus[]
  gatewayToken: { issued: boolean; propagatedToOrchestrator: boolean }
  /** 0-1~0-5 전부 통과 여부 — §1.0.7의 전환 조건. */
  ready: boolean
}

/**
 * §2(온보딩 상태기계) 값. **확정(소스 확인)**:
 * `api/internal/onboarding/handlers.go`의 `Status` 핸들러는 `phase`를
 * 항상 무조건 `"NotStarted"`로 고정해 반환한다(step0.ready 값과 무관 —
 * 그 핸들러의 doc comment가 "phase can only ever be NotStarted here,
 * honestly"라고 명시). 즉 step0.ready가 true가 되어도 phase는 여전히
 * "NotStarted"다 — 1단계(대화형 온보딩)가 구현되지 않았기 때문에 그
 * 이후 값으로 전이할 코드 자체가 없다. 이 프론트는 계속
 * **step0.ready를 1차 게이트로 삼고 phase는 보조 표시로만 쓴다** — 이제
 * 그것이 추측이 아니라 확인된 사실에 근거한 결정이다.
 */
export type OnboardingPhase =
  | 'Wiring'
  | 'NotStarted'
  | 'Drafting'
  | 'Reviewing'
  | 'Committing'
  | 'Committed'
  | 'Provisioning'
  | 'Steady'

export interface OnboardingOrgStatus {
  teams: number
  roles: number
  capabilityMappingRatio: number | null
  approvalPolicyDefined: boolean
}

export interface OnboardingStatusResponse {
  step0: OnboardingStep0Status
  phase: OnboardingPhase
  org: OnboardingOrgStatus
}

// ── POST /onboarding/step0/omniroute-admin {password} ───────────────────

export interface ExchangeAdminRequest {
  password: string
}
export interface ExchangeAdminResponse {
  /** 응답에는 password도 key도 에코하지 않는다(§1.0.8) — exchanged만. */
  exchanged: boolean
}

// ── POST /onboarding/step0/providers {name, provider, apiKey} ───────────

export interface RegisterProviderRequest {
  name: string
  /**
   * **확정(소스 확인)**: `omniroute/operations.mjs`의
   * `registerProviderCredential`은 `provider` 값을 검증 없이 그대로
   * omniroute 관리 API(`POST /api/providers`)에 전달만 한다 — 이 저장소
   * 어디에도 provider 식별자 enum/화이트리스트가 없다(omniroute 자신이
   * 최종 검증한다). 자유 입력을 유지하는 것이 맞는 결정이었다 — 유지.
   */
  provider: string
  apiKey: string
}
export interface RegisterProviderResponse {
  /**
   * **확정(소스 확인)**: `api/internal/onboarding/handlers.go`의
   * `Providers` 핸들러는 omniroute의 `{ok, created|updated, id?}` 응답을
   * 받고도 `created`/`updated`를 버린 채 `{"ok": true}`만 반환한다 —
   * `status` 필드는 이 API에 **존재하지 않는다**. 이전 워커가 추정한
   * `status: 'created'|'updated'` 필드는 실재하지 않으므로 제거했다
   * (프론트 어디도 이 필드를 렌더링하지 않고 있었어서 런타임 영향 없음).
   */
  ok: boolean
}

// ── GET /onboarding/step0/alias-defaults ────────────────────────────────

export interface AliasDefaultEntry {
  /**
   * **확정(소스 확인)**: `omniroute/config.json`의 `_aliasComment`가
   * "설계가 정확히 이 세 별칭 이름만 허용한다"고 명시하고
   * `core/internal/graphdb/writeops.go`의 `allowedModelAliases`와
   * 일치해야 하는 고정값이다. 정확히 3개, 이름 고정:
   * `osiris/planner`, `osiris/worker`, `osiris/cheap`. 이 프론트는 여전히
   * `GET /onboarding/step0/alias-defaults` 응답을 그대로 쓴다(하드코딩
   * 안 함) — 이름이 고정이어도 provider/model은 대표가 바꿀 수 있어서다.
   */
  name: string
  provider: string
  model: string
}

// ── POST /onboarding/step0/aliases {entries} ────────────────────────────

export interface AliasEntryInput {
  name: string
  provider: string
  model: string
}
export interface UpsertAliasesRequest {
  entries: AliasEntryInput[]
}
/**
 * **확정(소스 확인)**: `api/internal/onboarding/handlers.go`의 `Aliases`
 * 핸들러는 `omniroute/operations.mjs`의 `upsertAlias` 반환값을 가공 없이
 * `{name, ...}`로 그대로 통과시킨다 — 단일 `status` 문자열 필드가 아니라
 * **불리언 플래그들의 조합**이다(`upsertAlias`의 실제 반환 형태:
 * `{ok, inSync?}` | `{ok, converged?, before?, after?}` |
 * `{ok, created?, after?}` | `{ok:false, providerMissing?, error?}`).
 * 이전 워커가 추정한 `status: 'created'|'converged'|...` 단일 enum
 * 필드는 실재하지 않으므로 제거하고 실제 플래그 조합으로 교체했다.
 */
export interface AliasApplyResult {
  name: string
  ok: boolean
  inSync?: boolean
  converged?: boolean
  created?: boolean
  providerMissing?: boolean
  before?: string
  after?: string
  id?: string
  error?: string
}
export interface UpsertAliasesResponse {
  results: AliasApplyResult[]
}

// ── POST /onboarding/step0/gateway-token ────────────────────────────────

export interface IssueGatewayTokenResponse {
  issued: boolean
  needsOrchestratorRestart: boolean
  restartCommand: string
}

// ── POST /onboarding/step0/verify-gateway ───────────────────────────────

/**
 * **확정(소스 확인), 필드명 불일치 수정됨**: `api/internal/onboarding/
 * handlers.go`의 `verifyResult` 구조체가 실제로 내보내는 JSON 태그는
 * `gatewayHealthOk`/`aliasesInSync`/`propagatedToOrchestrator`/`ready` —
 * 이전 워커가 추정한 `healthPathOk`/`orchestratorRestarted`는 API에
 * **존재하지 않는 필드명**이었다(그 값들은 항상 undefined였을 것 —
 * VerifyStep.tsx가 목 모드에서만 우연히 맞았던 것). API가 실제로 도는
 * 쪽이므로 콘솔을 API에 맞췄다(VerifyStep.tsx도 함께 수정).
 */
export interface VerifyGatewayResponse {
  gatewayHealthOk: boolean
  aliasesInSync: boolean
  propagatedToOrchestrator: boolean
  ready: boolean
}

// ─────────────────────────────────────────────────────────────────────────
// 프로바이더 연결 (설정 → omniroute 설정 → 프로바이더 연결 탭)
// GET/POST/PUT/DELETE `/providers`, `/oauth/{provider}/*` — api/internal/
// providers/handlers.go가 정의한 계약 그대로. 응답의 커넥션 객체는 서버가
// 이미 시크릿을 불리언으로 치환해 보낸다(redact()) — 이 타입도 그 모양을
// 반영해 apiKey/token류를 string이 아니라 boolean으로 둔다.
// ─────────────────────────────────────────────────────────────────────────

export type ProviderAuthType = 'apikey' | 'oauth' | string

export interface ProviderConnection {
  id: string
  provider: string
  authType: ProviderAuthType
  name: string
  email?: string
  isActive: boolean
  priority?: number
  testStatus?: string
  expiresAt?: string | null
  defaultModel?: string
  /** 서버가 redact() 처리한 나머지 필드 — apiKey/token류는 boolean으로 온다. */
  [key: string]: unknown
}

export interface ListProvidersResponse {
  connections: ProviderConnection[]
}

export interface CreateProviderRequest {
  provider: string
  apiKey?: string
  name: string
  url?: string
  priority?: number
}

export interface UpdateProviderRequest {
  name?: string
  priority?: number
  isActive?: boolean
  defaultModel?: string
  apiKey?: string
}

export interface OAuthAuthorizeResponse {
  authUrl: string
  state?: string
  codeVerifier?: string
  flowType?: string
  redirectUri?: string
  fixedPort?: number
  [key: string]: unknown
}

export interface OAuthDeviceCodeResponse {
  user_code: string
  verification_uri: string
  interval: number
  codeVerifier?: string
  device_code?: string
  [key: string]: unknown
}

export interface OAuthExchangeRequest {
  code: string
  redirectUri: string
  codeVerifier?: string
  state?: string
}
