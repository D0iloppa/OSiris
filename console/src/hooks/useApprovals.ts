import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { approvalsApi, type CreateProposalRequest, type DecideProposalRequest } from '../api/approvals'

/**
 * 인프라 제안/승인 훅 — docs/design/90-infra-topology.md §11 C-1.
 * Ops 화면의 다른 폴링 훅(useInfra.ts)과 같은 30초 간격을 쓴다: 이 표면도
 * 아직 SSE 이벤트 스트림에 얹혀 있지 않다.
 */
const REFRESH_MS = 30_000
const PENDING_KEY = ['infra', 'proposals', 'pending'] as const

export function usePendingProposals() {
  return useQuery({
    queryKey: PENDING_KEY,
    queryFn: () => approvalsApi.listPending(),
    refetchInterval: REFRESH_MS,
  })
}

export function useCreateProposal() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: CreateProposalRequest) => approvalsApi.create(req),
    onSuccess: () => qc.invalidateQueries({ queryKey: PENDING_KEY }),
  })
}

export function useDecideProposal() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, req }: { id: string; req: DecideProposalRequest }) => approvalsApi.decide(id, req),
    onSuccess: () => qc.invalidateQueries({ queryKey: PENDING_KEY }),
  })
}
