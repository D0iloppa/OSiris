/**
 * 목(mock) ↔ 실제 API 전환 지점 — 이 파일 하나가 유일한 스위치다.
 *
 * **2026-07-27부터 기본값은 real** — `osiris-api`가 온보딩 0단계 전
 * 엔드포인트(§84 계약)를 구현했고, 이 콘솔이 게이트웨이(`osiris-gw`)에
 * 배선되어 실제로 서빙되는 빌드는 실제 백엔드를 쓰는 것이 목표이기
 * 때문(작업 지시 "대표가 브라우저를 열어 온보딩 0단계를 실제로 완주").
 * mock은 삭제하지 않고 개발용으로 남긴다 — `osiris-api` 없이 `npm run dev`로
 * 프론트만 붙잡고 작업할 때 켠다:
 *
 *   VITE_API_MODE=mock npm run dev   (또는 .env.local에 VITE_API_MODE=mock)
 *
 * 코드 안에서 mock/real을 개별적으로 import하는 곳은 `src/api/onboarding.ts`
 * 단 한 파일뿐이다 — 그 외 어디에서도 `./mock/onboardingMock`을 직접
 * import하지 않는다(그래야 스위치가 실제로 "한 곳"이 된다).
 */
export const USE_MOCK_API: boolean = (import.meta.env.VITE_API_MODE ?? 'real') === 'mock'

/**
 * 브라우저(클라이언트) 쪽 API base URL은 `/_api`다 — `osiris-gw`가
 * `/_api/*`를 `/api/*`로 벗겨서 `osiris-api`에 전달한다(41 §0, §10 1단계).
 * `osiris-api` 내부 라우트 자체는 `/api/*`이지만 그건 서버 쪽 사정이고,
 * 콘솔은 절대 그 경로를 직접 부르지 않는다 — 항상 `/_api`를 base로 쓴다.
 */
export const API_BASE = '/_api'

/** 시뮬레이션 지연(ms) — mock이 실제 네트워크 왕복처럼 보이게 하기 위함. */
export const MOCK_LATENCY_MS = 350
