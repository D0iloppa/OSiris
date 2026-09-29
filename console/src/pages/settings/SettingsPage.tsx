import { useState } from 'react'
import { Empty, Menu, Typography } from 'antd'
import {
  ApiOutlined,
  ClusterOutlined,
  KeyOutlined,
  SafetyCertificateOutlined,
  UserOutlined,
} from '@ant-design/icons'
import { OmnirouteConfigPage } from './OmnirouteConfigPage'
import { MeSettingsPage } from './MeSettingsPage'
import { neutral, radius } from '../../theme/tokens'

const { Paragraph } = Typography

/**
 * 설정 — **2단 full-bleed** (감독 지시, BlueUrban 그룹웨어 참조).
 *
 * ── [2026-09-11] IA ───────────────────────────────────────────────────────
 *
 * 외부 패딩을 거의 두지 않고 화면을 꽉 채운다: 좌측에 설정 **메뉴**, 우측에 그
 * 메뉴의 **페이지**. 큰 인페이지 타이틀("설정 / 개인 설정과 시스템 설정")은
 * 두지 않는다 — 탑바가 이미 "설정"을 표시하고, 공간을 먹는 헤더가 full-bleed
 * 의도와 어긋난다.
 *
 * 좌측 메뉴는 **관심사 단위**로만 나눈다 — 프로바이더·별칭·원본 콘솔처럼 한
 * 관심사(LLM 배선)에 속하는 것들을 각각 메뉴로 쪼개지 않는다. 그것들은
 * `omniroute 설정` **한 항목**의 우측 페이지 안에서 탭으로 갈린다
 * (OmnirouteConfigPage). 항목이 늘면 이 좌측 메뉴에 새 관심사를 더한다.
 */
type SettingKey = 'me' | 'omniroute' | 'identity' | 'machine-principals' | 'cluster'

interface SettingItem {
  key: SettingKey
  label: string
  desc: string
  icon: React.ComponentType
}

const PERSONAL: SettingItem[] = [
  { key: 'me', label: '내 정보', desc: '프로필, 비밀번호', icon: UserOutlined },
]

const SYSTEM: SettingItem[] = [
  { key: 'omniroute', label: 'omniroute 설정', desc: '프로바이더·별칭·LLM 배선', icon: ApiOutlined },
  { key: 'identity', label: '자격증명', desc: '사람 계정·등록 티켓', icon: SafetyCertificateOutlined },
  { key: 'machine-principals', label: '기계 주체', desc: '읽기 전용 API 자격증명', icon: KeyOutlined },
  { key: 'cluster', label: '클러스터', desc: '노드·자원 상한', icon: ClusterOutlined },
]

const ALL = [...PERSONAL, ...SYSTEM]

export function SettingsPage() {
  const [active, setActive] = useState<SettingKey>('omniroute')
  const current = ALL.find((i) => i.key === active)

  return (
    <div style={{ display: 'flex', flex: 1, minHeight: 0, height: '100%', background: neutral.canvas }}>
      {/* 좌측 설정 메뉴 — 앱 사이드바에 이어 붙는 두 번째 열. */}
      <nav
        style={{
          width: 260,
          flex: '0 0 260px',
          borderRight: `1px solid ${neutral.border}`,
          background: neutral.white,
          overflowY: 'auto',
          padding: '12px 8px',
        }}
      >
        <Menu
          mode="inline"
          selectedKeys={[active]}
          onClick={({ key }) => setActive(key as SettingKey)}
          style={{ border: 'none', background: 'transparent' }}
          items={[
            { type: 'group', label: '개인 설정', children: PERSONAL.map(toMenuItem) },
            { type: 'divider' },
            { type: 'group', label: '시스템 설정', children: SYSTEM.map(toMenuItem) },
          ]}
        />
      </nav>

      {/* 우측 — 선택된 메뉴의 페이지. */}
      <section style={{ flex: 1, minWidth: 0, overflowY: 'auto', padding: '24px 28px 40px' }}>
        {active === 'omniroute' ? (
          <OmnirouteConfigPage />
        ) : active === 'me' ? (
          <MeSettingsPage />
        ) : (
          <NotReady label={current?.label ?? ''} desc={current?.desc ?? ''} />
        )}
      </section>
    </div>
  )
}

function NotReady({ label, desc }: { label: string; desc: string }) {
  return (
    <div
      style={{
        background: neutral.white,
        border: `1px solid ${neutral.border}`,
        borderRadius: radius.xl,
        padding: 64,
        display: 'flex',
        justifyContent: 'center',
      }}
    >
      <Empty
        image={Empty.PRESENTED_IMAGE_SIMPLE}
        description={
          <>
            <div style={{ fontWeight: 600, color: neutral.text }}>{label}</div>
            <Paragraph type="secondary" style={{ margin: '4px 0 0', fontSize: 13 }}>
              {desc} — 준비 중입니다.
            </Paragraph>
          </>
        }
      />
    </div>
  )
}

function toMenuItem(i: SettingItem) {
  const Icon = i.icon
  return {
    key: i.key,
    icon: <Icon />,
    label: (
      <span style={{ display: 'block', lineHeight: 1.25, padding: '4px 0' }}>
        <span style={{ display: 'block', fontSize: 13, fontWeight: 600 }}>{i.label}</span>
        <span style={{ display: 'block', fontSize: 11, color: neutral.gray500 }}>{i.desc}</span>
      </span>
    ),
    style: { height: 'auto', borderRadius: 8, marginBottom: 2 },
  }
}
