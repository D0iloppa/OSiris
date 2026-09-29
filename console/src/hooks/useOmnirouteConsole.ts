import { useQuery } from '@tanstack/react-query'
import { OMNI_URL_FALLBACK, omnirouteApi } from '../api/omniroute'

/** omniroute 관리 UI 주소(모드에 따라 osiris-api가 알려 줌) — api/omniroute.ts 주석 참고. */
export function useOmnirouteConsole() {
  const q = useQuery({
    queryKey: ['omniroute', 'console'],
    queryFn: () => omnirouteApi.getConsole(),
    staleTime: Infinity,
    retry: 1,
  })
  const url = q.data?.url || OMNI_URL_FALLBACK
  return {
    url,
    mode: q.data?.mode ?? 'embedded',
    /** 다른 오리진이면 iframe 내용 확인(same-origin probe)을 할 수 없다. */
    crossOrigin: new URL(url, window.location.href).origin !== window.location.origin,
    ready: !q.isLoading,
  }
}
