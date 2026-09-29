import { osirisCssVars } from './tokens'

/**
 * `--osiris-*` CSS 변수를 :root에 주입한다. antd `cssVar: true`가 이미
 * `--ant-*` 변수를 노출하므로, 이 함수는 antd가 커버하지 못하는 값만 다룬다
 * (41 §2 서두 규칙). 앱 부팅 시 1회 호출.
 */
export function applyCssVars(): void {
  const root = document.documentElement
  for (const [key, value] of Object.entries(osirisCssVars)) {
    root.style.setProperty(key, value)
  }
}
