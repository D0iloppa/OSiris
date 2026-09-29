import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { providersApi } from '../api/providers'
import type {
  CreateProviderRequest,
  OAuthExchangeRequest,
  UpdateProviderRequest,
} from '../api/types'

/**
 * 프로바이더 연결 목록/CRUD 훅 — 설정 화면(ProviderConnectionsSection)과
 * 온보딩(ProviderStep)이 **둘 다** 이 하나의 훅 세트를 쓴다(등록 UI·로직
 * 중복 금지, 감독 지시). 온보딩 쪽은 추가로 자기 자신의 `onboarding.status`
 * 쿼리도 무효화해야 진행 단계가 갱신되므로, 그 쪽은
 * `useOnboarding.ts`에서처럼 각자 onSuccess에서 그 쿼리키도 함께
 * invalidate한다 — 이 파일은 `providers` 목록 쿼리 하나만 책임진다.
 */
const PROVIDERS_KEY = ['providers', 'list'] as const

export function useProviders() {
  return useQuery({
    queryKey: PROVIDERS_KEY,
    queryFn: () => providersApi.list(),
  })
}

export function invalidateProviders(qc: ReturnType<typeof useQueryClient>) {
  return qc.invalidateQueries({ queryKey: PROVIDERS_KEY })
}

export function useCreateProvider() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: CreateProviderRequest) => providersApi.create(req),
    onSuccess: () => invalidateProviders(qc),
  })
}

export function useUpdateProvider() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ id, req }: { id: string; req: UpdateProviderRequest }) => providersApi.update(id, req),
    onSuccess: () => invalidateProviders(qc),
  })
}

export function useDeleteProvider() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => providersApi.remove(id),
    onSuccess: () => invalidateProviders(qc),
  })
}

// ── OAuth 흐름 — 목록을 바꾸지 않는 중간 단계는 invalidate하지 않고, 최종
// 성공(exchange/poll 성공, importToken 성공)에서만 목록을 갱신한다. ────────

export function useOAuthAuthorize() {
  return useMutation({
    mutationFn: ({ provider, redirectUri }: { provider: string; redirectUri?: string }) =>
      providersApi.authorize(provider, redirectUri),
  })
}

export function useOAuthExchange() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ provider, req }: { provider: string; req: OAuthExchangeRequest }) =>
      providersApi.exchange(provider, req),
    onSuccess: () => invalidateProviders(qc),
  })
}

export function useOAuthDeviceCode() {
  return useMutation({
    mutationFn: (provider: string) => providersApi.deviceCode(provider),
  })
}

export function useOAuthPoll() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ provider, body }: { provider: string; body: Record<string, unknown> }) =>
      providersApi.poll(provider, body),
    onSuccess: () => invalidateProviders(qc),
  })
}
