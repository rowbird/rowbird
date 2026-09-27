<script setup lang="ts">
import { ref, watch } from 'vue'

import FormField from '@/components/common/FormField.vue'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { useApiError } from '@/composables/useApiError'

// Asks for the current password before a sensitive change (turning off 2FA, new recovery codes).
const props = defineProps<{ title: string; description: string; confirmLabel: string; destructive?: boolean; onConfirm: (password: string) => Promise<void> }>()
const open = defineModel<boolean>('open', { required: true })
const password = ref('')
const busy = ref(false)
const err = useApiError()

watch(open, (v) => {
  if (v) {
    password.value = ''
    err.clear()
  }
})

async function submit() {
  busy.value = true
  err.clear()
  try {
    await props.onConfirm(password.value)
    open.value = false
  } catch (e) {
    err.error.value = e
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog v-model:open="open">
    <DialogContent>
      <form class="grid gap-4" @submit.prevent="submit">
        <DialogHeader>
          <DialogTitle>{{ title }}</DialogTitle>
          <DialogDescription>{{ description }}</DialogDescription>
        </DialogHeader>
        <FormField id="confirm-with-password" :label="$t('fields.currentPassword')" :error="err.field('password')">
          <Input id="confirm-with-password" v-model="password" type="password" autocomplete="current-password" required />
        </FormField>
        <p v-if="err.message.value && !err.hasFieldErrors.value" class="text-destructive text-sm">{{ err.message.value }}</p>
        <DialogFooter>
          <Button type="submit" :variant="destructive ? 'destructive' : 'default'" :disabled="busy || !password">{{ confirmLabel }}</Button>
        </DialogFooter>
      </form>
    </DialogContent>
  </Dialog>
</template>
