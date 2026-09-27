import type { ConfigSchema } from '@/lib/schema'

/** A schema like the postgres connector's, trimmed to what the tests exercise. */
export const pgSchema: ConfigSchema = {
  type: 'object',
  required: ['host', 'user', 'ssh_host'],
  'x-order': ['host', 'port', 'user', 'password', 'tls_mode', 'tls_ca', 'ssh_enabled', 'ssh_host', 'ssh_host_key'],
  properties: {
    host: { type: 'string', 'x-label': 'plugin.common.host.label', 'x-group': 'connection' },
    port: { type: 'integer', default: 5432, 'x-label': 'plugin.common.port.label', 'x-group': 'connection' },
    user: { type: 'string', 'x-label': 'plugin.common.user.label', 'x-group': 'connection' },
    password: { type: 'string', 'x-secret': true, 'x-label': 'plugin.common.password.label', 'x-group': 'connection' },
    tls_mode: { type: 'string', enum: ['disable', 'require', 'verify-full'], default: 'disable', 'x-label': 'plugin.common.tls_mode.label', 'x-group': 'tls' },
    tls_ca: { type: 'string', 'x-multiline': true, 'x-label': 'plugin.common.tls_ca.label', 'x-group': 'tls', 'x-show-if': { field: 'tls_mode', in: ['verify-full'] } },
    ssh_enabled: { type: 'boolean', default: false, 'x-label': 'plugin.common.ssh_enabled.label', 'x-group': 'ssh' },
    ssh_host: { type: 'string', 'x-label': 'plugin.common.ssh_host.label', 'x-group': 'ssh', 'x-show-if': { field: 'ssh_enabled', in: [true] } },
    ssh_host_key: { type: 'string', 'x-label': 'plugin.common.ssh_host_key.label', 'x-group': 'ssh', 'x-show-if': { field: 'ssh_enabled', in: [true] } },
  },
}
