import { useState } from 'react'
import { Alert, Button, Empty, Input, Popconfirm, Space, Spin, Table, Tag, Typography, message } from 'antd'
import { CheckOutlined, CloseOutlined, ReloadOutlined } from '@ant-design/icons'
import { PageHeader } from '../layout/PageHeader'
import { usePendingProposals, useDecideProposal } from '../hooks/useApprovals'
import { parseTarget, type InfraProposal } from '../api/approvals'
import { neutral, radius } from '../theme/tokens'

const { Text } = Typography

/**
 * Approvals — 인프라 제안→승인 큐 (docs/design/90-infra-topology.md §11,
 * "C-1 — 제안→승인 척추"). 지금 이 화면이 다루는 것은 인프라 제안뿐이다
 * (git.commit 등 §10 §8.2 커밋 승인 파이프라인은 별도 표면 — 이 화면은
 * 그것을 다루지 않는다. 자리표시자 시절의 설명 "외부영향 액션(커밋/발송/
 * 지출) 대기열"은 그 넓은 범위를 가리켰지만, 실제로 지금 배선된 것은
 * 인프라 제안뿐이므로 이 화면은 그 범위만 정직하게 다룬다).
 *
 * IN19 그대로: 승인/반려는 이 화면(세션 인증된 콘솔)에서만 나간다 —
 * `POST /_api/infra/proposals/{id}/decision`, PersonOnly + org.admin.
 * 기계 주체·에이전트·supervisor는 이 버튼을 누를 수 없다(애초에 이 화면에
 * 접근할 세션이 없다).
 *
 * ★ 정직 고지 — 승인해도 "적용"되지 않는다. C-1은 :Approval.status만
 * 바꾼다. 실제 클러스터 반영(k8s apply/scale/delete)은 C-2이며 이 빌드에
 * 없다. 아래 승인 확인 다이얼로그와 성공 메시지가 이것을 매번 반복해서
 * 알린다 — "승인됨"을 "적용됨"으로 오인하는 것이 이 화면이 가장 경계하는
 * 실패 모드다.
 */
export function ApprovalsPage() {
  const pending = usePendingProposals()
  const decide = useDecideProposal()
  const [rejecting, setRejecting] = useState<Record<string, string>>({})

  const proposals = pending.data?.proposals ?? []

  function approve(p: InfraProposal) {
    decide.mutate(
      { id: p.id, req: { decision: 'approve' } },
      {
        onSuccess: () => {
          message.success(
            `승인됨 — ${p.id}는 이제 "approved" 상태입니다. 실제 클러스터 적용은 다음 단계(C-2)에서 이뤄지며, 이 빌드는 아무것도 적용하지 않았습니다.`,
            6,
          )
        },
        onError: (err) => message.error(err instanceof Error ? err.message : '승인 처리 실패'),
      },
    )
  }

  function reject(p: InfraProposal) {
    const comment = rejecting[p.id]
    decide.mutate(
      { id: p.id, req: { decision: 'reject', comment } },
      {
        onSuccess: () => message.success(`반려됨 — ${p.id}`),
        onError: (err) => message.error(err instanceof Error ? err.message : '반려 처리 실패'),
      },
    )
  }

  return (
    <div className="osiris-content-max" style={{ padding: '28px 38px 44px' }}>
      <PageHeader
        title="Approvals"
        description="인프라 제안 대기열 — 승인/반려 (C-1, 90-infra-topology.md §11)"
        extra={
          <Button icon={<ReloadOutlined />} size="small" loading={pending.isFetching} onClick={() => pending.refetch()}>
            새로고침
          </Button>
        }
      />

      <Alert
        type="warning"
        showIcon
        style={{ marginBottom: 16 }}
        message="승인해도 아직 아무것도 적용되지 않습니다"
        description="여기서 승인하면 제안의 상태가 'approved'로 바뀔 뿐입니다. 실제로 Kubernetes에 적용/스케일/삭제하는 실행 단계(C-2)는 아직 이 빌드에 구현되어 있지 않습니다 — 승인은 결재이지 실행이 아닙니다."
      />

      {pending.isError && (
        <Alert
          type="error"
          showIcon
          style={{ marginBottom: 16 }}
          message="대기 목록을 불러오지 못했습니다"
          description={(pending.error as Error)?.message ?? '알 수 없는 오류'}
        />
      )}

      <div
        style={{
          background: '#fff',
          border: `1px solid ${neutral.border}`,
          borderRadius: radius.xl,
          padding: 4,
        }}
      >
        {pending.isLoading ? (
          <div style={{ padding: 48, textAlign: 'center' }}>
            <Spin />
          </div>
        ) : proposals.length === 0 ? (
          <Empty description="대기 중인 인프라 제안이 없습니다" style={{ padding: 48 }} />
        ) : (
          <Table<InfraProposal>
            rowKey="id"
            dataSource={proposals}
            pagination={false}
            columns={[
              {
                title: '액션',
                dataIndex: 'actionTypeId',
                width: 140,
                render: (v: string) => <Tag color="blue">{v}</Tag>,
              },
              {
                title: '대상',
                key: 'target',
                render: (_: unknown, p: InfraProposal) => {
                  const t = parseTarget(p.targetJson)
                  if (!t) return <Text type="secondary">{p.targetJson}</Text>
                  return (
                    <Text>
                      {t.namespace ?? '?'} / {t.kind ?? '?'} / {t.name ?? '?'}
                    </Text>
                  )
                },
              },
              { title: '제안자', dataIndex: 'proposedByRef', width: 200 },
              {
                title: '요청 시각',
                dataIndex: 'requestedAt',
                width: 200,
                render: (v: string) => <Text type="secondary">{v}</Text>,
              },
              {
                title: '',
                key: 'actions',
                width: 320,
                render: (_: unknown, p: InfraProposal) => (
                  <Space>
                    <Popconfirm
                      title="이 제안을 승인할까요?"
                      description="상태만 'approved'로 바뀝니다 — 클러스터에는 아직 아무것도 적용되지 않습니다(C-2 미구현)."
                      okText="승인"
                      onConfirm={() => approve(p)}
                    >
                      <Button size="small" type="primary" icon={<CheckOutlined />} loading={decide.isPending}>
                        승인
                      </Button>
                    </Popconfirm>
                    <Input
                      size="small"
                      placeholder="반려 사유(선택)"
                      style={{ width: 140 }}
                      value={rejecting[p.id] ?? ''}
                      onChange={(e) => setRejecting((prev) => ({ ...prev, [p.id]: e.target.value }))}
                    />
                    <Popconfirm
                      title="이 제안을 반려할까요?"
                      okText="반려"
                      okButtonProps={{ danger: true }}
                      onConfirm={() => reject(p)}
                    >
                      <Button size="small" danger icon={<CloseOutlined />} loading={decide.isPending}>
                        반려
                      </Button>
                    </Popconfirm>
                  </Space>
                ),
              },
            ]}
          />
        )}
      </div>
    </div>
  )
}
