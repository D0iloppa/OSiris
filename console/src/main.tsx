import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { ConfigProvider } from 'antd'
import koKR from 'antd/locale/ko_KR'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import './index.css'
import App from './App.tsx'
import { AuthGate } from './components/AuthGate'
import { ErrorBoundary } from './components/ErrorBoundary'
import { osirisTheme } from './theme/tokens'
import { applyCssVars } from './theme/applyCssVars'

// --osiris-* CSS 변수 주입 — antd ConfigProvider(cssVar:true)가 --ant-*를
// 노출하는 것과 별개로, tokens.ts §2의 "antd가 커버하지 못하는 개념"만
// 여기서 :root에 얹는다. 앱 부팅 시 1회.
applyCssVars()

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      retry: 1,
      refetchOnWindowFocus: false,
    },
  },
})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ConfigProvider theme={osirisTheme} locale={koKR}>
        <ErrorBoundary>
          <AuthGate>
            <App />
          </AuthGate>
        </ErrorBoundary>
      </ConfigProvider>
    </QueryClientProvider>
  </StrictMode>,
)
