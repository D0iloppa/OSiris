import { useEffect, useState } from 'react'
import { AppShell } from './layout/AppShell'
import { meApi, type MeResponse } from './api/me'
import type { ScreenKey } from './layout/screens'
import { ChatPage } from './pages/ChatPage'
import { OrgPage } from './pages/OrgPage'
import { WorkPage } from './pages/WorkPage'
import { ApprovalsPage } from './pages/ApprovalsPage'
import { OpsPage } from './pages/OpsPage'
import { InfraPage } from './pages/InfraPage'
import { OntologyPage } from './pages/OntologyPage'
import { MyTasksPage } from './pages/MyTasksPage'
import { OnboardingPage } from './pages/onboarding/OnboardingPage'
import { WorkMapPage } from './pages/WorkMapPage'
import { SettingsPage } from './pages/settings/SettingsPage'

/**
 * 화면 전환은 URL 라우팅이 아니라 순수 React state다 — Sidebar.tsx의
 * 주석 참고("이 콘솔은 주소창 경로를 전혀 바꾸지 않는다"). 초기 화면은
 * Onboarding — 대표가 브라우저를 열어 온보딩 0단계를 바로 진행할 수
 * 있어야 한다는 작업 목표에 맞춘 선택(원래 IA상 홈은 Admin Chat이지만,
 * 이번 구현 범위가 온보딩뿐이라 실질적으로 열어야 할 화면은 이쪽).
 */
function App() {
  const [active, setActive] = useState<ScreenKey>('onboarding')

  // 로그인한 사람 정보 — 사이드바 계정 카드가 하드코딩("대표/관리자") 대신
  // 실제 사용자를 표시하도록. App은 AuthGate 안에서만 렌더되므로(인증된
  // 상태) 마운트 시 한 번 조회한다. 실패해도(네트워크 등) 화면은 자리표시자로
  // 동작하면 되므로 조용히 무시.
  const [me, setMe] = useState<MeResponse | null>(null)
  useEffect(() => {
    let cancelled = false
    meApi.getMe().then(
      (m) => {
        if (!cancelled) setMe(m)
      },
      () => {
        /* 자리표시자 유지 */
      },
    )
    return () => {
      cancelled = true
    }
  }, [])

  const renderPage = () => {
    switch (active) {
      case 'chat':
        return <ChatPage />
      case 'org':
        return <OrgPage />
      case 'work':
        return <WorkPage />
      case 'work-map':
        return <WorkMapPage />
      case 'approvals':
        return <ApprovalsPage />
      case 'ops':
        return <OpsPage />
      case 'infra':
        return <InfraPage />
      case 'onboarding':
        return <OnboardingPage />
      case 'ontology':
        return <OntologyPage />
      case 'my-tasks':
        return <MyTasksPage />
      case 'settings':
        return <SettingsPage />
    }
  }

  return (
    <AppShell active={active} onNavigate={setActive} user={me}>
      {renderPage()}
    </AppShell>
  )
}

export default App
