import DefaultTheme from 'vitepress/theme'
import type { Theme } from 'vitepress'
import ApiEndpoints from './ApiEndpoints.vue'

export default {
  extends: DefaultTheme,
  enhanceApp({ app }) {
    app.component('ApiEndpoints', ApiEndpoints)
  },
} satisfies Theme
