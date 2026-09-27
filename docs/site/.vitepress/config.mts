import { defineConfig } from 'vitepress'

// docs.rowbird.dev. English only for now; pt-BR pages will go under /pt-BR/ through `locales`.
export default defineConfig({
  title: 'Rowbird',
  description: 'Your SQL results, delivered. Self-hosted scheduled SQL reports and data alerts.',
  lang: 'en-US',
  cleanUrls: true,
  lastUpdated: true,
  srcExclude: ['README.md'],
  // The guides point at local addresses (the demo, a fresh install) that are not pages here.
  ignoreDeadLinks: 'localhostLinks',
  head: [['link', { rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }]],
  themeConfig: {
    logo: '/logo-mark.svg',
    nav: [
      { text: 'Guide', link: '/guide/introduction' },
      { text: 'Install', link: '/install/docker' },
      { text: 'Operations', link: '/operations/configuration' },
      { text: 'Reference', link: '/reference/cli' },
      { text: 'Plugins', link: '/plugins/development' },
    ],
    sidebar: [
      {
        text: 'Guide',
        items: [
          { text: 'Introduction', link: '/guide/introduction' },
          { text: 'Quick start', link: '/guide/quick-start' },
          { text: 'Try the demo', link: '/guide/demo' },
          { text: 'Concepts', link: '/guide/concepts' },
        ],
      },
      {
        text: 'Install',
        items: [
          { text: 'Docker', link: '/install/docker' },
          { text: 'Docker Compose', link: '/install/compose' },
          { text: 'Binary and systemd', link: '/install/binary' },
          { text: 'Kubernetes', link: '/install/kubernetes' },
          { text: 'Verify downloads', link: '/install/verify' },
        ],
      },
      {
        text: 'Operations',
        items: [
          { text: 'Configuration', link: '/operations/configuration' },
          { text: 'Reverse proxies', link: '/operations/reverse-proxy' },
          { text: 'Backups and restore', link: '/operations/backups' },
          { text: 'Master key rotation', link: '/operations/key-rotation' },
          { text: 'Upgrades', link: '/operations/upgrades' },
          { text: 'Observability', link: '/operations/observability' },
          { text: 'Scaling', link: '/operations/scaling' },
        ],
      },
      {
        text: 'Security',
        items: [
          { text: 'Sign-in: OIDC, passkeys, 2FA', link: '/security/authentication' },
          { text: 'Hardening', link: '/security/hardening' },
        ],
      },
      {
        text: 'Reference',
        items: [
          { text: 'Config as code (YAML)', link: '/reference/config-as-code' },
          { text: 'CLI', link: '/reference/cli' },
          { text: 'HTTP API', link: '/reference/api' },
          { text: 'API endpoints', link: '/reference/api-endpoints' },
          { text: 'Webhook payloads', link: '/reference/webhooks' },
        ],
      },
      {
        text: 'Extending',
        items: [{ text: 'Plugin development', link: '/plugins/development' }],
      },
    ],
    socialLinks: [{ icon: 'github', link: 'https://github.com/rowbird/rowbird' }],
    editLink: {
      pattern: 'https://github.com/rowbird/rowbird/edit/main/docs/site/:path',
    },
    search: { provider: 'local' },
    footer: {
      message: 'Released under the Apache License 2.0.',
    },
  },
})
