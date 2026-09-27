/** A plugin catalog like GET /api/v1/plugins returns, with the sqlite connector and three conditions. */
export const conditionCatalog = {
  plugins: [
    { kind: 'connector', id: 'sqlite', name: 'n', description: 'd', icon: '', version: '1', schema: { type: 'object', properties: {}, required: [], 'x-order': [] }, capabilities: { dialect: 'sqlite' } },
    { kind: 'condition', id: 'always', name: 'plugin.condition.always.name', description: 'plugin.condition.always.description', icon: '', version: '1', schema: { type: 'object', properties: {}, required: [], 'x-order': [] }, capabilities: {} },
    { kind: 'condition', id: 'has_rows', name: 'plugin.condition.has_rows.name', description: 'plugin.condition.has_rows.description', icon: '', version: '1', schema: { type: 'object', properties: {}, required: [], 'x-order': [] }, capabilities: {} },
    {
      kind: 'condition', id: 'row_count', name: 'plugin.condition.row_count.name', description: 'plugin.condition.row_count.description', icon: '', version: '1', capabilities: {},
      schema: {
        type: 'object', required: ['op', 'value'], 'x-order': ['op', 'value'],
        properties: {
          op: { type: 'string', enum: ['eq', 'gt'], default: 'gt', 'x-label': 'plugin.condition.row_count.op.label' },
          value: { type: 'integer', default: 0, minimum: 0, 'x-label': 'plugin.condition.row_count.value.label' },
        },
      },
    },
  ],
  messages: {
    en: {
      'plugin.condition.always.name': 'Always', 'plugin.condition.always.description': 'Always continues.',
      'plugin.condition.has_rows.name': 'Has rows', 'plugin.condition.has_rows.description': 'At least one row.',
      'plugin.condition.row_count.name': 'Row count', 'plugin.condition.row_count.description': 'Compares the number of rows.',
      'plugin.condition.row_count.op.label': 'Comparison', 'plugin.condition.row_count.op.gt': 'is greater than', 'plugin.condition.row_count.op.eq': 'equals',
      'plugin.condition.row_count.value.label': 'Number of rows',
    },
  },
}

const emptySchema = { type: 'object', properties: {}, required: [], 'x-order': [] }

/** Destinations and file formatters like the server's, trimmed to what the tests exercise. */
export const deliveryCatalog = {
  plugins: [
    { kind: 'formatter', id: 'csv', name: 'plugin.format.csv.name', description: '', icon: '', version: '1', schema: emptySchema, capabilities: { kind: 'file', extension: 'csv' } },
    { kind: 'formatter', id: 'xlsx', name: 'plugin.format.xlsx.name', description: '', icon: '', version: '1', schema: emptySchema, capabilities: { kind: 'file', extension: 'xlsx' } },
    { kind: 'formatter', id: 'html_table', name: 'plugin.format.html_table.name', description: '', icon: '', version: '1', schema: emptySchema, capabilities: { kind: 'inline' } },
    {
      kind: 'destination', id: 'email', name: 'plugin.email.name', description: 'plugin.email.description', icon: 'mail', version: '1',
      capabilities: { supports_attachments: true, max_attachment_bytes: 20971520, inline_target: 'html', supports_status: false, always_notify: false, modes: ['inline', 'attachment', 'link'] },
      schema: {
        type: 'object', required: ['host', 'from'], 'x-order': ['host', 'password', 'from'],
        properties: {
          host: { type: 'string', 'x-label': 'plugin.email.host.label' },
          password: { type: 'string', 'x-secret': true, 'x-label': 'plugin.email.password.label' },
          from: { type: 'string', 'x-label': 'plugin.email.from.label' },
        },
      },
      delivery_schema: {
        type: 'object', required: ['to'], 'x-order': ['to', 'subject'],
        properties: {
          to: { type: 'string', 'x-label': 'plugin.email.to.label' },
          subject: { type: 'string', 'x-label': 'plugin.email.subject.label', 'x-help': 'plugin.template.help' },
        },
      },
    },
    {
      kind: 'destination', id: 'webhook', name: 'plugin.webhook.name', description: 'plugin.webhook.description', icon: 'webhook', version: '1',
      capabilities: { supports_attachments: false, supports_status: false, always_notify: false, modes: ['inline', 'link'] },
      schema: { type: 'object', required: ['url'], 'x-order': ['url'], properties: { url: { type: 'string', 'x-label': 'plugin.webhook.url.label' } } },
      delivery_schema: emptySchema,
    },
  ],
  messages: {
    en: {
      'plugin.format.csv.name': 'CSV', 'plugin.format.xlsx.name': 'Excel', 'plugin.format.html_table.name': 'HTML table',
      'plugin.email.name': 'Email', 'plugin.email.description': 'Sends email through SMTP.', 'plugin.email.host.label': 'SMTP host',
      'plugin.email.password.label': 'Password', 'plugin.email.from.label': 'From', 'plugin.email.to.label': 'To', 'plugin.email.subject.label': 'Subject',
      'plugin.webhook.name': 'Webhook', 'plugin.webhook.description': 'Posts JSON.', 'plugin.webhook.url.label': 'URL',
    },
  },
}
