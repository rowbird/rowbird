<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { api, type Schemas } from '@/api/client'
import { ApiError, unwrap } from '@/api/errors'
import ScheduleAssistant from '@/components/ai/ScheduleAssistant.vue'
import FormField from '@/components/common/FormField.vue'
import NativeSelect from '@/components/common/NativeSelect.vue'
import { Checkbox } from '@/components/ui/checkbox'
import { Input } from '@/components/ui/input'
import { fromCron, MINUTE_STEPS, SCHEDULE_KINDS, type ScheduleModel, toCron } from '@/lib/cron'
import { browserTimeZone, timeZones } from '@/lib/timezones'

/**
 * Schedule section of the report editor: a builder for common shapes, a custom cron field, the
 * time zone and the next five runs as the server computes them (daylight saving included).
 */
const props = defineProps<{ readonly?: boolean; cronError?: string; timezoneError?: string }>()
const cron = defineModel<string>({ required: true })
const timezone = defineModel<string>('timezone', { required: true })
const { t, te, locale } = useI18n()

const model = ref<ScheduleModel>(fromCron(cron.value))
watch(cron, (v) => {
  if (v !== toCron(model.value)) model.value = fromCron(v)
})
watch(model, (m) => {
  const expr = toCron(m)
  // Outside custom mode the expression follows the builder, so switching to custom starts from it.
  if (m.kind !== 'custom') m.expression = expr
  cron.value = expr
}, { deep: true })

/** A schedule the assistant proposed and the user applied. */
function applySuggestion(expr: string, tz: string) {
  timezone.value = tz
  cron.value = expr
}

const preview = ref<Schemas['SchedulePreview'] | null>(null)
const previewError = ref('')
let timer: ReturnType<typeof setTimeout> | undefined
let seq = 0

async function refresh() {
  const id = ++seq
  try {
    const res = unwrap(await api.POST('/api/v1/schedules/preview', {
      body: { cron: cron.value, timezone: timezone.value || undefined, count: 5, locale: locale.value },
    }))
    if (id !== seq) return
    preview.value = res
    previewError.value = ''
  } catch (e) {
    if (id !== seq) return
    preview.value = null
    const code = e instanceof ApiError ? (e.fields.timezone ?? e.fields.cron ?? e.code) : 'internal'
    previewError.value = te(`errors.${code}`) ? t(`errors.${code}`) : t('errors.internal')
  }
}
watch([cron, timezone, locale], () => {
  clearTimeout(timer)
  timer = setTimeout(refresh, 250)
}, { immediate: true })
onBeforeUnmount(() => clearTimeout(timer))

const kindOptions = computed(() => SCHEDULE_KINDS.map((k) => ({ value: k, label: t(`schedule.kinds.${k}`) })))
const stepOptions = computed(() => MINUTE_STEPS.map((n) => ({ value: String(n), label: t('schedule.everyMinutes', { n }, n) })))
const zoneOptions = computed(() => timeZones().map((z) => ({ value: z, label: z })))
/** Weekday names in the user's language, Sunday first (0..6, like cron). */
const weekdays = computed(() => {
  const f = new Intl.DateTimeFormat(locale.value, { weekday: 'short', timeZone: 'UTC' })
  return Array.from({ length: 7 }, (_, d) => ({ day: d, label: f.format(new Date(Date.UTC(2023, 0, 1 + d))) }))
})

function toggleDay(day: number, on: boolean) {
  const days = model.value.days.filter((d) => d !== day)
  model.value.days = on ? [...days, day].sort((a, b) => a - b) : days
}

const every = computed({
  get: () => String(model.value.every),
  set: (v: string) => (model.value.every = Number(v)),
})

/** Occurrences in the report's time zone, and in the browser's when they differ. */
function occurrence(iso: string) {
  const date = new Date(iso)
  const inZone = new Intl.DateTimeFormat(locale.value, { dateStyle: 'full', timeStyle: 'short', timeZone: timezone.value || undefined }).format(date)
  const local = browserTimeZone()
  if (!timezone.value || local === timezone.value) return { inZone }
  return { inZone, local: new Intl.DateTimeFormat(locale.value, { dateStyle: 'medium', timeStyle: 'short' }).format(date) }
}
</script>

