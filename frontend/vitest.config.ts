import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'happy-dom',
    globals: true,
    setupFiles: ['./src/test-setup.ts'],
    css: true,
    coverage: {
      provider: 'v8',
      reporter: ['text', 'lcov'],
      include: ['src/**/*.{ts,tsx}'],
      exclude: [
        'src/**/*.test.{ts,tsx}',
        'src/main.tsx',
        'src/types.ts',
        // App.tsx is a routing/composition component — integration test territory
        'src/App.tsx',
        // Editor.tsx wraps CodeMirror, which requires complex 3rd-party library mocking
        'src/components/Editor.tsx',
      ],
      thresholds: {
        lines: 80,
      },
    },
  },
});
