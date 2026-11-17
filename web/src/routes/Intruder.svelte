<script>
  import { api, fetchBody, fmtBytes, statusClass } from '../lib/api.js'
  import { intruderTemplate } from '../lib/store.js'

  let payloadsText = '1\n2\n3'
  let results = []
  let running = false
  let stop = false
  let selected = null
  let respBody = null
  let showHeaders = true
  let concurrency = 1

  function parsePayloads() {
    return payloadsText.split(/\r?\n/).map((s) => s.trimEnd()).filter((s) => s !== '')
  }

  async function start() {
    const payloads = parsePayloads()
    if (!payloads.length || running) return
    running = true
    stop = false
    results = payloads.map((p) => ({ payload: p, status: 0, length: 0, time: 0, error: '', flow: null }))
    selected = 0
    respBody = null

    const conc = Math.max(1, Math.min(10, Number(concurrency) || 1))
    let idx = 0
    async function worker() {
      while (!stop) {
        const i = idx++
        if (i >= payloads.length) return
        const p = payloads[i]
        const raw = $intruderTemplate.split('§§').join(p)
        const t0 = performance.now()
        try {
          const r = await api.repeater(raw)
          const ms = Math.round(performance.now() - t0)
          results[i] = {
            payload: p,
            status: r.flow?.status || 0,
            length: r.flow?.resp?.bodyLen || 0,
            time: r.flow?.durationMs ?? ms,
            error: r.flow?.err || '',
            flow: r.flow || null,
          }
        } catch (e) {
          results[i] = { payload: p, status: 0, length: 0, time: 0, error: e.message, flow: null }
        }
        results = [...results]
      }
    }
    const workers = Array.from({ length: Math.min(conc, payloads.length) }, () => worker())
    await Promise.all(workers)
    running = false
  }

  function stopRun() {
    stop = true
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

  $: cur = selected != null ? results[selected] : null
  $: resp = cur?.flow || null
</script>

<div class="intruder">
  <div class="intruder-top">
    <div class="intruder-pane">
      <div class="toolbar">
        <span class="faint mono">request template (use §§ as payload marker)</span>
        <div class="spacer"></div>
        <label class="faint">concurrency <input class="mini-num" type="number" min="1" max="10" bind:value={concurrency} disabled={running} /></label>
        {#if running}
          <button class="btn danger" on:click={stopRun}>Stop</button>
        {:else}
          <button class="btn primary" on:click={start} disabled={!parsePayloads().length}>Start attack</button>
        {/if}
      </div>
      <textarea class="editor" bind:value={$intruderTemplate} spellcheck="false"></textarea>
    </div>
    <div class="intruder-pane narrow">
      <div class="toolbar"><span class="faint mono">payloads (one per line)</span></div>
      <textarea class="editor" bind:value={payloadsText} spellcheck="false" placeholder="one payload per line"></textarea>
    </div>
  </div>

  <div class="intruder-bottom">
    <div class="intruder-results">
      <div class="toolbar">
        <span class="faint">results {results.length ? `(${results.filter(r=>r.status||r.error).length}/${results.length})` : ''}</span>
      </div>
      {#if results.length === 0}
        <div class="empty">Run an attack to see results.</div>
      {:else}
        <div class="result-list">
          <div class="result-head grid-row">
            <span>#</span><span>payload</span><span>status</span><span>length</span><span>time</span><span>error</span>
          </div>
          {#each results as r, i (i)}
            <button class="grid-row result-row" class:sel={i === selected} on:click={() => select(i)}>
              <span class="muted">{i + 1}</span>
              <span class="trunc mono" title={r.payload}>{r.payload}</span>
              <span class="status {statusClass(r.status)}">{r.status || (r.error ? 'err' : '…')}</span>
              <span class="muted mono">{r.length ? fmtBytes(r.length) : ''}</span>
              <span class="muted mono">{r.time ? r.time + ' ms' : ''}</span>
              <span class="trunc err-msg" title={r.error}>{r.error || ''}</span>
            </button>
          {/each}
        </div>
      {/if}
    </div>

    <div class="intruder-detail">
      {#if !cur}
        <div class="empty">Select a result.</div>
      {:else if !resp}
        <div class="empty">{cur.error || 'No response.'}</div>
      {:else}
        <div class="statusline">
          <span class="status {statusClass(resp.status)}">{resp.status}</span>
          <span class="muted">{resp.proto}</span>
          <span class="faint">{resp.durationMs} ms</span>
          <span class="faint">{fmtBytes(resp.resp?.bodyLen || 0)}</span>
          <div class="spacer"></div>
          <button class="btn small" on:click={() => (showHeaders = !showHeaders)}>{showHeaders ? 'hide headers' : 'headers'}</button>
        </div>
        {#if resp.err}<div class="errorbar">{resp.err}</div>{/if}
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
        {#if respBody !== null}
          <pre class="body mono" style="flex:1;overflow:auto;margin:0;padding:10px;white-space:pre-wrap;word-break:break-all;font-size:12px">{respBody.text ?? ''}{respBody.binary ? `[binary ${fmtBytes(respBody.size || 0)}]` : ''}</pre>
        {/if}
      {/if}
    </div>
  </div>
</div>
