import { Empty, Typography } from 'antd'
import { PageHeader } from '../layout/PageHeader'
import { neutral, radius } from '../theme/tokens'

const { Paragraph } = Typography

/**
 * "준비 중" 플레이스홀더 — 이번 구현 범위(§구현 범위)는 온보딩 0단계뿐이고
 * 나머지 8개 화면은 자리만 잡는다(사이드바·라우팅은 전부 도달 가능해야
 * 함). admin §6.9 빈 상태 패턴 계승: 중앙 정렬 아이콘 + 한 줄 설명.
 */
export function Placeholder({ title, description }: { title: string; description: string }) {
  return (
    <div className="osiris-content-max" style={{ padding: '28px 38px 44px' }}>
      <PageHeader title={title} description={description} />
      <div
        style={{
          background: neutral.white,
          border: '1px solid var(--ant-color-border)',
          borderRadius: radius.xl,
          padding: 64,
          display: 'flex',
          justifyContent: 'center',
        }}
      >
        <Empty
          description={
            <Paragraph type="secondary" style={{ margin: 0 }}>
              이 화면은 이번 구현 범위 밖입니다 — 온보딩 0단계 이후 후속 작업으로 예정되어
              있습니다.
            </Paragraph>
          }
        />
      </div>
    </div>
  )
}
