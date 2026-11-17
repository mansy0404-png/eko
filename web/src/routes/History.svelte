<script>
  import { onMount, onDestroy } from 'svelte'
  import { api, fetchBody, fmtBytes, fmtTime, statusClass } from '../lib/api.js'
  import { headersText, requestHeaders } from '../lib/editor.js'
  import { activeTab, addRepeaterTab, bypass403Raw, bypass403Run, intruderTemplate, liveTick, stats } from '../lib/store.js'
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
  let ctx = null

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
    window.addEventListener('click', closeCtx)
    window.addEventListener('keydown', onCtxKey)
    return () => {
      ro?.disconnect()
      clearTimeout(debounceTimer)
      clearTimeout(reloadTimer)
      window.removeEventListener('click', closeCtx)
      window.removeEventListener('keydown', onCtxKey)
    }
  })

  onDestroy(() => {
    ro?.disconnect()
    clearTimeout(debounceTimer)
    clearTimeout(reloadTimer)
  })

  function closeCtx() {
    if (ctx) ctx = null
  }

  function onCtxKey(e) {
    if (e.key === 'Escape') closeCtx()
  }

  function openCtx(e, f) {
    e.preventDefault()
    selectedId = f.id
    ctx = {
      x: Math.min(e.clientX, window.innerWidth - 220),
      y: Math.min(e.clientY, window.innerHeight - 170),
      flow: f,
    }
  }

  async function rawFrom(f) {
    // The History list only carries summaries (no headers), so load the full
    // flow before building a raw request — otherwise every header is lost.
    let full = f
    try {
      full = await api.flow(f.id)
    } catch {}
    let decoded = false
    let bodyText = ''
    try {
      const rb = await fetchBody(f.id, 'req')
      decoded = !!(rb && rb.decoded)
      bodyText = rb && !rb.binary ? rb.text || '' : ''
    } catch {}
    const headers = headersText(requestHeaders(full, decoded))
    return `${full.method || f.method} ${full.url || f.url} HTTP/1.1\n${headers}\n\n${bodyText}`
  }

  // Put a payload marker in the first query value (or add one) so the request
  // is ready for an Intruder attack after it is seeded from history.
  function withMarker(raw) {
    const nl = raw.indexOf('\n')
    if (nl < 0) return raw + '§§'
    const line = raw.slice(0, nl)
    const m = line.match(/^(\S+)\s+(\S+)\s+(\S+)\s*$/)
    if (!m) {
      const sep = raw.indexOf('\n\n')
      return sep >= 0 ? `${raw.slice(0, sep + 2)}§§${raw.slice(sep + 2)}` : raw + '§§'
    }
    const url = m[2]
    const hash = url.indexOf('#')
    const clean = hash >= 0 ? url.slice(0, hash) : url
    const frag = hash >= 0 ? url.slice(hash) : ''
    const q = clean.indexOf('?')
    let marked
    if (q >= 0) {
      const base = clean.slice(0, q + 1)
      const kvs = clean.slice(q + 1).split('&')
      if (kvs[0].includes('=')) kvs[0] = kvs[0].replace(/=.*/, '=§§')
      else kvs[0] += '=§§'
      marked = base + kvs.join('&') + frag
    } else {
      marked = clean + '?x=§§' + frag
    }
    return `${m[1]} ${marked} ${m[3]}` + raw.slice(nl)
  }

  async function sendCtxTo(kind) {
    const f = ctx?.flow
    ctx = null
    if (!f) return
    const raw = await rawFrom(f)
    if (kind === 'repeater') {
      addRepeaterTab({ name: `${f.method} ${f.host}${f.path}`.slice(0, 48), raw })
    } else {
      intruderTemplate.set(withMarker(raw))
      activeTab.set('intruder')
    }
  }

  async function copyCtxURL() {
    const url = ctx?.flow?.url
    ctx = null
    if (url) await navigator.clipboard.writeText(url)
  }

  async function deleteCtx() {
    const f = ctx?.flow
    ctx = null
    if (!f?.id) return
    await api.deleteFlow(f.id)
    onDeleted(f.id)
  }

  // Right-click action: seed the 403 Bypass tab with this request and run it.
  async function seedBypass(f) {
    ctx = null
    bypass403Raw.set(await rawFrom(f))
    bypass403Run.update((n) => n + 1)
    activeTab.set('bypass')
  }

  async function clearAll() {
    if (!confirm('Delete the entire captured history?')) return
    await api.clearFlows()
    flows = []
    selectedId = null
  }

  function onScroll(e) {
    scrollTop = e.target.scrollTop
    closeCtx()
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
          on:contextmenu={(e) => openCtx(e, f)}
          title={f.url + ' — right-click for options'}
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

{#if ctx}
  <div class="history-ctx-menu" style="left:{ctx.x}px;top:{ctx.y}px">
    <div class="hctx-title trunc" title={ctx.flow.url}>{ctx.flow.method} {ctx.flow.host}{ctx.flow.path}</div>
    <button on:click={() => sendCtxTo('repeater')}>Send to <strong>Repeater</strong></button>
    <button on:click={() => sendCtxTo('intruder')}>
      Send to <strong>Intruder</strong> <span class="faint">(§§ marker)</span>
    </button>
    <button on:click={() => seedBypass(ctx.flow)}>
      <strong>403 bypass</strong> <span class="faint">(open in tab)</span>
    </button>
    <button on:click={copyCtxURL}>Copy URL</button>
    <button class="danger" on:click={deleteCtx}>Delete request</button>
  </div>
{/if}

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
