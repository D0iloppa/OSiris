import { MOCK_LATENCY_MS } from '../config'
import type {
  AliasDefaultEntry,
  AliasApplyResult,
  ExchangeAdminRequest,
  ExchangeAdminResponse,
  IssueGatewayTokenResponse,
  OnboardingStatusResponse,
  RegisterProviderRequest,
  RegisterProviderResponse,
  UpsertAliasesRequest,
  UpsertAliasesResponse,
  VerifyGatewayResponse,
} from '../types'

/**
 * 개발용 목(mock) — `osiris-api`가 아직 없는 동안 온보딩 0단계를 처음부터
 * 끝까지 클릭해서 통과할 수 있게 한다(작업 지시 §검증 4).
 *
 * 여기 저장되는 값은 전부 "서버가 가지고 있다고 치는" 값의 시뮬레이션이다
 * — 비밀번호/apiKey 원문은 이 모듈도 저장하지 않는다(받는 즉시 폐기,
 * 30 §1.0.4/O13과 동일한 취급을 mock에서도 지킨다). 모듈 스코프 변수라
 * 새로고침하면 초기화된다 — 이 자체가 "재실행 가능해야 한다"는 §1.0.6
 * 요구를 테스트하기에 오히려 적합하다(재개 지점 UI가 GET status만으로
 * 옳게 그려지는지 매번 확인 가능).
 */

interface MockState {
  reachable: boolean
  manageKeyExchanged: boolean
  providers: { name: string; provider: string }[]
  aliases: { name: string; provider: string; model: string; ok: boolean; status: string }[]
  gatewayTokenIssued: boolean
  gatewayTokenIssuedAt: number | null
  verified: boolean
}

const state: MockState = {
  reachable: true,
  manageKeyExchanged: false,
  providers: [],
  aliases: [],
  gatewayTokenIssued: false,
  gatewayTokenIssuedAt: null,
  verified: false,
}

// 실제 3개 별칭 이름(고정, types.ts AliasDefaultEntry 주석 참고) —
// omniroute/config.json의 프레임워크 기본값과 동일하게 맞춘다.
const ALIAS_DEFAULTS: AliasDefaultEntry[] = [
  { name: 'osiris/planner', provider: 'anthropic', model: 'claude-opus-5' },
  { name: 'osiris/worker', provider: 'anthropic', model: 'claude-sonnet-5' },
  { name: 'osiris/cheap', provider: 'anthropic', model: 'claude-haiku-4-5' },
]

function delay<T>(value: T): Promise<T> {
  return new Promise((resolve) => setTimeout(() => resolve(value), MOCK_LATENCY_MS))
}

function computeStatus(): OnboardingStatusResponse {
  const orchestratorRestarted =
    state.gatewayTokenIssued &&
    state.gatewayTokenIssuedAt !== null &&
    Date.now() - state.gatewayTokenIssuedAt > 3000

  return {
    step0: {
      omniroute: { reachable: state.reachable, manageKeyExchanged: state.manageKeyExchanged },
      providers: state.providers.map((p) => ({ name: p.name, provider: p.provider })),
      aliases: state.aliases.map((a) => ({
        name: a.name,
        inSync: a.ok,
        providerRegistered: state.providers.some((p) => p.provider === a.provider),
      })),
      gatewayToken: {
        issued: state.gatewayTokenIssued,
        propagatedToOrchestrator: orchestratorRestarted,
      },
      ready: state.verified,
    },
    phase: state.verified ? 'NotStarted' : 'Wiring',
    org: { teams: 0, roles: 0, capabilityMappingRatio: null, approvalPolicyDefined: false },
  }
}

