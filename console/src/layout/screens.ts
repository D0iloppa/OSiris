import {
  ApartmentOutlined,
  CheckSquareOutlined,
  ClusterOutlined,
  DesktopOutlined,
  InboxOutlined,
  MessageOutlined,
  NodeIndexOutlined,
  OrderedListOutlined,
  RocketOutlined,
  SettingOutlined,
  ShareAltOutlined,
} from '@ant-design/icons'

/**
 * 사이드바 화면 정의.
 *
 * ── [2026-09-11] 그룹 구조로 바뀐 이유 ────────────────────────────────────
 *
 * 초판은 40-console-ia.md A절("역할별 네비게이션 분기 없이 전원 동일
 * 사이드바")에 따라 **그룹 라벨 없는 평면 8항목**이었다. 그 원칙은 지금도
 * 유효하다 — 아래 구조는 **역할에 따라 달라지지 않는다.** 바뀐 것은
 * "누구에게 무엇을 보여주는가"가 아니라 "같은 항목을 어떻게 묶어 보여주는가"
 * 뿐이고, 평면 목록이 10항목을 넘기면서 관련 없는 화면들이 한 줄 간격으로
 * 붙어 있는 것이 읽기 어려워졌기 때문이다(감독 지시, BlueUrban 그룹웨어의
 * 사이드바를 참고 사례로 제시).
 *
 * 그룹은 **구분선으로만** 표현하고 접히는 서브메뉴로 만들지 않는다 — 클릭
 * 한 번으로 어디든 갈 수 있어야 한다는 성질(평면 목록의 실질적 장점)을
 * 잃지 않기 위해서다.
 *
 * `settings`는 이 목록에 넣지 않는다 — 사이드바 **하단에 고정**되는
 * 항목이라 스크롤되는 본 목록과 자리가 다르다(Sidebar.tsx 참고).
 */
export type ScreenKey =
  | 'chat'
  | 'org'
  | 'work'
  | 'work-map'
  | 'approvals'
  | 'ops'
  | 'infra'
  | 'onboarding'
  | 'ontology'
  | 'my-tasks'
  | 'settings'

export interface ScreenDef {
  key: ScreenKey
  label: string
  icon: React.ComponentType
}

/** 사이드바 그룹 — 구분선으로 나뉘는 묶음. 라벨은 두지 않는다(41 §5.1의
 *  "그룹 라벨 없는 사이드바"를 시각적으로는 그대로 유지). */
export interface ScreenGroup {
  key: string
  screens: ScreenDef[]
}

export const SCREEN_GROUPS: ScreenGroup[] = [
  {
    // 일상 진입점 — 대표가 가장 자주 여는 화면들.
    key: 'daily',
    screens: [
      { key: 'chat', label: 'Admin Chat', icon: MessageOutlined },
      { key: 'my-tasks', label: 'My Tasks', icon: InboxOutlined },
      { key: 'approvals', label: 'Approvals', icon: CheckSquareOutlined },
    ],
  },
  {
    // 일의 구조 — 무엇을 하고 있는가.
    key: 'work',
    screens: [
      { key: 'work', label: 'Work', icon: OrderedListOutlined },
      { key: 'work-map', label: '작업 지도', icon: NodeIndexOutlined },
    ],
  },
  {
    // 조직의 구조 — 누가/무엇이 있는가.
    key: 'structure',
    screens: [
      { key: 'org', label: 'Org', icon: ApartmentOutlined },
      { key: 'ontology', label: 'Ontology', icon: ShareAltOutlined },
    ],
  },
  {
    // 운영 — 시스템 자신에 대한 화면.
    key: 'platform',
    screens: [
      { key: 'ops', label: 'Ops', icon: DesktopOutlined },
      // [2026-09-11] 90-infra-topology.md §7 A단계 — Ops 옆에 신설.
      { key: 'infra', label: 'Infra', icon: ClusterOutlined },
      { key: 'onboarding', label: 'Onboarding', icon: RocketOutlined },
    ],
  },
]

/** 사이드바 하단 고정 항목. */
export const SETTINGS_SCREEN: ScreenDef = {
  key: 'settings',
  label: '설정',
  icon: SettingOutlined,
}

export const SCREENS: ScreenDef[] = [
  ...SCREEN_GROUPS.flatMap((g) => g.screens),
  SETTINGS_SCREEN,
]

export const SCREEN_TITLES: Record<ScreenKey, string> = Object.fromEntries(
  SCREENS.map((s) => [s.key, s.label]),
) as Record<ScreenKey, string>
