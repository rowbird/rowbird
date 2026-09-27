import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import type { Component } from 'vue'
import { createMemoryHistory } from 'vue-router'

import { createAppI18n, type Locale } from '@/i18n'
import { createAppRouter } from '@/router'

/** Mounts a view with the real i18n, Pinia and router (without guards). */
export async function mountView(component: Component, { locale = 'en' as Locale, path = '/' } = {}) {
  const pinia = createPinia()
  setActivePinia(pinia)
  const router = createAppRouter(createMemoryHistory())
  await router.push(path)
  await router.isReady()
  const wrapper = mount(component, { global: { plugins: [pinia, createAppI18n(locale), router] }, attachTo: document.body })
  return { wrapper, router, pinia }
}

export const flush = () => new Promise((r) => setTimeout(r))
