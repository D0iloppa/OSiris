import { Alert, Button, Card, Form, Input, Space, Tabs, Typography } from 'antd'
import { useState } from 'react'
import { authApi } from '../api/auth'
import { ApiError } from '../api/http'
import { BrandMark } from '../brand/BrandMark'
import { lapis, neutral, radius } from '../theme/tokens'

const { Title, Paragraph, Text } = Typography

/**
 * 로그인 화면 — **두 경로**를 한 화면의 탭으로 제공한다.
 *
 *   ① email + 비밀번호 (60-identity-authn.md §3.1) — 인수인계 이후의 정상 경로
 *   ② 마스터 키 (§4.1 ①) — 최초 설치 1회, 비밀번호를 세우는 순간 영구 폐기
 *
 * ── [2026-09-11] 이 화면이 TokenEntryScreen을 대체한 이유 ──────────────────
 *
 * 이전 화면(TokenEntryScreen)은 ②만 있었다. 그런데 ②는 ①이 생기는 순간
 * 죽는 값이다(H23) — 온보딩을 끝까지 진행한 조직은 콘솔에 로그인할 화면이
 * 하나도 없는 상태가 됐을 것이다. 서버(`authn.Exchange`)는 처음부터 세 경로를
 * 모두 받고 있었고 빠진 것은 입력창뿐이었다.
 *
 * **기본 탭은 ①(이메일)이다.** ②는 설치당 한 번 쓰고 영구히 닫히는 경로이므로
 * 정상 상태의 기본값이 될 수 없다. 다만 지금 어느 쪽이 유효한지를 화면이
 * 서버에 물어볼 방법은 없다 — 인증 없이 부트스트랩 상태를 알려주는 엔드포인트는
 * 존재하지 않고, 그런 엔드포인트를 새로 여는 것은 "조직에 관리자가 있는지"를
 * 미인증 호출자에게 알려주는 것이라 만들지 않았다. 그래서 판단을 화면이
 * 추측하지 않고 **두 탭을 모두 열어 두고 서버가 거절하게 한다**: ②는 폐기된
 * 뒤 403(H10), ①은 자격증명이 없으면 균일한 401(§8.2)로 답한다.
 *
 * 보안 규칙은 두 탭이 동일하다 — 값은 antd Form이 제출 시점까지만 들고 있다가
 * `onFinish`로 한 번 흘려보내고, 성공/실패와 무관하게 즉시 `resetFields()`된다.
 * 어떤 `useState`에도, localStorage/sessionStorage/URL에도 담기지 않는다.
 */
export function LoginScreen({ onAuthenticated }: { onAuthenticated: () => void }) {
  const [tab, setTab] = useState<'password' | 'masterKey'>('password')

  return (
    <div
      style={{
        minHeight: '100vh',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        background: neutral.canvas,
        padding: 24,
      }}
    >
      <Card
        style={{ width: 420, borderRadius: radius.xl, boxShadow: '0 12px 32px rgba(15,23,42,.10)' }}
        styles={{ body: { padding: 32 } }}
      >
        <Space direction="vertical" size={16} style={{ width: '100%' }}>
          <Space direction="vertical" size={8} style={{ width: '100%', alignItems: 'center' }}>
            <div style={{ color: lapis[600] }}>
              <BrandMark size={40} />
            </div>
            <Title level={4} style={{ margin: 0 }}>
              OSiris 콘솔
            </Title>
          </Space>

          <Tabs
            activeKey={tab}
            onChange={(k) => setTab(k as 'password' | 'masterKey')}
            items={[
              {
                key: 'password',
                label: '이메일 로그인',
                children: <PasswordForm onAuthenticated={onAuthenticated} />,
              },
              {
                key: 'masterKey',
                label: '마스터 키 (최초 설치)',
                children: <MasterKeyForm onAuthenticated={onAuthenticated} />,
              },
            ]}
          />
        </Space>
      </Card>
    </div>
  )
}

