import { useCallback, useEffect, useRef, useState } from 'react'
import { Alert, Button, Space, Spin, Tooltip, Typography } from 'antd'
import { ExportOutlined, ReloadOutlined } from '@ant-design/icons'
import { neutral, radius } from '../../theme/tokens'
import { useOmnirouteConsole } from '../../hooks/useOmnirouteConsole'

const { Paragraph, Text } = Typography

/**
 * omniroute 관리 UI를 **콘솔 안에 그대로 임베드**한다(감독 지시: 1순위
 * iframe, 팝업은 최후의 수단).
 *
 * ── 왜 iframe이 가능한가 ──────────────────────────────────────────────────
 *
 * omniroute는 `/omni` **서브 URL**로 서빙된다 — 별도 서브도메인이 아니다
 * (감독 확정: "서브도메인을 매번 발행하는 것은 적절하지 않음"). 그래서
 * 이 콘솔과 omniroute는 **같은 오리진**이고, 프레임 정책을
 * `frame-ancestors 'self'`까지만 풀면 임베드가 성립한다 — 남의 사이트에
 * 우리를 끼워 넣도록 여는 것이 아니라 우리 자신 안에서만 허용하는 것이다.
 * 서브도메인이었다면 `'self'`로는 부족해 출처를 명시적으로 나열해야 했다.
 *
 * ── 그래도 서버 설정이 필요하다 ───────────────────────────────────────────
 *
 * omniroute는 기본적으로 `X-Frame-Options: DENY` + CSP
 * `frame-ancestors 'none'`을 보낸다(`next.config.mjs`의 securityHeaders,
 * 하드코딩 — env 노브 없음). omniroute 소스는 고치지 않는 것이 이 저장소의
 * 규율이므로, **게이트웨이(ingress-nginx)에서 그 두 헤더만 덮는다.**
 * 절차는 `bin/omni-embed-enable.sh`에 있다.
 *
 * 참고로 이 완화는 omniroute 자신이 이미 쓰는 패턴이다 — 벤더도 자기
 * 대시보드에 9Router UI를 끼워 넣으려고 특정 경로에만
 * `frame-ancestors 'self'` 예외를 준다(`next.config.mjs`의 headers(),
 * `NinerouterEmbedFrame.tsx`). 우리가 발명한 우회가 아니라 같은 수단이다.
 *
 * ── 실패하면 숨기지 않는다 ────────────────────────────────────────────────
 *
 * 서버 설정이 아직 안 됐으면 브라우저는 프레임을 조용히 비워 둔다 —
 * 화면상으로는 "그냥 안 나오는" 상태가 되어 원인을 알 수 없다. 그래서
 * 로드 후 같은 오리진 접근을 시도해 **실제로 내용이 들어왔는지 확인**하고,
 * 아니면 무엇을 해야 하는지 화면에 적는다. 새 창 버튼은 그 실패 경로에만
 * 제공한다(팝업은 최후의 수단이라는 지시 그대로 — 평상시 동작이 아니라
 * 고장났을 때의 탈출구다).
 */
// 주소는 하드코딩하지 않는다 — embedded는 `/omni/`, external은 외부 인스턴스 주소를
// osiris-api(`GET /_api/omniroute/console`)가 배포 모드에 맞춰 알려 준다.

/** 프레임이 채워졌는지 판정하기까지 기다리는 시간. 블로킹된 경우 브라우저에
 *  따라 load 이벤트 자체가 오지 않을 수 있어 타임아웃이 필요하다. */
const PROBE_TIMEOUT_MS = 8000

type FrameState = 'loading' | 'ok' | 'blocked'

