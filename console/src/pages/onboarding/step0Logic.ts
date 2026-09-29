import type { OnboardingStep0Status } from '../../api/types'

/**
 * 0단계 재개 지점 계산 — 30-onboarding-provisioning.md §1.0.3 순서
 * (0-1 자격증명 → 0-2 프로바이더 → 0-3 별칭 → 0-4 게이트웨이 토큰 → 0-5
 * 헬스 검증) 그대로. §1.0.9: "콘솔이 0단계 화면을 다시 열면
 * GET /onboarding/step0/status가 각 하위 단계의 통과 여부를 즉시 알려주고,
 * 화면은 처음 실패한 단계로 포커스를 옮긴다" — 이 함수가 그 로직이다.
 * 상태는 서버 응답에서만 계산한다(로컬 저장소 없음, §1.0.5와 동일 원칙을
 * 프론트에도 적용).
 */
export function computeCurrentStepIndex(step0: OnboardingStep0Status): number {
  if (!step0.omniroute.manageKeyExchanged) return 0
  if (step0.providers.length === 0) return 1
  const aliasesOk = step0.aliases.length > 0 && step0.aliases.every((a) => a.inSync)
  if (!aliasesOk) return 2
  if (!step0.gatewayToken.issued) return 3
  if (!step0.ready) return 4
  return 5 // 전부 통과
}

export const STEP_TITLES = [
  '관리자 자격증명 교환',
  '프로바이더 등록',
  '별칭(alias) 확정',
  '게이트웨이 토큰 발급',
  '게이트웨이 헬스 검증',
] as const