export const onboardingMock = {
  getStatus(): Promise<OnboardingStatusResponse> {
    return delay(computeStatus())
  },

  exchangeAdmin(req: ExchangeAdminRequest): Promise<ExchangeAdminResponse> {
    if (!req.password || req.password.length < 4) {
      return delay(undefined).then(() => {
        throw new Error('비밀번호가 비어 있거나 너무 짧습니다.')
      })
    }
    state.manageKeyExchanged = true
    // 비밀번호는 이 함수 스코프를 벗어나면 소멸한다 — 어디에도 대입하지 않음.
    return delay({ exchanged: true })
  },

  getAliasDefaults(): Promise<AliasDefaultEntry[]> {
    return delay(ALIAS_DEFAULTS.map((a) => ({ ...a })))
  },

  registerProvider(req: RegisterProviderRequest): Promise<RegisterProviderResponse> {
    if (!req.apiKey || req.apiKey.trim().length === 0) {
      return delay(undefined).then(() => {
        throw new Error('API 키를 입력하세요.')
      })
    }
    const existing = state.providers.find((p) => p.name === req.name)
    if (existing) {
      existing.provider = req.provider
    } else {
      state.providers.push({ name: req.name, provider: req.provider })
    }
    // 실제 API(api/internal/onboarding/handlers.go의 Providers 핸들러)는
    // {ok:true}만 반환한다 — status 필드 없음(types.ts 주석 참고). mock도
    // 동일한 형태로 맞춘다.
    return delay({ ok: true })
  },

  upsertAliases(req: UpsertAliasesRequest): Promise<UpsertAliasesResponse> {
    const results: AliasApplyResult[] = req.entries.map((entry) => {
      const providerRegistered = state.providers.some((p) => p.provider === entry.provider)
      const status = providerRegistered ? 'inSync' : 'providerMissing'
      const existingIdx = state.aliases.findIndex((a) => a.name === entry.name)
      const record = {
        name: entry.name,
        provider: entry.provider,
        model: entry.model,
        ok: providerRegistered,
        status,
      }
      if (existingIdx >= 0) state.aliases[existingIdx] = record
      else state.aliases.push(record)
      // 실제 API는 단일 status 문자열이 아니라 불리언 플래그 조합을
      // 그대로 통과시킨다(types.ts AliasApplyResult 주석 참고) — mock도
      // 같은 모양으로 맞춘다.
      return providerRegistered
        ? { name: entry.name, ok: true, inSync: true }
        : { name: entry.name, ok: false, providerMissing: true }
    })
    return delay({ results })
  },

  issueGatewayToken(): Promise<IssueGatewayTokenResponse> {
    if (!state.manageKeyExchanged || state.providers.length === 0 || state.aliases.length === 0) {
      return delay(undefined).then(() => {
        throw new Error('선행 단계(관리자 인증/프로바이더/별칭)가 아직 완료되지 않았습니다.')
      })
    }
    state.gatewayTokenIssued = true
    state.gatewayTokenIssuedAt = Date.now()
    return delay({
      issued: true,
      needsOrchestratorRestart: true,
      restartCommand: 'docker compose up -d osiris-orchestrator',
    })
  },

  verifyGateway(): Promise<VerifyGatewayResponse> {
    const aliasesInSync = state.aliases.length > 0 && state.aliases.every((a) => a.ok)
    const propagatedToOrchestrator =
      state.gatewayTokenIssued &&
      state.gatewayTokenIssuedAt !== null &&
      Date.now() - state.gatewayTokenIssuedAt > 3000
    // gatewayHealthOk는 §1.0.3이 명시한 대로 "빈 상태에서도 200"이라 항상
    // true를 시뮬레이션한다 — 그래서 이것만으로는 충분치 않다는 게 요점.
    // 필드명은 실제 API(api/internal/onboarding/handlers.go의 verifyResult)
    // 그대로 맞춘다 — types.ts 주석 참고.
    const gatewayHealthOk = true
    const ready =
      gatewayHealthOk && aliasesInSync && propagatedToOrchestrator && state.manageKeyExchanged
    if (ready) state.verified = true
    return delay({ gatewayHealthOk, aliasesInSync, propagatedToOrchestrator, ready })
  },
}