export function OmnirouteSettingsPage() {
  const iframeRef = useRef<HTMLIFrameElement | null>(null)
  const [state, setState] = useState<FrameState>('loading')
  const [nonce, setNonce] = useState(0)
  const omni = useOmnirouteConsole()
  const OMNI_URL = omni.url

  const reload = useCallback(() => {
    setState('loading')
    setNonce((n) => n + 1)
  }, [])

  // 타임아웃 — load가 아예 오지 않는 차단 형태를 잡는다.
  useEffect(() => {
    if (state !== 'loading') return
    const t = window.setTimeout(() => setState((s) => (s === 'loading' ? 'blocked' : s)), PROBE_TIMEOUT_MS)
    return () => window.clearTimeout(t)
  }, [state, nonce])

  const handleLoad = () => {
    // 다른 오리진(external)은 내용 확인이 원천적으로 불가 — load를 성공으로 본다
    // (차단돼도 브라우저가 오류 문서로 load를 보내므로, 헤더의 새 창 버튼이 탈출구).
    if (omni.crossOrigin) {
      setState('ok')
      return
    }
    // 같은 오리진이므로 정상 로드라면 location을 읽을 수 있다. 프레임이
    // 차단되면 브라우저가 불투명한 오류 문서를 넣어 접근이 throw하거나
    // about:blank가 된다 — 그 둘을 모두 실패로 본다.
    try {
      const href = iframeRef.current?.contentWindow?.location?.href
      setState(href && href !== 'about:blank' ? 'ok' : 'blocked')
    } catch {
      setState('blocked')
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', minHeight: 0 }}>
      <div
        style={{
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'space-between',
          gap: 12,
          marginBottom: 12,
        }}
      >
        <div style={{ minWidth: 0 }}>
          <Text strong style={{ fontSize: 16 }}>
            omniroute 설정
          </Text>
          <Paragraph type="secondary" style={{ margin: 0, fontSize: 12 }}>
            LLM 프로바이더·별칭·API 키는 omniroute가 단일 보관처다. 이 화면은 그 관리 UI를 콘솔 안에
            그대로 띄운다.
          </Paragraph>
        </div>
        <Space>
          <Tooltip title="다시 불러오기">
            <Button icon={<ReloadOutlined />} onClick={reload} aria-label="다시 불러오기" />
          </Tooltip>
          <Button
            icon={<ExportOutlined />}
            onClick={() => window.open(OMNI_URL, '_blank', 'noopener,noreferrer')}
          >
            새 창
          </Button>
        </Space>
      </div>

      {state === 'blocked' && (
        <Alert
          type="warning"
          showIcon
          style={{ marginBottom: 12 }}
          message="omniroute UI를 이 페이지 안에 띄울 수 없습니다"
          description={
            <>
              omniroute가 프레임 임베드를 거부하고 있습니다(<Text code>X-Frame-Options: DENY</Text> /
              CSP <Text code>frame-ancestors 'none'</Text>). 서버에서 한 번만 열어 주면 됩니다:
              <Paragraph style={{ margin: '8px 0 0' }}>
                <Text code>bin/omni-embed-enable.sh</Text>
              </Paragraph>
              <Paragraph type="secondary" style={{ margin: '8px 0 0', fontSize: 12 }}>
                게이트웨이에서 그 두 헤더만 <Text code>frame-ancestors 'self'</Text>로 덮습니다 —
                같은 오리진(<Text code>/omni</Text> 서브 URL)이라 외부에는 여전히 닫혀 있습니다.
                그때까지는 위 <Text strong>새 창</Text> 버튼으로 쓰실 수 있습니다.
              </Paragraph>
            </>
          }
        />
      )}

      <div
        style={{
          position: 'relative',
          flex: 1,
          minHeight: 520,
          border: `1px solid ${neutral.border}`,
          borderRadius: radius.xl,
          overflow: 'hidden',
          background: neutral.white,
        }}
      >
        {state === 'loading' && (
          <div
            style={{
              position: 'absolute',
              inset: 0,
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              zIndex: 1,
              background: neutral.white,
            }}
          >
            <Spin size="large" />
          </div>
        )}
        {omni.ready && (
        <iframe
          key={`${nonce}:${OMNI_URL}`}
          ref={iframeRef}
          src={OMNI_URL}
          onLoad={handleLoad}
          title="omniroute 관리 UI"
          style={{ width: '100%', height: '100%', border: 'none', display: 'block' }}
        />
        )}
      </div>
    </div>
  )
}
