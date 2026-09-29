/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** 목(mock) ↔ 실제 API 전환 스위치. 'real'이 아니면 전부 mock. src/api/config.ts 참고. */
  readonly VITE_API_MODE?: 'mock' | 'real'
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
