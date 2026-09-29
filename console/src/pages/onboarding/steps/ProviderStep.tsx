import { useState } from 'react'
import { Button, List, Space, Typography } from 'antd'
import { PlusOutlined } from '@ant-design/icons'
import { useQueryClient } from '@tanstack/react-query'
import { ProviderRegisterModal } from '../../../components/providers/ProviderRegisterModal'
import { findCatalogEntry } from '../../../api/providerCatalog'
import type { OnboardingStep0Status } from '../../../api/types'

const { Text } = Typography

/**
 * 0-2 — 프로바이더 등록.
 *
 * [2026-09-11 개편] 이전엔 이 스텝이 자기 자신의 등록 폼(이름+provider+
 * apiKey 자유입력, `POST /onboarding/step0/providers`)을 따로 그렸다.
 * 이제는 설정 화면(ProviderConnectionsSection)과 **같은**
 * `ProviderRegisterModal`을 그대로 재사용한다(감독 지시: 등록 UI·로직을
 * 두 곳에 중복시키지 않는다) — 최초엔 프로바이더가 0개라 온보딩이 이
 * 등록을 담당하고, 이후 추가/수정/삭제는 설정 화면에서 계속한다는
 * 역할분담 그대로다. 모달은 콘솔의 "표준" 프로바이더 CRUD 표면
 * (`/_api/providers`, `/_api/oauth/*`)을 부르므로, 온보딩 전용 엔드포인트
 * (`step0/providers`)에 있던 "자유 입력 provider 문자열" 제약이 사라지고
 * 카탈로그 기반 선택 + OAuth 방식별 등록까지 온보딩에서도 그대로 된다.
 *
 * 등록 성공 시 이 온보딩 화면의 진행 상태(`step0.providers`)가 갱신되도록
 * `onboarding.status` 쿼리도 함께 invalidate한다 — omniroute 쪽 실제 데이터는
 * 같은 저장소이므로 목록은 이미 최신이고, 이건 온보딩 단계 전환 로직
 * (step0Logic.ts)이 구독하는 쿼리를 갱신하는 것뿐이다.
 */
export function ProviderStep({ step0 }: { step0: OnboardingStep0Status }) {
  const [open, setOpen] = useState(false)
  const qc = useQueryClient()

  return (
    <Space direction="vertical" style={{ width: '100%' }} size={16}>
      {step0.providers.length > 0 && (
        <List
          size="small"
          bordered
          dataSource={step0.providers}
          renderItem={(p) => (
            <List.Item>
              <Text strong>{p.name}</Text>
              <Text type="secondary" style={{ marginLeft: 8 }}>
                {findCatalogEntry(p.provider)?.label ?? p.provider}
              </Text>
            </List.Item>
          )}
        />
      )}

      <Button type="primary" icon={<PlusOutlined />} onClick={() => setOpen(true)}>
        프로바이더 연결 추가
      </Button>

      <ProviderRegisterModal
        open={open}
        onClose={() => setOpen(false)}
        onSuccess={() => qc.invalidateQueries({ queryKey: ['onboarding', 'status'] })}
      />
    </Space>
  )
}
