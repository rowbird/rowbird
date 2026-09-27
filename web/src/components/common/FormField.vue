<script setup lang="ts">
import { Label } from '@/components/ui/label'

defineProps<{ id: string; label: string; error?: string; hint?: string }>()
</script>

<template>
  <div class="grid gap-2">
    <Label :for="id">{{ label }}</Label>
    <slot :described-by="[hint && `${id}-hint`, error && `${id}-error`].filter(Boolean).join(' ') || undefined" :invalid="!!error" />
    <!-- The hint stays next to an error: it often explains how to fix it (template variables). -->
    <p v-if="hint" :id="`${id}-hint`" class="text-muted-foreground text-xs">{{ hint }}</p>
    <slot name="after" />
    <p v-if="error" :id="`${id}-error`" class="text-destructive text-sm">{{ error }}</p>
  </div>
</template>
