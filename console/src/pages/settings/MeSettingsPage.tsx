import { useEffect, useState } from 'react'
import { Alert, Button, Card, Descriptions, Form, Input, Space, Spin, Tag, Typography } from 'antd'
import { meApi, type MeResponse } from '../../api/me'
import { ApiError } from '../../api/http'
import { neutral, radius } from '../../theme/tokens'

const { Title, Paragraph, Text } = Typography

/**
 * 개인 설정 → 내 정보. 두 블록:
 *
 *   (a) 프로필 — `GET /_api/me`를 읽기 전용으로 보여준다. 표시 규칙 자체는
 *       (§3.5/§3.3처럼) 전부 서버가 이미 계산해 준 값이고, 이 화면은 그것을
 *       그대로 그릴 뿐이다 — 새 판단을 프론트에 만들지 않는다.
 *   (b) 비밀번호 변경 — `POST /_api/me/password`. 현재 비밀번호 재인증은
 *       서버가 강제하지만(§5.6 state C), 클라이언트도 최소 12자·확인 일치를
 *       먼저 검증해 왕복 한 번을 아낀다. 서버가 궁극적 판정자라는 점은 그대로다.
 *
 * 보안 규칙은 LoginScreen과 동일: 비밀번호 값은 antd Form이 제출 시점까지만
 * 들고 있다가 한 번 흘려보내고, 성공/실패와 무관하게 즉시 `resetFields()`된다.
 * `useState`에도, localStorage/sessionStorage/URL에도 담기지 않는다.
 */
export function MeSettingsPage() {
  return (
    <Space direction="vertical" size={20} style={{ width: '100%', maxWidth: 640 }}>
      <ProfileCard />
      <PasswordCard />
    </Space>
  )
}

function ProfileCard() {
  const [me, setMe] = useState<MeResponse | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)

  useEffect(() => {
    let cancelled = false
    meApi
      .getMe()
      .then((res) => {
        if (!cancelled) setMe(res)
      })
      .catch((e) => {
        if (!cancelled) setError(e instanceof ApiError ? e.message : '내 정보를 불러오지 못했습니다.')
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [])

  return (
    <Card
      style={{ borderRadius: radius.xl, border: `1px solid ${neutral.border}` }}
      styles={{ body: { padding: 24 } }}
    >
      <Title level={5} style={{ marginTop: 0 }}>
        프로필
      </Title>
      {loading && <Spin />}
      {error && <Alert type="error" showIcon message={error} />}
      {me && !loading && !error && (
        <Descriptions column={1} size="small" labelStyle={{ width: 120 }}>
          <Descriptions.Item label="ID">{me.partyId ?? '—'}</Descriptions.Item>
          <Descriptions.Item label="이름">{me.name ?? '—'}</Descriptions.Item>
          <Descriptions.Item label="Email">{me.email ?? '—'}</Descriptions.Item>
          <Descriptions.Item label="조직 역할">{me.orgRole?.title ?? '—'}</Descriptions.Item>
          <Descriptions.Item label="권한">
            <Space size={4} wrap>
              {me.effectivePermissions.length > 0 ? (
                me.effectivePermissions.map((p) => (
                  <Tag key={p} bordered={false}>
                    {p}
                  </Tag>
                ))
              ) : (
                <Text type="secondary">—</Text>
              )}
            </Space>
          </Descriptions.Item>
        </Descriptions>
      )}
    </Card>
  )
}

interface PasswordFormValues {
  currentPassword: string
  newPassword: string
  confirmPassword: string
}

const MIN_PASSWORD_LENGTH = 12

function PasswordCard() {
  const [form] = Form.useForm<PasswordFormValues>()
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [success, setSuccess] = useState(false)

  const handleSubmit = async (values: PasswordFormValues) => {
    setSubmitting(true)
    setError(null)
    setSuccess(false)
    try {
      await meApi.changePassword(values.currentPassword, values.newPassword)
      setSuccess(true)
    } catch (e) {
      // 서버 메시지를 그대로 보여준다 — "현재 비밀번호가 올바르지 않습니다" /
      // "비밀번호는 최소 12자여야 합니다"는 이미 인증된 본인 계정에 대한
      // 판정이라 §8.2의 균일 메시지 규칙(로그인 오라클 방지)이 적용되지
      // 않는다(서버 ChangePassword 주석 참고).
      setError(e instanceof ApiError ? e.message : '비밀번호 변경에 실패했습니다.')
    } finally {
      // 세 필드 모두 즉시 지운다 — 성공/실패 어느 쪽이든 값을 화면에 남기지
      // 않는다.
      form.resetFields()
      setSubmitting(false)
    }
  }

  return (
    <Card
      style={{ borderRadius: radius.xl, border: `1px solid ${neutral.border}` }}
      styles={{ body: { padding: 24 } }}
    >
      <Title level={5} style={{ marginTop: 0 }}>
        비밀번호 변경
      </Title>
      <Paragraph type="secondary" style={{ fontSize: 13, marginTop: -4 }}>
        현재 비밀번호를 다시 확인한 뒤에만 변경됩니다. 새 비밀번호는 최소 {MIN_PASSWORD_LENGTH}자
        이상이어야 합니다.
      </Paragraph>

      <Form form={form} layout="vertical" onFinish={handleSubmit}>
        <Form.Item
          name="currentPassword"
          label="현재 비밀번호"
          rules={[{ required: true, message: '현재 비밀번호를 입력하세요' }]}
        >
          <Input.Password autoComplete="current-password" placeholder="현재 비밀번호" />
        </Form.Item>
        <Form.Item
          name="newPassword"
          label="새 비밀번호"
          rules={[
            { required: true, message: '새 비밀번호를 입력하세요' },
            {
              min: MIN_PASSWORD_LENGTH,
              message: `새 비밀번호는 최소 ${MIN_PASSWORD_LENGTH}자여야 합니다`,
            },
          ]}
        >
          <Input.Password autoComplete="new-password" placeholder="새 비밀번호" />
        </Form.Item>
        <Form.Item
          name="confirmPassword"
          label="새 비밀번호 확인"
          dependencies={['newPassword']}
          rules={[
            { required: true, message: '새 비밀번호를 한 번 더 입력하세요' },
            ({ getFieldValue }) => ({
              validator(_, value) {
                if (!value || getFieldValue('newPassword') === value) return Promise.resolve()
                return Promise.reject(new Error('새 비밀번호가 서로 다릅니다'))
              },
            }),
          ]}
        >
          <Input.Password autoComplete="new-password" placeholder="새 비밀번호 확인" />
        </Form.Item>

        {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 12 }} />}
        {success && (
          <Alert
            type="success"
            showIcon
            message="비밀번호가 변경되었습니다."
            style={{ marginBottom: 12 }}
          />
        )}

        <Button type="primary" htmlType="submit" loading={submitting}>
          비밀번호 변경
        </Button>
      </Form>
    </Card>
  )
}
