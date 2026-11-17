<script>
  import { tick } from 'svelte'
  import { fmtBytes } from './api.js'

  export let body = null
  export let id = ''
  export let kind = 'resp'
  export let search = ''
  export let matchCount = 0

  let pretty = true
  let copied = false
  let currentMatch = 0
  let lastSearch = search

  $: text = body && !body.binary ? body.text : ''
  $: isJSON = /^\s*[\[{]/.test(text)
  $: shown = pretty && isJSON ? tryPretty(text) : text
  $: if (search !== lastSearch) {
    lastSearch = search
    currentMatch = 0
  }
  $: marked = markText(shown, search, currentMatch)

  function tryPretty(t) {
    try {
      return JSON.stringify(JSON.parse(t), null, 2)
    } catch {
      return t
    }
  }

  function escapeHtml(s) {
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;')
  }

  function markText(content, term, cur) {
    const value = content || ''
    if (!term) {
      matchCount = 0
      return escapeHtml(value)
    }
    const needle = term.toLowerCase()
    const lower = value.toLowerCase()
    let out = ''
    let from = 0
    let n = 0
    let idx
    while ((idx = lower.indexOf(needle, from)) !== -1) {
      out += escapeHtml(value.slice(from, idx))
      out += `<mark class="find-hit${n === cur ? ' current' : ''}" data-find="${n}">${escapeHtml(
        value.slice(idx, idx + term.length),
      )}</mark>`
      from = idx + term.length
      n++
      if (n > 10000) break
    }
    out += escapeHtml(value.slice(from))
    matchCount = n
    return out
  }

  async function scrollToCurrent() {
    await tick()
    const el = document.querySelector(`.bodyview mark[data-find="${currentMatch}"]`)
    if (el && typeof el.scrollIntoView === 'function') el.scrollIntoView({ block: 'center' })
  }

  export function findNext(dir = 1) {
    if (!matchCount) return
    currentMatch = (currentMatch + dir + matchCount) % matchCount
    marked = markText(shown, search, currentMatch)
    scrollToCurrent()
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(text)
      copied = true
      setTimeout(() => (copied = false), 1200)
    } catch {}
  }

  $: size = body ? (body.binary ? body.size : text.length) : 0
</script>

<div class="bodyview">
  <div class="bodybar">
    <span>{body ? fmtBytes(size) : '…'}</span>
    {#if body?.truncated}<span class="badge warn" title="Body larger than the capture limit">truncated</span>{/if}
    {#if body?.decoded}<span class="badge ok">{body.encoding} decoded</span>{/if}
    {#if !body?.binary && isJSON}
      <label class="chk"><input type="checkbox" bind:checked={pretty} /> pretty</label>
    {/if}
    <div class="spacer"></div>
    {#if text}
      <button class="btn small" on:click={copy}>{copied ? 'copied' : 'copy'}</button>
    {/if}
    {#if id}
      <a class="btn small" href={`/api/flows/${id}/body/${kind}?raw=1`} download>download</a>
    {/if}
  </div>

  {#if !body}
    <div class="empty">no body</div>
  {:else if body.binary}
    <div class="empty">binary body — {fmtBytes(body.size)}</div>
  {:else if text === ''}
    <div class="empty">empty body</div>
  {:else}
    <pre class="pre">{@html marked}</pre>
  {/if}
</div>
