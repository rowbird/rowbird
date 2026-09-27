import { computed, ref } from 'vue'
import { useRoute } from 'vue-router'

/**
 * Whether the sidebar is expanded, remembered separately for editors (routes with meta.wide, where
 * it starts collapsed to leave room for the SQL and the results) and for every other page. The
 * choice is a per-browser convenience, so it lives in localStorage and survives without it.
 */
const KEYS = { page: 'rowbird.sidebar.page', wide: 'rowbird.sidebar.wide' } as const
const DEFAULTS = { page: true, wide: false }

function read(key: string, fallback: boolean): boolean {
  try {
    const v = localStorage.getItem(key)
    return v === null ? fallback : v === 'true'
  } catch {
    return fallback
  }
}

function write(key: string, value: boolean) {
  try {
    localStorage.setItem(key, String(value))
  } catch {
    // Storage may be unavailable (private windows); the choice then lasts for this visit.
  }
}

const state = ref({ page: read(KEYS.page, DEFAULTS.page), wide: read(KEYS.wide, DEFAULTS.wide) })

export function useSidebarPreference() {
  const route = useRoute()
  const kind = computed(() => (route.meta.wide ? 'wide' : 'page'))
  return computed({
    get: () => state.value[kind.value],
    set: (open: boolean) => {
      state.value = { ...state.value, [kind.value]: open }
      write(KEYS[kind.value], open)
    },
  })
}
