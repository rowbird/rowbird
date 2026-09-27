<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { toast } from 'vue-sonner'

import { api, type Schemas } from '@/api/client'
import { ApiError, unwrap } from '@/api/errors'
import FormField from '@/components/common/FormField.vue'
import NativeSelect from '@/components/common/NativeSelect.vue'
import DeliveryPreview from '@/components/delivery/DeliveryPreview.vue'
import SchemaForm from '@/components/plugin/SchemaForm.vue'
import { Alert, AlertDescription } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from '@/components/ui/sheet'
import { Switch } from '@/components/ui/switch'
import { useApiError } from '@/composables/useApiError'
import { applyDefaults, type ConfigSchema, type ConfigValues, toPayload } from '@/lib/schema'
import { type DestinationCapabilities, usePluginsStore } from '@/stores/plugins'

/** Creates or edits one delivery of a report, with a preview of the message it sends. */
const props = defineProps<{
  reportId: string
  channels: Schemas['Channel'][]
  /** The delivery to edit; null creates one. */
  delivery: Schemas['Delivery'] | null
  linksEnabled: boolean
}>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ saved: [delivery: Schemas['Delivery']] }>()

const { t } = useI18n()
const plugins = usePluginsStore()
const err = useApiError()
/** The preview validates as the user types; its field errors show next to the fields until saving. */
const previewErr = useApiError()
const fieldError = (name: string) => err.field(name) ?? previewErr.field(name)

const DAY = 86400
const expiryChoices = [1, 7, 30, 90]

const channelId = ref('')
const mode = ref<Schemas['DeliveryMode']>('inline')
const formats = ref<string[]>([])
const inlineRows = ref(20)
const inlineWithFiles = ref(true)
const expiresDays = ref('7')
const requireLogin = ref(false)
const enabled = ref(true)
const options = ref<ConfigValues>({})
const saving = ref(false)
const preview = ref<Schemas['DeliveryPreview'] | null>(null)

const channel = computed(() => props.channels.find((c) => c.id === channelId.value))
const caps = computed(() => channel.value?.capabilities as DestinationCapabilities | undefined)
const destination = computed(() => (channel.value ? plugins.destination(channel.value.type) : undefined))
const optionSchema = computed<ConfigSchema | undefined>(() => destination.value?.delivery_schema)

const channelOptions = computed(() => props.channels.map((c) => ({ value: c.id, label: `${c.name} (${plugins.destination(c.type) ? t(plugins.destination(c.type)!.name) : c.type})` })))
const modeOptions = computed(() =>
  (caps.value?.modes ?? ['inline']).map((m) => ({
    value: m,
    label: m === 'link' && !props.linksEnabled ? `${t('deliveries.modes.link')} (${t('deliveries.linksDisabledShort')})` : t(`deliveries.modes.${m}`),
  })),
)
const expiryOptions = computed(() => expiryChoices.map((d) => ({ value: String(d), label: t('deliveries.days', { n: d }, d) })))
/** Links are made in link mode, and in attachment mode for files the destination cannot take. */
const usesLinks = computed(() => mode.value === 'link' || (mode.value === 'attachment' && !!caps.value?.max_attachment_bytes))

watch(open, (v) => {
  if (!v) return
  err.clear()
  previewErr.clear()
  preview.value = null
  const d = props.delivery
  channelId.value = d?.channel_id ?? props.channels[0]?.id ?? ''
  mode.value = d?.mode ?? 'inline'
  formats.value = [...(d?.formats ?? [])]
  inlineRows.value = d?.inline_row_limit ?? 20
  inlineWithFiles.value = d?.include_inline_with_files ?? true
  expiresDays.value = String(Math.round((d?.link_expires_seconds ?? 7 * DAY) / DAY))
  requireLogin.value = d?.link_require_login ?? false
  enabled.value = d?.enabled ?? true
  if (caps.value && !caps.value.modes.includes(mode.value)) mode.value = caps.value.modes[0] ?? 'inline'
  resetOptions(d?.options)
}, { immediate: true })

function resetOptions(values?: Record<string, unknown>) {
  options.value = optionSchema.value ? applyDefaults(optionSchema.value, values ?? {}) : { ...(values ?? {}) }
}

/** A new channel keeps the options when it is of the same type, and its modes decide the mode. */
watch(channelId, (id, old) => {
  if (!old || !open.value) return
  if (!caps.value?.modes.includes(mode.value)) mode.value = caps.value?.modes[0] ?? 'inline'
  const sameType = props.channels.find((c) => c.id === old)?.type === channel.value?.type
  resetOptions(props.delivery?.channel_id === id ? props.delivery.options : sameType ? options.value : undefined)
})

function toggleFormat(id: string, on: boolean) {
  formats.value = on ? [...new Set([...formats.value, id])] : formats.value.filter((f) => f !== id)
}

function body(): Schemas['DeliveryInput'] {
  return {
    channel_id: channelId.value,
    enabled: enabled.value,
    mode: mode.value,
    formats: mode.value === 'inline' ? [] : formats.value,
    inline_row_limit: Number(inlineRows.value),
    include_inline_with_files: inlineWithFiles.value,
    link_expires_seconds: Number(expiresDays.value) * DAY,
    link_require_login: requireLogin.value,
    options: optionSchema.value ? toPayload(optionSchema.value, options.value) : options.value,
  }
}

/** Option errors arrive as "options.<key>". */
const optionErrors = computed(() => {
  const out: Record<string, string | undefined> = {}
  for (const e of [previewErr.error.value, err.error.value]) {
    if (!(e instanceof ApiError)) continue
    for (const key of Object.keys(e.fields)) {
      if (key.startsWith('options.')) out[key.slice(8)] = fieldError(key)
    }
  }
  return out
})

