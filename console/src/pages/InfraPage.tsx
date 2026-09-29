import { useMemo, useState } from 'react'
import { Alert, Button, Card, Empty, Input, Space, Spin, Tag, Typography } from 'antd'
import { ReloadOutlined, SendOutlined } from '@ant-design/icons'
import { PageHeader } from '../layout/PageHeader'
import { StatusTag } from '../components/StatusTag'
import { GraphCanvas, type GraphEdge, type GraphLane, type GraphNodeItem, type GraphStatus } from '../components/graph/GraphCanvas'
import { neutral, radius } from '../theme/tokens'
import { useInfraMetrics, useInfraTopology } from '../hooks/useInfra'
import type { InfraMetrics, InfraNode, InfraPod, InfraTopology } from '../api/infra'

const { Text, Paragraph, Title } = Typography

/**
 * Infra — 인프라 토폴로지 A단계(90-infra-topology.md §7, §7.2).
 *
 * [2026-09-11 감독 확정] 화면 형태는 **작업 지도와 같은 2단 레이아웃**이다
 * (표/트리 뷰가 아니라):
 *   - 좌: 토폴로지를 그래프 캔버스로 — 노드→네임스페이스→워크로드→파드.
 *     `components/graph/GraphCanvas`를 쓴다(그 파일 doc comment 참고 —
 *     작업 지도·온톨로지가 아직 이 컴포넌트를 가진 적이 없어서 여기서
 *     처음 만들고, 그 두 화면이 나중에 그대로 재사용한다).
 *   - 우: 작업 패널 **셸**만. 대화형으로 인프라를 구성하는 실제 기능(MCP
 *     도구 연결, 에이전트 제안)은 B단계 이후다 — 지금은 자리와 정직한
 *     안내만 둔다.
 *
 * 데이터는 전부 `/_api/infra/*`(osiris-api, 읽기 전용) — 이 화면은 어떤
 * 쓰기도 하지 않는다(IN4).
 */
