<script>
  import { onMount, onDestroy } from 'svelte'
  import History from './routes/History.svelte'
  import Intercept from './routes/Intercept.svelte'
  import Repeater from './routes/Repeater.svelte'
  import Intruder from './routes/Intruder.svelte'
  import Bypass403 from './routes/Bypass403.svelte'
  import SettingsView from './routes/Settings.svelte'
  import { api } from './lib/api.js'
  import {
    activeTab,
    interceptState,
    liveTick,
    pending,
    settings,
    stats,
    TABS,
  } from './lib/store.js'

  let es
  let error = ''
  let statsTimer
  let stopped = false
  let stopping = false
  let projects = []
  let activeProjectId = ''
  let ctxMenu = { show: false, x: 0, y: 0 }

  $: if ($activeTab) history.replaceState(null, '', '#' + $activeTab)

  function onHashChange() {
    const h = location.hash.replace(/^#/, '')
    if (TABS.includes(h)) activeTab.set(h)
  }

  onMount(async () => {
    addEventListener('hashchange', onHashChange)
    try {
      settings.set(await api.settings())
    } catch (e) {
      error = e.message
    }
    try {
      const i = await api.intercept()
      pending.set(i.pending)
      interceptState.set({ request: i.request, response: i.response })
    } catch {}
    await refreshStats()
    await refreshProjects()
    statsTimer = setInterval(refreshStats, 5000)

    es = new EventSource('/api/events')
    es.addEventListener('flow', () => liveTick.update((n) => n + 1))
    es.addEventListener('project', async () => {
      await refreshProjects()
      await refreshStats()
      liveTick.update((n) => n + 1)
    })
    es.addEventListener('intercept', (e) => {
      const p = JSON.parse(e.data).data
      pending.update((l) => (l.some((x) => x.id === p.id) ? l.map((x) => (x.id === p.id ? p : x)) : [...l, p]))
    })
    es.addEventListener('intercept-gone', (e) => {
      const id = JSON.parse(e.data).data
      pending.update((l) => l.filter((x) => x.id !== id))
    })
    let closeCheckTimer
    es.addEventListener('error', () => {
      // If the server stops (externally or after shutdown), close this window.
      if (stopped) { try { window.close() } catch {}; return }
      if (es && es.readyState === EventSource.CLOSED) {
        stopped = true
        try { window.close() } catch {}
        return
      }
      clearTimeout(closeCheckTimer)
      closeCheckTimer = setTimeout(async () => {
        if (stopped) return
        try {
          const r = await fetch('/api/healthz', { cache: 'no-store' })
          if (!r.ok) throw new Error('down')
        } catch {
          stopped = true
          try { window.close() } catch {}
        }
      }, 600)
    })
  })

  onDestroy(() => {
    es?.close()
    removeEventListener('hashchange', onHashChange)
    clearInterval(statsTimer)
  })

  async function refreshStats() {
    try {
      stats.set(await api.stats())
    } catch {}
  }

  async function refreshProjects() {
    try {
      const r = await api.projects()
      projects = r.projects || []
      activeProjectId = r.active || ''
    } catch {}
  }

  async function onProjectChange(e) {
    const v = e.target.value
    if (v === '__new__') {
      const name = prompt('New project name:')
      if (!name) { e.target.value = activeProjectId; return }
      try {
        await api.createProject(name, true)
        await refreshProjects()
        await refreshStats()
        liveTick.update((n) => n + 1)
      } catch (err) {
        error = err.message
      }
      return
    }
    if (v && v !== activeProjectId) {
      try {
        await api.selectProject(v)
        await refreshProjects()
        await refreshStats()
        liveTick.update((n) => n + 1)
      } catch (err) {
        error = err.message
      }
    }
  }

  function openProjectMenu(e) {
    e.preventDefault()
    ctxMenu = { show: true, x: e.clientX, y: e.clientY }
  }
  function closeProjectMenu() {
    ctxMenu = { ...ctxMenu, show: false }
  }
  async function renameProject(p) {
    const name = prompt('Rename project:', p.name)
    if (name == null) return
    const trimmed = name.trim()
    if (!trimmed || trimmed === p.name) return
    try {
      await api.updateProject(p.id, { name: trimmed })
      await refreshProjects()
      await refreshStats()
    } catch (err) {
      error = err.message
    } finally {
      closeProjectMenu()
    }
  }
  async function deleteProject(p) {
    if (p.id === activeProjectId) {
      error = 'Cannot delete the active project. Switch to another project first.'
      closeProjectMenu()
      return
    }
    if (!confirm(`Delete project "${p.name}"?\n\nThis removes its captured history and cannot be undone.`)) return
    try {
      await api.deleteProject(p.id)
      await refreshProjects()
    } catch (err) {
      error = err.message
    } finally {
      closeProjectMenu()
    }
  }

  function onWindowKeydown(e) {
    if (e.key === 'Escape' && ctxMenu.show) closeProjectMenu()
  }

  async function toggle(kind) {
    const cur = $interceptState
    const next = { request: cur.request, response: cur.response }
    next[kind] = !cur[kind]
    try {
      const s = await api.interceptConfig(next.request, next.response)
      settings.set(s)
      interceptState.set({ request: s.interceptRequest, response: s.interceptResponse })
    } catch (e) {
      error = e.message
    }
  }

  async function toggleRecord() {
    if (!$settings) return
    try {
      settings.set(await api.saveSettings({ ...$settings, recordEnabled: !$settings.recordEnabled }))
    } catch (e) {
      error = e.message
    }
  }

  async function stopServer() {
    if (stopping || stopped) return
    if (!confirm('Turn off Eko?\n\nThe proxy stops immediately and this window can be closed.')) return
    stopping = true
    try {
      await api.shutdown()
    } catch {}
    stopped = true
    stopping = false
    es?.close()
    clearInterval(statsTimer)
    try { window.close() } catch {}
    setTimeout(() => { try { window.close() } catch {} }, 150)
  }

  $: capturing = $settings ? $settings.recordEnabled : true
  $: flowCount = $stats?.store?.flows ?? 0
</script>

<svelte:window on:keydown={onWindowKeydown} />

<div class="app">
  <header class="header">
    <div class="brand">
      <span class="logo">eko</span>
      <span class="ver">{$stats?.version || ''}</span>
    </div>

    <nav class="tabs">
      <button class:active={$activeTab === 'history'} on:click={() => activeTab.set('history')}>
        History
      </button>
      <button class:active={$activeTab === 'intercept'} on:click={() => activeTab.set('intercept')}>
        Intercept
        {#if $pending.length}<span class="count">{$pending.length}</span>{/if}
      </button>
      <button class:active={$activeTab === 'repeater'} on:click={() => activeTab.set('repeater')}>
        Repeater
      </button>
      <button class:active={$activeTab === 'intruder'} on:click={() => activeTab.set('intruder')}>
        Intruder
      </button>
      <button class:active={$activeTab === 'bypass'} on:click={() => activeTab.set('bypass')}>
        403 Bypass
      </button>
      <button class:active={$activeTab === 'settings'} on:click={() => activeTab.set('settings')}>
        Settings
      </button>
    </nav>

    <div class="spacer"></div>

    <select
      class="project-select"
      title="Project (right-click to rename/delete)"
      value={activeProjectId}
      on:change={onProjectChange}
      on:contextmenu={openProjectMenu}
    >
      {#each projects as p (p.id)}
        <option value={p.id}>{p.name}</option>
      {/each}
      <option value="__new__">+ New project…</option>
    </select>

    <button
      class="toggle"
      class:on={$interceptState.request}
      title="Pause outgoing requests"
      on:click={() => toggle('request')}>req</button
    >
    <button
      class="toggle"
      class:on={$interceptState.response}
      title="Pause incoming responses"
      on:click={() => toggle('response')}>resp</button
    >

    <button class="pill" on:click={toggleRecord} title="Toggle history capture">
      <span class="dot" class:live={capturing} class:paused={!capturing}></span>
      {capturing ? 'recording' : 'paused'}
    </button>

    <span class="pill mono" title="Proxy address">{$stats?.proxyAddr || '…'}</span>
    <span class="pill">{flowCount} flows · {Math.round($stats?.heapMB ?? 0)} MB</span>

    <button
      class="stop-btn"
      on:click={stopServer}
      disabled={stopping || stopped}
      title="Turn off the proxy and stop Eko"
    >
      <span class="power">⏻</span> stop
    </button>
  </header>

  {#if error}
    <div class="errorbar">
      <span>{error}</span>
      <button class="btn small" on:click={() => (error = '')}>dismiss</button>
    </div>
  {/if}

  <main class="main">
    {#if $activeTab === 'history'}
      <History />
    {:else if $activeTab === 'intercept'}
      <Intercept />
    {:else if $activeTab === 'repeater'}
      <Repeater />
    {:else if $activeTab === 'intruder'}
      <Intruder />
    {:else if $activeTab === 'bypass'}
      <Bypass403 />
    {:else if $activeTab === 'settings'}
      <SettingsView />
    {/if}
  </main>

  {#if stopped}
    <div class="stopped">
      <div class="stopped-card">
        <div class="logo">eko</div>
        <h1>Eko is stopped</h1>
        <p>The proxy is off and no traffic is being captured.</p>
        <p class="faint">You can close this window. Reopen Eko from the app menu to start it again.</p>
      </div>
    </div>
  {/if}

  {#if ctxMenu.show}
    <div class="ctx-overlay" on:click={closeProjectMenu} on:contextmenu|preventDefault={closeProjectMenu}></div>
    <div class="ctx-menu" style="left:{ctxMenu.x}px; top:{ctxMenu.y}px">
      <div class="ctx-title">Projects</div>
      {#each projects as p (p.id)}
        <div class="ctx-item" class:active={p.id === activeProjectId}>
          <span class="ctx-name" title={p.name}>{p.name}{p.id === activeProjectId ? ' (active)' : ''}</span>
          <span class="ctx-actions">
            <button class="ctx-btn" title="Rename" on:click|stopPropagation={() => renameProject(p)}>rename</button>
            <button class="ctx-btn danger" title="Delete" disabled={p.id === activeProjectId} on:click|stopPropagation={() => deleteProject(p)}>delete</button>
          </span>
        </div>
      {/each}
      {#if projects.length === 0}
        <div class="ctx-empty">No projects</div>
      {/if}
    </div>
  {/if}
</div>