let timer: ReturnType<typeof setTimeout> | undefined
let seq = 0
async function refreshPreview() {
  if (!channelId.value) return
  const mine = ++seq
  try {
    const p = unwrap(await api.POST('/api/v1/deliveries/preview', { body: { ...body(), report_id: props.reportId } }))
    if (mine !== seq) return
    preview.value = p
    previewErr.clear()
  } catch (e) {
    if (mine !== seq) return
    preview.value = null
    previewErr.error.value = e
  }
}
watch([open, channelId, mode, formats, inlineRows, inlineWithFiles, expiresDays, requireLogin, options], () => {
  if (!open.value) return
  clearTimeout(timer)
  timer = setTimeout(refreshPreview, 400)
}, { deep: true, immediate: true })

async function save() {
  saving.value = true
  err.clear()
  try {
    const d = props.delivery
    const saved = d
      ? unwrap(await api.PUT('/api/v1/reports/{reportId}/deliveries/{deliveryId}', {
        params: { path: { reportId: props.reportId, deliveryId: d.id } },
        body: { ...body(), version: d.version },
      }))
      : unwrap(await api.POST('/api/v1/reports/{reportId}/deliveries', { params: { path: { reportId: props.reportId } }, body: body() }))
    toast.success(t('deliveries.saved'))
    emit('saved', saved)
    open.value = false
  } catch (e) {
    err.error.value = e
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <Sheet v-model:open="open">
    <SheetContent class="w-full overflow-y-auto sm:max-w-4xl" data-testid="delivery-editor">
      <SheetHeader>
        <SheetTitle>{{ delivery ? t('deliveries.editTitle') : t('deliveries.add') }}</SheetTitle>
        <SheetDescription>{{ t('deliveries.editorHelp') }}</SheetDescription>
      </SheetHeader>
      <div class="grid gap-6 px-4 pb-4 lg:grid-cols-2">
        <form class="grid content-start gap-4" novalidate @submit.prevent="save">
          <FormField id="delivery-channel" :label="t('deliveries.channel')" :error="fieldError('channel_id')">
            <NativeSelect id="delivery-channel" v-model="channelId" :options="channelOptions" data-testid="delivery-channel" />
          </FormField>
          <FormField id="delivery-mode" :label="t('deliveries.mode')" :hint="t(`deliveries.modeHelp.${mode}`)" :error="fieldError('mode')">
            <NativeSelect id="delivery-mode" v-model="mode" :options="modeOptions" data-testid="delivery-mode" />
          </FormField>
          <Alert v-if="!linksEnabled && usesLinks" data-testid="links-disabled">
            <AlertDescription>{{ t('deliveries.linksDisabled') }}</AlertDescription>
          </Alert>

          <fieldset v-if="mode !== 'inline'" class="grid gap-2">
            <legend class="mb-1 text-sm font-medium">{{ t('deliveries.formats') }}</legend>
            <div class="flex flex-wrap gap-4">
              <label v-for="f in plugins.fileFormatters" :key="f.id" class="flex items-center gap-2 text-sm">
                <Checkbox :model-value="formats.includes(f.id)" :data-testid="`delivery-format-${f.id}`" @update:model-value="(v) => toggleFormat(f.id, v === true)" />
                {{ t(f.name) }}
              </label>
            </div>
            <p v-if="fieldError('formats')" class="text-destructive text-sm" data-testid="delivery-formats-error">{{ fieldError('formats') }}</p>
          </fieldset>

          <FormField v-if="caps?.inline_target" id="delivery-rows" :label="t('deliveries.inlineRows')" :hint="t('deliveries.inlineRowsHint')" :error="fieldError('inline_row_limit')">
            <Input id="delivery-rows" v-model.number="inlineRows" type="number" min="1" max="200" />
          </FormField>
          <label v-if="mode !== 'inline' && caps?.inline_target" class="flex items-center gap-2 text-sm">
            <Switch v-model="inlineWithFiles" />{{ t('deliveries.inlineWithFiles') }}
          </label>

          <template v-if="usesLinks">
            <FormField id="delivery-expiry" :label="t('deliveries.linkExpiry')" :hint="mode === 'attachment' ? t('deliveries.linkFallbackHint') : undefined" :error="fieldError('link_expires_seconds')">
              <NativeSelect id="delivery-expiry" v-model="expiresDays" :options="expiryOptions" />
            </FormField>
            <label class="flex items-center gap-2 text-sm"><Switch v-model="requireLogin" />{{ t('deliveries.requireLogin') }}</label>
          </template>

          <SchemaForm v-if="optionSchema" v-model="options" :schema="optionSchema" :errors="optionErrors" id-prefix="delivery-option" hide-legend />

          <label class="flex items-center gap-2 text-sm"><Switch v-model="enabled" data-testid="delivery-enabled" />{{ t('deliveries.enabled') }}</label>
          <p v-if="err.message.value" class="text-destructive text-sm" role="alert">{{ err.message.value }}</p>
          <div class="flex gap-2">
            <Button type="submit" :disabled="saving || !channelId" data-testid="delivery-save">{{ t('common.save') }}</Button>
            <Button type="button" variant="ghost" @click="open = false">{{ t('common.cancel') }}</Button>
          </div>
        </form>

        <section class="grid content-start gap-2">
          <h3 class="text-sm font-semibold">{{ t('deliveries.preview.heading') }}</h3>
          <p class="text-muted-foreground text-xs">{{ t('deliveries.preview.help') }}</p>
          <DeliveryPreview v-if="preview" :preview="preview" />
          <p v-else-if="previewErr.message.value" class="text-muted-foreground text-sm" data-testid="preview-error">{{ previewErr.message.value }}</p>
        </section>
      </div>
    </SheetContent>
  </Sheet>
</template>
