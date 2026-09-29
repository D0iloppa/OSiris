import { useState } from 'react'
import {
  Alert,
  Button,
  Empty,
  Form,
  Input,
  InputNumber,
  Modal,
  Popconfirm,
  Space,
  Spin,
  Switch,
  Table,
  Tag,
  Typography,
} from 'antd'
import { EditOutlined, DeleteOutlined, PlusOutlined } from '@ant-design/icons'
import { neutral, radius } from '../../theme/tokens'
import { findCatalogEntry, METHOD_LABEL } from '../../api/providerCatalog'
import { useDeleteProvider, useProviders, useUpdateProvider } from '../../hooks/useProviders'
import { ProviderRegisterModal } from '../../components/providers/ProviderRegisterModal'
import type { ProviderConnection, UpdateProviderRequest } from '../../api/types'

const { Paragraph, Text } = Typography

/**
 * 프로바이더 연결 — omniroute에 등록된 LLM 계정/키를 실동작으로 관리한다.
 *
 * 등록은 `ProviderRegisterModal`(재사용 컴포넌트, ../../components/
 * providers/ProviderRegisterModal.tsx) 하나로 — 온보딩의 ProviderStep도
 * 같은 컴포넌트를 쓴다(등록 UI·로직 중복 없음). 이 파일은 목록 표시 +
 * 수정/삭제만 책임진다.
 */
export function ProviderConnectionsSection() {
  const { data: connections, isLoading, isError, error, refetch } = useProviders()
  const [registerOpen, setRegisterOpen] = useState(false)
  const [editing, setEditing] = useState<ProviderConnection | null>(null)
  const deleteProvider = useDeleteProvider()

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', justifyContent: 'space-between', gap: 12 }}>
        <div>
          <Text strong style={{ fontSize: 16 }}>
            프로바이더 연결
          </Text>
          <Paragraph type="secondary" style={{ margin: '2px 0 0', fontSize: 13 }}>
            LLM 계정·API 키는 omniroute가 단일 보관처다 — 이 목록은 osiris-api를 경유해 실시간으로
            읽고 쓴다.
          </Paragraph>
        </div>
        <Button type="primary" icon={<PlusOutlined />} onClick={() => setRegisterOpen(true)}>
          추가
        </Button>
      </div>

      {isLoading && <Spin />}

      {isError && (
        <Alert
          type="error"
          showIcon
          message="연결 목록을 불러오지 못했습니다"
          description={(error as Error).message}
          action={
            <Button size="small" onClick={() => refetch()}>
              다시 시도
            </Button>
          }
        />
      )}

      {connections && connections.length === 0 && (
        <Empty description="등록된 프로바이더 연결이 없습니다" style={{ padding: '32px 0' }}>
          <Button type="primary" icon={<PlusOutlined />} onClick={() => setRegisterOpen(true)}>
            첫 연결 추가
          </Button>
        </Empty>
      )}

      {connections && connections.length > 0 && (
        <Table<ProviderConnection>
          rowKey="id"
          dataSource={connections}
          pagination={false}
          size="small"
          style={{ borderRadius: radius.xl, border: `1px solid ${neutral.border}`, overflow: 'hidden' }}
          columns={[
            {
              title: '프로바이더',
              dataIndex: 'provider',
              render: (v: string) => {
                const catalog = findCatalogEntry(v)
                return (
                  <span>
                    <Text strong>{catalog?.label ?? v}</Text>
                    {catalog && (
                      <Tag style={{ marginLeft: 8 }}>{METHOD_LABEL[catalog.method]}</Tag>
                    )}
                  </span>
                )
              },
            },
            { title: '이름', dataIndex: 'name' },
            {
              title: '방식',
              dataIndex: 'authType',
              render: (v: string) => <Tag>{v}</Tag>,
            },
            {
              title: '상태',
              dataIndex: 'isActive',
              render: (v: boolean) => (
                <Tag color={v ? 'green' : 'default'}>{v ? '활성' : '비활성'}</Tag>
              ),
            },
            {
              title: '테스트',
              dataIndex: 'testStatus',
              render: (v?: string) => (v ? <Tag>{v}</Tag> : <Text type="secondary">-</Text>),
            },
            {
              title: '',
              key: 'actions',
              width: 120,
              render: (_: unknown, record: ProviderConnection) => (
                <Space>
                  <Button
                    size="small"
                    icon={<EditOutlined />}
                    onClick={() => setEditing(record)}
                    aria-label="수정"
                  />
                  <Popconfirm
                    title="이 연결을 삭제할까요?"
                    description={`"${record.name}" 연결이 omniroute에서 제거됩니다.`}
                    okText="삭제"
                    okButtonProps={{ danger: true }}
                    onConfirm={() => deleteProvider.mutate(record.id)}
                  >
                    <Button size="small" danger icon={<DeleteOutlined />} aria-label="삭제" />
                  </Popconfirm>
                </Space>
              ),
            },
          ]}
        />
      )}

      <ProviderRegisterModal
        open={registerOpen}
        onClose={() => setRegisterOpen(false)}
        onSuccess={() => refetch()}
      />

      {editing && (
        <EditProviderModal connection={editing} onClose={() => setEditing(null)} onSuccess={() => refetch()} />
      )}
    </Space>
  )
}

// ── 수정 모달 — name/priority/isActive (+ apikey형은 키 재입력) ────────────

interface EditProviderModalProps {
  connection: ProviderConnection
  onClose: () => void
  onSuccess: () => void
}

function EditProviderModal({ connection, onClose, onSuccess }: EditProviderModalProps) {
  const updateProvider = useUpdateProvider()
  const [form] = Form.useForm<{ name: string; priority?: number; isActive: boolean; apiKey?: string }>()
  const isApiKey = connection.authType === 'apikey'

  return (
    <Modal
      title={`"${connection.name}" 수정`}
      open
      onCancel={onClose}
      onOk={() => form.submit()}
      confirmLoading={updateProvider.isPending}
      okText="저장"
    >
      <Form
        form={form}
        layout="vertical"
        initialValues={{
          name: connection.name,
          priority: connection.priority,
          isActive: connection.isActive,
        }}
        onFinish={(values) => {
          const req: UpdateProviderRequest = {
            name: values.name,
            priority: values.priority,
            isActive: values.isActive,
          }
          if (isApiKey && values.apiKey) req.apiKey = values.apiKey
          updateProvider.mutate(
            { id: connection.id, req },
            { onSuccess: () => { onSuccess(); onClose() }, onSettled: () => form.setFieldValue('apiKey', undefined) }
          )
        }}
      >
        <Form.Item name="name" label="연결 이름" rules={[{ required: true }]}>
          <Input />
        </Form.Item>
        <Form.Item name="priority" label="우선순위">
          <InputNumber style={{ width: '100%' }} />
        </Form.Item>
        <Form.Item name="isActive" label="활성 상태" valuePropName="checked">
          <Switch />
        </Form.Item>
        {isApiKey ? (
          <Form.Item name="apiKey" label="API 키 재입력 (선택 — 비워두면 기존 값 유지)">
            <Input.Password autoComplete="new-password" placeholder="sk-..." />
          </Form.Item>
        ) : (
          <Paragraph type="secondary" style={{ fontSize: 12 }}>
            OAuth 연결은 이 화면에서 토큰을 바꿀 수 없습니다 — omniroute가 그렇게 강제합니다. 이름·우선순위
            ·활성 여부만 바꿀 수 있습니다.
          </Paragraph>
        )}
        {updateProvider.isError && (
          <Alert type="error" showIcon message={(updateProvider.error as Error).message} />
        )}
      </Form>
    </Modal>
  )
}
