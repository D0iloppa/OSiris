import { Component, type ErrorInfo, type ReactNode } from 'react'
import { Button, Result } from 'antd'

/**
 * 화면 렌더 중 던져진 예외에 대한 최후 방어선.
 *
 * 배경: 2026-07-27 사용자가 보고한 장애 — 브라우저 콘솔에
 * `TypeError: Cannot read properties of null (reading 'length')`가 찍히고
 * UI가 최초 렌더 직후 흰 화면으로 전환됐다. 근본 원인은 Go API가
 * `providers`/`aliases` 배열을 계약(`console/src/api/types.ts`, 비
 * nullable)과 달리 `null`로 내보낸 것(`api/internal/onboarding/
 * handlers.go`에서 수정됨) — 이 컴포넌트는 그 근본 원인의 수정이 아니라,
 * "렌더 중 예외 하나가 React 트리 전체를 무정보 흰 화면으로 만드는" 최악의
 * 실패 형태 자체를 없애는 안전망이다. 계약이 다시 깨지거나 예상 못한 다른
 * 렌더 예외가 나더라도, 사용자는 최소한 "무슨 일이 났는지" 보고 다시
 * 시도할 수 있어야 한다.
 *
 * React의 에러 바운더리는 클래스 컴포넌트로만 구현 가능하다(getDerived
 * StateFromError/componentDidCatch에 훅 대응 API가 없음 — React 공식
 * 문서상의 제약이며 이 파일만의 특이사항이 아니다).
 */
interface Props {
  children: ReactNode
}

interface State {
  error: Error | null
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // 흰 화면 대신 최소한 콘솔에는 무엇이 어디서 죽었는지 남긴다.
    console.error('ErrorBoundary caught a render error:', error, info.componentStack)
  }

  private handleRetry = () => {
    this.setState({ error: null })
  }

  render() {
    const { error } = this.state
    if (!error) {
      return this.props.children
    }
    return (
      <Result
        status="error"
        title="화면을 표시하는 중 오류가 발생했습니다"
        subTitle={error.message}
        extra={[
          <Button type="primary" key="retry" onClick={this.handleRetry}>
            다시 시도
          </Button>,
          <Button key="reload" onClick={() => window.location.reload()}>
            새로고침
          </Button>,
        ]}
      />
    )
  }
}
