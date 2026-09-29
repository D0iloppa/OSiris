import { useMemo } from 'react'
import { neutral, semantic } from '../../theme/tokens'

/**
 * GraphCanvas — 레인(컬럼) 기반 계층 그래프 캔버스.
 *
 * ── 왜 이 컴포넌트가 새로 생겼는가 ────────────────────────────────────────
 *
 * 90-infra-topology.md §7은 Infra 화면이 "작업 지도가 쓰는 기존 그래프
 * 컴포넌트를 재사용"하라고 지시한다. 그러나 실측: `WorkMapPage.tsx`와
 * `OntologyPage.tsx`는 둘 다 아직 **플레이스홀더**다(각자 "그릴 데이터가
 * 없다"고 명시) — 재사용할 그래프 컴포넌트가 이 코드베이스에 아직
 * 존재하지 않는다. 없는 것을 있는 척 import하지 않는다.
 *
 * 그래서 이 컴포넌트가 **그 셋(작업 지도·온톨로지·Infra)이 공유할 최초의
 * 그래프 캔버스**다 — Infra가 먼저 실제 데이터(k8s 토폴로지)를 가지므로
 * 여기서 만들고, WorkMapPage/OntologyPage가 백엔드를 갖추면 이 컴포넌트를
 * 그대로 재사용한다(범용 인자: lanes/nodes/edges — 인프라 전용 타입은
 * InfraPage 쪽에만 있다).
 *
 * 힘-지향(force-directed) 레이아웃이 아니라 **레인(컬럼) 고정 배치**를
 * 택한 이유: 인프라 토폴로지도, 작업 지도도, 온톨로지 계층도 전부 "왼쪽에서
 * 오른쪽으로 흐르는 단계"라는 성질을 공유한다(노드→네임스페이스→워크로드
 * →파드 / 프로젝트→작업→하위작업 / 팀→역할→사람). 레인 배치는 이 성질을
 * 그대로 드러내고, 힘-지향 레이아웃보다 훨씬 적은 코드로 안정적으로
 * 그려진다 — 매 렌더마다 위치가 흔들리지 않는다.
 */

export type GraphStatus = 'success' | 'warning' | 'error' | 'neutral'

export interface GraphLane {
  key: string
  title: string
}

export interface GraphNodeItem {
  id: string
  lane: string
  label: string
  subLabel?: string
  status?: GraphStatus
}

export interface GraphEdge {
  from: string
  to: string
  /** 보조 관계(예: 파드가 실제로 배치된 물리 노드)는 옅은 점선으로 그린다 — 1차 계층(네임스페이스→워크로드→파드)과 시각적으로 구분한다. */
  secondary?: boolean
}

const NODE_W = 176
const NODE_H = 46
const LANE_GAP = 56
const ROW_GAP = 12
const LANE_HEADER_H = 28
const PADDING = 16

export function GraphCanvas({
  lanes,
  nodes,
  edges,
  onNodeClick,
  selectedId,
}: {
  lanes: GraphLane[]
  nodes: GraphNodeItem[]
  edges: GraphEdge[]
  onNodeClick?: (id: string) => void
  selectedId?: string
}) {
  const layout = useMemo(() => {
    const byLane = new Map<string, GraphNodeItem[]>()
    for (const lane of lanes) byLane.set(lane.key, [])
    for (const n of nodes) {
      const arr = byLane.get(n.lane)
      if (arr) arr.push(n)
    }
    const pos = new Map<string, { x: number; y: number }>()
    lanes.forEach((lane, laneIdx) => {
      const items = byLane.get(lane.key) ?? []
      items.forEach((item, rowIdx) => {
        pos.set(item.id, {
          x: PADDING + laneIdx * (NODE_W + LANE_GAP),
          y: PADDING + LANE_HEADER_H + rowIdx * (NODE_H + ROW_GAP),
        })
      })
    })
    const maxRows = Math.max(1, ...lanes.map((l) => (byLane.get(l.key) ?? []).length))
    const width = PADDING * 2 + lanes.length * NODE_W + (lanes.length - 1) * LANE_GAP
    const height = PADDING * 2 + LANE_HEADER_H + maxRows * (NODE_H + ROW_GAP)
    return { byLane, pos, width, height }
  }, [lanes, nodes])

  const statusColor = (s?: GraphStatus) => {
    switch (s) {
      case 'success':
        return semantic.success.base
      case 'warning':
        return semantic.warning.base
      case 'error':
        return semantic.error.base
      default:
        return neutral.gray400
    }
  }

  return (
    <div style={{ width: '100%', height: '100%', overflow: 'auto' }}>
      <svg width={layout.width} height={layout.height} style={{ display: 'block' }}>
        {lanes.map((lane, i) => (
          <text
            key={lane.key}
            x={PADDING + i * (NODE_W + LANE_GAP) + NODE_W / 2}
            y={PADDING + 14}
            textAnchor="middle"
            fontSize={12}
            fontWeight={700}
            fill={neutral.gray500}
          >
            {lane.title}
          </text>
        ))}

        {edges.map((e, i) => {
          const from = layout.pos.get(e.from)
          const to = layout.pos.get(e.to)
          if (!from || !to) return null
          const x1 = from.x + NODE_W
          const y1 = from.y + NODE_H / 2
          const x2 = to.x
          const y2 = to.y + NODE_H / 2
          const mx = (x1 + x2) / 2
          return (
            <path
              key={`${e.from}->${e.to}:${i}`}
              d={`M ${x1} ${y1} C ${mx} ${y1}, ${mx} ${y2}, ${x2} ${y2}`}
              fill="none"
              stroke={e.secondary ? neutral.gray300 : neutral.border}
              strokeWidth={e.secondary ? 1 : 1.5}
              strokeDasharray={e.secondary ? '3,3' : undefined}
            />
          )
        })}

        {nodes.map((n) => {
          const p = layout.pos.get(n.id)
          if (!p) return null
          const selected = n.id === selectedId
          return (
            <g
              key={n.id}
              transform={`translate(${p.x},${p.y})`}
              onClick={() => onNodeClick?.(n.id)}
              style={{ cursor: onNodeClick ? 'pointer' : 'default' }}
            >
              <rect
                width={NODE_W}
                height={NODE_H}
                rx={10}
                fill={neutral.white}
                stroke={selected ? '#1B4F9C' : neutral.border}
                strokeWidth={selected ? 2 : 1}
              />
              <circle cx={12} cy={NODE_H / 2} r={4} fill={statusColor(n.status)} />
              <text x={24} y={18} fontSize={12} fontWeight={600} fill={neutral.text}>
                {truncate(n.label, 20)}
              </text>
              {n.subLabel && (
                <text x={24} y={33} fontSize={11} fill={neutral.gray500}>
                  {truncate(n.subLabel, 24)}
                </text>
              )}
            </g>
          )
        })}
      </svg>
    </div>
  )
}

function truncate(s: string, n: number) {
  return s.length > n ? s.slice(0, n - 1) + '…' : s
}
