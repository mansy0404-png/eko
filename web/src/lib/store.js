import { writable } from 'svelte/store'

export const TABS = ['history', 'intercept', 'repeater', 'intruder', 'settings']

function initialTab() {
  const h = location.hash.replace(/^#/, '')
  return TABS.includes(h) ? h : 'history'
}

export const activeTab = writable(initialTab())
export const settings = writable(null)
export const stats = writable(null)
export const pending = writable([])
export const interceptState = writable({ request: false, response: false })
export const liveTick = writable(0)
export const repeaterTabs = writable([])
export const repeaterActive = writable(0)

let nextId = 1

export function addRepeaterTab(tab) {
  const id = nextId++
  const t = {
    id,
    name: tab.name || `tab ${id}`,
    raw: tab.raw || 'GET https://example.com/ HTTP/1.1\nAccept: */*\n\n',
    response: null,
    error: '',
    busy: false,
  }
  repeaterTabs.update((list) => [...list, t])
  repeaterActive.set(id)
  activeTab.set('repeater')
}

export function closeRepeaterTab(id) {
  repeaterTabs.update((list) => list.filter((t) => t.id !== id))
  repeaterActive.update((cur) => (cur === id ? 0 : cur))
}
