import { http } from './http'
import type {
  AliasDefaultEntry,
  ExchangeAdminRequest,
  ExchangeAdminResponse,
  IssueGatewayTokenResponse,
  OnboardingStatusResponse,
  RegisterProviderRequest,
  RegisterProviderResponse,
  UpsertAliasesRequest,
  UpsertAliasesResponse,
  VerifyGatewayResponse,
} from './types'

/**
 * 실제 `osiris-api` 호출. 경로는 30-onboarding-provisioning.md §1.0.8의
 * 계약표 그대로(브라우저는 `/_api` base를 통해 부른다 — http.ts 참고).
 */
export const onboardingReal = {
  getStatus: () => http.get<OnboardingStatusResponse>('/onboarding/status'),

  exchangeAdmin: (req: ExchangeAdminRequest) =>
    http.post<ExchangeAdminResponse>('/onboarding/step0/omniroute-admin', req),

  // 와이어 모양은 `{entries:[...]}` 래퍼(IMPLEMENTATION-NOTES.md:2426) —
  // 클라이언트 인터페이스(mock과 동일)는 배열을 반환해야 하므로 여기서
  // 벗긴다. `?? []`는 Go 쪽이 이미 빈 슬라이스를 보장하므로(직전 워커
  // 수정) 원칙적으로는 중복이지만, 네트워크 응답 형태가 문서와 어긋나는
  // 경우(예: entries 필드 자체가 누락)에도 콘솔이 크래시 대신 빈 목록으로
  // 방어하도록 방어적으로 남겨둔다.
  getAliasDefaults: () =>
    http
      .get<{ entries: AliasDefaultEntry[] }>('/onboarding/step0/alias-defaults')
      .then((r) => r.entries ?? []),

  registerProvider: (req: RegisterProviderRequest) =>
    http.post<RegisterProviderResponse>('/onboarding/step0/providers', req),

  upsertAliases: (req: UpsertAliasesRequest) =>
    http.post<UpsertAliasesResponse>('/onboarding/step0/aliases', req),

  issueGatewayToken: () =>
    http.post<IssueGatewayTokenResponse>('/onboarding/step0/gateway-token'),

  verifyGateway: () => http.post<VerifyGatewayResponse>('/onboarding/step0/verify-gateway'),
}
