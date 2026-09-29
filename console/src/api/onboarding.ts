import { USE_MOCK_API } from './config'
import { onboardingMock } from './mock/onboardingMock'
import { onboardingReal } from './onboardingReal'

/**
 * ★ 목↔실제 전환 지점 ★
 *
 * 이 파일이 콘솔 나머지 부분(훅·페이지)이 임포트하는 유일한 진입점이다.
 * `USE_MOCK_API`(src/api/config.ts, 기본 true) 하나로 아래 전체가 갈린다.
 * 페이지/훅 코드는 이 객체만 알고, mock/real 구현이 각각 있다는 사실을
 * 모른다 — 백엔드가 준비되면 config.ts의 스위치만 바꾸면 된다.
 */
export const onboardingApi = USE_MOCK_API ? onboardingMock : onboardingReal
