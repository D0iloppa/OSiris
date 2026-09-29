import { Avatar, Badge, Dropdown, Empty, Typography } from 'antd'
import { BellOutlined, LogoutOutlined } from '@ant-design/icons'
import { neutral, lapis } from '../theme/tokens'
import { SCREEN_TITLES, type ScreenKey } from './screens'
import { authApi } from '../api/auth'
import { emitUnauthorized } from '../api/authEvents'
import { USE_MOCK_API } from '../api/config'

const { Text } = Typography

/**
 * 탑바 (41 §5.2). 좌: 현재 화면명. 우: 알림 벨(뱃지+드롭다운) + 계정.
 *
 * 알림벨 도입은 DESIGN-admin.md §5.2("도입하지 않음")과 정면으로
 * 다른 결정이다 — 41 §5.2 "불일치 기록"이 이미 이 콘솔은
 * 40-console-ia.md를 따른다고 못박았으므로 그 결정을 그대로 구현한다.
 * Notifications(화면 #9)는 별도 라우트가 아니라 이 드롭다운이 전부다.
 */
export function Topbar({ activeScreen }: { activeScreen: ScreenKey }) {
  const unreadCount = 0 // 알림 백엔드 미구현 — 자리만 잡음(§구현 범위 밖)

  return (
    <header
      style={{
        height: 64,
        background: neutral.white,
        borderBottom: `1px solid ${neutral.border}`,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        padding: '0 24px',
        position: 'sticky',
        top: 0,
        zIndex: 110, // --osiris-z-topbar
      }}
    >
      <Text style={{ fontSize: 14, fontWeight: 600, color: neutral.text }}>
        {SCREEN_TITLES[activeScreen]}
      </Text>

      <div style={{ display: 'flex', alignItems: 'center', gap: 16 }}>
        <Dropdown
          trigger={['click']}
          dropdownRender={() => (
            <div
              style={{
                width: 320,
                background: neutral.white,
                borderRadius: 10,
                boxShadow: '0 4px 16px rgba(15,23,42,.08)',
                padding: 16,
              }}
            >
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description="알림이 없습니다"
                style={{ margin: '8px 0' }}
              />
            </div>
          )}
        >
          <Badge count={unreadCount} size="small">
            <BellOutlined style={{ fontSize: 18, color: neutral.gray600, cursor: 'pointer' }} />
          </Badge>
        </Dropdown>

        <Dropdown
          trigger={['click']}
          menu={{
            items: [
              {
                key: 'logout',
                icon: <LogoutOutlined />,
                label: '로그아웃',
                disabled: USE_MOCK_API,
                onClick: () => {
                  // 서버측 세션 무효화(§8.4) 후 AuthGate가 구독 중인
                  // emitUnauthorized로 토큰 입력 화면으로 되돌린다. mock
                  // 모드엔 세션 개념이 없어 비활성화한다.
                  authApi.logout().finally(() => emitUnauthorized())
                },
              },
            ],
          }}
        >
          <div style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer' }}>
            <Avatar size="small" style={{ background: lapis[100], color: lapis[700] }}>
              대
            </Avatar>
            <Text style={{ fontSize: 13 }}>대표</Text>
          </div>
        </Dropdown>
      </div>
    </header>
  )
}
