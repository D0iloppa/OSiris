import { Alert, Button, Space, Typography, message } from 'antd'
import { CopyOutlined } from '@ant-design/icons'
import { useIssueGatewayToken } from '../../../hooks/useOnboarding'
import type { OnboardingStep0Status } from '../../../api/types'
import { neutral, radius } from '../../../theme/tokens'

const { Text, Paragraph } = Typography

/**
 * 0-4 — 게이트웨이 토큰 발급(`issueOrGetGatewayToken` +
 * `applyGatewayTokenToEnv`). 이 시점에 `osiris-orchestrator` 재기동이
 * 필요하다(30 §1.0.3) — 알파에서는 수동이므로, Ops "노드 join" 모달과
 * 동일한 패턴(복사 가능한 커맨드)을 재사용한다(41 §5.4 매핑, 이 화면은
 * 그 패턴의 첫 적용 사례).
 */
export function GatewayTokenStep({ step0 }: { step0: OnboardingStep0Status }) {
  const mutation = useIssueGatewayToken()

  if (step0.gatewayToken.issued && mutation.data === undefined) {
    return (
      <Space direction="vertical">
        <Text>토큰이 이미 발급되어 있습니다.</Text>
        <Text type="secondary" style={{ fontSize: 12 }}>
          재기동이 아직 반영되지 않았다면 다음 단계(헬스 검증)에서 확인할 수 있습니다.
        </Text>
      </Space>
    )
  }

  return (
    <Space direction="vertical" style={{ width: '100%' }} size={12}>
      <Button
        type="primary"
        loading={mutation.isPending}
        onClick={() => mutation.mutate()}
      >
        토큰 발급
      </Button>
      {mutation.isError && (
        <Alert type="error" showIcon message={(mutation.error as Error).message} />
      )}
      {mutation.data?.needsOrchestratorRestart && (
        <Alert
          type="warning"
          showIcon
          message="orchestrator 재기동이 필요합니다"
          description={
            <Space direction="vertical" style={{ width: '100%' }}>
              <Paragraph style={{ margin: 0, fontSize: 12 }}>
                아래 커맨드를 서버에서 실행해 주세요. 완료 후 다음 단계(헬스 검증)로 넘어가면
                재기동 여부를 자동으로 확인합니다.
              </Paragraph>
              <div
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: 8,
                  background: neutral.gray50,
                  border: '1px solid var(--ant-color-border)',
                  borderRadius: radius.md,
                  padding: '8px 12px',
                  fontFamily: 'var(--osiris-font-mono)',
                  fontSize: 12,
                }}
              >
                <code style={{ flex: 1 }}>{mutation.data.restartCommand}</code>
                <Button
                  size="small"
                  icon={<CopyOutlined />}
                  onClick={() => {
                    navigator.clipboard.writeText(mutation.data!.restartCommand)
                    message.success('복사했습니다')
                  }}
                >
                  복사
                </Button>
              </div>
            </Space>
          }
        />
      )}
    </Space>
  )
}
