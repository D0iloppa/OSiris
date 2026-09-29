# OSiris 콘솔 (osiris-webui)

React 18.3 + Vite 5 + antd 5.21 + @tanstack/react-query + recharts 3 SPA.
디자인 스펙: `docs/design/41-console-design-system.md`(디자인 시스템) ·
`docs/design/40-console-ia.md`(정보구조) · `docs/design/30-onboarding-provisioning.md`
§1.0(온보딩 0단계 — 이번 구현 범위).

## 개발

```bash
npm install
npm run dev        # http://localhost:5173, 기본 mock API
```

## 빌드

```bash
npm run build       # tsc -b && vite build
```

**산출물은 `dist/`** — nginx(`osiris-gw`)가 정적 파일로 서빙할 디렉터리.
`vite.config.ts`의 `build.outDir`이 이 값을 고정한다. gw 배선(`gw/conf.d/default.conf`)
연결은 이 워커의 소유 범위 밖이다.

```bash
npm run preview     # dist/ 를 그대로 로컬에서 확인
npm run typecheck   # tsc -b --noEmit
npm run lint        # oxlint
```

## 목(mock) ↔ 실제 API 전환

**단일 스위치: `src/api/config.ts`의 `USE_MOCK_API`.** 기본값은 mock —
`osiris-api`가 아직 없어도 온보딩 0단계를 처음부터 끝까지 진행할 수 있다.

```bash
VITE_API_MODE=real npm run build   # 또는 .env.local에 VITE_API_MODE=real
```

페이지/훅 코드는 전부 `src/api/onboarding.ts`의 `onboardingApi` 객체만 참조한다 —
mock/real 구현(`src/api/mock/onboardingMock.ts` / `src/api/onboardingReal.ts`)을
직접 import하는 곳은 이 파일 하나뿐이다.

브라우저가 실제로 호출하는 API base URL은 `/_api`다(`src/api/config.ts`) —
`osiris-gw`가 `/_api/*`를 `/api/*`로 벗겨 `osiris-api`에 전달한다.

## 디자인 토큰

단일 소스: `src/theme/tokens.ts`. antd `ConfigProvider theme={{ token, components,
cssVar: true }}`(`src/main.tsx`)로 공급하고, antd가 커버하지 못하는 값만
`--osiris-*` CSS 변수로 `src/theme/applyCssVars.ts`가 `:root`에 주입한다.
컴포넌트 코드에 리터럴 hex를 새로 쓰지 않는다.

## 구현 범위 (이번 커밋)

- 토큰 + ConfigProvider, 레이아웃 셸(사이드바 8항목 + 탑바 알림 드롭다운),
  9개 화면 라우팅 자리.
- **온보딩 0단계**(결정적 스택 배선, LLM 없음)만 실제 구현. 나머지 8개
  화면은 "준비 중" 플레이스홀더.
- 화면 전환은 URL 라우팅이 아니라 React state다(`src/layout/Sidebar.tsx`
  주석 참고) — `osiris-gw`가 `= /`만 콘솔에 예약하고 그 외 최상위 경로는
  전부 omniroute로 프록시하므로, 주소창 경로를 바꾸는 라우터를 쓰면
  새로고침 시 충돌한다.

## 자격증명 처리

비밀번호·프로바이더 API 키 입력값은 antd `Form`의 제출 인자로만 흐르고,
제출 직후 `resetFields()`로 지운다. `localStorage`/`sessionStorage`/URL에
쓰는 코드 경로가 없다(레이아웃 원칙, 관련 사고 재발 방지).
