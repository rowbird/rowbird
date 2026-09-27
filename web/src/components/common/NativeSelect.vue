<script setup lang="ts">
// A styled native <select>, used for long lists such as time zones where native search and
// scrolling work better than a custom popover.
defineProps<{ id?: string; options: { value: string; label: string }[]; disabled?: boolean }>()
const model = defineModel<string>({ required: true })
</script>

<template>
  <select
    :id="id"
    v-model="model"
    :disabled="disabled"
    class="border-input bg-background focus-visible:ring-ring/50 h-9 w-full rounded-md border px-3 text-sm shadow-xs outline-none focus-visible:ring-[3px] disabled:opacity-50"
  >
    <option v-for="o in options" :key="o.value" :value="o.value">{{ o.label }}</option>
  </select>
</template>
