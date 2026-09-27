<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { errorMessage, unwrap } from '@/api/errors'
import ConfirmDialog from '@/components/common/ConfirmDialog.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { useFormat } from '@/composables/useFormat'

import DiffView from './DiffView.vue'

/** Version history of a query: pick a version to compare it with the current one, or restore it. */
const props = defineProps<{ query: Schemas['Query']; canRestore: boolean }>()
const emit = defineEmits<{ restored: [query: Schemas['Query']] }>()
const { t, te } = useI18n()
const fmt = useFormat()

const versions = ref<Schemas['QueryVersionInfo'][]>([])
const selected = ref<Schemas['QueryVersion'] | null>(null)
const confirmOpen = ref(false)

async function load() {
  try {
    versions.value = unwrap(await api.GET('/api/v1/queries/{queryId}/versions', { params: { path: { queryId: props.query.id } } })).items
    const previous = versions.value.find((v) => v.number !== props.query.current_version)
    if (previous) await select(previous.number)
    else selected.value = null
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}

async function select(number: number) {
  selected.value = unwrap(await api.GET('/api/v1/queries/{queryId}/versions/{number}', { params: { path: { queryId: props.query.id, number } } }))
}

async function restore() {
  if (!selected.value) return
  try {
    const q = unwrap(await api.POST('/api/v1/queries/{queryId}/restore', {
      params: { path: { queryId: props.query.id } },
      body: { number: selected.value.number, version: props.query.version },
    }))
    toast.success(t('queries.versions.restored', { n: selected.value.number, current: q.current_version }))
    emit('restored', q)
  } catch (e) {
    toast.error(errorMessage(e, t, te))
  }
}

onMounted(load)
watch(() => props.query.current_version, load)
</script>

<template>
  <div class="grid gap-4 md:grid-cols-[14rem_1fr]" data-testid="versions">
    <ul class="grid content-start gap-1">
      <li v-for="v in versions" :key="v.number">
        <button
          type="button"
          class="hover:bg-muted w-full rounded-md border p-2 text-left text-sm"
          :class="selected?.number === v.number ? 'border-primary' : ''"
          :data-testid="`version-${v.number}`"
          @click="select(v.number)"
        >
          <div class="flex items-center gap-2 font-medium">
            {{ t('queries.versions.label', { n: v.number }) }}
            <Badge v-if="v.number === query.current_version" variant="secondary">{{ t('queries.versions.current') }}</Badge>
          </div>
          <div class="text-muted-foreground text-xs">{{ v.author_name }} · {{ fmt.dateTime(v.created_at) }}</div>
          <div v-if="v.restored_from" class="text-xs">{{ t('queries.versions.restoredFrom', { n: v.restored_from }) }}</div>
          <div v-else-if="v.note" class="truncate text-xs">{{ v.note }}</div>
        </button>
      </li>
    </ul>
    <div class="grid content-start gap-3">
      <template v-if="selected && selected.number !== query.current_version">
        <p class="text-sm">{{ t('queries.versions.comparing', { n: selected.number, current: query.current_version }) }}</p>
        <DiffView :original="selected.sql" :modified="query.sql" />
        <div v-if="canRestore">
          <Button variant="outline" data-testid="version-restore" @click="confirmOpen = true">{{ t('queries.versions.restore', { n: selected.number }) }}</Button>
        </div>
      </template>
      <p v-else class="text-muted-foreground text-sm">{{ t('queries.versions.pick') }}</p>
    </div>
    <ConfirmDialog
      v-model:open="confirmOpen"
      :title="t('queries.versions.restore', { n: selected?.number ?? 0 })"
      :description="t('queries.versions.restoreImpact', { n: selected?.number ?? 0 })"
      :confirm-label="t('queries.versions.restore', { n: selected?.number ?? 0 })"
      :on-confirm="restore"
    />
  </div>
</template>
