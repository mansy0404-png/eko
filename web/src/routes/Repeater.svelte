<script>
  import { onMount, tick } from 'svelte'
  import { api, fetchBody, fmtBytes, statusClass } from '../lib/api.js'
  import { applyUrlTransform, parseRawHeaders } from '../lib/editor.js'
  import { addRepeaterTab, closeRepeaterTab, repeaterActive, repeaterTabs } from '../lib/store.js'
  import BodyView from '../lib/BodyView.svelte'

  let respBody = null
  let respLoadedId = ''
  let showHeaders = true
  let showReqHeaders = true
  let transformMsg = ''
  let transformTimer
  let splitPct = 50
  let dragging = false
  let splitEl
  let vertical = false

  let reqEl
  let reqSearch = ''
  let reqIdx = 0
  let respSearch = ''
  let respMatchCount = 0
  let bodyView

  $: current = $repeaterTabs.find((t) => t.id === $repeaterActive) || null
  $: if (!current && $repeaterTabs.length > 0) repeaterActive.set($repeaterTabs[0].id)
  $: if (current?.response?.id && current.response.id !== respLoadedId) {
    respLoadedId = current.response.id
    loadResp(current.response.id)
  }
  $: resp = current?.response || null

  $: reqText = current?.raw || ''
  $: reqMatches = findPositions(reqText, reqSearch)
  $: reqTotal = reqMatches.length
  $: reqHeaders = parseRawHeaders(reqText)

  function flash(msg) {
    transformMsg = msg
    clearTimeout(transformTimer)
    transformTimer = setTimeout(() => (transformMsg = ''), 4000)
  }

  // URL encode/decode the text the user marked in the request editor.
  async function urlTransform(mode) {
    if (!current || !reqEl) return
    const result = applyUrlTransform(reqEl.value, reqEl.selectionStart, reqEl.selectionEnd, mode)
    if (result.error) return flash(result.error)
    current.raw = result.text
    await tick()
    reqEl.focus()
    reqEl.setSelectionRange(result.start, result.end)
    flash(result.message)
  }

  function findPositions(text, term) {
    if (!term) return []
    const lower = (text || '').toLowerCase()
    const needle = term.toLowerCase()
    const out = []
    let from = 0
    let idx
    while ((idx = lower.indexOf(needle, from)) !== -1) {
      out.push(idx)
      from = idx + Math.max(1, needle.length)
      if (out.length > 10000) break
    }
    return out
  }

  async function selectReq(dir = 0) {
    if (!reqTotal) return
    reqIdx = (reqIdx + dir + reqTotal) % reqTotal
    const pos = reqMatches[reqIdx]
    if (reqEl) {
      reqEl.focus()
      reqEl.setSelectionRange(pos, pos + reqSearch.length)
      const line = reqText.slice(0, pos).split('\n').length - 1
      reqEl.scrollTop = Math.max(0, line * 19 - reqEl.clientHeight / 2)
    }
  }

  async function onReqSearch() {
    reqIdx = 0
    await tick()
    selectReq(0)
  }

  onMount(() => {
    if ($repeaterTabs.length === 0) {
      addRepeaterTab({
        name: 'new',
        raw: 'GET https://example.com/ HTTP/1.1\nAccept: */*\nUser-Agent: eko\n\n',
      })
    }
  })

  async function loadResp(id) {
    try {
      respBody = await fetchBody(id, 'resp')
    } catch {
      respBody = null
    }
  }

  async function send() {
    const t = current
    if (!t || t.busy) return
    t.busy = true
    t.error = ''
    repeaterTabs.update((l) => [...l])
    try {
      const r = await api.repeater(t.raw)
      t.response = r.flow
      t.name = `${r.flow.method} ${r.flow.host}${r.flow.path}`.slice(0, 48)
      respLoadedId = ''
      respBody = null
    } catch (e) {
      t.error = e.message
    } finally {
      t.busy = false
      repeaterTabs.update((l) => [...l])
    }
  }

  function key(e) {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
      e.preventDefault()
      send()
    }
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'f') {
      e.preventDefault()
      reqFindOpen = true
      tick().then(() => document.getElementById('rep-req-find')?.focus())
    }
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'u') {
      e.preventDefault()
      urlTransform(e.shiftKey ? 'decode' : 'encode')
    }
  }

  let reqFindOpen = false

  function newTab() {
    addRepeaterTab({
      name: 'new',
      raw: 'GET https://example.com/ HTTP/1.1\nAccept: */*\nUser-Agent: eko\n\n',
    })
  }

  function closeTab(id) {
    closeRepeaterTab(id)
  }

  function startDrag(e) {
    e.preventDefault()
    dragging = true
    document.body.style.userSelect = 'none'
    document.body.style.cursor = vertical ? 'row-resize' : 'col-resize'
    const move = (ev) => {
      if (!dragging || !splitEl) return
      const r = splitEl.getBoundingClientRect()
      const pct = vertical
        ? ((ev.clientY - r.top) / r.height) * 100
        : ((ev.clientX - r.left) / r.width) * 100
      splitPct = Math.min(85, Math.max(15, pct))
    }
    const up = () => {
      dragging = false
      document.body.style.userSelect = ''
      document.body.style.cursor = ''
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }

  function nudgeSplit(e) {
    if (e.key === 'ArrowLeft' || e.key === 'ArrowUp') {
      splitPct = Math.max(15, splitPct - 2)
      e.preventDefault()
    } else if (e.key === 'ArrowRight' || e.key === 'ArrowDown') {
      splitPct = Math.min(85, splitPct + 2)
      e.preventDefault()
    }
  }
</script>

<div class="rep-tabs">
  {#each $repeaterTabs as t (t.id)}
    <button class="rep-tab" class:active={t.id === $repeaterActive} on:click={() => repeaterActive.set(t.id)}>
      <span class="trunc" style="max-width:220px">{t.name}</span>
      <span class="x" on:click|stopPropagation={() => closeTab(t.id)}>×</span>
    </button>
  {/each}
  <button class="btn small" on:click={newTab} title="New repeater tab">+</button>
  <div class="spacer"></div>
  <button
    class="btn small"
    title={vertical ? 'Side-by-side layout' : 'Top/bottom layout'}
    on:click={() => (vertical = !vertical)}
  >
    {vertical ? 'columns' : 'rows'}
  </button>
  <button class="btn small" title="Reset split (or double-click divider)" on:click={() => (splitPct = 50)}>
    reset split
  </button>
</div>

{#if !current}
  <div class="empty">No repeater tab. Press + to create one.</div>
{:else}
  <div
    class="rep-split"
    class:vertical
    class:dragging
    bind:this={splitEl}
  >
    <div class="rep-req" style="flex: 0 0 {splitPct}%;">
      <div class="toolbar">
        <button class="btn primary" on:click={send} disabled={current.busy}>
          {current.busy ? 'sending…' : 'Send'}
        </button>
        <span class="faint">Ctrl+Enter</span>
        <div class="spacer"></div>
        <button class="btn small" title="Find in request (Ctrl+F)" on:click={() => (reqFindOpen = !reqFindOpen)}>find</button>
        <button class="btn small" title="URL-encode selected text (Ctrl+U)" on:click={() => urlTransform('encode')}>URL enc</button>
        <button class="btn small" title="URL-decode selected text (Ctrl+Shift+U)" on:click={() => urlTransform('decode')}>URL dec</button>
        <button class="btn small" title="Show/hide parsed request headers" on:click={() => (showReqHeaders = !showReqHeaders)}>
          {showReqHeaders ? 'hide headers' : 'headers'}
        </button>
        <span class="faint mono">{transformMsg}</span>
      </div>
      {#if reqFindOpen}
        <div class="find-bar">
          <input
            id="rep-req-find"
            class="find-input"
            placeholder="find in request…"
            bind:value={reqSearch}
            on:input={onReqSearch}
            on:keydown={(e) => {
              if (e.key === 'Enter') { e.preventDefault(); selectReq(e.shiftKey ? -1 : 1) }
              if (e.key === 'Escape') { reqFindOpen = false; reqEl?.focus() }
            }}
          />
          <span class="find-count">{reqTotal ? `${reqIdx + 1}/${reqTotal}` : (reqSearch ? '0/0' : '')}</span>
          <button class="btn small" title="Previous" on:click={() => selectReq(-1)}>↑</button>
          <button class="btn small" title="Next" on:click={() => selectReq(1)}>↓</button>
          <button class="btn small" title="Close find" on:click={() => (reqFindOpen = false)}>×</button>
        </div>
      {/if}
      {#if showReqHeaders}
        <div class="headers req-headers">
          {#if reqHeaders.length}
            <div class="kv">
              {#each reqHeaders as [k, v]}
                <span class="k">{k}</span><span class="v">{v}</span>
              {/each}
            </div>
          {:else}
            <span class="faint">no headers</span>
          {/if}
        </div>
      {/if}
      <textarea
        class="editor"
        bind:this={reqEl}
        bind:value={current.raw}
        on:keydown={key}
        spellcheck="false"
      ></textarea>
      {#if current.error}
        <div class="errorbar">{current.error}</div>
      {/if}
    </div>

    <div
      class="rep-divider"
      on:pointerdown={startDrag}
      on:dblclick={() => (splitPct = 50)}
      on:keydown={nudgeSplit}
      role="separator"
      tabindex="0"
      title="Drag (or arrow keys) to resize; double-click resets"
    ></div>

    <div class="rep-resp" style="flex: 1 1 {100 - splitPct}%;">
      <div class="toolbar">
        {#if resp}
          <span class="status {statusClass(resp.status)}">{resp.status}</span>
          <span class="muted">{resp.proto}</span>
          <span class="faint">{resp.durationMs} ms</span>
          <span class="faint">{fmtBytes(resp.resp?.bodyLen || 0)}</span>
        {:else}
          <span class="faint">Send the request to see the response.</span>
        {/if}
        <div class="spacer"></div>
        {#if resp}
          <input
            class="find-input small"
            placeholder="find in response…"
            bind:value={respSearch}
            title="Search response body (highlights matches)"
          />
          <span class="find-count">{respSearch ? (respMatchCount ? `↕ ${respMatchCount}` : '0') : ''}</span>
          <button class="btn small" title="Previous match" on:click={() => bodyView?.findNext(-1)}>↑</button>
          <button class="btn small" title="Next match" on:click={() => bodyView?.findNext(1)}>↓</button>
          <button class="btn small" on:click={() => (showHeaders = !showHeaders)}>
            {showHeaders ? 'hide headers' : 'headers'}
          </button>
        {/if}
      </div>
      {#if resp?.err}
        <div class="errorbar">{resp.err}</div>
      {/if}
      {#if resp && showHeaders}
        <div class="headers">
          {#if resp.resp?.headers?.length}
            <div class="kv">
              {#each resp.resp.headers as [k, v]}
                <span class="k">{k}</span><span class="v">{v}</span>
              {/each}
            </div>
          {:else}
            <span class="faint">no headers</span>
          {/if}
        </div>
      {/if}
      {#if resp}
        <BodyView body={respBody} id={resp?.id || ''} kind="resp" search={respSearch} bind:matchCount={respMatchCount} bind:this={bodyView} />
      {/if}
    </div>
  </div>
{/if}
