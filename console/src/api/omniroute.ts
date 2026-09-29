import { http } from './http'

/**
 * omniroute 관리 UI를 어디서 여는가 — `GET /_api/omniroute/console`.
 *
 * embedded(osiris-omniroute 내장)는 `/omni/` 서브 URL(같은 오리진),
 * external(이미 운용 중인 omniroute에 붙은 배포)은 그 인스턴스 자신의 주소다.
 * 배포(차트 values `omniroute`)가 osiris-api에 넣어 주는 값을 그대로 쓴다 —
 * 콘솔은 모드를 추측하지 않는다.
 */
export interface OmnirouteConsole {
  mode: 'embedded' | 'external' | string
  url: string
}

/** 응답을 못 받았을 때(구버전 osiris-api·mock 모드)의 기존 동작. */
export const OMNI_URL_FALLBACK = '/omni/'

export const omnirouteApi = {
  getConsole: () => http.get<OmnirouteConsole>('/omniroute/console'),
}
