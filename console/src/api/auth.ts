import { http } from './http'

/**
 * 브라우저 세션 인증 — `POST /_api/auth/session` 하나가 **세 가지 자격증명**을
 * 모두 받는다(서버: `api/internal/authn/authn.go`의 `Exchange`). 어느 경로인지는
 * 요청 본문의 필드가 결정한다:
 *
 *   `{ ticket }`    → 등록 티켓 교환(신규 구성원) — [미구현: 이 콘솔에 화면 없음]
 *   `{ email, … }`  → email + 비밀번호 (60-identity-authn.md §3.1) — 정상 운영
 *   그 외            → 마스터 키 (§4.1 ①) — 최초 설치 1회
 *
 * ── [2026-09-11] 이메일 로그인이 추가된 이유 ───────────────────────────────
 *
 * 이 모듈에는 `exchange`(마스터 키) 하나뿐이었고, 콘솔의 유일한 로그인 화면도
 * 마스터 키 입력 하나뿐이었다. 그런데 마스터 키는 **초기 관리자가 비밀번호를
 * 세우는 순간 영구 폐기된다**(H23). 즉 온보딩을 끝까지 진행하면 그 순간부터
 * 콘솔에 로그인할 수 있는 화면이 하나도 남지 않는 구조였다 — 서버는 §3.1의
 * email+비밀번호를 이미 구현해 두었는데(`passwordLogin`) 콘솔에 그 입력창이
 * 없었을 뿐이다. 온보딩을 완주하기 전에 발견해서 막았다.
 *
 * 세 경로 모두 같은 보안 규칙을 지킨다: 값은 인자로만 흐르고, 호출부가 성공/
 * 실패와 무관하게 즉시 폼을 리셋하며, 이 모듈도 다른 어떤 콘솔 코드도 그 값을
 * localStorage/sessionStorage/URL에 쓰지 않는다. 인증은 응답의 HttpOnly 세션
 * 쿠키로만 이어진다(브라우저 JS가 읽을 수 없음).
 */
export const authApi = {
  /**
   * 마스터 키 — 최초 설치 1회. 403이 오면 그것은 오류가 아니라 **인수인계
   * 완료**를 뜻한다(H10): 조직에 로그인 가능한 관리자가 생겼으므로 이 경로는
   * 영구히 닫혔고, 관리자 본인의 email + 비밀번호로 들어와야 한다.
   */
  exchange: (masterKey: string) => http.post<{ ok: boolean }>('/auth/session', { masterKey }),

  /**
   * email + 비밀번호 — 인수인계 이후의 정상 로그인 경로(§3.1).
   *
   * 서버는 존재하지 않는 email과 틀린 비밀번호를 **구분하지 않는다**(§8.2의
   * 균일한 401 — `passwordLogin`은 없는 사람에 대해서도 빈 id로 Verify를
   * 끝까지 돌려 같은 비용을 쓴다). 그러므로 이 화면도 "그런 계정이 없습니다"
   * 류의 문구를 만들어내서는 안 된다 — 서버가 준 메시지를 그대로 보여준다.
   */
  loginWithPassword: (email: string, password: string) =>
    http.post<{ ok: boolean }>('/auth/session', { email, password }),

  logout: () => http.post<{ ok: boolean }>('/auth/logout'),
}
