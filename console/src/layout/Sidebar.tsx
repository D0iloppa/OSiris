import { useState } from 'react'
import { Avatar, Badge, Button, Menu, Tooltip, Typography } from 'antd'
import { LogoutOutlined, MenuFoldOutlined, MenuUnfoldOutlined } from '@ant-design/icons'
import { BrandMark } from '../brand/BrandMark'
import { authApi } from '../api/auth'
import { emitUnauthorized } from '../api/authEvents'
import { SCREEN_GROUPS, SETTINGS_SCREEN, type ScreenKey } from './screens'
import type { MeResponse } from '../api/me'
import { lapis, neutral } from '../theme/tokens'

const { Text } = Typography

export const SIDEBAR_WIDTH = 256
export const SIDEBAR_WIDTH_COLLAPSED = 80

/**
 * 사이드바 (41 §5.1).
 *
 * 라우팅 노트 — 41 부록 B가 "다음 워커가 명시적으로 결정할 것"이라 남긴
 * 문제(게이트웨이가 `/`만 콘솔에 예약하고 그 외 최상위 경로는 omniroute로
 * 보내므로, 주소창 경로를 바꾸는 클라이언트 라우터를 쓰면 새로고침 시
 * 충돌한다)를 여기서 해소한다 — **이 콘솔은 주소창 경로를 전혀 바꾸지
 * 않는다.** 네비게이션은 React state(App.tsx의 activeScreen)만으로
 * 처리한다. 2026-09-11에 Ingress가 `^/$`(콘솔 루트 하나)만 예약하도록
 * 좁아지면서 이 제약은 오히려 더 엄격해졌다.
 *
 * ── [2026-09-11] 접이식(collapsible) ──────────────────────────────────────
 * 접힌 상태는 아이콘만 남기고 80px로 줄인다 — antd `inlineCollapsed` 메뉴의
 * 고정 폭이 80px이라 레일을 그보다 좁게 잡으면 아이콘이 밖으로 삐져나온다
 * (실측: 72px일 때 선택된 항목의 배경 박스가 레일 경계를 넘었다). 같은
 * 이유로 접혔을 때는 목록 컨테이너의 **수평 패딩을 0으로** 둔다 — 패딩이
 * 있으면 80px 메뉴가 들어갈 자리가 그만큼 줄어 같은 증상이 난다. 접힘 여부는 AppShell이 들고
 * 있고(본문 영역도 같이 넓어져야 하므로), 이 컴포넌트는 표시만 한다.
 *
 * 접힌 상태에서 라벨이 사라지므로 **모든 항목에 Tooltip을 단다** — 아이콘만
 * 보고 뜻을 추측하게 두지 않는다. antd Menu의 `inlineCollapsed`가 자체
 * 툴팁을 붙여 주지만, 그것은 `label`이 문자열일 때만이다. Approvals는
 * 배지 때문에 label이 ReactNode라 자체 툴팁이 뜨지 않으므로, 접혔을 때는
 * 배지를 빼고 문자열 label로 되돌린다(배지 수는 아이콘 위 dot으로 표시).
 */
