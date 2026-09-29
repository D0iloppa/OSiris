import { Alert, Button, Form, Input, Space, Typography } from 'antd'
import { useState } from 'react'
import { useExchangeAdmin } from '../../../hooks/useOnboarding'
import { StatusTag } from '../../../components/StatusTag'
import type { OnboardingStep0Status } from '../../../api/types'
import { useOmnirouteConsole } from '../../../hooks/useOmnirouteConsole'

const { Text, Link } = Typography

/**
 * 0-1a/0-1b — 관리자 자격증명 교환 (30 §1.0.3, §1.0.4/O13).
 *
 * 보안 노트(작업 지시 필수 요구사항): 비밀번호는 이 컴포넌트의 어떤
 * `useState`에도 담기지 않는다 — antd `Form`이 제출 시점까지만 내부적으로
 * 들고 있다가 `onFinish(values)`로 한 번 흘려보내고, 성공/실패와 무관하게
 * `onSettled`에서 즉시 `resetFields()`한다. localStorage/sessionStorage/
 * URL 어디에도 쓰지 않는다(그 경로 자체가 코드에 없다).
 */
export function AdminCredentialStep({ step0 }: { step0: OnboardingStep0Status }) {
  const [form] = Form.useForm<{ password: string }>()
  const mutation = useExchangeAdmin()
  const [showForm, setShowForm] = useState(!step0.omniroute.manageKeyExchanged)
  const omniUrl = useOmnirouteConsole().url

  if (step0.omniroute.manageKeyExchanged && !showForm) {
    return (
      <Space direction="vertical">
        <StatusTag kind="success">매니지 키 교환 완료</StatusTag>
        <Text type="secondary" style={{ fontSize: 12 }}>
          omniroute 관리 API 인증은 이제 저장된 매니지 키(Bearer)로 조용히 이루어집니다 — 비밀번호는
          이 교환 순간에만 메모리에 존재했다가 폐기되었습니다.
        </Text>
        <Button size="small" onClick={() => setShowForm(true)}>
          키 재교환(회전)
        </Button>
      </Space>
    )
  }

  return (
    <Space direction="vertical" style={{ width: '100%' }} size={12}>
      <Alert
        type="info"
        showIcon
        message="omniroute 자체 셋업이 먼저입니다"
        description={
          <span>
            아직 omniroute 관리자 비밀번호를 설정하지 않았다면{' '}
            <Link href={omniUrl} target="_blank" rel="noreferrer">
              {omniUrl}
            </Link>
            을 열어 초기 비밀번호부터 설정하세요. 그 값을 아래에 한 번만 입력하면, OSiris는
            비밀번호 대신 그 결과로 발급된 매니지 키만 저장합니다(비밀번호 자체는 저장하지
            않습니다 — O13).
          </span>
        }
      />
      <Form<{ password: string }>
        form={form}
        layout="vertical"
        onFinish={(values) => {
          mutation.mutate(values.password, {
            onSettled: () => form.resetFields(),
          })
        }}
      >
        <Form.Item
          name="password"
          label="omniroute 관리자 비밀번호"
          rules={[{ required: true, message: '비밀번호를 입력하세요' }]}
        >
          <Input.Password autoComplete="new-password" placeholder="••••••••" />
        </Form.Item>
        {mutation.isError && (
          <Alert
            type="error"
            showIcon
            message={(mutation.error as Error).message}
            style={{ marginBottom: 12 }}
          />
        )}
        <Button type="primary" htmlType="submit" loading={mutation.isPending}>
          교환
        </Button>
      </Form>
    </Space>
  )
}
