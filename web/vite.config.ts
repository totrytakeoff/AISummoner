import react from '@vitejs/plugin-react'
import { loadEnv } from 'vite'
import { defineConfig } from 'vitest/config'

export default defineConfig(({ mode }) => {
  const environment = loadEnv(mode, '.', '')
  return {
    plugins: [react()],
    server: {
      proxy: {
        '/api': {
          target: environment.AISUMMONER_WEB_API_ORIGIN || 'http://127.0.0.1:8080',
          changeOrigin: false,
          ws: true,
        },
      },
    },
    test: {
      include: ['src/**/*.test.{ts,tsx}'],
      environment: 'jsdom',
      globals: true,
      setupFiles: './src/test/setup.ts',
      css: true,
      restoreMocks: true,
      clearMocks: true,
    },
  }
})
