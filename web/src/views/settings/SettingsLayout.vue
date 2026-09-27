<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'

import type { Role } from '@/stores/session'
import { useSessionStore } from '@/stores/session'

const session = useSessionStore()
const route = useRoute()

const sections: { name: string; labelKey: string; role?: Role }[] = [
  { name: 'settings-general', labelKey: 'settings.general.title' },
  { name: 'settings-users', labelKey: 'settings.users.title' },
  { name: 'settings-alerts', labelKey: 'settings.alerts.title', role: 'admin' },
  { name: 'settings-ai', labelKey: 'settings.ai.title', role: 'admin' },
  { name: 'settings-storage', labelKey: 'settings.storage.title', role: 'admin' },
  { name: 'settings-import-export', labelKey: 'config.title', role: 'admin' },
  { name: 'settings-api-keys', labelKey: 'settings.apiKeys.title', role: 'admin' },
  { name: 'settings-security', labelKey: 'settings.security.title', role: 'admin' },
  { name: 'settings-about', labelKey: 'settings.about.title' },
]
const visible = computed(() => sections.filter((s) => !s.role || session.hasRole(s.role)))
</script>

<template>
  <div class="grid gap-6">
    <h1 class="text-2xl font-semibold">{{ $t('nav.settings') }}</h1>
    <nav :aria-label="$t('settings.sections')" class="flex flex-wrap gap-1 border-b">
      <RouterLink
        v-for="s in visible"
        :key="s.name"
        :to="{ name: s.name }"
        :data-testid="`settings-tab-${s.name}`"
        class="-mb-px border-b-2 px-3 py-2 text-sm transition-colors"
        :class="route.name === s.name ? 'border-primary font-medium' : 'text-muted-foreground hover:text-foreground border-transparent'"
      >
        {{ $t(s.labelKey) }}
      </RouterLink>
    </nav>
    <RouterView />
  </div>
</template>