/** ① email + 비밀번호 — 인수인계 이후의 정상 로그인. */
function PasswordForm({ onAuthenticated }: { onAuthenticated: () => void }) {
  const [form] = Form.useForm<{ email: string; password: string }>()
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleSubmit = async (values: { email: string; password: string }) => {
    setSubmitting(true)
    setError(null)
    try {
      await authApi.loginWithPassword(values.email, values.password)
      onAuthenticated()
    } catch (e) {
      // 서버가 준 메시지를 그대로 쓴다 — "그런 계정이 없습니다" 류의 문구를
      // 이 화면이 만들어내면 §8.2가 일부러 균일하게 만든 401을 화면이 다시
      // 구분해 주는 꼴이 된다(계정 존재 여부 누출).
      setError(e instanceof ApiError ? e.message : '로그인에 실패했습니다.')
    } finally {
      // 비밀번호만 지운다 — email은 오타 정정을 위해 남긴다(시크릿이 아니다).
      form.resetFields(['password'])
      setSubmitting(false)
    }
  }

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Paragraph type="secondary" style={{ margin: 0, fontSize: 13 }}>
        조직에 등록된 본인의 email과 비밀번호로 로그인하세요.
      </Paragraph>

      <Form form={form} layout="vertical" onFinish={handleSubmit}>
        <Form.Item
          name="email"
          label="Email"
          rules={[{ required: true, message: 'email을 입력하세요' }]}
        >
          <Input autoComplete="username" inputMode="email" placeholder="you@example.com" autoFocus />
        </Form.Item>
        <Form.Item
          name="password"
          label="비밀번호"
          rules={[{ required: true, message: '비밀번호를 입력하세요' }]}
        >
          <Input.Password autoComplete="current-password" placeholder="비밀번호" />
        </Form.Item>
        {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 12 }} />}
        <Button type="primary" htmlType="submit" loading={submitting} block>
          로그인
        </Button>
      </Form>

      <Paragraph type="secondary" style={{ margin: 0, fontSize: 11 }}>
        아직 조직이 없는 최초 설치라면 <Text strong>마스터 키</Text> 탭으로 시작하세요.
      </Paragraph>
    </Space>
  )
}

/**
 * ② 마스터 키 — 구 TokenEntryScreen의 폼을 그대로 옮겼다.
 *
 * ── [H22/H23] 문구가 바뀐 지점과 지킨 지점 (60-identity-authn.md §4.0.1) ──
 *
 * 지킨다(감독 질문 "화면만 봐서는 출처를 알 수 없다"의 답이 화면 안에 있어야
 * 한다는 요구는 재정의와 무관하게 유효하다):
 *   (1) 이 값은 사람이 정하는 것이 아니라 설치 시 자동 생성된 값이라는 **출처**
 *   (2) `osiris-up` 1회 출력 + 재조회 스크립트라는 **획득 경로**
 *
 * 바꾼다:
 *   (1) 명칭 — "온보딩 관리 설정 토큰" → **마스터 키**, 경로·스크립트명도 함께
 *   (2) **다음에 무슨 일이 일어나는지**를 화면이 말한다.
 *   (3) 마지막 줄의 "교환 직후 폐기됩니다"는 브라우저 측 사실만 말해
 *       H11(서버측 회전)·H23(영구 폐기)이 생긴 뒤에는 오해를 부른다.
 */
function MasterKeyForm({ onAuthenticated }: { onAuthenticated: () => void }) {
  const [form] = Form.useForm<{ masterKey: string }>()
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const handleSubmit = async (values: { masterKey: string }) => {
    setSubmitting(true)
    setError(null)
    try {
      await authApi.exchange(values.masterKey)
      onAuthenticated()
    } catch (e) {
      setError(e instanceof ApiError ? e.message : '마스터 키 교환에 실패했습니다.')
    } finally {
      form.resetFields()
      setSubmitting(false)
    }
  }

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Paragraph type="secondary" style={{ margin: 0, fontSize: 13 }}>
        이 값은 <Text strong>사람이 정하는 것이 아니라</Text> 설치 시 <Text code>osiris-api</Text>가
        자동으로 생성한 값입니다.
        <br />
        최초 설치 시 <Text code>./osiris-up</Text> 실행 터미널에 한 번 출력되었습니다 — 놓쳤거나 다시
        봐야 하면 서버에서 <Text code>bin/show-master-key.sh</Text>를 실행하세요.
        <br />
        <Text strong>
          마스터 키로 로그인하면 조직을 만들고, 그 다음 본인의 비밀번호를 설정하게 됩니다. 비밀번호를
          설정하는 순간 이 마스터 키는 영구히 폐기되고, 이후 로그인은 이메일 탭으로 합니다.
        </Text>
      </Paragraph>

      <Form form={form} layout="vertical" onFinish={handleSubmit}>
        <Form.Item
          name="masterKey"
          label="마스터 키"
          rules={[{ required: true, message: '마스터 키를 입력하세요' }]}
        >
          <Input.Password autoComplete="off" placeholder="마스터 키 붙여넣기 (osk_…)" />
        </Form.Item>
        {error && <Alert type="error" showIcon message={error} style={{ marginBottom: 12 }} />}
        <Button type="primary" htmlType="submit" loading={submitting} block>
          로그인
        </Button>
      </Form>

      <Paragraph type="secondary" style={{ margin: 0, fontSize: 11 }}>
        이 값은 브라우저에 저장되지 않으며, 서버에서도 사용 즉시 새 값으로 회전됩니다.
      </Paragraph>
    </Space>
  )
}
