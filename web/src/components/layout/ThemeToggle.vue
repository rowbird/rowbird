<script setup lang="ts">
import { Monitor, Moon, Sun } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { Button } from '@/components/ui/button'
import { usePreferenceActions } from '@/composables/usePreferenceActions'
import { usePreferencesStore } from '@/stores/preferences'

const prefs = usePreferencesStore()
const actions = usePreferenceActions()
const { t } = useI18n()

const icon = computed(() => ({ system: Monitor, light: Sun, dark: Moon })[prefs.theme])
const label = computed(() => `${t('theme.toggle')}: ${t(`theme.${prefs.theme}`)}`)
</script>

<template>
  <Button
    variant="ghost"
    size="icon"
    :aria-label="label"
    :title="label"
    data-testid="theme-toggle"
    @click="actions.cycleTheme()"
  >
    <component :is="icon" aria-hidden="true" />
  </Button>
</template>
