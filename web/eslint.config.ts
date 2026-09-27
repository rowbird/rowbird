import { defineConfigWithVueTs, vueTsConfigs } from '@vue/eslint-config-typescript'
import pluginVue from 'eslint-plugin-vue'

export default defineConfigWithVueTs(
  { ignores: ['dist/**', 'coverage/**', 'playwright-report/**', 'test-results/**', 'src/api/schema.d.ts'] },
  pluginVue.configs['flat/recommended'],
  vueTsConfigs.recommended,
  {
    // Template layout is a formatting concern; these rules only add noise to short templates.
    rules: {
      'vue/max-attributes-per-line': 'off',
      'vue/singleline-html-element-content-newline': 'off',
    },
  },
  {
    // shadcn-vue components are vendored as generated source; keep them close to upstream.
    files: ['src/components/ui/**'],
    rules: {
      'vue/require-default-prop': 'off',
      'vue/multi-word-component-names': 'off',
    },
  },
)
