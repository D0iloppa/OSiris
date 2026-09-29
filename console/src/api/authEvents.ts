/**
 * 세션 만료/무효 감지 파이프 — `http.ts`가 어떤 요청에서든 401을 받으면
 * 이 버스로 알리고, `AuthGate`(components/AuthGate.tsx)가 구독해 화면을
 * 토큰 입력 화면으로 되돌린다. Context/상태 관리 라이브러리를 새로 들여오지
 * 않기 위한 최소 pub-sub — 이 파일 하나가 그 전부다.
 */
type Listener = () => void

const listeners = new Set<Listener>()

export function onUnauthorized(listener: Listener): () => void {
  listeners.add(listener)
  return () => listeners.delete(listener)
}

export function emitUnauthorized(): void {
  for (const l of listeners) l()
}
