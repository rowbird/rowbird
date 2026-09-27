<script setup lang="ts">
import { Languages } from '@lucide/vue'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { isLocale, LOCALE_NAMES, SUPPORTED_LOCALES } from '@/i18n'
import { usePreferencesStore } from '@/stores/preferences'

import ThemeToggle from './ThemeToggle.vue'

const prefs = usePreferencesStore()

function onLocale(value: unknown) {
  if (isLocale(value)) prefs.setLocale(value)
}
</script>

<template>
  <div class="flex items-center gap-1">
    <DropdownMenu>
      <DropdownMenuTrigger as-child>
        <Button variant="ghost" size="icon" :aria-label="$t('language.label')" data-testid="language-menu">
          <Languages aria-hidden="true" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuRadioGroup :model-value="prefs.locale" @update:model-value="onLocale">
          <DropdownMenuRadioItem v-for="l in SUPPORTED_LOCALES" :key="l" :value="l" :lang="l">
            {{ LOCALE_NAMES[l] }}
          </DropdownMenuRadioItem>
        </DropdownMenuRadioGroup>
      </DropdownMenuContent>
    </DropdownMenu>
    <ThemeToggle />
  </div>
</template>
