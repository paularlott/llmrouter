import { defineConfig } from 'vite';
import { resolve } from 'path';
import tailwindcss from '@tailwindcss/vite';

export default defineConfig({
  root: 'web',
  publicDir: false,
  plugins: [tailwindcss()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    manifest: true,
    rollupOptions: {
      input: {
        main: resolve(__dirname, 'web/src/main.js'),
      },
      output: {
        entryFileNames: 'assets/[name].js',
        chunkFileNames: 'assets/[name].js',
        assetFileNames: 'assets/[name].[ext]',
      },
    },
  },
  server: {
    port: 5173,
    strictPort: false,
    proxy: {
      '/api': {
        target: 'http://localhost:12345',
        changeOrigin: true,
      },
      '/admin': {
        target: 'http://localhost:12345',
        changeOrigin: true,
      },
    },
  },
});
