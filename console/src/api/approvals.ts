import { http } from './http'

/**
 * `osiris-api`의 인프라 제안/승인 표면 —
 * docs/design/90-infra-topology.md §11 (IN17/IN18/IN19, "C-1 — 제안→승인
 * 척추"). 콘솔은 항상 `/_api` base로 부른다(http.ts) — 실제 경로는
 * `/api/infra/proposals*`이고, osiris-gw가 `/_api/*` → `/api/*`로
 * rewrite한다(gw/conf.d/default.conf).
 *
 * 이 화면이 다루는 것은 "제안"뿐이다 — 승인해도 이 빌드는 실제로 아무것도
 * 적용하지 않는다(C-2가 아직 없음). `decision.applied`가 이 타입에 없는
 * 이유가 그것이다 — 존재하지 않는 필드를 있는 척 만들지 않는다.
 */

export type ProposalStatus = 'pending' | 'approved' | 'rejected' | 'changes_requested'

export interface InfraProposal {
  id: string
  status: ProposalStatus
  actionTypeId: string // "infra.apply" | "infra.scale" | "infra.delete"
  targetJson: string // {"namespace":...,"kind":...,"name":...} — 문자열 그대로 저장/전달됨
  payloadJson?: string
  proposedByRef: string
  requestedAt: string
}

export interface CreateProposalRequest {
  actionTypeId: 'infra.apply' | 'infra.scale' | 'infra.delete'
  namespace: string
  kind: string
  name: string
  manifestJson?: string
}

export type Decision = 'approve' | 'reject'

export interface DecideProposalRequest {
  decision: Decision
  comment?: string
}

export const approvalsApi = {
  listPending: () => http.get<{ proposals: InfraProposal[] }>('/infra/proposals?status=pending'),
  create: (req: CreateProposalRequest) =>
    http.post<{ id: string; revision: number; status: ProposalStatus }>('/infra/proposals', req),
  decide: (id: string, req: DecideProposalRequest) =>
    http.post<{ id: string; revision: number; status: ProposalStatus; note: string }>(
      `/infra/proposals/${encodeURIComponent(id)}/decision`,
      req,
    ),
}

/** targetJson은 서버가 저장한 그대로의 문자열이다 — 파싱 실패 시 원문을 보여준다(정직 우선). */
export function parseTarget(targetJson: string): { namespace?: string; kind?: string; name?: string } | undefined {
  try {
    return JSON.parse(targetJson)
  } catch {
    return undefined
  }
}
