<script setup lang="ts">
import { Search } from '@lucide/vue'
import { onBeforeUnmount, onMounted, ref } from 'vue'

import { Separator } from '@/components/ui/separator'
import { SidebarInset, SidebarProvider, SidebarTrigger } from '@/components/ui/sidebar'
import { useSidebarPreference } from '@/composables/useSidebarPreference'

import AppSidebar from './AppSidebar.vue'
import CommandPalette from './CommandPalette.vue'
import HealthBanner from './HealthBanner.vue'
import NotificationBell from './NotificationBell.vue'
import ThemeToggle from './ThemeToggle.vue'
import UserMenu from './UserMenu.vue'

const sidebarOpen = useSidebarPreference()
const paletteOpen = ref(false)
const isMac = typeof navigator !== 'undefined' && /Mac|iPhone|iPad/.test(navigator.platform)

// Ctrl/Cmd+K opens the command palette from anywhere, the SQL editor included.
function onKey(e: KeyboardEvent) {
  if ((e.metaKey || e.ctrlKey) && !e.altKey && !e.shiftKey && e.key.toLowerCase() === 'k') {
    e.preventDefault()
    paletteOpen.value = !paletteOpen.value
  }
}
onMounted(() => window.addEventListener('keydown', onKey))
onBeforeUnmount(() => window.removeEventListener('keydown', onKey))
</script>

<template>
  <SidebarProvider v-model:open="sidebarOpen">
    <AppSidebar />
    <SidebarInset>
      <header class="bg-background/95 sticky top-0 z-10 flex h-14 shrink-0 items-center gap-2 border-b px-4 backdrop-blur md:px-6">
        <SidebarTrigger class="-ml-1" />
        <Separator orientation="vertical" class="mr-2 data-[orientation=vertical]:h-4" />
        <button
          type="button"
          class="border-input text-muted-foreground hover:bg-muted flex h-8 w-full max-w-72 items-center gap-2 rounded-md border px-2.5 text-sm"
          data-testid="palette-open"
          @click="paletteOpen = true"
        >
          <Search class="size-4" aria-hidden="true" />
          <span class="truncate">{{ $t('palette.open') }}</span>
          <kbd class="bg-muted text-foreground ml-auto hidden rounded px-1.5 font-mono text-[11px] sm:inline">{{ isMac ? '⌘' : 'Ctrl' }} K</kbd>
        </button>
        <div class="flex-1" />
        <NotificationBell />
        <ThemeToggle />
        <UserMenu />
      </header>
      <!-- Pages start at the same left edge as the header; forms cap their width but never center. -->
      <main class="flex-1 p-4 md:p-6">
        <HealthBanner />
        <slot />
      </main>
    </SidebarInset>
    <CommandPalette v-model:open="paletteOpen" />
  </SidebarProvider>
</template>
