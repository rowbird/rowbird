<script setup lang="ts">
import { Trash2 } from '@lucide/vue'
import { computed, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Schemas } from '@/api/client'
import NativeSelect from '@/components/common/NativeSelect.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { type Dialect, findParams, isBuiltin, PARAM_TYPES, type ParamType } from '@/lib/params'

type Param = Schemas['QueryParam']

/**
 * Parameters of the query: built-ins used in the SQL, and a definition (type and default) for every
 * other {{name}}, added automatically as the user types. Preview values override defaults.
 */
const props = defineProps<{ dialect: Dialect; sql: string; readonly?: boolean; errors?: Record<string, string | undefined> }>()
const defs = defineModel<Param[]>('params', { required: true })
const values = defineModel<Record<string, string>>('values', { required: true })
const { t } = useI18n()

const used = computed(() => findParams(props.dialect, props.sql))
const builtinsUsed = computed(() => used.value.filter(isBuiltin))

watch(
  used,
  (names) => {
    if (props.readonly) return
    const missing = names.filter((n) => !isBuiltin(n) && !defs.value.some((d) => d.name === n))
    if (missing.length) defs.value = [...defs.value, ...missing.map((name) => ({ name, type: 'text' as ParamType, default: null }))]
  },
  { immediate: true },
)

const typeOptions = computed(() => PARAM_TYPES.map((v) => ({ value: v, label: t(`params.types.${v}`) })))

function update(i: number, patch: Partial<Param>) {
  defs.value = defs.value.map((d, j) => (j === i ? { ...d, ...patch } : d))
}

function remove(i: number) {
  const name = defs.value[i]!.name
  defs.value = defs.value.filter((_, j) => j !== i)
  values.value = Object.fromEntries(Object.entries(values.value).filter(([k]) => k !== name))
}

function setValue(name: string, v: string) {
  values.value = { ...values.value, [name]: v }
}

const inputType = (type: string) => (type === 'date' ? 'date' : type === 'datetime' ? 'datetime-local' : type === 'integer' || type === 'decimal' ? 'text' : 'text')
</script>

<template>
  <div class="grid gap-3" data-testid="params-panel">
    <p v-if="used.length === 0 && defs.length === 0" class="text-muted-foreground text-sm">{{ t('params.none') }}</p>

    <div v-if="builtinsUsed.length" class="flex flex-wrap items-center gap-1 text-sm">
      <span class="text-muted-foreground">{{ t('params.builtins') }}</span>
      <Badge v-for="b in builtinsUsed" :key="b" variant="secondary" class="font-mono" :title="t(`params.builtin.${b}`)">{{ b }}</Badge>
    </div>

    <div v-for="(d, i) in defs" :key="d.name" class="grid gap-2 rounded-md border p-3 sm:grid-cols-[1fr_1fr_1fr_1fr_auto] sm:items-end" :data-testid="`param-${d.name}`">
      <div class="grid gap-1">
        <span class="text-muted-foreground text-xs">{{ t('params.name') }}</span>
        <code class="font-mono text-sm">{{ d.name }}</code>
        <Badge v-if="!used.includes(d.name)" variant="outline" class="w-fit text-amber-700 dark:text-amber-400">{{ t('params.unused') }}</Badge>
      </div>
      <label class="grid gap-1 text-xs">
        <span class="text-muted-foreground">{{ t('params.type') }}</span>
        <NativeSelect :id="`param-${d.name}-type`" :model-value="d.type" :options="typeOptions" :disabled="readonly" @update:model-value="(v) => update(i, { type: v as ParamType })" />
      </label>
      <label class="grid gap-1 text-xs">
        <span class="text-muted-foreground">{{ t('params.default') }}</span>
        <Input :id="`param-${d.name}-default`" :type="inputType(d.type)" :model-value="d.default ?? ''" :disabled="readonly" @update:model-value="(v) => update(i, { default: String(v) || null })" />
        <span v-if="errors?.[`params[${i}].default`]" class="text-destructive">{{ errors[`params[${i}].default`] }}</span>
      </label>
      <label class="grid gap-1 text-xs">
        <span class="text-muted-foreground">{{ t('params.previewValue') }}</span>
        <Input :id="`param-${d.name}-value`" :type="inputType(d.type)" :model-value="values[d.name] ?? ''" :placeholder="d.default ?? ''" @update:model-value="(v) => setValue(d.name, String(v))" />
        <span v-if="errors?.[`values.${d.name}`]" class="text-destructive">{{ errors[`values.${d.name}`] }}</span>
      </label>
      <Button v-if="!readonly && !used.includes(d.name)" type="button" variant="ghost" size="icon" :aria-label="t('params.remove', { name: d.name })" @click="remove(i)">
        <Trash2 aria-hidden="true" />
      </Button>
    </div>
  </div>
</template>
