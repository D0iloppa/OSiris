import { http } from './http'

/**
 * `GET /_api/me` / `POST /_api/me/password` — 로그인한 사람 자신의 화면.
 *
 * 서버: `api/internal/people/me.go`의 `Me`, `api/internal/authn/authn.go`의
 * `ChangePassword`. 둘 다 세션 쿠키로만 인증되고(§8.4), 기계 주체는 애초에
 * `PersonOnly`에서 걸러진다.
 */

/** GET /_api/me 응답 — `people.Me`가 실제로 쓰는 필드만 옮겼다(§3.5). */
export interface MeResponse {
  partyId: string | null
  kind: string
  name?: string
  email?: string
  active?: boolean
  orgRole?: { id: string; title: string; membershipId: string } | null
  isOrgAdmin: boolean
  effectivePermissions: string[]
  consoleSurfaces: string[]
  credentialPending: boolean
}

export const meApi = {
  getMe: () => http.get<MeResponse>('/me'),

  /**
   * 비밀번호 변경 — 현재 비밀번호 재인증 필수(서버 `ChangePassword`가 §5.6
   * state C대로 `authncred.Verify`를 먼저 통과시키고서야 `Set`을 호출한다).
   *
   * 값은 인자로만 흐른다 — 호출부(MeSettingsPage)가 성공/실패와 무관하게
   * 폼을 즉시 리셋하고, 이 모듈도 어디에도 값을 저장하지 않는다.
   */
  changePassword: (currentPassword: string, newPassword: string) =>
    http.post<{ ok: boolean }>('/me/password', { currentPassword, newPassword }),
}
