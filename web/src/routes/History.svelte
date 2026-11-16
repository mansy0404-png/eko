<script>
  import { onMount, onDestroy } from 'svelte'
  import { api, fmtBytes, fmtTime, statusClass } from '../lib/api.js'
  import { liveTick, stats } from '../lib/store.js'
  import Detail from './Detail.svelte'

  const ROW = 28
  let flows = []
  let q = ''
  let method = ''
  let status = ''
  let repeaterOnly = false
  let selectedId = null
  let loading = false
  let scopeOnly = false
  let showScope = false
  let scopeIncludeText = ''
  let scopeExcludeText = ''

  let listEl
  let scrollTop = 0
  let viewH = 600
  let ro

  let dirty = false
  let reloadTimer
  let debounceTimer

  function globMatch(pattern, s) {
    if (!pattern) return false
    let re = '^'
    for (let i = 0; i < pattern.length; i++) {
      const c = pattern[i]
      if (c === '*') re += '.*'
      else if (c === '?') re += '.'
      else re += c.replace(/[.+^${}()|[\]\\]/g, '\\$&')
    }
    re += '$'
    try { return new RegExp(re, 'i').test(s) } catch { return false }
  }
  function matchAny(patterns, url, host) {
    for (const p of (patterns || [])) {
      if (!p) continue
      if (globMatch(p, url) || globMatch(p, host)) return true
    }
    return false
  }
  function inScope(f, inc, exc) {
    const url = f.url || ''
    const host = f.host || ''
    if (exc && exc.length && matchAny(exc, url, host)) return false
    if (inc && inc.length) return matchAny(inc, url, host)
    return true
  }

  async function load() {
    dirty = false
    loading = true
    try {
      const params = { limit: '2000' }
      if (q) params.q = q
      if (method) params.method = method
      if (status) params.status = status
      if (repeaterOnly) params.repeater = '1'
      const r = await api.flows(params)
      flows = r.flows || []
    } catch {
      /* keep showing what we have */
    } finally {
      loading = false
    }
  }

  function schedule() {
    clearTimeout(debounceTimer)
    debounceTimer = setTimeout(load, 250)
  }

  function onEvent() {
    if (dirty) return
    dirty = true
    clearTimeout(reloadTimer)
    reloadTimer = setTimeout(() => {
      if (dirty) load()
    }, 350)
  }

  $: $liveTick, onEvent()

  $: activeProject = $stats?.project
  $: scopeInc = activeProject?.scopeInclude || []
  $: scopeExc = activeProject?.scopeExclude || []
  $: displayFlows = scopeOnly ? flows.filter((f) => inScope(f, scopeInc, scopeExc)) : flows
  $: start = clampStart(scrollTop, displayFlows.length, viewH)
  $: end = Math.min(displayFlows.length, start + Math.ceil(viewH / ROW) + 16)
  $: visible = displayFlows.slice(start, end)
  $: topPad = start * ROW
  $: botPad = Math.max(0, (displayFlows.length - end) * ROW)

  function clampStart(top, count, height) {
    const maxStart = Math.max(0, count - Math.ceil(height / ROW) - 16)
    return Math.min(maxStart, Math.max(0, Math.floor(top / ROW) - 8))
  }

  function measure() {
    if (!listEl) return
    viewH = listEl.clientHeight
  }

  onMount(() => {
    load()
    measure()
    ro = new ResizeObserver(measure)
    if (listEl) ro.observe(listEl)
    return () => {
      ro?.disconnect()
      clearTimeout(debounceTimer)
      clearTimeout(reloadTimer)
    }
  })

  onDestroy(() => {
    ro?.disconnect()
    clearTimeout(debounceTimer)
    clearTimeout(reloadTimer)
  })

  async function clearAll() {
    if (!confirm('Delete the entire captured history?')) return
    await api.clearFlows()
    flows = []
    selectedId = null
  }

  function onScroll(e) {
    scrollTop = e.target.scrollTop
  }

  function pick(f) {
    selectedId = f.id
  }

  function onDeleted(id) {
    flows = flows.filter((f) => f.id !== id)
    if (selectedId === id) selectedId = null
  }

  function openScope() {
    scopeIncludeText = (scopeInc || []).join('\n')
    scopeExcludeText = (scopeExc || []).join('\n')
    showScope = true
  }
  async function saveScope() {
    const id = activeProject?.id
    if (!id) return
    const inc = scopeIncludeText.split(/\r?\n/).map((s) => s.trim()).filter(Boolean)
    const exc = scopeExcludeText.split(/\r?\n/).map((s) => s.trim()).filter(Boolean)
    try {
      await api.updateProject(id, { scopeInclude: inc, scopeExclude: exc })
      showScope = false
      try { stats.set(await api.stats()) } catch {}
    } catch (e) {
      alert(e.message)
    }
  }
