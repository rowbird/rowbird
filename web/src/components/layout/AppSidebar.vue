<script setup lang="ts">
import BrandMark from './BrandMark.vue'
import { ArrowUpCircle } from '@lucide/vue'
import { onMounted, onUnmounted } from 'vue'
import { useRoute } from 'vue-router'


import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
} from '@/components/ui/sidebar'
import { NEW_VERSION_POLL_MS, refreshNewVersion, useNewVersion } from '@/composables/useNewVersion'
import { NAV_GROUPS, NAV_SETTINGS } from '@/router/nav'
import { useSessionStore } from '@/stores/session'

const route = useRoute()
const session = useSessionStore()
/** A newer release, shown to admins when the update check found one. */
const newVersion = useNewVersion()
let poll: ReturnType<typeof setInterval> | undefined

onMounted(() => {
  if (!session.hasRole('admin')) return
  void refreshNewVersion()
  poll = setInterval(() => void refreshNewVersion(), NEW_VERSION_POLL_MS)
})
onUnmounted(() => clearInterval(poll))

function isActive(path: string) {
  return path === '/' ? route.path === '/' : route.path === path || route.path.startsWith(`${path}/`)
}
</script>

<template>
  <Sidebar collapsible="icon">
    <SidebarHeader>
      <SidebarMenu>
        <SidebarMenuItem>
          <SidebarMenuButton size="lg" as-child>
            <RouterLink to="/">
              <BrandMark />
              <div class="grid flex-1 text-left text-sm leading-tight">
                <span class="truncate font-semibold">{{ $t('app.name') }}</span>
                <span class="text-muted-foreground truncate text-xs">{{ $t('app.tagline') }}</span>
              </div>
            </RouterLink>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </SidebarHeader>
    <SidebarContent>
      <nav :aria-label="$t('nav.label')">
        <SidebarGroup v-for="(group, i) in NAV_GROUPS" :key="i">
          <SidebarGroupLabel v-if="group.labelKey">{{ $t(group.labelKey) }}</SidebarGroupLabel>
          <SidebarGroupContent>
            <SidebarMenu>
              <SidebarMenuItem v-for="item in group.items" :key="item.name">
                <SidebarMenuButton as-child :is-active="isActive(item.path)" :tooltip="$t(item.labelKey)">
                  <RouterLink :to="item.path" :data-testid="`nav-${item.name}`">
                    <component :is="item.icon" aria-hidden="true" />
                    <span>{{ $t(item.labelKey) }}</span>
                  </RouterLink>
                </SidebarMenuButton>
              </SidebarMenuItem>
            </SidebarMenu>
          </SidebarGroupContent>
        </SidebarGroup>
      </nav>
    </SidebarContent>
    <SidebarFooter>
      <SidebarMenu>
        <SidebarMenuItem v-if="newVersion">
          <SidebarMenuButton as-child :tooltip="$t('settings.about.available', { version: newVersion })">
            <RouterLink :to="{ name: 'settings-about' }" data-testid="nav-update">
              <ArrowUpCircle aria-hidden="true" />
              <span>{{ $t('settings.about.available', { version: newVersion }) }}</span>
            </RouterLink>
          </SidebarMenuButton>
        </SidebarMenuItem>
        <SidebarMenuItem>
          <SidebarMenuButton as-child :is-active="isActive(NAV_SETTINGS.path)" :tooltip="$t(NAV_SETTINGS.labelKey)">
            <RouterLink :to="NAV_SETTINGS.path" :data-testid="`nav-${NAV_SETTINGS.name}`">
              <component :is="NAV_SETTINGS.icon" aria-hidden="true" />
              <span>{{ $t(NAV_SETTINGS.labelKey) }}</span>
            </RouterLink>
          </SidebarMenuButton>
        </SidebarMenuItem>
      </SidebarMenu>
    </SidebarFooter>
    <SidebarRail />
  </Sidebar>
</template>
