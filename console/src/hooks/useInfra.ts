import { useQuery } from '@tanstack/react-query'
import { infraApi } from '../api/infra'

/**
 * 인프라 토폴로지 A단계 훅. 폴링 간격은 30초 — SSE 배선(`/_api/events`)이
 * 있지만 인프라 토폴로지 이벤트는 아직 그 스트림에 얹혀 있지 않으므로
 * (B단계 이후 후보), 지금은 폴링으로 "거의 실시간"을 흉내낸다.
 */
const REFRESH_MS = 30_000

export function useInfraTopology() {
  return useQuery({
    queryKey: ['infra', 'topology'],
    queryFn: () => infraApi.getTopology(),
    refetchInterval: REFRESH_MS,
  })
}

export function useInfraMetrics() {
  return useQuery({
    queryKey: ['infra', 'metrics'],
    queryFn: () => infraApi.getMetrics(),
    refetchInterval: REFRESH_MS,
  })
}
