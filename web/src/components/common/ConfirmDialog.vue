<script setup lang="ts">
import { ref } from 'vue'

import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from '@/components/ui/alert-dialog'
import { Button } from '@/components/ui/button'

const props = defineProps<{
  title: string
  description: string
  confirmLabel: string
  destructive?: boolean
  onConfirm: () => Promise<void> | void
}>()
const open = defineModel<boolean>('open', { required: true })
const busy = ref(false)

async function confirm() {
  busy.value = true
  try {
    await props.onConfirm()
    open.value = false
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <AlertDialog v-model:open="open">
    <AlertDialogContent>
      <AlertDialogHeader>
        <AlertDialogTitle>{{ title }}</AlertDialogTitle>
        <AlertDialogDescription>{{ description }}</AlertDialogDescription>
      </AlertDialogHeader>
      <AlertDialogFooter>
        <AlertDialogCancel :disabled="busy">{{ $t('common.cancel') }}</AlertDialogCancel>
        <Button :variant="destructive ? 'destructive' : 'default'" :disabled="busy" data-testid="confirm-action" @click="confirm">
          {{ confirmLabel }}
        </Button>
      </AlertDialogFooter>
    </AlertDialogContent>
  </AlertDialog>
</template>
