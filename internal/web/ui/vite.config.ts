import tailwindcss from '@tailwindcss/vite';
import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// The build goes to ../dist, which the Go binary embeds.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  build: { outDir: '../dist', emptyOutDir: true, assetsInlineLimit: 0 },
  server: { proxy: { '/api': 'http://127.0.0.1:8443' } }
});
