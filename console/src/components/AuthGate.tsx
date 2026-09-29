import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { Button, Result, Spin } from 'antd'
import { LoginScreen } from './LoginScreen'
import { onUnauthorized } from '../api/authEvents'
import { USE_MOCK_API } from '../api/config'
import { ApiError, http } from '../api/http'
import { neutral } from '../theme/tokens'

type Status = 'checking' | 'authenticated' | 'unauthenticated' | 'unavailable'

/**
 * 콘솔 전체를 감싸는 세션 게이트 (작업 지시 "작업 1"). 렌더 트리 최상단
 * (`main.tsx`)에서 `<App/>`을 이 컴포넌트로 감싼다.
 *
 * 동작:
 *  1. 마운트 시 `GET /onboarding/status`(이미 인증이 걸려 있는 기존
 *     엔드포인트) 한 번을 호출해 세션 쿠키가 유효한지 확인한다 — 별도의
 *     "세션 확인" 엔드포인트를 새로 만들지 않고 기존 인증 표면을 그대로
 *     재사용한다.
 *  2. **401이면** `LoginScreen`을 보여준다. 그 화면에서 로그인에 성공하면
 *     `authenticated`로 전환한다.
 *  3. **401이 아닌 실패면** `unavailable`이다 — 아래 참고.
 *  4. 그 후 어떤 API 호출이든 401을 받으면(`http.ts`가 `emitUnauthorized`로
 *     알림) 세션이 만료/무효화된 것으로 보고 다시 로그인 화면으로 되돌린다
 *     — 이것이 "세션 만료·무효 시 동작" 정의다.
 *
 * ── [2026-09-11] 3번(`unavailable`)이 추가된 이유 ──────────────────────────
 *
 * 이 게이트는 원래 `http.get(...)`의 **모든** 거절을 `unauthenticated`로
 * 뭉뚱그렸다. 그 결과 실제 사건에서 이렇게 됐다: osiris-api가 기동 경합으로
 * NotReady에 갇혀 Ingress가 `/_api/*`에 503을 돌려주는 동안, 콘솔은
 * 태연히 **로그인 화면**을 띄웠다. 조작자는 "로그아웃됐나 보다"라고 읽고
 * 마스터 키를 찾아 붙여넣었지만 그 요청도 503이라 아무 일도 일어나지 않았고,
 * 화면 어디에도 백엔드가 죽었다는 말이 없었다. 로그인 화면은 "당신의 자격이
 * 부족하다"는 주장인데, 그 주장을 할 자격이 있는 것은 서버가 **대답했을 때**
 * 뿐이다 — 대답하지 못한 것과 거절한 것은 다른 사건이고, 화면도 달라야 한다.
 *
 * 그래서 여기서는 401(서버가 "당신이 누군지 모른다"고 **대답**한 것)만
 * 로그인 화면으로 보내고, 그 외 모든 실패(5xx, 네트워크 단절, 비-JSON 응답)는
 * 원인을 그대로 말하는 화면 + 재시도 버튼으로 보낸다.
 */
export function AuthGate({ children }: { children: ReactNode }) {
  const [status, setStatus] = useState<Status>(USE_MOCK_API ? 'authenticated' : 'checking')
  const [detail, setDetail] = useState<string>('')

  const check = useCallback(() => {
    setStatus('checking')
    return http.get('/onboarding/status').then(
      () => setStatus('authenticated'),
      (e: unknown) => {
        if (e instanceof ApiError && e.status === 401) {
          setStatus('unauthenticated')
          return
        }
        setDetail(
          e instanceof ApiError
            ? `osiris-api ${e.status}${e.message ? ` — ${e.message}` : ''}`
            : '네트워크 요청이 실패했습니다.',
        )
        setStatus('unavailable')
      },
    )
  }, [])

  useEffect(() => {
    if (USE_MOCK_API) return
    void check()
  }, [check])

  useEffect(() => {
    if (USE_MOCK_API) return
    return onUnauthorized(() => setStatus('unauthenticated'))
  }, [])

  if (status === 'checking') {
    return (
      <Centered>
        <Spin size="large" />
      </Centered>
    )
  }

  if (status === 'unavailable') {
    return (
      <Centered>
        <Result
          status="warning"
          title="백엔드에 연결할 수 없습니다"
          subTitle={
            <>
              콘솔은 떴지만 <code>osiris-api</code>가 응답하지 않습니다. 로그아웃된 것이 아니므로
              자격증명을 다시 입력할 필요는 없습니다.
              <br />
              {detail}
            </>
          }
          extra={
            <Button type="primary" onClick={check}>
              다시 시도
            </Button>
          }
        />
      </Centered>
    )
  }

  if (status === 'unauthenticated') {
    return <LoginScreen onAuthenticated={() => setStatus('authenticated')} />
  }

  return <>{children}</>
}

function Centered({ children }: { children: ReactNode }) {
  return (
    <div
      style={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: neutral.canvas,
        padding: 24,
      }}
    >
      {children}
    </div>
  )
}
