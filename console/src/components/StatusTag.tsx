import {
  CheckCircleFilled,
  CloseCircleFilled,
  ExclamationCircleFilled,
  MinusCircleFilled,
  SyncOutlined,
} from '@ant-design/icons'
import { semantic } from '../theme/tokens'

export type StatusKind = 'success' | 'warning' | 'error' | 'info' | 'neutral'

/**
 * 상태는 항상 이중 인코딩(41 §1 원칙 6, §2.3.3) — 색만으로 상태를
 * 구분하지 않는다. warning(느낌표 삼각 느낌)과 error(원형 X)는 특히
 * CVD(적록색맹)에서 거의 구분되지 않는 hue 쌍(ΔE 1.6, §2.3.3 실측)이므로
 * 모양이 반드시 달라야 한다.
 */
const ICONS: Record<StatusKind, React.ComponentType<{ style?: React.CSSProperties }>> = {
  success: CheckCircleFilled,
  warning: ExclamationCircleFilled,
  error: CloseCircleFilled,
  info: SyncOutlined,
  neutral: MinusCircleFilled,
}

export function StatusTag({ kind, children }: { kind: StatusKind; children: React.ReactNode }) {
  const { base, tint } = semantic[kind]
  const Icon = ICONS[kind]
  return (
    <span
      style={{
        display: 'inline-flex',
        alignItems: 'center',
        gap: 4,
        padding: '2px 10px',
        borderRadius: 999,
        background: tint,
        color: base,
        fontSize: 11,
        fontWeight: 600,
        lineHeight: '18px',
      }}
    >
      <Icon style={{ fontSize: 12, color: base }} />
      {children}
    </span>
  )
}
