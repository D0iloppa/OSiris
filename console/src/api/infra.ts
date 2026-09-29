import { http } from './http'

/**
 * `osiris-api`의 `/api/infra/*` — docs/design/90-infra-topology.md §7 A단계
 * (읽기 전용 토폴로지 뷰). 콘솔은 항상 `/_api` base로 부른다(http.ts).
 *
 * 여기 정의된 타입은 `api/internal/k8sread/client.go`의 프로젝션 구조체와
 * 1:1로 대응한다 — 그쪽에 없는 필드(env, image, secret 참조 등)는 여기도
 * 없다. 콘솔이 받는 것 이상을 서버가 보내지 않는다는 것이 그 파일의
 * 보장이므로, 이 타입을 넓히더라도 서버가 실제로 그 필드를 채워 보내지
 * 않으면 항상 `undefined`다.
 */

export interface InfraNode {
  name: string
  roles: string[]
  ready: boolean
  kubeletVersion: string
  os: string
  arch: string
  cpuCapacity: string
  memCapacity: string
  cpuAllocatable: string
  memAllocatable: string
  podCIDR?: string
}

export interface InfraNamespace {
  name: string
  phase: string
}

export interface InfraPod {
  name: string
  namespace: string
  node: string
  phase: string
  restarts: number
  readyContainers: number
  totalContainers: number
  startTime?: string
  ownerKind?: string
  ownerName?: string
  component?: string
}

export interface InfraTopologyWorkload {
  name: string
  pods: InfraPod[]
}

export interface InfraTopologyNamespace {
  name: string
  phase: string
  isInfra: boolean
  workloads: InfraTopologyWorkload[]
}

export interface InfraTopology {
  nodes: InfraNode[]
  namespaces: InfraTopologyNamespace[]
}

export interface InfraNodeMetric {
  name: string
  cpu: string
  memory: string
}

export interface InfraPodMetric {
  name: string
  namespace: string
  cpu: string
  memory: string
}

export interface InfraMetrics {
  available: boolean
  error?: string
  nodes?: InfraNodeMetric[]
  pods?: InfraPodMetric[]
}

export const infraApi = {
  getTopology: () => http.get<InfraTopology>('/infra/topology'),
  getNodes: () => http.get<{ nodes: InfraNode[] }>('/infra/nodes'),
  getNamespaces: () => http.get<{ namespaces: InfraNamespace[] }>('/infra/namespaces'),
  getPods: () => http.get<{ pods: InfraPod[] }>('/infra/pods'),
  getMetrics: () => http.get<InfraMetrics>('/infra/metrics'),
}