<template>
  <div class="grid gap-4" data-testid="schedule-builder">
    <ScheduleAssistant v-if="!props.readonly" :timezone="timezone" @apply="applySuggestion" />
    <div class="grid gap-4 sm:grid-cols-2">
      <FormField id="schedule-kind" :label="t('schedule.kind')">
        <NativeSelect id="schedule-kind" v-model="model.kind" :options="kindOptions" :disabled="props.readonly" />
      </FormField>
      <FormField id="report-timezone" :label="t('schedule.timezone')" :error="timezoneError">
        <NativeSelect id="report-timezone" v-model="timezone" :options="zoneOptions" :disabled="props.readonly" />
      </FormField>
    </div>

    <div class="flex flex-wrap items-end gap-4">
      <FormField v-if="['daily', 'weekdays', 'weekly', 'monthly'].includes(model.kind)" id="schedule-time" :label="t('schedule.time')">
        <Input id="schedule-time" v-model="model.time" type="time" class="w-32" :readonly="props.readonly" />
      </FormField>
      <FormField v-if="model.kind === 'monthly'" id="schedule-day" :label="t('schedule.dayOfMonth')" :hint="t('schedule.dayOfMonthHint')">
        <Input id="schedule-day" v-model.number="model.dayOfMonth" type="number" min="1" max="31" class="w-24" :readonly="props.readonly" />
      </FormField>
      <FormField v-if="model.kind === 'hourly'" id="schedule-minute" :label="t('schedule.minute')">
        <Input id="schedule-minute" v-model.number="model.minute" type="number" min="0" max="59" class="w-24" :readonly="props.readonly" />
      </FormField>
      <FormField v-if="model.kind === 'minutes'" id="schedule-every" :label="t('schedule.every')">
        <NativeSelect id="schedule-every" v-model="every" :options="stepOptions" :disabled="props.readonly" />
      </FormField>
    </div>

    <fieldset v-if="model.kind === 'weekly'" class="grid gap-2">
      <legend class="text-sm font-medium">{{ t('schedule.days') }}</legend>
      <div class="flex flex-wrap gap-3">
        <label v-for="d in weekdays" :key="d.day" class="flex items-center gap-1.5 text-sm">
          <Checkbox
            :id="`schedule-day-${d.day}`"
            :model-value="model.days.includes(d.day)"
            :disabled="props.readonly"
            @update:model-value="(v) => toggleDay(d.day, v === true)"
          />
          {{ d.label }}
        </label>
      </div>
    </fieldset>

    <FormField id="schedule-cron" :label="t('schedule.cron')" :hint="model.kind === 'custom' ? t('schedule.cronHint') : undefined" :error="cronError">
      <Input
        id="schedule-cron"
        class="font-mono"
        :readonly="props.readonly || model.kind !== 'custom'"
        :model-value="model.expression"
        @update:model-value="(v) => (model.expression = String(v))"
      />
    </FormField>

    <div class="bg-muted/40 grid gap-2 rounded-md border p-3 text-sm" aria-live="polite">
      <p v-if="previewError" class="text-destructive" data-testid="schedule-error">{{ previewError }}</p>
      <template v-else-if="preview">
        <p class="font-medium" data-testid="schedule-description">{{ preview.description || preview.expression }}</p>
        <p class="text-muted-foreground text-xs">{{ t('schedule.nextRuns', { tz: preview.timezone }) }}</p>
        <ol class="grid gap-1" data-testid="schedule-next">
          <li v-for="n in preview.next" :key="n">
            {{ occurrence(n).inZone }}
            <span v-if="occurrence(n).local" class="text-muted-foreground text-xs">({{ t('schedule.yourTime', { time: occurrence(n).local }) }})</span>
          </li>
        </ol>
      </template>
    </div>
  </div>
</template>
