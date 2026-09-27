import { ref } from 'vue'

import { api } from '@/api/client'
import { unwrap } from '@/api/errors'

/** Whether the AI assistant is configured, read once per page. */
export function useAIStatus() {
  const enabled = ref<boolean | null>(null)
  let loading: Promise<void> | null = null

  function load() {
    loading ??= (async () => {
      try {
        enabled.value = unwrap(await api.GET('/api/v1/ai/status')).enabled
      } catch {
        enabled.value = false
      }
    })()
    return loading
  }

  return { enabled, load }
}
