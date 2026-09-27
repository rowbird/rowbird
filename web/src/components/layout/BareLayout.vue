<script setup lang="ts">
import BrandMark from './BrandMark.vue'

import LanguageThemeControls from './LanguageThemeControls.vue'
</script>

<template>
  <div class="bg-muted/40 flex min-h-svh flex-col">
    <header class="flex items-center justify-between px-4 py-3">
      <div class="flex items-center gap-2 font-semibold">
        <BrandMark />
        {{ $t('app.name') }}
      </div>
      <LanguageThemeControls />
    </header>
    <main class="flex flex-1 items-start justify-center px-4 py-8 sm:items-center">
      <slot />
    </main>
  </div>
</template>