export function Sidebar({
  active,
  onNavigate,
  approvalsPending = 0,
  collapsed = false,
  onToggleCollapsed,
  user,
}: {
  active: ScreenKey
  onNavigate: (key: ScreenKey) => void
  approvalsPending?: number
  collapsed?: boolean
  onToggleCollapsed?: () => void
  user?: MeResponse | null
}) {
  const [loggingOut, setLoggingOut] = useState(false)

  // 계정 카드 표시값 — 실제 로그인 사용자(user). 아직 로딩 전이거나 실패하면
  // 자리표시자로 폴백한다(하드코딩된 "대표/관리자"를 마지막 안전망으로 둔다).
  const displayName = user?.name || '사용자'
  const displayRole = user?.orgRole?.title || (user?.isOrgAdmin ? '관리자' : '구성원')
  const avatarChar = (user?.name || '·').trim().charAt(0) || '·'

  // 로그아웃 배선: 서버 세션 쿠키를 지운 뒤(POST /_api/auth/logout) AuthGate를
  // 미인증으로 되돌린다(emitUnauthorized → LoginScreen). 서버 호출이 실패해도
  // 클라이언트 관점의 로그아웃(화면 전환)은 반드시 수행한다 — 쿠키는 HttpOnly라
  // 서버가 실제로 지우지만, 사용자를 로그인 화면으로 보내는 것 자체는 실패해도
  // 막지 않는다.
  const handleLogout = async () => {
    setLoggingOut(true)
    try {
      await authApi.logout()
    } catch {
      // 서버 세션이 이미 없거나 네트워크 실패 — 어느 쪽이든 아래에서 로그인 화면으로.
    } finally {
      setLoggingOut(false)
      emitUnauthorized()
    }
  }

  return (
    <aside
      style={{
        width: collapsed ? SIDEBAR_WIDTH_COLLAPSED : SIDEBAR_WIDTH,
        flex: `0 0 ${collapsed ? SIDEBAR_WIDTH_COLLAPSED : SIDEBAR_WIDTH}px`,
        background: neutral.white,
        borderRight: `1px solid ${neutral.border}`,
        height: '100vh',
        position: 'sticky',
        top: 0,
        display: 'flex',
        flexDirection: 'column',
        transition: 'width .18s ease, flex-basis .18s ease',
      }}
      className="osiris-sidebar"
    >
      {/* 브랜드 영역 — 라이트 사이드바 위이므로 lapis-600 (금 마크 금지, 41 §5.1) */}
      <div
        style={{
          height: 64,
          display: 'flex',
          alignItems: 'center',
          gap: 8,
          padding: collapsed ? '0 12px' : '0 12px 0 20px',
          justifyContent: collapsed ? 'center' : 'space-between',
          borderBottom: `1px solid ${neutral.borderLight}`,
        }}
      >
        {!collapsed && (
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, minWidth: 0 }}>
            <span style={{ color: lapis[600], display: 'flex' }}>
              <BrandMark size={24} />
            </span>
            <Text style={{ fontSize: 15, fontWeight: 700, color: neutral.text }}>OSiris</Text>
          </div>
        )}
        {onToggleCollapsed && (
          <Tooltip title={collapsed ? '사이드바 펼치기' : '사이드바 접기'} placement="right">
            <Button
              type="text"
              size="small"
              onClick={onToggleCollapsed}
              icon={collapsed ? <MenuUnfoldOutlined /> : <MenuFoldOutlined />}
              aria-label={collapsed ? '사이드바 펼치기' : '사이드바 접기'}
            />
          </Tooltip>
        )}
      </div>

      <div style={{
          flex: 1,
          overflowY: 'auto',
          overflowX: 'hidden',
          padding: collapsed ? '8px 0' : 8,
        }}>
        {SCREEN_GROUPS.map((group, i) => (
          <div key={group.key}>
            {i > 0 && (
              <div
                style={{
                  height: 1,
                  background: neutral.borderLight,
                  margin: collapsed ? '8px 16px' : '8px 12px',
                }}
              />
            )}
            <Menu
              mode="inline"
              inlineCollapsed={collapsed}
              selectable
              selectedKeys={[active]}
              onClick={({ key }) => onNavigate(key as ScreenKey)}
              style={{ border: 'none' }}
              items={group.screens.map((s) => {
                const Icon = s.icon
                // 접혔을 때는 label을 문자열로 — antd가 붙여 주는 자체 툴팁이
                // ReactNode label에는 뜨지 않기 때문이다(위 주석).
                const showBadgeInline = s.key === 'approvals' && !collapsed
                return {
                  key: s.key,
                  icon:
                    s.key === 'approvals' && collapsed ? (
                      <Badge dot count={approvalsPending}>
                        <Icon />
                      </Badge>
                    ) : (
                      <Icon />
                    ),
                  label: showBadgeInline ? (
                    <span
                      style={{
                        display: 'flex',
                        justifyContent: 'space-between',
                        alignItems: 'center',
                        width: '100%',
                      }}
                    >
                      <span>{s.label}</span>
                      <Badge count={approvalsPending} size="small" />
                    </span>
                  ) : (
                    s.label
                  ),
                  style: { height: 40, borderRadius: 8 },
                }
              })}
            />
          </div>
        ))}
      </div>

      {/* 설정 — 스크롤되는 목록과 분리해 하단에 고정한다. */}
      <div
        style={{
          borderTop: `1px solid ${neutral.borderLight}`,
          padding: collapsed ? '8px 0' : 8,
        }}
      >
        <Menu
          mode="inline"
          inlineCollapsed={collapsed}
          selectable
          selectedKeys={active === 'settings' ? ['settings'] : []}
          onClick={({ key }) => onNavigate(key as ScreenKey)}
          style={{ border: 'none' }}
          items={[
            {
              key: SETTINGS_SCREEN.key,
              icon: <SETTINGS_SCREEN.icon />,
              label: SETTINGS_SCREEN.label,
              style: { height: 40, borderRadius: 8 },
            },
          ]}
        />
      </div>

      {/* 계정 카드 — 온보딩 단계에서는 실제 인증 정보가 없으므로 자리표시자 */}
      <div
        style={{
          borderTop: `1px solid ${neutral.borderLight}`,
          padding: 12,
          display: 'flex',
          alignItems: 'center',
          justifyContent: collapsed ? 'center' : undefined,
          gap: 8,
        }}
      >
        <Avatar style={{ background: lapis[100], color: lapis[700], fontWeight: 700, flexShrink: 0 }}>
          {avatarChar}
        </Avatar>
        {!collapsed && (
          <>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div
                style={{
                  fontSize: 13,
                  fontWeight: 600,
                  color: neutral.text,
                  overflow: 'hidden',
                  textOverflow: 'ellipsis',
                  whiteSpace: 'nowrap',
                }}
              >
                {displayName}
              </div>
              <div style={{ fontSize: 11, color: neutral.gray500 }}>{displayRole}</div>
            </div>
            <Tooltip title="로그아웃">
              <Button
                type="text"
                size="small"
                icon={<LogoutOutlined />}
                aria-label="로그아웃"
                loading={loggingOut}
                onClick={handleLogout}
              />
            </Tooltip>
          </>
        )}
      </div>
    </aside>
  )
}
