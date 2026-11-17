<script>
  import { onMount, onDestroy } from 'svelte'
  import { api, fetchBody, fmtBytes, fmtTime, statusClass } from '../lib/api.js'
  import { activeTab, addRepeaterTab, intruderTemplate, liveTick, stats } from '../lib/store.js'
  import { buildBypassVariants } from '../lib/bypass403.js'
  import BodyView from '../lib/BodyView.svelte'
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

  let showBypass = false
  let bypassRaw = ''
  let bypassResults = []
  let bypassRunning = false
  let bypassSelected = 0
  let bypassRespBody = null
  let bypassInterestingOnly = false
  let bypassErr = ''

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

  function cleanHeaders(headers, decoded) {
    return (headers || [])
      .filter(([k]) => {
        const lk = (k || '').toLowerCase()
        if (lk === 'host' || lk === 'content-length' || lk === 'transfer-encoding') return false
        if (lk === 'content-encoding' && decoded) return false
        return true
      })
      .map(([k, v]) => `${k}: ${v}`)
      .join('\n')
  }

  async function rawFrom(f) {
    let decoded = false
    let bodyText = ''
    try {
      const rb = await fetchBody(f.id, 'req')
      decoded = !!(rb && rb.decoded)
      bodyText = rb && !rb.binary ? rb.text || '' : ''
    } catch {}
    const headers = cleanHeaders(f.req?.headers, decoded)
    return `${f.method} ${f.url} HTTP/1.1\n${headers}\n\n${bodyText}`
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

  $: bypassHits = bypassResults.filter((r) => r.status && r.status !== 403 && !r.error).length
  $: bypassShown = bypassInterestingOnly
    ? bypassResults.filter((r) => r.status && r.status !== 403 && !r.error)
    : bypassResults
  $: bypassSelectedResult = bypassResults[bypassSelected] || null

  async function openBypass(f) {
    ctx = null
    showBypass = true
    bypassRaw = await rawFrom(f)
    bypassResults = []
    bypassRunning = false
    bypassSelected = 0
    bypassRespBody = null
    bypassInterestingOnly = false
    bypassErr = ''
    runBypass()
  }

  async function runBypass() {
    if (bypassRunning) return
    const variants = buildBypassVariants(bypassRaw)
    if (!variants.length) {
      bypassErr = 'Could not find a valid absolute URL in the request line.'
      return
    }
    bypassErr = ''
    bypassRunning = true
    bypassResults = variants.map((v, i) => ({
      i,
      name: v.name,
      group: v.group,
      raw: v.raw,
      status: 0,
      length: 0,
      time: 0,
      error: '',
      flow: null,
    }))

    let cursor = 0
    async function worker() {
      while (bypassRunning && cursor < variants.length) {
        const i = cursor++
        const start = performance.now()
        try {
          const r = await api.repeater(variants[i].raw)
          const f = r.flow || {}
          bypassResults[i] = {
            ...bypassResults[i],
            status: f.status || 0,
            length: f.resp?.bodyLen || 0,
            time: Math.round(performance.now() - start),
            error: f.err || '',
            flow: f,
          }
        } catch (e) {
          bypassResults[i] = {
            ...bypassResults[i],
            error: e.message,
            time: Math.round(performance.now() - start),
          }
        }
        bypassResults = [...bypassResults]
      }
    }
    await Promise.all(Array.from({ length: 3 }, () => worker()))
    bypassRunning = false
  }

  function stopBypass() {
    bypassRunning = false
  }

  async function selectBypass(i) {
    bypassSelected = i
    bypassRespBody = null
    const f = bypassResults[i]?.flow
    if (f?.id) {
      try {
        bypassRespBody = await fetchBody(f.id, 'resp')
      } catch {}
    }
  }

  function bypassToRepeater() {
    const v = bypassSelectedResult
    if (v) addRepeaterTab({ name: `403: ${v.name}`.slice(0, 48), raw: v.raw })
  }

  async function copyBypass() {
    if (bypassSelectedResult) await navigator.clipboard.writeText(bypassSelectedResult.raw)
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
    <button on:click={() => openBypass(ctx.flow)}>
      <strong>403 bypass</strong> <span class="faint">(try variants)</span>
    </button>
    <button on:click={copyCtxURL}>Copy URL</button>
    <button class="danger" on:click={deleteCtx}>Delete request</button>
  </div>
{/if}

{#if showBypass}
  <div class="scope-modal bypass-modal" on:click|self={() => (showBypass = false)}>
    <div class="bypass-card">
      <div class="bypass-head">
        <div>
          <h3>403 bypass</h3>
          <span class="faint mono" title={bypassRaw.split('\n')[0]}>{bypassRaw.split('\n')[0]}</span>
        </div>
        <div class="spacer"></div>
        <label class="chk faint" title="Hide 403 / error responses">
          <input type="checkbox" bind:checked={bypassInterestingOnly} /> interesting only ({bypassHits})
        </label>
        {#if bypassRunning}
          <button class="btn small danger" on:click={stopBypass}>Stop</button>
        {:else}
          <button class="btn small primary" on:click={runBypass}>Re-run</button>
        {/if}
        <button class="btn small" on:click={() => (showBypass = false)}>Close</button>
      </div>
      <div class="note faint">
        Sends header, path and method variations of the selected request. Only test targets you are
        authorized to probe — some method variants may change server state. Variants with a status
        other than 403 (and no error) are highlighted.
      </div>
      {#if bypassErr}<div class="errorbar">{bypassErr}</div>{/if}
      {#if bypassRunning}
        <div class="faint" style="font-size:11px">
          running… {bypassResults.filter((r) => r.status || r.error).length}/{bypassResults.length}
        </div>
      {/if}
      <div class="bypass-body">
        <div class="bypass-list">
          {#each bypassShown as r (r.i)}
            <button
              class="bypass-row"
              class:sel={r.i === bypassSelected}
              class:hit={r.status && r.status !== 403 && !r.error}
              on:click={() => selectBypass(r.i)}
            >
              <span class="muted mono">#{r.i + 1}</span>
              <span class="bname trunc" title={r.name}>{r.name}</span>
              <span class="bgroup faint">{r.group}</span>
              <span class="status {statusClass(r.status)}">{r.status || (r.error ? 'err' : '…')}</span>
              <span class="muted mono">{r.length ? fmtBytes(r.length) : ''}</span>
            </button>
          {/each}
        </div>
        <div class="bypass-detail">
          {#if bypassSelectedResult}
            <div class="bypass-detail-head">
              <span class="faint mono trunc">{bypassSelectedResult.name}</span>
              <span class="faint">{bypassSelectedResult.time} ms</span>
              <div class="spacer"></div>
              <button class="btn small" on:click={bypassToRepeater}>to repeater</button>
              <button class="btn small" on:click={copyBypass}>copy request</button>
            </div>
            <div class="bypass-preview">
              <pre class="mono request-preview">{bypassSelectedResult.raw}</pre>
              {#if bypassSelectedResult.flow}
                <BodyView body={bypassRespBody} id={bypassSelectedResult.flow.id || ''} kind="resp" />
              {:else}
                <div class="empty">{bypassSelectedResult.error || 'Waiting for response…'}</div>
              {/if}
            </div>
          {:else}
            <div class="empty">Select a variant.</div>
          {/if}
        </div>
      </div>
    </div>
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
