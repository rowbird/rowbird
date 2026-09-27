<script setup lang="ts">
import type { HTMLAttributes } from "vue"
import { cn } from "@/lib/utils"

const props = defineProps<{
  class?: HTMLAttributes["class"]
}>()
</script>

<template>
  <!-- Focusable so keyboard users can scroll a table wider than the screen. -->
  <div data-slot="table-container" class="focus-visible:ring-ring/50 relative w-full overflow-auto rounded-[inherit] outline-none focus-visible:ring-[3px]" tabindex="0">
    <table data-slot="table" :class="cn('w-full caption-bottom text-sm', props.class)">
      <slot />
    </table>
  </div>
</template>
