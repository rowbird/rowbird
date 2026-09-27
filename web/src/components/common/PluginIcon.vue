<script setup lang="ts">
import { Activity, Archive, Database, Hash, Mail, Plug, Webhook } from '@lucide/vue'
import { type SimpleIcon, siDiscord, siMysql, siPostgresql, siSqlite, siTelegram, siUptimekuma } from 'simple-icons'
import { type Component, computed } from 'vue'

/**
 * The icon a plugin declares (its metadata `icon`): the brand's logo where a free one exists
 * (simple-icons, CC0), otherwise a generic icon. Logos whose owners asked simple-icons to remove
 * them (Slack, SQL Server, Amazon S3) get the generic one too.
 */
const props = withDefaults(defineProps<{ icon?: string; size?: number }>(), { icon: '', size: 20 })

const brands: Record<string, SimpleIcon> = {
  postgres: siPostgresql, sqlite: siSqlite, mysql: siMysql, discord: siDiscord, telegram: siTelegram, uptimekuma: siUptimekuma,
}
const generic: Record<string, Component> = {
  mssql: Database, mail: Mail, slack: Hash, bucket: Archive, webhook: Webhook, activity: Activity, database: Database,
}

const brand = computed(() => brands[props.icon])
const fallback = computed(() => generic[props.icon] ?? Plug)
</script>

<template>
  <svg
    v-if="brand"
    role="img"
    aria-hidden="true"
    viewBox="0 0 24 24"
    :width="size"
    :height="size"
    :fill="`#${brand.hex}`"
    class="plugin-icon shrink-0"
    :data-icon="icon"
  ><path :d="brand.path" /></svg>
  <component :is="fallback" v-else aria-hidden="true" :size="size" class="text-muted-foreground shrink-0" :data-icon="icon" />
</template>

<style scoped>
/* Dark logos (SQLite, MariaDB) would vanish on a dark background. */
:global(.dark) .plugin-icon {
  filter: drop-shadow(0 0 0.5px rgb(255 255 255 / 0.6));
}
</style>
