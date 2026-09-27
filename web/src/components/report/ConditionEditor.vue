<script setup lang="ts">
import { Plus, Trash2 } from '@lucide/vue'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Schemas } from '@/api/client'
import NativeSelect from '@/components/common/NativeSelect.vue'
import SchemaForm from '@/components/plugin/SchemaForm.vue'
import { Button } from '@/components/ui/button'
import { applyDefaults, type ConfigValues } from '@/lib/schema'
import { usePluginsStore } from '@/stores/plugins'

/**
 * Condition section of the report editor. Each rule is a condition plugin whose parameters are
 * rendered from its schema, so a new condition needs no change here (ADR-0004).
 */
const props = defineProps<{
  readonly?: boolean
  /** Field errors from the API, keyed like "condition.rules[0].value". */
  errors?: Record<string, string | undefined>
}>()
const condition = defineModel<Schemas['Condition']>({ required: true })
const { t } = useI18n()
const plugins = usePluginsStore()

const conditions = computed(() => plugins.plugins.filter((p) => p.kind === 'condition'))
const addable = computed(() => conditions.value.filter((p) => p.id !== 'always'))
const choice = ref('has_rows')
const typeOptions = computed(() => addable.value.map((p) => ({ value: p.id, label: t(p.name) })))
const matchOptions = computed(() => [
  { value: 'all', label: t('conditions.matchAll') },
  { value: 'any', label: t('conditions.matchAny') },
])
const match = computed({
  get: () => condition.value.match,
  set: (v: string) => (condition.value = { ...condition.value, match: v as Schemas['Condition']['match'] }),
})

function plugin(type: string) {
  return conditions.value.find((p) => p.id === type)
}

function params(rule: Schemas['ConditionRule']): ConfigValues {
  return Object.fromEntries(Object.entries(rule).filter(([k]) => k !== 'type'))
}

function setParams(i: number, values: ConfigValues) {
  const rules = [...condition.value.rules]
  rules[i] = { ...values, type: rules[i]!.type }
  condition.value = { ...condition.value, rules }
}

function add() {
  const p = plugin(choice.value)
  if (!p) return
  condition.value = { ...condition.value, rules: [...condition.value.rules, { ...applyDefaults(p.schema), type: p.id }] }
}

function remove(i: number) {
  condition.value = { ...condition.value, rules: condition.value.rules.filter((_, j) => j !== i) }
}

/** Errors of rule i, keyed by parameter. */
function ruleErrors(i: number) {
  const prefix = `condition.rules[${i}].`
  const out: Record<string, string | undefined> = {}
  for (const [k, v] of Object.entries(props.errors ?? {})) if (k.startsWith(prefix)) out[k.slice(prefix.length)] = v
  return out
}
</script>

<template>
  <div class="grid gap-4" data-testid="condition-editor">
    <p v-if="condition.rules.length === 0" class="text-muted-foreground text-sm" data-testid="condition-always">{{ t('conditions.always') }}</p>
    <div v-else-if="condition.rules.length > 1" class="flex flex-wrap items-center gap-2 text-sm">
      <label for="condition-match">{{ t('conditions.deliverWhen') }}</label>
      <div class="w-56"><NativeSelect id="condition-match" v-model="match" :options="matchOptions" :disabled="readonly" /></div>
    </div>
    <p v-if="errors?.['condition.match']" class="text-destructive text-sm">{{ errors['condition.match'] }}</p>

    <div v-for="(rule, i) in condition.rules" :key="i" class="grid gap-3 rounded-md border p-3" :data-testid="`condition-rule-${i}`">
      <div class="flex items-start justify-between gap-2">
        <div>
          <p class="text-sm font-medium">{{ plugin(rule.type) ? t(plugin(rule.type)!.name) : rule.type }}</p>
          <p v-if="plugin(rule.type)" class="text-muted-foreground text-xs">{{ t(plugin(rule.type)!.description) }}</p>
          <p v-if="errors?.[`condition.rules[${i}].type`]" class="text-destructive text-sm">{{ errors[`condition.rules[${i}].type`] }}</p>
        </div>
        <Button v-if="!readonly" type="button" variant="ghost" size="icon" :aria-label="t('conditions.remove')" :data-testid="`condition-remove-${i}`" @click="remove(i)">
          <Trash2 aria-hidden="true" />
        </Button>
      </div>
      <SchemaForm
        v-if="plugin(rule.type) && Object.keys(plugin(rule.type)!.schema.properties ?? {}).length"
        :model-value="params(rule)"
        :schema="plugin(rule.type)!.schema"
        :errors="ruleErrors(i)"
        :disabled="readonly"
        :id-prefix="`rule-${i}`"
        hide-legend
        @update:model-value="(v) => setParams(i, v)"
      />
    </div>

    <div v-if="!readonly" class="flex flex-wrap items-center gap-2">
      <div class="w-56"><NativeSelect id="condition-add-type" v-model="choice" :options="typeOptions" :aria-label="t('conditions.addType')" /></div>
      <Button type="button" variant="outline" data-testid="condition-add" @click="add"><Plus aria-hidden="true" />{{ t('conditions.add') }}</Button>
    </div>
  </div>
</template>
