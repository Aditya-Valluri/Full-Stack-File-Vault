import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import tailwind from '@tailwindcss/vite';

const target = process.env.API_PROXY_TARGET ?? 'http://127.0.0.1:8080';
export default defineConfig({
 plugins: [react(), tailwind()],
 server: {
  port: Number(process.env.WEB_PORT ?? 5173), strictPort: true,
  proxy: Object.fromEntries(['/graphql', '/content/', '/shared-content/'].map(path => [path, { target }])),
 },
 build: { sourcemap: false },
});
