import { Alert, Button, Space, Typography } from 'antd'
import { useVerifyGateway } from '../../../hooks/useOnboarding'
import { StatusTag } from '../../../components/StatusTag'

const { Paragraph } = Typography

/**
 * 0-5 — 게이트웨이 헬스 검증. §1.0.3이 명시적으로 경고한 대로 "헬스만으로는
 * 불충분"하다(빈 DB에서도 200) — 그래서 (a)(b)(c) 세 신호를 각각 보여준다.
 */
export function VerifyStep() {
  const mutation = useVerifyGateway()
  const result = mutation.data

  return (
    <Space direction="vertical" style={{ width: '100%' }} size={12}>
      <Paragraph type="secondary" style={{ fontSize: 12, margin: 0 }}>
        헬스체크(a)만으로는 "빈 상태에서도 200"이라 불충분합니다 — 별칭 동기화(b)와 orchestrator
        재기동 확인(c)까지 셋 다 참이어야 통과합니다.
      </Paragraph>
      <Button type="primary" loading={mutation.isPending} onClick={() => mutation.mutate()}>
        헬스 검증 실행
      </Button>
      {mutation.isError && (
        <Alert type="error" showIcon message={(mutation.error as Error).message} />
      )}
      {result && (
        <Space direction="vertical" size={6}>
          <div>
            (a) 헬스 경로 200:{' '}
            {result.gatewayHealthOk ? (
              <StatusTag kind="success">OK</StatusTag>
            ) : (
              <StatusTag kind="error">실패</StatusTag>
            )}
          </div>
          <div>
            (b) 별칭 3개 inSync:{' '}
            {result.aliasesInSync ? (
              <StatusTag kind="success">OK</StatusTag>
            ) : (
              <StatusTag kind="error">미동기화</StatusTag>
            )}
          </div>
          <div>
            (c) orchestrator 재기동 확인:{' '}
            {result.propagatedToOrchestrator ? (
              <StatusTag kind="success">OK</StatusTag>
            ) : (
              <StatusTag kind="warning">대기 중</StatusTag>
            )}
          </div>
          {result.ready ? (
            <Alert
              type="success"
              showIcon
              message="0단계 통과 — 1단계(대화형) 온보딩을 시작할 수 있습니다."
            />
          ) : (
            <Alert
              type="warning"
              showIcon
              message="아직 통과하지 못했습니다. 위 실패 항목을 확인하고 재시도하세요."
            />
          )}
        </Space>
      )}
    </Space>
  )
}
