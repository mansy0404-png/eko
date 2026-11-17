<script>
  import { api, fetchBody, fmtBytes, fmtTime, statusClass } from '../lib/api.js'
  import { headersText, requestHeaders } from '../lib/editor.js'
  import { addRepeaterTab, liveTick } from '../lib/store.js'
  import BodyView from '../lib/BodyView.svelte'

  export let id
  export let onDeleted = () => {}

  let flow = null
  let tab = 'req'
  let reqBody = null
  let respBody = null
  let err = ''
  let loading = false
  let loadedId = ''
  let seq = 0

  $: if (id !== loadedId) {
    loadedId = id
    load(id)
  }

  $: $liveTick, maybeRefresh()

  function maybeRefresh() {
    if (flow && flow.id && (!flow.resp || flow.status === 0)) load(flow.id)
  }

  async function load(fid) {
    const my = ++seq
    loading = true
    err = ''
    try {
      const f = await api.flow(fid)
      const [rb, sb] = await Promise.all([fetchBody(fid, 'req'), fetchBody(fid, 'resp')])
      if (my !== seq) return
      flow = f
      reqBody = rb
      respBody = sb
    } catch (e) {
      if (my === seq) {
        err = e.message
        flow = null
      }
    } finally {
      if (my === seq) loading = false
    }
  }

  $: reqHeaders = flow ? requestHeaders(flow, !!(reqBody && reqBody.decoded)) : []

  function sendToRepeater() {
    if (!flow) return
    const bodyText = reqBody && !reqBody.binary ? reqBody.text : ''
    addRepeaterTab({
      name: `${flow.method} ${flow.host}${flow.path}`,
      raw: `${flow.method} ${flow.url} HTTP/1.1\n${headersText(reqHeaders)}\n\n${bodyText}`,
    })
  }

  async function remove() {
    if (!flow?.id) return
    const fid = flow.id
    await api.deleteFlow(fid)
    onDeleted(fid)
  }

  async function copyURL() {
    if (flow) await navigator.clipboard.writeText(flow.url)
  }

  function headerCount(m) {
    return (m?.headers || []).length
  }
</script>

<div class="detail">
  {#if err}
    <div class="empty">{err}</div>
  {:else if !flow}
    <div class="empty">{loading ? 'loading…' : 'no request selected'}</div>
  {:else}
    <div class="pane-head">
      <span class="method {flow.method}">{flow.method}</span>
      <span class="status {statusClass(flow.status)}">{flow.status || '…'}</span>
      <span class="url" title={flow.url}>{flow.url}</span>
      <div class="spacer"></div>
      <button class="btn small" on:click={copyURL}>copy url</button>
      <button class="btn small" on:click={sendToRepeater}>→ repeater</button>
      <button class="btn small danger" on:click={remove}>delete</button>
    </div>

    <div class="pane-head" style="min-height:30px;padding:4px 10px">
      <span class="faint mono" style="font-size:11px">
        {fmtTime(flow.startedAt)} · {flow.durationMs} ms · {flow.client}
      </span>
      <div class="spacer"></div>
      {#if flow.repeater}<span class="badge">repeater</span>{/if}
      {#if flow.intercepted}<span class="badge warn">edited</span>{/if}
      {#if flow.err}<span class="badge warn" title={flow.err}>error</span>{/if}
    </div>

    <div class="subtabs">
      <button class:active={tab === 'req'} on:click={() => (tab = 'req')}>
        Request {headerCount(flow.req) ? `(${headerCount(flow.req)})` : ''}
      </button>
      <button class:active={tab === 'resp'} on:click={() => (tab = 'resp')}>
        Response {headerCount(flow.resp) ? `(${headerCount(flow.resp)})` : ''}
      </button>
    </div>

    <div class="pane-body" style="display:flex;flex-direction:column">
      {#if tab === 'req'}
        <div class="headers">
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
        <BodyView body={reqBody} id={flow.id} kind="req" />
      {:else}
        {#if flow.err}
          <div class="headers"><span class="err-msg">{flow.err}</span></div>
        {/if}
        <div class="headers">
          {#if flow.resp?.headers?.length}
            <div class="kv">
              {#each flow.resp.headers as [k, v]}
                <span class="k">{k}</span><span class="v">{v}</span>
              {/each}
            </div>
          {:else}
            <span class="faint">no response yet</span>
          {/if}
        </div>
        <BodyView body={respBody} id={flow.id} kind="resp" />
      {/if}
    </div>
  {/if}
</div>
