import { defineConfig, mergeConfig } from 'vite'
import vue from '@vitejs/plugin-vue'
import { tokenrouterPlugin } from '@tokenflux/tokenrouter-plugin-sdk/vite'

export default defineConfig(mergeConfig(
  tokenrouterPlugin({ pluginId: 'tokenflux.ticket', outDir: '../dist/frontend' }),
  { plugins: [vue()] },
))
