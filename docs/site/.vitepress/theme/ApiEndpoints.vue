<script setup lang="ts">
import { data as tags } from '../../reference/api.data'

function access(op: (typeof tags)[number]['operations'][number]): string {
  if (op.public) return 'public'
  const a = op.authz
  if (!a) return ''
  const parts = [`role ${a.role}`, `scope ${a.scope}`]
  if (a.session_only) parts.push('session only')
  return parts.join(', ')
}
</script>

<template>
  <section v-for="tag in tags" :key="tag.name">
    <h2 :id="tag.name">{{ tag.name }}</h2>
    <p>{{ tag.description }}</p>
    <table>
      <thead>
        <tr>
          <th>Endpoint</th>
          <th>What it does</th>
          <th>Access</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="op in tag.operations" :key="op.method + op.path">
          <td>
            <code>{{ op.method }} {{ op.path }}</code>
          </td>
          <td>{{ op.summary }}</td>
          <td>{{ access(op) }}</td>
        </tr>
      </tbody>
    </table>
  </section>
</template>
