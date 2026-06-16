/// <reference types="vite/client" />

// Type the Vite env vars we consume so `import.meta.env.VITE_*` is checked.
interface ImportMetaEnv {
  readonly VITE_API_BASE?: string;
  readonly VITE_WS_URL?: string;
}

interface ImportMeta {
  readonly env: ImportMetaEnv;
}
