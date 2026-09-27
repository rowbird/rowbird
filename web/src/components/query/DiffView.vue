<script setup lang="ts">
import { sql } from '@codemirror/lang-sql'
import { unifiedMergeView } from '@codemirror/merge'
import { EditorState } from '@codemirror/state'
import { EditorView, lineNumbers } from '@codemirror/view'
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

/** Read-only unified diff between two SQL texts. */
const props = defineProps<{ original: string; modified: string }>()
const host = ref<HTMLDivElement>()
let view: EditorView | null = null

function build() {
  view?.destroy()
  view = new EditorView({
    parent: host.value!,
    state: EditorState.create({
      doc: props.modified,
      extensions: [
        lineNumbers(),
        sql(),
        EditorState.readOnly.of(true),
        EditorView.editable.of(false),
        EditorView.lineWrapping,
        unifiedMergeView({ original: props.original, mergeControls: false, highlightChanges: true }),
        EditorView.theme({ '&': { fontSize: '12px', backgroundColor: 'var(--background)', color: 'var(--foreground)' }, '.cm-gutters': { backgroundColor: 'var(--muted)' } }),
      ],
    }),
  })
}

onMounted(build)
watch(() => [props.original, props.modified], build)
onBeforeUnmount(() => view?.destroy())
</script>

<template>
  <div ref="host" class="max-h-[60vh] overflow-auto rounded-md border" data-testid="diff-view" />
</template>
