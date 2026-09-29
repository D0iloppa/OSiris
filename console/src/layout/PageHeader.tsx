import type { ReactNode } from 'react'
import { Typography } from 'antd'

const { Title, Text } = Typography

/**
 * 공용 PageHeader (41 §5.3, DESIGN-admin.md §5.3 계승) — 9개 화면 전부
 * 최상단에 배치. title(h1 20/700) + description(캡션, gray-500) + extra
 * (우측 액션). 하단 마진 20px.
 */
export function PageHeader({
  title,
  description,
  extra,
}: {
  title: string
  description?: string
  extra?: ReactNode
}) {
  return (
    <div
      style={{
        display: 'flex',
        justifyContent: 'space-between',
        alignItems: 'flex-start',
        marginBottom: 20,
      }}
    >
      <div>
        <Title level={4} style={{ margin: 0, fontSize: 20, fontWeight: 700 }}>
          {title}
        </Title>
        {description && (
          <Text type="secondary" style={{ fontSize: 12 }}>
            {description}
          </Text>
        )}
      </div>
      {extra && <div>{extra}</div>}
    </div>
  )
}
