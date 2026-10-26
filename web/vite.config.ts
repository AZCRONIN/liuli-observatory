import { defineConfig } from 'vite';

export default defineConfig({
    build: {
        outDir: 'dist',
        emptyOutDir: true,
        assetsDir: 'assets',
        chunkSizeWarningLimit: 800,
    },
    server: {
        proxy: { '/api': 'http://127.0.0.1:8090' },
    },
});
