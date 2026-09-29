import { Collapse, Tabs, Tag, Typography } from 'antd'
import { ApiOutlined, ExperimentOutlined } from '@ant-design/icons'
import { ProviderConnectionsSection } from './ProviderConnectionsSection'
import { OmnirouteSettingsPage } from './OmnirouteSettingsPage'
import { neutral } from '../../theme/tokens'

const { Paragraph, Text } = Typography

/**
 * omniroute 설정 — 좌측 설정메뉴의 한 항목에 대응하는 우측 페이지.
 *
 * ── [2026-09-11] IA (감독 지시, 반복 조정 반영) ───────────────────────────
 *
 * 프로바이더 연결·모델 별칭을 각각 좌측 메뉴로 쪼개지 않는다 — 한 관심사(LLM
 * 배선)이므로 좌측에는 `omniroute 설정` 항목 하나만 두고, 그 안을 **탭**으로
 * 가른다(감독이 고른 탭 구조가 여기 페이지 내부에 적용).
 *
 * 원본 콘솔(omniroute UI 임베드)은 **탭이 아니라 이 페이지 하단의 접이식**이다.
 * 처음엔 설정 화면 공통 바닥에 뒀는데, 감독이 "공통 영역이 아니라 omniroute
 * 설정의 하단이어야 한다"고 명확히 했다 — 그 임베드는 omniroute 설정에만
 * 딸린 고급 기능이지 다른 설정 항목과 무관하기 때문이다. 그래서 이 페이지가
 * 선택됐을 때만, 탭 아래에 `───── 고급 설정 ─────` + 접이식(기본 접힘)으로
 * 나타난다.
 */
export function OmnirouteConfigPage() {
  return (
    // 페이지를 세로로 꽉 채운다: [헤더] · [탭 영역 flex:1, 내부 스크롤] · [고급 설정 하단 고정].
    // 이 구조가 "탭 내용 높이에 따라 고급 설정 구분선이 떠다니는" 어색함을 없앤다 —
    // 고급 설정은 탭 흐름 밖에 있고 항상 페이지 맨 아래 같은 자리에 놓인다.
    <div style={{ display: 'flex', flexDirection: 'column', flex: 1, minHeight: 0, height: '100%' }}>
      <div style={{ marginBottom: 8, flexShrink: 0 }}>
        <Text strong style={{ fontSize: 18 }}>
          omniroute 설정
        </Text>
        <Paragraph type="secondary" style={{ margin: '2px 0 0', fontSize: 13 }}>
          조직의 LLM 배선 — 프로바이더 계정·모델 별칭은 omniroute가 단일 보관처다.
        </Paragraph>
      </div>

      {/* 탭 영역 — 남은 높이를 전부 차지하고, 탭 내용이 길면 이 안에서만 스크롤한다.
          그래서 짧은 탭(모델 별칭)이든 긴 탭(프로바이더 연결)이든 아래 고급 설정을
          밀어 올리거나 내리지 않는다. */}
      <div
        style={{ flex: 1, minHeight: 0, overflowY: 'auto', paddingRight: 4 }}
        className="osiris-omni-tabs"
      >
        <Tabs
          defaultActiveKey="providers"
          items={[
            {
              key: 'providers',
              label: (
                <span>
                  <ApiOutlined /> 프로바이더 연결
                </span>
              ),
              children: <ProviderConnectionsSection />,
            },
            {
              key: 'aliases',
              label: (
                <span>
                  <ExperimentOutlined /> 모델 별칭
                </span>
              ),
              children: (
                <div style={{ padding: 48, textAlign: 'center', color: neutral.gray500, fontSize: 13 }}>
                  planner · worker · cheap 별칭 매핑 — 프로바이더 연결이 배선된 뒤 이어서 배선합니다.
                </div>
              ),
            },
          ]}
        />
      </div>

      {/* ───────────── 고급 설정 — 탭 흐름 밖, 페이지 하단에 고정 ───────────── */}
      <div style={{ flexShrink: 0, marginTop: 12 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 12, margin: '0 4px 8px' }}>
          <div style={{ flex: 1, height: 1, background: neutral.border }} />
          <Text style={{ fontSize: 12, fontWeight: 600, letterSpacing: 1, color: neutral.gray500 }}>
            고급 설정
          </Text>
          <div style={{ flex: 1, height: 1, background: neutral.border }} />
        </div>

        <Collapse
          defaultActiveKey={[]}
          items={[
            {
              key: 'omniroute-embed',
              label: (
                <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <ApiOutlined />
                  <span style={{ fontWeight: 600 }}>omniroute 원본 콘솔</span>
                  <Tag style={{ marginInlineStart: 4 }}>직접 경유</Tag>
                </span>
              ),
              children: (
                <>
                  <Paragraph type="secondary" style={{ marginTop: 0, fontSize: 12 }}>
                    omniroute 관리 UI를 그대로 임베드해, OSiris가 아직 위 탭으로 배선하지 않은 설정을{' '}
                    <Text strong>원본 UI에서 직접</Text> 다룹니다. 위 탭이 채워질수록 쓸 일이 줄어드는
                    탈출구입니다.
                  </Paragraph>
                  {/* 펼쳤을 때 뷰포트 비율로 높이를 잡는다 — 화면이 작아도 고정 640px가
                      페이지를 넘겨 버리지 않도록. */}
                  <div style={{ height: '62vh', minHeight: 420, display: 'flex', flexDirection: 'column' }}>
                    <OmnirouteSettingsPage />
                  </div>
                </>
              ),
            },
          ]}
        />
      </div>
    </div>
  )
}
