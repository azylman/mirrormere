/// <reference types="vitest/config" />
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

// The build output is committed to web/static/ui/ (fixed names, no hashes) so
// hosts need no Node toolchain. See AGENTS.md ("web/ui/").
export default defineConfig({
  base: './',
  plugins: [react()],
  // Tests read the e-ink settle predicate from sidecars/ (raw import), outside this root.
  server: { fs: { allow: ['../..'] } },
  build: {
    outDir: '../static/ui',
    emptyOutDir: true,
    rollupOptions: {
      // The JS entry, not index.html: index.html is only the dev-server shell.
      input: 'src/main.tsx',
      output: {
        entryFileNames: 'app.js',
        assetFileNames: 'app[extname]',
        codeSplitting: false,
      },
    },
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
  },
});
