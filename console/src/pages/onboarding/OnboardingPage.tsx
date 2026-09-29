import { Alert, Button, Row, Col, Spin, Typography } from 'antd'
import { PageHeader } from '../../layout/PageHeader'
import { useOnboardingStatus } from '../../hooks/useOnboarding'
import { Step0Wizard } from './Step0Wizard'
import { OnboardingChecklistPanel } from './OnboardingChecklistPanel'

const { Paragraph } = Typography

/**
 * Onboarding 화면 (40-console-ia.md #6). 이번 구현 범위는 **0단계만**
 * (30-onboarding-provisioning.md §1.0) — 1단계(대화형, bootstrap-master와의
 * 대화)는 백엔드 LLM 게이트웨이가 실제로 서 있어야 의미가 있으므로 이번
 * 범위 밖이다(작업 지시 §제외). 0단계가 통과(`step0.ready`)하면 이
 * 화면은 "1단계 대기" 안내로 전환된다.
 */
export function OnboardingPage() {
  const statusQuery = useOnboardingStatus()

  return (
    <div className="osiris-content-max" style={{ padding: '28px 38px 44px' }}>
      <PageHeader
        title="Onboarding"
        description="조직 정의 — 온보딩 0단계(결정적 스택 배선)부터 순서대로 진행합니다"
      />

      {statusQuery.isLoading && <Spin />}

      {statusQuery.isError && (
        <Alert
          type="error"
          showIcon
          message="상태를 불러오지 못했습니다"
          description={(statusQuery.error as Error).message}
          action={
            <Button size="small" onClick={() => statusQuery.refetch()}>
              다시 시도
            </Button>
          }
        />
      )}

      {statusQuery.data && (
        <Row gutter={24}>
          <Col flex="auto" style={{ minWidth: 0 }}>
            {statusQuery.data.step0.ready ? (
              <Alert
                type="success"
                showIcon
                message="0단계 완료"
                description={
                  <Paragraph style={{ margin: 0, fontSize: 13 }}>
                    결정적 스택 배선이 전부 통과했습니다. 1단계(대화형 온보딩 — bootstrap-master와의
                    대화)는 이 빌드의 구현 범위 밖입니다.
                  </Paragraph>
                }
              />
            ) : (
              <Step0Wizard step0={statusQuery.data.step0} />
            )}
          </Col>
          <Col flex="320px">
            <OnboardingChecklistPanel status={statusQuery.data} />
          </Col>
        </Row>
      )}
    </div>
  )
}
