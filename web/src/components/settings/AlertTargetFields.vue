<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Schemas } from '@/api/client'
import FormField from '@/components/common/FormField.vue'
import NativeSelect from '@/components/common/NativeSelect.vue'
import SchemaForm from '@/components/plugin/SchemaForm.vue'
import { alertOptionSchema, applyDefaults, type ConfigValues } from '@/lib/schema'
import { usePluginsStore } from '@/stores/plugins'

/** A system alert channel: which channel, and the options its destination needs (recipients, chat). */
const props = defineProps<{
  name: 'primary' | 'fallback'
  channels: Schemas['Channel'][]
  disabled?: boolean
  /** Field errors of the whole form, keyed like the API ("primary.options.to"). */
  errorFor: (field: string) => string | undefined
}>()
const channelId = defineModel<string>('channelId', { required: true })
const options = defineModel<ConfigValues>('options', { required: true })
const { t } = useI18n()
const plugins = usePluginsStore()

const channel = computed(() => props.channels.find((c) => c.id === channelId.value))
const schema = computed(() => {
  const s = channel.value ? plugins.destination(channel.value.type)?.delivery_schema : undefined
  return s ? alertOptionSchema(s) : undefined
})
const channelOptions = computed(() => [
  { value: '', label: t('settings.alerts.none') },
  ...props.channels.map((c) => ({ value: c.id, label: `${c.name} (${plugins.destination(c.type) ? t(plugins.destination(c.type)!.name) : c.type})` })),
])
const errors = computed(() => {
  const out: Record<string, string | undefined> = {}
  for (const key of schema.value?.['x-order'] ?? []) out[key] = props.errorFor(`${props.name}.options.${key}`)
  return out
})

/** Choosing another channel starts its options from their defaults. */
function pick(id: string) {
  channelId.value = id
  options.value = schema.value ? applyDefaults(schema.value, {}) : {}
}
</script>

<template>
  <div class="grid gap-4">
    <FormField :id="`alert-${name}-channel`" :label="t(`settings.alerts.${name}`)" :hint="t(`settings.alerts.${name}Hint`)" :error="errorFor(`${name}.channel_id`)">
      <NativeSelect :id="`alert-${name}-channel`" :model-value="channelId" :options="channelOptions" :disabled="disabled" :data-testid="`alert-${name}-channel`" @update:model-value="pick(String($event))" />
    </FormField>
    <SchemaForm v-if="schema && schema['x-order'].length > 0" v-model="options" :schema="schema" :errors="errors" :id-prefix="`alert-${name}`" :disabled="disabled" hide-legend />
  </div>
</template>
