import { API_BASE } from './config'
import { emitUnauthorized } from './authEvents'

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
    this.name = 'ApiError'
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`${API_BASE}${path}`, {
    ...init,
    // 명시적으로 'same-origin' — 콘솔과 osiris-api는 osiris-gw를 통해 항상
    // 같은 오리진으로 서빙되므로(§84 "브라우저는 /_api/* 를 쓴다") 기본값과
    // 동일하지만, 세션 쿠키 인증이 실제로 무엇에 의존하는지 코드에서
    // 명시적으로 드러내기 위해 적는다.
    credentials: 'same-origin',
    headers: {
      'Content-Type': 'application/json',
      ...(init?.headers ?? {}),
    },
  })
  if (res.status === 401) {
    // 세션 쿠키가 없거나 만료/무효 — AuthGate가 구독해 토큰 입력 화면으로
    // 되돌린다. 최초 로그인 시도 실패(POST /auth/session 자체의 401)에도
    // 발생하지만, 그 시점엔 이미 토큰 화면이 떠 있으므로 무해하다.
    emitUnauthorized()
  }
  if (!res.ok) {
    let message = res.statusText
    try {
      const body = await res.json()
      message = body?.message ?? body?.error ?? message
    } catch {
      // 응답 본문이 JSON이 아닌 경우 statusText를 유지
    }
    throw new ApiError(res.status, message)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const http = {
  get: <T>(path: string) => request<T>(path, { method: 'GET' }),
  post: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'POST', body: body ? JSON.stringify(body) : undefined }),
  put: <T>(path: string, body?: unknown) =>
    request<T>(path, { method: 'PUT', body: body ? JSON.stringify(body) : undefined }),
  delete: <T>(path: string) => request<T>(path, { method: 'DELETE' }),
}