</script>

<div class="toolbar">
  <input
    class="search"
    placeholder="filter url / host / method…"
    bind:value={q}
    on:input={schedule}
  />
  <select bind:value={method} on:change={load}>
    <option value="">all methods</option>
    <option>GET</option>
    <option>POST</option>
    <option>PUT</option>
    <option>PATCH</option>
    <option>DELETE</option>
    <option>HEAD</option>
    <option>OPTIONS</option>
  </select>
  <select bind:value={status} on:change={load}>
    <option value="">any status</option>
    <option value="2xx">2xx</option>
    <option value="3xx">3xx</option>
    <option value="4xx">4xx</option>
    <option value="5xx">5xx</option>
  </select>
  <label class="chk muted">
    <input type="checkbox" bind:checked={repeaterOnly} on:change={load} /> repeater only
  </label>
  <label class="chk muted" title="Show only URLs/hosts matching this project's scope">
    <input type="checkbox" bind:checked={scopeOnly} /> in scope
  </label>
  <button class="btn small" on:click={openScope} title="Edit project scope patterns">scope…</button>
  <div class="spacer"></div>
  <span class="faint">{displayFlows.length} shown{loading ? ' · loading…' : ''}</span>
  <button class="btn" on:click={load}>refresh</button>
  <button class="btn danger" on:click={clearAll}>clear</button>
</div>

<div class="split">
  <div class="list" bind:this={listEl} on:scroll={onScroll}>
    <div class="thead">
      <div class="grid-row">
        <span>time</span><span>method</span><span>status</span><span>host</span><span>path</span
        ><span>size</span><span>ms</span>
      </div>
    </div>
    {#if displayFlows.length === 0}
      <div class="empty">
        {flows.length === 0
          ? 'No traffic yet in this project. Point FoxyProxy at the proxy port, install the CA, and browse something.'
          : 'No flows match the current filters/scope.'}
      </div>
    {:else}
      <div style="height:{topPad}px"></div>
      {#each visible as f (f.id)}
        <div
          class="grid-row"
          class:sel={f.id === selectedId}
          on:click={() => pick(f)}
          title={f.url}
        >
          <span class="muted mono">{fmtTime(f.startedAt)}</span>
          <span class="method {f.method}">{f.method}</span>
          <span class="status {statusClass(f.status)}">{f.status || '…'}</span>
          <span class="host trunc">{f.host}</span>
          <span class="path trunc">{f.path}{f.query ? '?' + f.query : ''}</span>
          <span class="muted mono">{fmtBytes((f.req?.bodyLen || 0) + (f.resp?.bodyLen || 0))}</span>
          <span class="muted mono">{f.durationMs >= 0 ? f.durationMs : ''}</span>
        </div>
      {/each}
      <div style="height:{botPad}px"></div>
    {/if}
  </div>

  {#if selectedId}
    <Detail id={selectedId} onDeleted={onDeleted} />
  {:else}
    <div class="detail">
      <div class="empty">Select a request to inspect it.</div>
    </div>
  {/if}
</div>

{#if showScope}
  <div class="scope-modal" on:click|self={() => (showScope = false)}>
    <div class="scope-card">
      <h3>Project scope</h3>
      <p class="faint">One pattern per line. Use * and ? wildcards; matched against URL or host. Leave "Include" empty to treat everything (not excluded) as in scope. The "in scope" filter uses these rules.</p>
      <label>Include patterns</label>
      <textarea bind:value={scopeIncludeText} spellcheck="false" placeholder="*.target.com/*&#10;api.example.com"></textarea>
      <label>Exclude patterns</label>
      <textarea bind:value={scopeExcludeText} spellcheck="false" placeholder="*.google-analytics.com/*"></textarea>
      <div class="row">
        <button class="btn primary" on:click={saveScope}>Save</button>
        <button class="btn" on:click={() => (showScope = false)}>Cancel</button>
      </div>
    </div>
  </div>
{/if}
