import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Alert,
  Button,
  Form,
  Input,
  InputNumber,
  Modal,
  Space,
  Steps,
  Tag,
  Typography,
} from 'antd'
import { CopyOutlined, ExportOutlined } from '@ant-design/icons'
import {
  METHOD_LABEL,
  PROVIDER_CATALOG,
  type ProviderCatalogEntry,
  type ProviderMethod,
} from '../../api/providerCatalog'
import {
  useCreateProvider,
  useOAuthAuthorize,
  useOAuthDeviceCode,
  useOAuthExchange,
  useOAuthPoll,
} from '../../hooks/useProviders'
import { neutral, radius } from '../../theme/tokens'

const { Text, Paragraph } = Typography
const { Search } = Input

/**
 * 프로바이더 등록 모달 — **재사용 컴포넌트** (감독 지시: 설정 화면과
 * 온보딩이 같은 컴포넌트를 쓴다, 등록 UI·로직 중복 금지). 두 화면 모두
 * `<ProviderRegisterModal open={...} onClose={...} onSuccess={...} />`를
 * 그대로 임베드한다 — 어느 화면에서 열렸는지에 따라 이 컴포넌트가 달라질
 * 이유가 없다(둘 다 결국 같은 osiris-api 엔드포인트를 부른다).
 *
 * 1단계: 카탈로그(providerCatalog.ts)에서 프로바이더 선택 — 검색 + 등록
 * 방식별 그룹.
 * 2단계: 선택한 provider의 `method`에 따라 폼 분기:
 *   - redirect: authUrl 열기 → 콜백 URL/코드 붙여넣기 → exchange
 *   - device:   device-code 발급 → user_code 표시 → interval 자동 폴링
 *   - apikey:   이름 + API 키 → POST /providers
 *   - import:   페이로드 미확정 provider는 "지원 예정" 정직 안내
 */
interface ProviderRegisterModalProps {
  open: boolean
  onClose: () => void
  onSuccess: () => void
}

type Step2State =
  | { kind: 'idle' }
  | { kind: 'redirect-authorized'; authUrl: string; state?: string; codeVerifier?: string; redirectUri?: string }
  | { kind: 'device-waiting'; userCode: string; verificationUri: string; deviceCode?: string; codeVerifier?: string; interval: number }

