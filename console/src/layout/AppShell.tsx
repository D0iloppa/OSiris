import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { Sidebar } from './Sidebar'
import { Topbar } from './Topbar'
import type { ScreenKey } from './screens'
import type { MeResponse } from '../api/me'
import { neutral } from '../theme/tokens'

/**
 * 사이드바 접힘 상태만 브라우저에 기억한다.
 *
 * 이 콘솔에는 "값을 브라우저에 저장하지 않는다"는 규율이 있지만, 그것은
 * **자격증명**에 대한 규율이다(api/auth.ts 주석). 사이드바를 접었는지는
 * 시크릿이 아니라 이 사람 이 브라우저에서만 의미 있는 편의값이라 여기에
 * 두는 것이 맞다. 읽기·쓰기 모두 try/catch로 감싼다 — 프라이빗 창이나
 * 사이트 데이터 차단 환경에서 접근 자체가 throw할 수 있고, 그때는 그냥
 * 기본값(펼침)으로 동작하면 된다.
 */
const COLLAPSED_KEY = 'osiris.console.sidebarCollapsed'

function readCollapsed(): boolean {
  try {
    return window.localStorage.getItem(COLLAPSED_KEY) === '1'
  } catch {
    return false
  }
}

export function AppShell({
  active,
  onNavigate,
  children,
  user,
}: {
  active: ScreenKey
  onNavigate: (key: ScreenKey) => void
  children: ReactNode
  user?: MeResponse | null
}) {
  const [collapsed, setCollapsed] = useState<boolean>(readCollapsed)

  useEffect(() => {
    try {
      window.localStorage.setItem(COLLAPSED_KEY, collapsed ? '1' : '0')
    } catch {
      // 저장할 수 없어도 이번 세션 동안의 접힘/펼침은 정상 동작한다.
    }
  }, [collapsed])

  const toggle = useCallback(() => setCollapsed((v) => !v), [])

  // 앱을 뷰포트 높이에 **고정**하고 스크롤을 내부(main)로 가둔다.
  //
  // [2026-09-11] 이전엔 root가 `minHeight:100vh`라 본문이 길어지면 **문서
  // 전체**가 스크롤됐고, 그때 사이드바(높이 100vh, sticky)까지 함께 밀려
  // 올라갔다(실측: 설정 화면에서 임베드가 길어지자 좌측 사이드바가 위로
  // 사라짐). root를 `height:100vh; overflow:hidden`으로 잠그면 문서 자체는
  // 스크롤되지 않으므로 사이드바는 항상 제자리에 있고, 길어지는 본문은
  // `main`이 자기 안에서만 스크롤한다. 스크롤 컨테이너가 문서 → main으로
  // 한 단계 내려간 것이 이 수정의 전부다.
  return (
    <div style={{ display: 'flex', height: '100vh', overflow: 'hidden', background: neutral.canvas }}>
      <Sidebar
        active={active}
        onNavigate={onNavigate}
        collapsed={collapsed}
        onToggleCollapsed={toggle}
        user={user}
      />
      <div style={{ flex: 1, display: 'flex', flexDirection: 'column', minWidth: 0, minHeight: 0 }}>
        <Topbar activeScreen={active} />
        {/* main이 유일한 세로 스크롤 컨테이너다. 대부분의 페이지는 여기서
            스크롤되고, 설정처럼 height:100%로 main을 꽉 채우는 페이지는
            자기 우측 열만 내부에서 스크롤한다(좌측 설정메뉴는 안 밀린다). */}
        <main
          style={{
            flex: 1,
            minWidth: 0,
            minHeight: 0,
            overflowY: 'auto',
            display: 'flex',
            flexDirection: 'column',
          }}
        >
          {children}
        </main>
      </div>
    </div>
  )
}
