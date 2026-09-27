<script setup lang="ts">
import { KeyRound } from '@lucide/vue'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import FormField from '@/components/common/FormField.vue'
import NativeSelect from '@/components/common/NativeSelect.vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Switch } from '@/components/ui/switch'
import { type ConfigSchema, type ConfigValues, type Field, groups, isPlaceholder, isVisible, TEMPLATE_VARIABLES, valueOf } from '@/lib/schema'

/**
 * Renders a plugin configuration form from its schema (ADR-0004: adding a plugin needs no frontend
 * change). Stored secrets show as "configured" until the user chooses to replace them.
 */
const props = defineProps<{
  schema: ConfigSchema
  /** Field key -> error message. */
  errors?: Record<string, string | undefined>
  disabled?: boolean
  idPrefix?: string
  /** Leave out the group titles, for small schemas shown inline (condition rules). */
  hideLegend?: boolean
}>()
const model = defineModel<ConfigValues>({ required: true })
const { t, te } = useI18n()

const sections = computed(() =>
  groups(props.schema)
    .map((g) => ({ ...g, fields: g.fields.filter((f) => isVisible(props.schema, model.value, f.key)) }))
    .filter((g) => g.fields.length > 0),
)

const id = (f: Field) => `${props.idPrefix ?? 'cfg'}-${f.key}`

function set(key: string, value: unknown) {
  model.value = { ...model.value, [key]: value }
}

function label(f: Field) {
  return te(f['x-label']) ? t(f['x-label']) : f.key
}

function help(f: Field) {
  return f['x-help'] && te(f['x-help']) ? t(f['x-help']) : undefined
}

/** Option labels come from "<label key without .label>.<value>" when translated. */
function optionLabel(f: Field, value: string) {
  const key = f['x-label'].replace(/\.label$/, `.${value}`)
  return te(key) ? t(key) : value
}

function numberValue(f: Field, raw: string) {
  if (raw === '') return set(f.key, undefined)
  const n = Number(raw)
  set(f.key, Number.isFinite(n) ? (f.type === 'integer' ? Math.trunc(n) : n) : raw)
}

const numberOf = (f: Field) => valueOf(props.schema, model.value, f.key) as number | undefined

const isTemplate = (f: Field) => f['x-help'] === 'plugin.template.help'

/** Inserts {{name}} at the cursor of a template field, or at its end when it never had focus. */
function insertVariable(f: Field, name: string) {
  const el = document.getElementById(id(f)) as HTMLInputElement | HTMLTextAreaElement | null
  const current = String(model.value[f.key] ?? '')
  const tag = `{{${name}}}`
  const start = el?.selectionStart ?? current.length
  const end = el?.selectionEnd ?? current.length
  set(f.key, current.slice(0, start) + tag + current.slice(end))
  requestAnimationFrame(() => {
    if (!el) return
    el.focus()
    el.setSelectionRange(start + tag.length, start + tag.length)
  })
}

const isSecret = (f: Field) => !!f['x-secret']
const storedSecret = (f: Field) => {
  const v = model.value[f.key]
  return isPlaceholder(v) && v.configured
}
</script>

<template>
  <div class="grid gap-6">
    <fieldset v-for="section in sections" :key="section.name" class="grid gap-4" :data-testid="`group-${section.name}`">
      <legend v-if="!hideLegend" class="mb-1 text-sm font-semibold">{{ te(`plugin.group.${section.name}`) ? t(`plugin.group.${section.name}`) : section.name }}</legend>
      <template v-for="f in section.fields" :key="f.key">
        <div v-if="f.type === 'boolean'" class="flex items-start gap-3">
          <Switch
            :id="id(f)"
            :model-value="valueOf(schema, model, f.key) === true"
            :disabled="disabled"
            @update:model-value="(v: boolean) => set(f.key, v)"
          />
          <div class="grid gap-1">
            <label :for="id(f)" class="text-sm font-medium">{{ label(f) }}</label>
            <p v-if="help(f)" class="text-muted-foreground text-xs">{{ help(f) }}</p>
          </div>
        </div>

        <FormField v-else :id="id(f)" :label="label(f) + (f.required ? ' *' : '')" :hint="help(f)" :error="errors?.[f.key]">
          <div v-if="isSecret(f) && storedSecret(f)" class="flex flex-wrap items-center gap-2" :data-testid="`secret-${f.key}`">
            <Badge variant="secondary"><KeyRound class="size-3" aria-hidden="true" />{{ t('schemaForm.configured') }}</Badge>
            <Button type="button" variant="outline" size="sm" :disabled="disabled" @click="set(f.key, '')">{{ t('schemaForm.replace') }}</Button>
            <Button v-if="!f.required" type="button" variant="ghost" size="sm" :disabled="disabled" @click="set(f.key, null)">{{ t('schemaForm.remove') }}</Button>
          </div>
          <NativeSelect
            v-else-if="f.enum"
            :id="id(f)"
            :model-value="String(valueOf(schema, model, f.key) ?? '')"
            :options="f.enum.map((v) => ({ value: v, label: optionLabel(f, v) }))"
            :disabled="disabled"
            @update:model-value="(v: string) => set(f.key, v)"
          />
          <textarea
            v-else-if="f['x-multiline']"
            :id="id(f)"
            :value="String(model[f.key] ?? '')"
            :disabled="disabled"
            rows="4"
            spellcheck="false"
            autocomplete="off"
            class="border-input bg-background focus-visible:ring-ring/50 w-full rounded-md border px-3 py-2 font-mono text-xs shadow-xs outline-none focus-visible:ring-[3px]"
            @input="set(f.key, ($event.target as HTMLTextAreaElement).value)"
          />
          <Input
            v-else-if="f.type === 'integer' || f.type === 'number'"
            :id="id(f)"
            type="number"
            :min="f.minimum"
            :max="f.maximum"
            :model-value="numberOf(f)"
            :disabled="disabled"
            @update:model-value="(v) => numberValue(f, String(v))"
          />
          <Input
            v-else
            :id="id(f)"
            :type="isSecret(f) ? 'password' : 'text'"
            :autocomplete="isSecret(f) ? 'new-password' : 'off'"
            :model-value="String(model[f.key] ?? '')"
            :maxlength="f.maxLength"
            :disabled="disabled"
            @update:model-value="(v) => set(f.key, String(v))"
          />
          <template v-if="isTemplate(f) && !disabled" #after>
            <div class="flex flex-wrap items-center gap-1.5" role="group" :aria-label="t('schemaForm.variables')" :data-testid="`variables-${f.key}`">
              <span class="text-muted-foreground text-xs">{{ t('schemaForm.variables') }}</span>
              <button
                v-for="v in TEMPLATE_VARIABLES"
                :key="v"
                type="button"
                class="bg-muted hover:bg-accent focus-visible:ring-ring/50 rounded px-1.5 py-0.5 font-mono text-xs outline-none focus-visible:ring-[3px]"
                :title="t('schemaForm.insertVariable', { name: v })"
                @mousedown.prevent
                @click="insertVariable(f, v)"
              >
                {{ v }}
              </button>
            </div>
          </template>
        </FormField>
      </template>
    </fieldset>
  </div>
</template>
