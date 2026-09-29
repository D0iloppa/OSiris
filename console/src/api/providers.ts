import { http } from './http'
import type {
  CreateProviderRequest,
  ListProvidersResponse,
  OAuthAuthorizeResponse,
  OAuthDeviceCodeResponse,
  OAuthExchangeRequest,
  ProviderConnection,
  UpdateProviderRequest,
} from './types'

/**
 * `osiris-api`의 `/api/providers`, `/api/oauth/{provider}/*` —
 * api/internal/providers/handlers.go의 계약 그대로. 콘솔은 항상 `/_api`
 * base로 부른다(http.ts). omniroute를 브라우저가 직접 부르는 경로는 없다.
 *
 * 시크릿(apiKey/token류)은 서버가 이미 redact()해 boolean으로만 보낸다 —
 * 이 파일은 그 값을 그대로 통과시킬 뿐 가공하지 않는다.
 */
export const providersApi = {
  list: () => http.get<ListProvidersResponse>('/providers').then((r) => r.connections ?? []),

  create: (req: CreateProviderRequest) =>
    http.post<{ connection: ProviderConnection }>('/providers', req).then((r) => r.connection),

  update: (id: string, req: UpdateProviderRequest) =>
    http.put<{ connection: ProviderConnection }>(`/providers/${id}`, req).then((r) => r.connection),

  remove: (id: string) => http.delete<void>(`/providers/${id}`),

  // ── OAuth 대리 (redirect/device/import) ──────────────────────────────
  authorize: (provider: string, redirectUri?: string) =>
    http.get<OAuthAuthorizeResponse>(
      `/oauth/${provider}/authorize${redirectUri ? `?redirect_uri=${encodeURIComponent(redirectUri)}` : ''}`
    ),

  deviceCode: (provider: string) => http.get<OAuthDeviceCodeResponse>(`/oauth/${provider}/device-code`),

  exchange: (provider: string, req: OAuthExchangeRequest) =>
    http.post<Record<string, unknown>>(`/oauth/${provider}/exchange`, req),

  poll: (provider: string, body: Record<string, unknown>) =>
    http.post<Record<string, unknown>>(`/oauth/${provider}/poll`, body),

  importToken: (provider: string, body: Record<string, unknown>) =>
    http.post<Record<string, unknown>>(`/oauth/${provider}/import-token`, body),
}
