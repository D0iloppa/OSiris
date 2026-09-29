import { useState } from 'react'
import { Collapse } from 'antd'
import type { OnboardingStep0Status } from '../../api/types'
import { StatusTag, type StatusKind } from '../../components/StatusTag'
import { neutral } from '../../theme/tokens'
import { computeCurrentStepIndex, STEP_TITLES } from './step0Logic'
import { AdminCredentialStep } from './steps/AdminCredentialStep'
import { ProviderStep } from './steps/ProviderStep'
import { AliasStep } from './steps/AliasStep'
import { GatewayTokenStep } from './steps/GatewayTokenStep'
import { VerifyStep } from './steps/VerifyStep'

/**
 * 온보딩 0단계 — 결정적 스택 배선 (30-onboarding-provisioning.md §1.0).
 * LLM 없음, 순수 폼(§1.0.2가 이미 "이 화면은 Admin Chat의 카드 패턴을
 * 재사용하지 않는다"고 못박음) — 그래서 ChatArtifactCard가 아니라 일반
 * antd Collapse 아코디언으로 구현한다.
 *
 * 재개 지점: 최초 렌더 시점에 `GET /onboarding/status`(정확히는 그 안의
 * step0)에서 계산한 단계를 연다 — 로컬에 진행 상태를 저장하지 않는다
 * (§1.0.5와 동일 원칙). 새로고침/재진입하면 옳은 단계에서 다시 시작한다.
 *
 * 주의(설계 이력): 처음엔 단계가 바뀔 때마다 아코디언을 강제
 * 재마운트해서 다음 패널을 자동으로 여는 방식을 시도했으나, 그러면
 * 방금 완료한 패널의 컴포넌트가 통째로 언마운트된다 — 특히
 * `GatewayTokenStep`은 토큰 발급 직후 재기동 커맨드를 그 패널 안에서
 * 보여줘야 하는데, 발급 성공과 동시에 `ready`가 아직 false라 다음
 * 단계로 넘어가 버리면 사용자가 커맨드를 보기도 전에 패널이 닫힌다.
 * 그래서 자동 전진을 포기하고, "최초 진입 시 재개 지점만" 계산하고
 * 이후 패널 전환은 사용자가 직접 클릭하게 둔다 — 완료된 패널의 상태를
 * 안전하게 보존하는 쪽이 더 중요하다는 판단.
 */
export function Step0Wizard({ step0 }: { step0: OnboardingStep0Status }) {
  const currentIndex = computeCurrentStepIndex(step0)
  const [initialIndex] = useState(() => Math.min(computeCurrentStepIndex(step0), 4))

  const steps = [
    { content: <AdminCredentialStep step0={step0} /> },
    { content: <ProviderStep step0={step0} /> },
    { content: <AliasStep step0={step0} /> },
    { content: <GatewayTokenStep step0={step0} /> },
    { content: <VerifyStep /> },
  ]

  const items = steps.map((step, i) => {
    const kind: StatusKind = i < currentIndex ? 'success' : i === currentIndex ? 'info' : 'neutral'
    const label = i < currentIndex ? '완료' : i === currentIndex ? '진행 중' : '대기'
    return {
      key: String(i),
      label: (
        <span style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <span style={{ fontWeight: 600 }}>
            {i + 1}. {STEP_TITLES[i]}
          </span>
          <StatusTag kind={kind}>{label}</StatusTag>
        </span>
      ),
      children: step.content,
      collapsible: (i > currentIndex ? 'disabled' : undefined) as 'disabled' | undefined,
    }
  })

  return (
    <Collapse
      accordion
      defaultActiveKey={String(initialIndex)}
      items={items}
      style={{ background: neutral.white }}
    />
  )
}