export function ProviderRegisterModal({ open, onClose, onSuccess }: ProviderRegisterModalProps) {
  const [step, setStep] = useState(0)
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<ProviderCatalogEntry | null>(null)
  const [step2, setStep2] = useState<Step2State>({ kind: 'idle' })
  const [error, setError] = useState<string | null>(null)
  const pollTimer = useRef<number | null>(null)

  const createProvider = useCreateProvider()
  const authorize = useOAuthAuthorize()
  const exchange = useOAuthExchange()
  const deviceCode = useOAuthDeviceCode()
  const poll = useOAuthPoll()

  const reset = () => {
    setStep(0)
    setSearch('')
    setSelected(null)
    setStep2({ kind: 'idle' })
    setError(null)
    if (pollTimer.current) {
      window.clearInterval(pollTimer.current)
      pollTimer.current = null
    }
  }

  useEffect(() => {
    if (!open) reset()
    return () => {
      if (pollTimer.current) window.clearInterval(pollTimer.current)
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  const filtered = useMemo(() => {
    const q = search.trim().toLowerCase()
    if (!q) return PROVIDER_CATALOG
    return PROVIDER_CATALOG.filter(
      (p) => p.label.toLowerCase().includes(q) || p.id.includes(q) || p.group.toLowerCase().includes(q)
    )
  }, [search])

  const grouped = useMemo(() => {
    const byMethod: Record<ProviderMethod, ProviderCatalogEntry[]> = {
      redirect: [],
      device: [],
      apikey: [],
      import: [],
    }
    for (const p of filtered) byMethod[p.method].push(p)
    return byMethod
  }, [filtered])

  const handleClose = () => {
    onClose()
  }

  const handleSelect = (entry: ProviderCatalogEntry) => {
    setSelected(entry)
    setError(null)
    setStep(1)
  }

  const handleBack = () => {
    if (pollTimer.current) {
      window.clearInterval(pollTimer.current)
      pollTimer.current = null
    }
    setStep2({ kind: 'idle' })
    setError(null)
    setStep(0)
  }

  const handleDone = () => {
    onSuccess()
    handleClose()
  }

  return (
    <Modal
      title="프로바이더 연결 추가"
      open={open}
      onCancel={handleClose}
      footer={null}
      width={640}
      destroyOnClose
    >
      <Steps
        current={step}
        size="small"
        style={{ marginBottom: 20 }}
        items={[{ title: '프로바이더 선택' }, { title: '등록' }]}
      />

      {step === 0 && (
        <div>
          <Search
            placeholder="프로바이더 검색 (예: claude, openai, github)"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            style={{ marginBottom: 16 }}
            allowClear
          />
          <div style={{ maxHeight: 420, overflowY: 'auto' }}>
            {(['redirect', 'device', 'apikey', 'import'] as ProviderMethod[]).map((method) =>
              grouped[method].length === 0 ? null : (
                <div key={method} style={{ marginBottom: 16 }}>
                  <Text
                    style={{
                      fontSize: 11,
                      fontWeight: 700,
                      letterSpacing: 0.6,
                      color: neutral.gray500,
                      textTransform: 'uppercase',
                    }}
                  >
                    {METHOD_LABEL[method]}
                  </Text>
                  <div style={{ display: 'flex', flexWrap: 'wrap', gap: 8, marginTop: 8 }}>
                    {grouped[method].map((p) => (
                      <Button
                        key={p.id}
                        onClick={() => handleSelect(p)}
                        style={{ borderRadius: radius.md }}
                      >
                        {p.label}
                      </Button>
                    ))}
                  </div>
                </div>
              )
            )}
            {filtered.length === 0 && (
              <Text type="secondary" style={{ fontSize: 13 }}>
                일치하는 프로바이더가 없습니다.
              </Text>
            )}
          </div>
        </div>
      )}

      {step === 1 && selected && (
        <Step2Form
          entry={selected}
          step2={step2}
          setStep2={setStep2}
          error={error}
          setError={setError}
          onBack={handleBack}
          onDone={handleDone}
          createProvider={createProvider}
          authorize={authorize}
          exchange={exchange}
          deviceCode={deviceCode}
          poll={poll}
          pollTimer={pollTimer}
        />
      )}
    </Modal>
  )
}

// ── 2단계 폼 — method별 분기 ─────────────────────────────────────────────

interface Step2FormProps {
  entry: ProviderCatalogEntry
  step2: Step2State
  setStep2: (s: Step2State) => void
  error: string | null
  setError: (e: string | null) => void
  onBack: () => void
  onDone: () => void
  createProvider: ReturnType<typeof useCreateProvider>
  authorize: ReturnType<typeof useOAuthAuthorize>
  exchange: ReturnType<typeof useOAuthExchange>
  deviceCode: ReturnType<typeof useOAuthDeviceCode>
  poll: ReturnType<typeof useOAuthPoll>
  pollTimer: React.MutableRefObject<number | null>
}

function Step2Form({
  entry,
  step2,
  setStep2,
  error,
  setError,
  onBack,
  onDone,
  createProvider,
  authorize,
  exchange,
  deviceCode,
  poll,
  pollTimer,
}: Step2FormProps) {
  if (entry.method === 'apikey') {
    return (
      <ApiKeyForm entry={entry} onBack={onBack} onDone={onDone} createProvider={createProvider} />
    )
  }
  if (entry.method === 'redirect') {
    return (
      <RedirectForm
        entry={entry}
        step2={step2}
        setStep2={setStep2}
        error={error}
        setError={setError}
        onBack={onBack}
        onDone={onDone}
        authorize={authorize}
        exchange={exchange}
      />
    )
  }
  if (entry.method === 'device') {
    return (
      <DeviceForm
        entry={entry}
        step2={step2}
        setStep2={setStep2}
        error={error}
        setError={setError}
        onBack={onBack}
        onDone={onDone}
        deviceCode={deviceCode}
        poll={poll}
        pollTimer={pollTimer}
      />
    )
  }
  // import
  return (
    <div>
      <Alert
        type="info"
        showIcon
        message={`${entry.label}은(는) 아직 지원 예정입니다`}
        description="토큰 임포트 페이로드가 provider별로 다르고 이 프로바이더의 정확한 형태가 아직 확정되지 않았습니다 — 정직하게 여기까지만 안내합니다. 확정되면 이 자리에 폼이 들어갑니다."
      />
      <div style={{ marginTop: 16, textAlign: 'right' }}>
        <Button onClick={onBack}>뒤로</Button>
      </div>
    </div>
  )
}

function ApiKeyForm({
  entry,
  onBack,
  onDone,
  createProvider,
}: {
  entry: ProviderCatalogEntry
  onBack: () => void
  onDone: () => void
  createProvider: ReturnType<typeof useCreateProvider>
}) {
  const [form] = Form.useForm<{ name: string; apiKey: string; priority?: number }>()
  return (
    <Form
      form={form}
      layout="vertical"
      initialValues={{ name: `osiris-${entry.id}` }}
      onFinish={(values) => {
        createProvider.mutate(
          { provider: entry.id, name: values.name, apiKey: values.apiKey, priority: values.priority },
          { onSuccess: onDone, onSettled: () => form.setFieldValue('apiKey', undefined) }
        )
      }}
    >
      <Paragraph type="secondary" style={{ marginTop: 0, fontSize: 13 }}>
        <Tag>{entry.label}</Tag> API 키로 연결을 등록합니다.
      </Paragraph>
      <Form.Item name="name" label="연결 이름" rules={[{ required: true }]}>
        <Input placeholder={`osiris-${entry.id}`} />
      </Form.Item>
      <Form.Item name="apiKey" label="API 키" rules={[{ required: true }]}>
        <Input.Password autoComplete="new-password" placeholder="sk-..." />
      </Form.Item>
      <Form.Item name="priority" label="우선순위 (선택)">
        <InputNumber style={{ width: '100%' }} />
      </Form.Item>
      {createProvider.isError && (
        <Alert
          type="error"
          showIcon
          message={(createProvider.error as Error).message}
          style={{ marginBottom: 12 }}
        />
      )}
      <div style={{ display: 'flex', justifyContent: 'space-between' }}>
        <Button onClick={onBack}>뒤로</Button>
        <Button type="primary" htmlType="submit" loading={createProvider.isPending}>
          등록
        </Button>
      </div>
    </Form>
  )
}

function RedirectForm({
  entry,
  step2,
  setStep2,
  error,
  setError,
  onBack,
  onDone,
  authorize,
  exchange,
}: {
  entry: ProviderCatalogEntry
  step2: Step2State
  setStep2: (s: Step2State) => void
  error: string | null
  setError: (e: string | null) => void
  onBack: () => void
  onDone: () => void
  authorize: ReturnType<typeof useOAuthAuthorize>
  exchange: ReturnType<typeof useOAuthExchange>
}) {
  const [callbackInput, setCallbackInput] = useState('')

  const handleAuthorize = () => {
    setError(null)
    authorize.mutate(
      { provider: entry.id },
      {
        onSuccess: (res) => {
          setStep2({
            kind: 'redirect-authorized',
            authUrl: res.authUrl,
            state: res.state,
            codeVerifier: res.codeVerifier,
            redirectUri: res.redirectUri,
          })
          window.open(res.authUrl, '_blank', 'noopener,noreferrer')
        },
        onError: (e) => setError((e as Error).message),
      }
    )
  }

  const extractCode = (raw: string): string => {
    const trimmed = raw.trim()
    try {
      const url = new URL(trimmed)
      const code = url.searchParams.get('code')
      if (code) return code
    } catch {
      // not a URL — treat as a bare code
    }
    return trimmed
  }

  const handleExchange = () => {
    if (step2.kind !== 'redirect-authorized') return
    setError(null)
    const code = extractCode(callbackInput)
    exchange.mutate(
      {
        provider: entry.id,
        req: {
          code,
          redirectUri: step2.redirectUri ?? '',
          codeVerifier: step2.codeVerifier,
          state: step2.state,
        },
      },
      {
        onSuccess: onDone,
        onError: (e) => setError((e as Error).message),
      }
    )
  }

  return (
    <div>
      <Paragraph type="secondary" style={{ marginTop: 0, fontSize: 13 }}>
        <Tag>{entry.label}</Tag> 새 탭에서 승인한 뒤, 리다이렉트된 콜백 URL(또는 코드)을 붙여넣으세요.
      </Paragraph>

      {step2.kind !== 'redirect-authorized' ? (
        <Button icon={<ExportOutlined />} onClick={handleAuthorize} loading={authorize.isPending}>
          승인 페이지 열기
        </Button>
      ) : (
        <Space direction="vertical" style={{ width: '100%' }} size={12}>
          <Alert
            type="info"
            showIcon
            message="승인 페이지를 새 탭에서 열었습니다"
            description="승인이 끝나면 브라우저 주소창의 콜백 URL 전체(또는 code 파라미터 값)를 아래에 붙여넣으세요."
          />
          <Input.TextArea
            rows={2}
            placeholder="https://.../callback?code=... 또는 코드 값"
            value={callbackInput}
            onChange={(e) => setCallbackInput(e.target.value)}
          />
        </Space>
      )}

      {error && <Alert type="error" showIcon message={error} style={{ marginTop: 12 }} />}

      <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 16 }}>
        <Button onClick={onBack}>뒤로</Button>
        {step2.kind === 'redirect-authorized' && (
          <Button
            type="primary"
            onClick={handleExchange}
            loading={exchange.isPending}
            disabled={!callbackInput.trim()}
          >
            등록 완료
          </Button>
        )}
      </div>
    </div>
  )
}

function DeviceForm({
  entry,
  step2,
  setStep2,
  error,
  setError,
  onBack,
  onDone,
  deviceCode,
  poll,
  pollTimer,
}: {
  entry: ProviderCatalogEntry
  step2: Step2State
  setStep2: (s: Step2State) => void
  error: string | null
  setError: (e: string | null) => void
  onBack: () => void
  onDone: () => void
  deviceCode: ReturnType<typeof useOAuthDeviceCode>
  poll: ReturnType<typeof useOAuthPoll>
  pollTimer: React.MutableRefObject<number | null>
}) {
  const started = useRef(false)

  useEffect(() => {
    if (started.current) return
    started.current = true
    deviceCode.mutate(entry.id, {
      onSuccess: (res) => {
        const interval = Math.max(1, Number(res.interval) || 5)
        setStep2({
          kind: 'device-waiting',
          userCode: res.user_code,
          verificationUri: res.verification_uri,
          deviceCode: res.device_code,
          codeVerifier: res.codeVerifier,
          interval,
        })
        pollTimer.current = window.setInterval(() => {
          poll.mutate(
            {
              provider: entry.id,
              body: { device_code: res.device_code, codeVerifier: res.codeVerifier },
            },
            {
              onSuccess: () => {
                if (pollTimer.current) {
                  window.clearInterval(pollTimer.current)
                  pollTimer.current = null
                }
                onDone()
              },
              // 폴링 중 "아직 승인 안 됨" 오류는 정상 흐름이라 화면에 에러로
              // 띄우지 않는다 — interval마다 계속 시도한다.
            }
          )
        }, interval * 1000)
      },
      onError: (e) => setError((e as Error).message),
    })
    return () => {
      if (pollTimer.current) {
        window.clearInterval(pollTimer.current)
        pollTimer.current = null
      }
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  return (
    <div>
      <Paragraph type="secondary" style={{ marginTop: 0, fontSize: 13 }}>
        <Tag>{entry.label}</Tag> 아래 코드를 인증 페이지에서 입력하세요. 자동으로 승인 여부를 확인합니다.
      </Paragraph>

      {deviceCode.isPending && !error && <Text type="secondary">코드 발급 중...</Text>}

      {step2.kind === 'device-waiting' && (
        <Space direction="vertical" size={12} style={{ width: '100%' }}>
          <div
            style={{
              display: 'flex',
              alignItems: 'center',
              gap: 8,
              padding: 16,
              border: `1px solid ${neutral.border}`,
              borderRadius: radius.lg,
              background: neutral.gray50,
            }}
          >
            <Text style={{ fontSize: 24, fontWeight: 700, letterSpacing: 2 }}>{step2.userCode}</Text>
            <Button
              size="small"
              icon={<CopyOutlined />}
              onClick={() => navigator.clipboard?.writeText(step2.userCode)}
            />
          </div>
          <Button
            icon={<ExportOutlined />}
            onClick={() => window.open(step2.verificationUri, '_blank', 'noopener,noreferrer')}
          >
            인증 페이지 열기
          </Button>
          <Text type="secondary" style={{ fontSize: 12 }}>
            {step2.interval}초마다 자동으로 확인합니다. 이 창을 닫지 마세요.
          </Text>
        </Space>
      )}

      {error && <Alert type="error" showIcon message={error} style={{ marginTop: 12 }} />}

      <div style={{ marginTop: 16 }}>
        <Button onClick={onBack}>뒤로</Button>
      </div>
    </div>
  )
}
