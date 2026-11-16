<script>
  import { onMount } from 'svelte'
  import { api, fetchBody, fmtBytes, statusClass } from '../lib/api.js'
  import { addRepeaterTab, closeRepeaterTab, repeaterActive, repeaterTabs } from '../lib/store.js'
  import BodyView from '../lib/BodyView.svelte'

  let respBody = null
  let respLoadedId = ''
  let showHeaders = true
  let splitPct = 50
  let dragging = false
  let splitEl

  $: current = $repeaterTabs.find((t) => t.id === $repeaterActive) || null
  $: if (!current && $repeaterTabs.length > 0) repeaterActive.set($repeaterTabs[0].id)
  $: if (current?.response?.id && current.response.id !== respLoadedId) {
    respLoadedId = current.response.id
    loadResp(current.response.id)
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
  }

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
    const move = (ev) => {
      if (!dragging || !splitEl) return
      const r = splitEl.getBoundingClientRect()
      const pct = ((ev.clientX - r.left) / r.width) * 100
      splitPct = Math.min(80, Math.max(20, pct))
    }
    const up = () => {
      dragging = false
      window.removeEventListener('pointermove', move)
      window.removeEventListener('pointerup', up)
    }
    window.addEventListener('pointermove', move)
    window.addEventListener('pointerup', up)
  }

  $: resp = current?.response || null
</script>

<div class="rep-tabs">
  {#each $repeaterTabs as t (t.id)}
    <button class="rep-tab" class:active={t.id === $repeaterActive} on:click={() => repeaterActive.set(t.id)}>
      <span class="trunc" style="max-width:220px">{t.name}</span>
      <span class="x" on:click|stopPropagation={() => closeTab(t.id)}>×</span>
    </button>
  {/each}
  <button class="btn small" on:click={newTab} title="New repeater tab">+</button>
</div>

{#if !current}
  <div class="empty">No repeater tab. Press + to create one.</div>
{:else}
  <div class="rep-split" class:dragging bind:this={splitEl}>
    <div class="rep-req" style="flex: 0 0 {splitPct}%;">
      <div class="toolbar">
        <button class="btn primary" on:click={send} disabled={current.busy}>
          {current.busy ? 'sending…' : 'Send'}
        </button>
        <span class="faint">Ctrl+Enter</span>
        <div class="spacer"></div>
        <span class="faint mono">raw request</span>
      </div>
      <textarea class="editor" bind:value={current.raw} on:keydown={key} spellcheck="false"></textarea>
      {#if current.error}
        <div class="errorbar">{current.error}</div>
      {/if}
    </div>

    <div class="rep-divider" on:pointerdown={startDrag} title="Drag to resize"></div>

    <div class="rep-resp" style="flex: 1 1 {100 - splitPct}%;">
      {#if !resp}
        <div class="empty">Send the request to see the response.</div>
      {:else}
        <div class="statusline">
          <span class="status {statusClass(resp.status)}">{resp.status}</span>
          <span class="muted">{resp.proto}</span>
          <span class="faint">{resp.durationMs} ms</span>
          <span class="faint">{fmtBytes(resp.resp?.bodyLen || 0)}</span>
          <div class="spacer"></div>
          <button class="btn small" on:click={() => (showHeaders = !showHeaders)}>
            {showHeaders ? 'hide headers' : 'headers'}
          </button>
        </div>
        {#if resp.err}
          <div class="errorbar">{resp.err}</div>
        {/if}
        {#if showHeaders}
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
        <BodyView body={respBody} id={resp?.id || ''} kind="resp" />
      {/if}
    </div>
  </div>
{/if}
