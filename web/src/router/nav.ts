import {
  Cable,
  FileClock,
  House,
  Link,
  type LucideIcon,
  Megaphone,
  Play,
  ScrollText,
  Settings,
} from '@lucide/vue'

export interface NavItem {
  name: string
  path: string
  labelKey: string
  icon: LucideIcon
}

export interface NavGroup {
  /** Shown above the group; the first group (home) has none. */
  labelKey?: string
  items: readonly NavItem[]
}

/**
 * Sidebar groups, following the product's flow (docs/spec/06-ui.md): build queries and reports,
 * follow their runs and links, set up where data comes from and goes to. Settings sits apart, at
 * the bottom.
 */
export const NAV_GROUPS: readonly NavGroup[] = [
  { items: [{ name: 'home', path: '/', labelKey: 'nav.home', icon: House }] },
  {
    labelKey: 'nav.groups.build',
    items: [
      { name: 'queries', path: '/queries', labelKey: 'nav.queries', icon: ScrollText },
      { name: 'reports', path: '/reports', labelKey: 'nav.reports', icon: FileClock },
    ],
  },
  {
    labelKey: 'nav.groups.follow',
    items: [
      { name: 'runs', path: '/runs', labelKey: 'nav.runs', icon: Play },
      { name: 'links', path: '/links', labelKey: 'nav.links', icon: Link },
    ],
  },
  {
    labelKey: 'nav.groups.setup',
    items: [
      { name: 'connections', path: '/connections', labelKey: 'nav.connections', icon: Cable },
      { name: 'channels', path: '/channels', labelKey: 'nav.channels', icon: Megaphone },
    ],
  },
]

export const NAV_SETTINGS: NavItem = { name: 'settings', path: '/settings', labelKey: 'nav.settings', icon: Settings }

/** Every entry, for the command palette. */
export const NAV_ITEMS: readonly NavItem[] = [...NAV_GROUPS.flatMap((g) => g.items), NAV_SETTINGS]
