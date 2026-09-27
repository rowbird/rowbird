<script setup lang="ts">
import { CircleUser, Languages, LogOut, Palette, UserRound } from '@lucide/vue'
import { useRouter } from 'vue-router'

import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import { usePreferenceActions } from '@/composables/usePreferenceActions'
import { isLocale, LOCALE_NAMES, SUPPORTED_LOCALES } from '@/i18n'
import { THEMES, type Theme, usePreferencesStore } from '@/stores/preferences'
import { useSessionStore } from '@/stores/session'

const prefs = usePreferencesStore()
const session = useSessionStore()
const actions = usePreferenceActions()
const router = useRouter()

function onLocale(value: unknown) {
  if (isLocale(value)) actions.setLocale(value)
}

function onTheme(value: unknown) {
  if (THEMES.includes(value as Theme)) actions.setTheme(value as Theme)
}

async function signOut() {
  await session.logout()
  await router.replace({ name: 'login' })
}
</script>

<template>
  <DropdownMenu>
    <DropdownMenuTrigger as-child>
      <Button variant="ghost" size="icon" :aria-label="$t('userMenu.label')" data-testid="user-menu">
        <CircleUser aria-hidden="true" />
      </Button>
    </DropdownMenuTrigger>
    <DropdownMenuContent align="end" class="w-60">
      <DropdownMenuLabel v-if="session.me" class="grid font-normal">
        <span class="truncate font-medium">{{ session.me.name }}</span>
        <span class="text-muted-foreground truncate text-xs">{{ session.me.email }}</span>
      </DropdownMenuLabel>
      <DropdownMenuItem as-child>
        <RouterLink to="/profile" data-testid="menu-profile">
          <UserRound aria-hidden="true" />
          {{ $t('profile.title') }}
        </RouterLink>
      </DropdownMenuItem>
      <DropdownMenuSeparator />
      <DropdownMenuLabel class="flex items-center gap-2">
        <Languages class="size-4" aria-hidden="true" />
        {{ $t('language.label') }}
      </DropdownMenuLabel>
      <DropdownMenuRadioGroup :model-value="prefs.locale" @update:model-value="onLocale">
        <DropdownMenuRadioItem v-for="locale in SUPPORTED_LOCALES" :key="locale" :value="locale" :lang="locale">
          {{ LOCALE_NAMES[locale] }}
        </DropdownMenuRadioItem>
      </DropdownMenuRadioGroup>
      <DropdownMenuSeparator />
      <DropdownMenuLabel class="flex items-center gap-2">
        <Palette class="size-4" aria-hidden="true" />
        {{ $t('theme.label') }}
      </DropdownMenuLabel>
      <DropdownMenuRadioGroup :model-value="prefs.theme" @update:model-value="onTheme">
        <DropdownMenuRadioItem v-for="theme in THEMES" :key="theme" :value="theme">
          {{ $t(`theme.${theme}`) }}
        </DropdownMenuRadioItem>
      </DropdownMenuRadioGroup>
      <DropdownMenuSeparator />
      <DropdownMenuItem data-testid="menu-sign-out" @select="signOut">
        <LogOut aria-hidden="true" />
        {{ $t('userMenu.signOut') }}
      </DropdownMenuItem>
    </DropdownMenuContent>
  </DropdownMenu>
</template>
