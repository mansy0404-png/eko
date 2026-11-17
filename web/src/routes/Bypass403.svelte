<script>
  import { tick } from 'svelte'
  import { api, fetchBody, fmtBytes, statusClass } from '../lib/api.js'
  import { applyUrlTransform } from '../lib/editor.js'
  import { addRepeaterTab, bypass403Raw, bypass403Run } from '../lib/store.js'
  import { buildBypassVariants } from '../lib/bypass403.js'
  import BodyView from '../lib/BodyView.svelte'

  let results = []
  let running = false
  let selected = 0
  let respBody = null
  let onlyInteresting = false
  let err = ''
  let seenRun = 0
  let rawEl
  let transformMsg = ''
  let transformTimer

  $: hits = results.filter((r) => r.status && r.status !== 403 && !r.error).length
  $: shown = onlyInteresting
    ? results.filter((r) => r.status && r.status !== 403 && !r.error)
    : results
  $: selectedResult = results[selected] || null

  // A right-click in History bumps the run counter after seeding the template.
  $: if ($bypass403Run > 0 && $bypass403Run !== seenRun) {
    seenRun = $bypass403Run
    run()
  }

  async function run() {
    if (running) return
    const variants = buildBypassVariants($bypass403Raw)
    if (!variants.length) {
      err = 'Could not find a valid absolute URL in the request line.'
      return
    }
    err = ''
    running = true
    selected = 0
    respBody = null
    results = variants.map((v, i) => ({
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
      while (running && cursor < variants.length) {
        const i = cursor++
        const start = performance.now()
        try {
          const r = await api.repeater(variants[i].raw)
          const f = r.flow || {}
          results[i] = {
            ...results[i],
            status: f.status || 0,
            length: f.resp?.bodyLen || 0,
            time: Math.round(performance.now() - start),
            error: f.err || '',
            flow: f,
          }
        } catch (e) {
          results[i] = {
            ...results[i],
            error: e.message,
            time: Math.round(performance.now() - start),
          }
        }
        results = [...results]
      }
    }
    await Promise.all(Array.from({ length: 3 }, () => worker()))
    running = false
  }

  function stop() {
    running = false
  }

  function flash(msg) {
    transformMsg = msg
    clearTimeout(transformTimer)
    transformTimer = setTimeout(() => (transformMsg = ''), 4000)
  }

  async function urlTransform(mode) {
    if (!rawEl) return
    const result = applyUrlTransform(rawEl.value, rawEl.selectionStart, rawEl.selectionEnd, mode)
    if (result.error) return flash(result.error)
    $bypass403Raw = result.text
    await tick()
    rawEl.focus()
    rawEl.setSelectionRange(result.start, result.end)
    flash(result.message)
  }

  function key(e) {
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'u') {
      e.preventDefault()
      urlTransform(e.shiftKey ? 'decode' : 'encode')
    }
    if ((e.ctrlKey || e.metaKey) && e.key === 'Enter') {
      e.preventDefault()
      run()
    }
  }

  async function select(i) {
    selected = i
    respBody = null
    const f = results[i]?.flow
    if (f?.id) {
      try {
        respBody = await fetchBody(f.id, 'resp')
      } catch {}
    }
  }

  function toRepeater() {
    if (selectedResult) addRepeaterTab({ name: `403: ${selectedResult.name}`.slice(0, 48), raw: selectedResult.raw })
  }

  async function copyRaw() {
    if (selectedResult) await navigator.clipboard.writeText(selectedResult.raw)
  }
</script>

<div class="bypass-page">
  <div class="bypass-top">
    <div class="toolbar">
      <span class="faint mono">raw request — bypass variants are derived from this</span>
      <div class="spacer"></div>
      <button class="btn small" title="URL-encode selected text (Ctrl+U)" on:click={() => urlTransform('encode')}>URL enc</button>
      <button class="btn small" title="URL-decode selected text (Ctrl+Shift+U)" on:click={() => urlTransform('decode')}>URL dec</button>
      <span class="faint mono">{transformMsg}</span>
      <label class="chk faint" title="Hide 403 / error responses">
        <input type="checkbox" bind:checked={onlyInteresting} /> interesting only ({hits})
      </label>
      {#if running}
        <button class="btn small danger" on:click={stop}>Stop</button>
      {:else}
        <button class="btn small primary" on:click={run}>Run 403 bypass</button>
      {/if}
    </div>
    <textarea class="editor" bind:this={rawEl} bind:value={$bypass403Raw} on:keydown={key} spellcheck="false"></textarea>
    {#if err}<div class="errorbar">{err}</div>{/if}
    {#if running}
      <div class="faint" style="font-size:11px;padding:2px 10px">
        running… {results.filter((r) => r.status || r.error).length}/{results.length}
      </div>
    {/if}
  </div>

  <div class="bypass-body">
    <div class="bypass-list">
      {#each shown as r (r.i)}
        <button
          class="bypass-row"
          class:sel={r.i === selected}
          class:hit={r.status && r.status !== 403 && !r.error}
          on:click={() => select(r.i)}
        >
          <span class="muted mono">#{r.i + 1}</span>
          <span class="bname trunc" title={r.name}>{r.name}</span>
          <span class="bgroup faint">{r.group}</span>
          <span class="status {statusClass(r.status)}">{r.status || (r.error ? 'err' : '…')}</span>
          <span class="muted mono">{r.length ? fmtBytes(r.length) : ''}</span>
        </button>
      {/each}
      {#if results.length === 0}
        <div class="empty">Edit the request above, then press <strong>Run 403 bypass</strong>.</div>
      {/if}
    </div>

    <div class="bypass-detail">
      {#if selectedResult}
        <div class="bypass-detail-head">
          <span class="faint mono trunc">{selectedResult.name}</span>
          <span class="faint">{selectedResult.time} ms</span>
          <div class="spacer"></div>
          <button class="btn small" on:click={toRepeater}>to repeater</button>
          <button class="btn small" on:click={copyRaw}>copy request</button>
        </div>
        <div class="bypass-preview">
          <div class="preview-label">
            <span>Request</span>
            <div class="spacer"></div>
            <span class="faint mono">full raw request</span>
          </div>
          <pre class="mono request-preview">{selectedResult.raw}</pre>
          {#if selectedResult.flow}
            <div class="preview-label"><span>Response headers</span></div>
            <div class="headers resp-headers">
              {#if selectedResult.flow.resp?.headers?.length}
                <div class="kv">
                  {#each selectedResult.flow.resp.headers as [k, v]}
                    <span class="k">{k}</span><span class="v">{v}</span>
                  {/each}
                </div>
              {:else}
                <span class="faint">no response headers</span>
              {/if}
            </div>
            <BodyView body={respBody} id={selectedResult.flow.id || ''} kind="resp" />
          {:else}
            <div class="empty">{selectedResult.error || 'Waiting for response…'}</div>
          {/if}
        </div>
      {:else}
        <div class="empty">Select a variant to inspect it.</div>
      {/if}
    </div>
  </div>
</div>