export function InfraPage() {
  const topology = useInfraTopology()
  const metrics = useInfraMetrics()
  const [selectedId, setSelectedId] = useState<string | undefined>(undefined)

  const { lanes, nodes, edges, selectedDetail } = useMemo(
    () => buildGraph(topology.data, metrics.data, selectedId),
    [topology.data, metrics.data, selectedId],
  )

  return (
    <div
      className="osiris-content-max"
      style={{ padding: '28px 38px 44px', display: 'flex', flexDirection: 'column', flex: 1, minHeight: 0 }}
    >
      <PageHeader
        title="Infra"
        description="인프라 토폴로지 — 노드/네임스페이스/워크로드/파드(읽기 전용, A단계)"
        extra={
          <Button
            icon={<ReloadOutlined />}
            size="small"
            loading={topology.isFetching || metrics.isFetching}
            onClick={() => {
              topology.refetch()
              metrics.refetch()
            }}
          >
            새로고침
          </Button>
        }
      />

      <Alert
        type="info"
        showIcon
        style={{ marginBottom: 16 }}
        message="A단계 — 읽기 전용"
        description="이 토폴로지는 온톨로지 구성에서도 동일하게 확인할 수 있습니다(같은 그래프 컴포넌트). restart·scale 같은 조작과 우측 작업 패널의 대화형 구성은 B단계 이후(승인 게이트 뒤)에 연결됩니다. infra-* 네임스페이스가 아직 없어, 지금은 이 클러스터에 이미 있는 AI-org 런타임(osiris-system/osiris-agents)과 ingress-nginx만 보입니다."
      />

      {(topology.isError || metrics.isError) && (
        <Alert
          type="error"
          showIcon
          style={{ marginBottom: 16 }}
          message="일부 데이터를 불러오지 못했습니다"
          description={((topology.error as Error)?.message) ?? ((metrics.error as Error)?.message) ?? '알 수 없는 오류'}
        />
      )}

      <div style={{ display: 'flex', gap: 20, flex: 1, minHeight: 0 }}>
        {/* ── 좌: 토폴로지 그래프 캔버스 ── */}
        <Card
          style={{ borderRadius: radius.xl, border: `1px solid ${neutral.border}`, flex: 1, minWidth: 0, display: 'flex', flexDirection: 'column' }}
          styles={{ body: { padding: 12, flex: 1, minHeight: 0, display: 'flex' } }}
        >
          {topology.isLoading ? (
            <div style={{ margin: 'auto' }}>
              <Spin />
            </div>
          ) : nodes.length === 0 ? (
            <Empty description="보여줄 노드/파드가 없습니다" style={{ margin: 'auto' }} />
          ) : (
            <GraphCanvas lanes={lanes} nodes={nodes} edges={edges} onNodeClick={setSelectedId} selectedId={selectedId} />
          )}
        </Card>

        {/* ── 우: 작업 패널 셸 ── */}
        <div style={{ width: 340, flex: '0 0 340px', display: 'flex', flexDirection: 'column', gap: 16, minHeight: 0 }}>
          <Card
            title="선택한 항목"
            style={{ borderRadius: radius.xl, border: `1px solid ${neutral.border}` }}
            styles={{ body: { padding: 16 } }}
          >
            {selectedDetail ?? (
              <Text type="secondary" style={{ fontSize: 13 }}>
                좌측 그래프에서 노드·네임스페이스·워크로드·파드를 클릭하면 상세가 여기 표시됩니다.
              </Text>
            )}
          </Card>

          <Card
            style={{ borderRadius: radius.xl, border: `1px solid ${neutral.border}`, flex: 1, minHeight: 0, display: 'flex', flexDirection: 'column' }}
            styles={{ body: { padding: 16, flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0 } }}
          >
            <Title level={5} style={{ margin: 0, fontSize: 14 }}>
              작업 패널
            </Title>
            <Paragraph type="secondary" style={{ fontSize: 12, marginTop: 8 }}>
              대화형으로 인프라를 구성하는 자리입니다. 이 빌드(A단계)에서는 자리와 안내만
              있습니다 — 실제 대화형 에이전트와 MCP 도구 연결은 다음 단계에 붙습니다.
            </Paragraph>
            <ul style={{ fontSize: 12, color: neutral.gray500, paddingLeft: 18, margin: '4px 0 16px' }}>
              <li>서비스 이관 제안 초안 작성 (에이전트)</li>
              <li>승인 게이트 통과 후 infra-operator가 적용</li>
              <li>제안·적용 이력이 여기 타임라인으로</li>
            </ul>
            <div style={{ marginTop: 'auto' }}>
              <Input
                disabled
                placeholder="Infra MCP 도구는 다음 단계에 연결됩니다"
                suffix={<SendOutlined style={{ color: neutral.gray300 }} />}
              />
            </div>
          </Card>
        </div>
      </div>
    </div>
  )
}

// ─────────────────────────────────────────────────────────────────────────
// 토폴로지 응답 -> GraphCanvas 인자 변환.
// ─────────────────────────────────────────────────────────────────────────

const LANES: GraphLane[] = [
  { key: 'node', title: '노드' },
  { key: 'namespace', title: '네임스페이스' },
  { key: 'workload', title: '워크로드' },
  { key: 'pod', title: '파드' },
]

function phaseStatus(phase: string): GraphStatus {
  switch (phase) {
    case 'Running':
      return 'success'
    case 'Pending':
      return 'warning'
    case 'Failed':
      return 'error'
    default:
      return 'neutral'
  }
}

