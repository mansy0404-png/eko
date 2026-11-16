<script>
  import { api, fmtTime, statusText } from '../lib/api.js'
  import { interceptState, pending, settings } from '../lib/store.js'

  let selectedId = ''
  let editor = ''
  let loadedId = ''
  let busy = false
  let err = ''

  $: current = $pending.find((p) => p.id === selectedId) || $pending[0] || null
  $: syncEditor(current)

  function syncEditor(c) {
    if (!c) {
      loadedId = ''
      editor = ''
      return
    }
    if (c.id !== loadedId) {
      loadedId = c.id
      editor = rawOf(c)
    }
  }

  function rawOf(p) {
    if (!p) return ''
    const headers = (p.headers || []).map(([k, v]) => `${k}: ${v}`).join('\n')
    if (p.kind === 'request') {
      return `${p.method} ${p.url} HTTP/1.1\n${headers}\n\n${p.body || ''}`
    }
    const st = statusText(p.status)
    return `HTTP/1.1 ${p.status}${st ? ' ' + st : ''}\n${headers}\n\n${p.body || ''}`
  }

  function removeLocal(id) {
    pending.update((l) => l.filter((p) => p.id !== id))
    if (selectedId === id) {
      selectedId = ''
      loadedId = ''
    }
  }

  async function forward() {
    if (!current || busy) return
    busy = true
    err = ''
    try {
      await api.resolve(current.id, 'forward', editor)
      removeLocal(current.id)
    } catch (e) {
      err = e.message
    } finally {
      busy = false
    }
  }

  async function drop() {
    if (!current || busy) return
    busy = true
    err = ''
    try {
      await api.resolve(current.id, 'drop', '')
      removeLocal(current.id)
    } catch (e) {
      err = e.message
    } finally {
      busy = false
    }
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
      err = e.message
    }
  }

  function key(e) {
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
      e.preventDefault()
      forward()
    }
  }

  $: requestOn = $interceptState.request
  $: responseOn = $interceptState.response
</script>

<div class="toolbar">
  <button class="toggle" class:on={requestOn} on:click={() => toggle('request')}>intercept requests</button>
  <button class="toggle" class:on={responseOn} on:click={() => toggle('response')}>intercept responses</button>
  <div class="spacer"></div>
  {#if current}
    <span class="faint mono">{current.kind} · {current.method} · {fmtTime(current.createdAt)}</span>
  {/if}
  {#if err}<span class="err-msg">{err}</span>{/if}
</div>

{#if $pending.length === 0}
  <div class="empty" style="height:100%">
    <div>
      Nothing is held.<br />
      Turn on <strong>intercept requests</strong> and browse; matching traffic will queue here.<br />
      <span class="faint">Ctrl+Enter forwards the edited item.</span>
    </div>
  </div>
{:else}
  <div class="split">
    <div class="pending-list">
      {#each $pending as p (p.id)}
        <button
          class="pending-item"
          class:active={current && p.id === current.id}
          on:click={() => {
            selectedId = p.id
            loadedId = p.id
            editor = rawOf(p)
          }}
        >
          <div class="line1">
            <span class="method {p.method}">{p.kind === 'response' ? '←' : '→'} {p.method}</span>
            <span class="status {p.status ? 'st-' + String(p.status)[0] + 'xx' : 'st-pending'}"
              >{p.status || ''}</span
            >
          </div>
          <div class="trunc muted mono" style="font-size:11px">{p.url}</div>
        </button>
      {/each}
    </div>

    <div class="editor-col">
      <textarea class="editor" bind:value={editor} on:keydown={key} spellcheck="false"></textarea>
      <div class="toolbar" style="border-top:1px solid var(--line);border-bottom:0">
        <button class="btn primary" on:click={forward} disabled={busy}>
          {busy ? 'sending…' : 'Forward'}
        </button>
        <button class="btn danger" on:click={drop} disabled={busy}>Drop</button>
        <div class="spacer"></div>
        <span class="faint">Ctrl+Enter</span>
        <button
          class="btn small"
          disabled={!current}
          on:click={() => {
            if (current) editor = rawOf(current)
          }}>reset</button
        >
      </div>
    </div>
  </div>
{/if}
