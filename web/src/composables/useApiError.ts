import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { ApiError, errorMessage, fieldMessage } from '@/api/errors'

/** Holds the last error of a form and turns it into translated messages. */
export function useApiError() {
  const { t, te } = useI18n()
  const error = ref<unknown>(null)

  const message = computed(() => {
    if (!error.value) return ''
    // Field errors are shown next to their fields; the banner only repeats the summary.
    return errorMessage(error.value, t, te)
  })
  const hasFieldErrors = computed(() => error.value instanceof ApiError && Object.keys(error.value.fields).length > 0)

  return {
    error,
    message,
    hasFieldErrors,
    field: (name: string) => fieldMessage(error.value, name, t, te),
    clear: () => (error.value = null),
  }
}
