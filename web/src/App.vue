<script setup lang="ts">
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { onUnauthorized } from '@/api/client'
import AppShell from '@/components/layout/AppShell.vue'
import BareLayout from '@/components/layout/BareLayout.vue'
import { Toaster } from '@/components/ui/sonner'
import { TooltipProvider } from '@/components/ui/tooltip'
import { isLocale } from '@/i18n'
import { useEventsStore } from '@/stores/events'
import { usePreferencesStore } from '@/stores/preferences'
import { useSessionStore } from '@/stores/session'

import 'vue-sonner/style.css'

const prefs = usePreferencesStore()
const session = useSessionStore()
const events = useEventsStore()
const { t, locale } = useI18n()
const route = useRoute()
const router = useRouter()

const layout = computed(() => (route.meta.layout === 'bare' ? BareLayout : AppShell))

// The signed-in user's profile decides language and theme; before sign-in the browser does.
watch(
  () => session.me,
  (me) => {
    if (!me) return
    if (isLocale(me.locale)) prefs.setLocale(me.locale)
    prefs.setTheme(me.theme)
  },
  { immediate: true },
)

// Real-time events flow while a session can use the app (restricted sessions only change a
// password or enroll a second factor).
watch(
  () => session.me && session.me.restriction === 'none',
  (live) => (live ? events.connect() : events.disconnect()),
  { immediate: true },
)

watch(
  () => prefs.locale,
  (value) => {
    locale.value = value
    document.documentElement.lang = value
  },
  { immediate: true },
)

watch(
  [() => route.meta.titleKey, locale],
  ([key]) => {
    document.title = key ? `${t(key)} · ${t('app.name')}` : t('app.name')
  },
  { immediate: true },
)

onUnauthorized(() => {
  if (!session.me) return
  session.expire()
  if (!route.meta.public) void router.push({ name: 'login', query: { redirect: route.fullPath } })
})
</script>

<template>
  <TooltipProvider>
    <component :is="layout">
      <RouterView />
    </component>
    <Toaster rich-colors position="top-right" :container-aria-label="$t('ui.notifications')" />
  </TooltipProvider>
</template>
