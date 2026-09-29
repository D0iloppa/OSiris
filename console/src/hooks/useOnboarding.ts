import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { onboardingApi } from '../api/onboarding'
import type { AliasEntryInput, RegisterProviderRequest } from '../api/types'

const STATUS_KEY = ['onboarding', 'status'] as const
const ALIAS_DEFAULTS_KEY = ['onboarding', 'alias-defaults'] as const

export function useOnboardingStatus() {
  return useQuery({
    queryKey: STATUS_KEY,
    queryFn: () => onboardingApi.getStatus(),
  })
}

export function useAliasDefaults(enabled: boolean) {
  return useQuery({
    queryKey: ALIAS_DEFAULTS_KEY,
    queryFn: () => onboardingApi.getAliasDefaults(),
    enabled,
  })
}

/**
 * 자격증명 입력 뮤테이션 공통 규칙: 변수(password/apiKey)는 mutationFn
 * 인자로만 흐르고, 어떤 컴포넌트 state·query 캐시에도 남지 않는다.
 * 성공/실패와 무관하게 폼 쪽(Step0Wizard)이 즉시 resetFields()하므로
 * antd Form 내부 상태에도 남지 않는다 — localStorage/sessionStorage/URL은
 * 애초에 건드리지 않는다.
 */
export function useExchangeAdmin() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (password: string) => onboardingApi.exchangeAdmin({ password }),
    onSuccess: () => qc.invalidateQueries({ queryKey: STATUS_KEY }),
  })
}

export function useRegisterProvider() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (req: RegisterProviderRequest) => onboardingApi.registerProvider(req),
    onSuccess: () => qc.invalidateQueries({ queryKey: STATUS_KEY }),
  })
}

export function useUpsertAliases() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (entries: AliasEntryInput[]) => onboardingApi.upsertAliases({ entries }),
    onSuccess: () => qc.invalidateQueries({ queryKey: STATUS_KEY }),
  })
}

export function useIssueGatewayToken() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => onboardingApi.issueGatewayToken(),
    onSuccess: () => qc.invalidateQueries({ queryKey: STATUS_KEY }),
  })
}

export function useVerifyGateway() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => onboardingApi.verifyGateway(),
    onSuccess: () => qc.invalidateQueries({ queryKey: STATUS_KEY }),
  })
}