function buildGraph(
  topo: InfraTopology | undefined,
  metrics: InfraMetrics | undefined,
  selectedId: string | undefined,
): { lanes: GraphLane[]; nodes: GraphNodeItem[]; edges: GraphEdge[]; selectedDetail: React.ReactNode } {
  if (!topo) return { lanes: LANES, nodes: [], edges: [], selectedDetail: null }

  const nodeMetricByName = new Map((metrics?.nodes ?? []).map((m) => [m.name, m]))

  const nodes: GraphNodeItem[] = []
  const edges: GraphEdge[] = []
  const podById = new Map<string, InfraPod>()
  const nodeById = new Map<string, InfraNode>()

  const podCountByK8sNode = new Map<string, number>()
  for (const ns of topo.namespaces) {
    for (const w of ns.workloads) {
      for (const p of w.pods) {
        if (p.node) podCountByK8sNode.set(p.node, (podCountByK8sNode.get(p.node) ?? 0) + 1)
      }
    }
  }

  for (const n of topo.nodes) {
    const id = `node:${n.name}`
    nodeById.set(id, n)
    const usage = nodeMetricByName.get(n.name)
    const usageLabel = usage ? ` · ${usage.cpu}/${usage.memory}` : ''
    nodes.push({
      id,
      lane: 'node',
      label: n.name,
      subLabel: `${podCountByK8sNode.get(n.name) ?? 0}개 파드 · ${n.ready ? 'Ready' : 'NotReady'}${usageLabel}`,
      status: n.ready ? 'success' : 'error',
    })
  }

  const seenNodeNsEdge = new Set<string>()

  for (const ns of topo.namespaces) {
    if (ns.workloads.length === 0) continue
    const nsId = `ns:${ns.name}`
    const podCount = ns.workloads.reduce((sum, w) => sum + w.pods.length, 0)
    nodes.push({
      id: nsId,
      lane: 'namespace',
      label: ns.name,
      subLabel: ns.isInfra ? `infra-* · ${podCount}개 파드` : `${podCount}개 파드`,
      status: 'neutral',
    })

    for (const w of ns.workloads) {
      const wId = `wl:${ns.name}:${w.name}`
      nodes.push({
        id: wId,
        lane: 'workload',
        label: w.name,
        subLabel: `${w.pods.length}개 파드`,
        status: 'neutral',
      })
      edges.push({ from: nsId, to: wId })

      for (const p of w.pods) {
        const pId = `pod:${ns.name}:${w.name}:${p.name}`
        podById.set(pId, p)
        nodes.push({
          id: pId,
          lane: 'pod',
          label: p.name,
          subLabel: `${p.phase}${p.restarts > 0 ? ` · 재시작 ${p.restarts}` : ''}`,
          status: phaseStatus(p.phase),
        })
        edges.push({ from: wId, to: pId })

        if (p.node) {
          const k8sNodeId = `node:${p.node}`
          const edgeKey = `${k8sNodeId}->${pId}`
          if (!seenNodeNsEdge.has(edgeKey)) {
            seenNodeNsEdge.add(edgeKey)
            edges.push({ from: k8sNodeId, to: pId, secondary: true })
          }
        }
      }
    }
  }

  let selectedDetail: React.ReactNode = null
  if (selectedId) {
    if (podById.has(selectedId)) {
      const p = podById.get(selectedId)!
      selectedDetail = (
        <Space direction="vertical" size={4} style={{ width: '100%' }}>
          <Text strong>{p.name}</Text>
          <Space size={6}>
            <StatusTag kind={phaseStatus(p.phase)}>{p.phase}</StatusTag>
            <Tag>{p.readyContainers}/{p.totalContainers} ready</Tag>
          </Space>
          <Text type="secondary" style={{ fontSize: 12 }}>namespace: {p.namespace}</Text>
          <Text type="secondary" style={{ fontSize: 12 }}>node: {p.node || '(미배치)'}</Text>
          <Text type="secondary" style={{ fontSize: 12 }}>재시작: {p.restarts}</Text>
          {p.ownerKind && (
            <Text type="secondary" style={{ fontSize: 12 }}>owner: {p.ownerKind}/{p.ownerName}</Text>
          )}
        </Space>
      )
    } else if (nodeById.has(selectedId)) {
      const n = nodeById.get(selectedId)!
      selectedDetail = (
        <Space direction="vertical" size={4} style={{ width: '100%' }}>
          <Text strong>{n.name}</Text>
          <StatusTag kind={n.ready ? 'success' : 'error'}>{n.ready ? 'Ready' : 'NotReady'}</StatusTag>
          <Text type="secondary" style={{ fontSize: 12 }}>CPU: {n.cpuAllocatable} / {n.cpuCapacity}</Text>
          <Text type="secondary" style={{ fontSize: 12 }}>Mem: {n.memAllocatable} / {n.memCapacity}</Text>
          <Text type="secondary" style={{ fontSize: 12 }}>kubelet: {n.kubeletVersion}</Text>
        </Space>
      )
    } else {
      selectedDetail = (
        <Text type="secondary" style={{ fontSize: 12 }}>{selectedId}</Text>
      )
    }
  }

  return { lanes: LANES, nodes, edges, selectedDetail }
}
