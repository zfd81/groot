import { defineConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import AutoImport from 'unplugin-auto-import/vite'
import Components from 'unplugin-vue-components/vite'
import { ElementPlusResolver } from 'unplugin-vue-components/resolvers'

// Web 界面部署在 groot 服务的 /ui/ 路径下，故 base 固定为 /ui/。
// 开发模式下将 API 请求代理到本地 groot 服务（默认 8080）。
export default defineConfig({
  base: '/ui/',
  plugins: [
    vue(),
    // Element Plus 按需自动引入：组件（<el-button> 等）与其对应样式在使用处
    // 自动导入，无需在每个文件手写 import；同时自动引入 ElMessage 等 API。
    AutoImport({
      imports: ['vue-i18n'],
      resolvers: [ElementPlusResolver()],
    }),
    Components({
      resolvers: [ElementPlusResolver()],
    }),
  ],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  server: {
    proxy: {
      '/chat': { target: 'http://localhost:8080', changeOrigin: true },
      '/sess': { target: 'http://localhost:8080', changeOrigin: true },
      '/agents': { target: 'http://localhost:8080', changeOrigin: true },
      '/skills': { target: 'http://localhost:8080', changeOrigin: true },
      '/tools': { target: 'http://localhost:8080', changeOrigin: true },
      '/models': { target: 'http://localhost:8080', changeOrigin: true },
      '/health': { target: 'http://localhost:8080', changeOrigin: true },
      '/web': { target: 'http://localhost:8080', changeOrigin: true },
    },
  },
})
