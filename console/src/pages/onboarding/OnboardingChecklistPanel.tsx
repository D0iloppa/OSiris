import { Card, Typography } from 'antd'
import type { OnboardingStatusResponse } from '../../api/types'
import { neutral } from '../../theme/tokens'

const { Text, Title } = Typography

/**
 * "정의 현황" 우측 패널 (40-console-ia.md #6 와이어프레임 — 대화 중에도
 * 진행 상황을 한눈에). 0단계에서는 조직이 항상 비어 있으므로(팀 0개 등)
 * 이 패널은 "아직 아무것도 시작되지 않았다"를 정직하게 보여주는 것이
 * 목적 — GET /onboarding/status의 org 필드를 그대로 표시할 뿐 프론트가
 * 재계산하지 않는다.
 */
export function OnboardingChecklistPanel({ status }: { status: OnboardingStatusResponse }) {
  const { org, phase } = status

  const row = (label: string, mark: string, value: string) => (
    <div style={{ display: 'flex', justifyContent: 'space-between', padding: '6px 0' }}>
      <Text style={{ fontSize: 13 }}>
        {mark} {label}
      </Text>
      <Text type="secondary" style={{ fontSize: 13 }}>
        {value}
      </Text>
    </div>
  )

  return (
    <Card
      title={
        <Title level={5} style={{ margin: 0, fontSize: 15, fontWeight: 600 }}>
          정의 현황
        </Title>
      }
      style={{ borderRadius: 12 }}
    >
      <Text type="secondary" style={{ fontSize: 11, textTransform: 'uppercase' }}>
        phase: {phase}
      </Text>
      <div style={{ marginTop: 12, borderTop: `1px solid ${neutral.borderLight}` }}>
        {row('팀', org.teams > 0 ? '✓' : '○', `${org.teams}개`)}
        {row('직무', org.roles > 0 ? '✓' : '○', `${org.roles}개`)}
        {row(
          'capability 매핑',
          org.capabilityMappingRatio === null
            ? '○'
            : org.capabilityMappingRatio >= 1
              ? '✓'
              : '◐',
          org.capabilityMappingRatio === null
            ? '미정의'
            : `${Math.round(org.capabilityMappingRatio * 100)}%`,
        )}
        {row('승인정책', org.approvalPolicyDefined ? '✓' : '○', org.approvalPolicyDefined ? '정의됨' : '미정의')}
      </div>
    </Card>
  )
}
