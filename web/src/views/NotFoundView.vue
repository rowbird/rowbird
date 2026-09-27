<script setup lang="ts">
import { Button } from '@/components/ui/button'
</script>

<template>
  <section class="mx-auto flex max-w-xl flex-col items-center gap-3 py-16 text-center">
    <h1 class="text-2xl font-semibold" data-testid="page-title">{{ $t('notFound.title') }}</h1>
    <p class="text-muted-foreground">{{ $t('notFound.body') }}</p>
    <Button as-child>
      <RouterLink to="/">{{ $t('notFound.action') }}</RouterLink>
    </Button>
  </section>
</template>
