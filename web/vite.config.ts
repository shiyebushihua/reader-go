import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
export default defineConfig({ plugins: [vue()], base: './', server: { proxy: { '/api': { target: process.env.VITE_API_PROXY || 'http://127.0.0.1:8080', changeOrigin: false } } } })
