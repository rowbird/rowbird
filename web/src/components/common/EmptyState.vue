<script setup lang="ts">
import type { Component } from 'vue'

defineProps<{ icon: Component; title: string; description: string }>()
</script>

<template>
  <div class="flex flex-col items-center gap-3 rounded-lg border border-dashed px-6 py-12 text-center">
    <component :is="icon" class="text-muted-foreground size-10" aria-hidden="true" />
    <h3 class="font-semibold">{{ title }}</h3>
    <p class="text-muted-foreground max-w-md text-sm">{{ description }}</p>
    <slot />
  </div>
</template>
