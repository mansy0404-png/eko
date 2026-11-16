<script>
  import { fmtBytes } from './api.js'

  export let body = null
  export let id = ''
  export let kind = 'resp'

  let pretty = true
  let copied = false

  $: text = body && !body.binary ? body.text : ''
  $: isJSON = /^\s*[\[{]/.test(text)
  $: shown = pretty && isJSON ? tryPretty(text) : text

  function tryPretty(t) {
    try {
      return JSON.stringify(JSON.parse(t), null, 2)
    } catch {
      return t
    }
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
    <pre class="pre">{shown}</pre>
  {/if}
</div>
