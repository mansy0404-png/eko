<script>
  import { api, fmtBytes } from '../lib/api.js'
  import { settings, stats } from '../lib/store.js'

  let form = null
  let msg = ''
  let err = ''
  let importing = false
  let importLog = ''
  let saving = false

  function fromSettings(s) {
    return {
      ...s,
      recordExclude: (s.recordExclude || []).join('\n'),
      tunnelHosts: (s.tunnelHosts || []).join('\n'),
      interceptInclude: (s.interceptInclude || []).join('\n'),
      interceptExclude: (s.interceptExclude || []).join('\n'),
      maxBodyCaptureKB: Math.round((s.maxBodyCapture || 0) / 1024),
      inlineBodyLimitKB: Math.round((s.inlineBodyLimit || 0) / 1024),
    }
  }

  function lines(v) {
    return (v || '')
      .split('\n')
      .map((x) => x.trim())
      .filter(Boolean)
  }

  function toSettings(f) {
    const s = { ...f }
    delete s.maxBodyCaptureKB
    delete s.inlineBodyLimitKB
    s.recordExclude = lines(f.recordExclude)
    s.tunnelHosts = lines(f.tunnelHosts)
    s.interceptInclude = lines(f.interceptInclude)
    s.interceptExclude = lines(f.interceptExclude)
    s.maxBodyCapture = Math.max(64, Number(f.maxBodyCaptureKB) || 4096) * 1024
    s.inlineBodyLimit = Math.max(4, Number(f.inlineBodyLimitKB) || 256) * 1024
    s.maxFlows = Number(f.maxFlows) || 10000
    s.interceptTimeoutSec = Number(f.interceptTimeoutSec) || 120
    return s
  }

  $: if ($settings && !form) form = fromSettings($settings)

  async function save() {
    if (!form) return
    saving = true
    msg = ''
    err = ''
    try {
      const s = await api.saveSettings(toSettings(form))
      settings.set(s)
      form = fromSettings(s)
      msg = 'saved'
      setTimeout(() => (msg = ''), 2000)
    } catch (e) {
      err = e.message
    } finally {
      saving = false
    }
  }

  async function importCA() {
    importing = true
    importLog = ''
    try {
      const r = await api.importCA()
      if (r.error) {
        importLog = r.error
      } else if (!r.results?.length) {
        importLog = 'No NSS certificate databases found.'
      } else {
        importLog = r.results.map((x) => `${x.ok ? '✓' : '✗'} ${x.db}\n${x.output}`).join('\n\n')
      }
    } catch (e) {
      importLog = e.message
    } finally {
      importing = false
    }
  }

  async function clearHistory() {
    if (!confirm('Delete all captured flows?')) return
    try {
      await api.clearFlows()
      msg = 'history cleared'
    } catch (e) {
      err = e.message
    }
  }

  $: store = $stats?.store
</script>

<div class="settings">
  <div class="settings-inner">
    {#if !form}
      <div class="empty">loading settings…</div>
    {:else}
      {#if msg}<div class="ok-msg">{msg}</div>{/if}
      {#if err}<div class="err-msg">{err}</div>{/if}

      <div class="card">
        <h2>Network</h2>
        <div class="form-grid">
          <div class="field">
            <label>Proxy listen address</label>
            <input bind:value={form.proxyAddr} spellcheck="false" />
            <span class="hint">Point FoxyProxy here (HTTP proxy). Takes effect after restart.</span>
          </div>
          <div class="field">
            <label>Control UI address</label>
            <input bind:value={form.apiAddr} spellcheck="false" />
            <span class="hint">Loopback only. Takes effect after restart.</span>
          </div>
          <div class="field">
            <label class="chk">
              <input type="checkbox" bind:checked={form.recordEnabled} /> record traffic to history
            </label>
            <span class="hint">Turning this off still allows intercept and repeater.</span>
          </div>
          <div class="field">
            <label class="chk">
              <input type="checkbox" bind:checked={form.stripZstd} /> strip zstd from Accept-Encoding
            </label>
            <span class="hint">Keeps response bodies viewable without extra decoders.</span>
          </div>
        </div>
      </div>

      <div class="card">
        <h2>Capture and storage</h2>
        <div class="form-grid">
          <div class="field">
            <label>Max captured body (KB)</label>
            <input type="number" min="64" bind:value={form.maxBodyCaptureKB} />
            <span class="hint">Bigger bodies stream through but are recorded truncated.</span>
          </div>
          <div class="field">
            <label>Inline body limit (KB)</label>
            <input type="number" min="4" bind:value={form.inlineBodyLimitKB} />
            <span class="hint">Bodies above this spill to disk instead of SQLite rows.</span>
          </div>
          <div class="field">
            <label>Max stored flows</label>
            <input type="number" min="100" bind:value={form.maxFlows} />
            <span class="hint">Oldest flows are pruned past this count.</span>
          </div>
          <div class="field">
            <label class="chk">
              <input type="checkbox" bind:checked={form.upstreamInsecure} /> skip upstream TLS verification
            </label>
            <span class="hint">Only for broken/staging targets.</span>
          </div>
          <div class="field wide">
            <label>Do not record URL patterns</label>
            <textarea bind:value={form.recordExclude} spellcheck="false" placeholder="*.static.example.com/*"></textarea>
            <span class="hint">One glob per line. * and ? wildcards are supported.</span>
          </div>
        </div>
      </div>

      <div class="card">
        <h2>Intercept</h2>
        <div class="form-grid">
          <div class="field">
            <label class="chk">
              <input type="checkbox" bind:checked={form.interceptRequest} /> intercept requests
            </label>
          </div>
          <div class="field">
            <label class="chk">
              <input type="checkbox" bind:checked={form.interceptResponse} /> intercept responses
            </label>
          </div>
          <div class="field">
            <label>Auto-forward after (seconds)</label>
            <input type="number" min="5" bind:value={form.interceptTimeoutSec} />
            <span class="hint">A forgotten pause cannot wedge the browser forever.</span>
          </div>
          <div class="field">
            <label>Passthrough hosts (no TLS MITM)</label>
            <textarea bind:value={form.tunnelHosts} spellcheck="false" placeholder="pinned.example.com:443"></textarea>
            <span class="hint">For certificate-pinned hosts. TLS stays opaque.</span>
          </div>
          <div class="field">
            <label>Intercept only these patterns</label>
            <textarea bind:value={form.interceptInclude} spellcheck="false" placeholder="*.target.com/*"></textarea>
            <span class="hint">Empty means everything is eligible.</span>
          </div>
          <div class="field">
            <label>Never intercept these patterns</label>
            <textarea bind:value={form.interceptExclude} spellcheck="false" placeholder="*.cdn.com/*"></textarea>
          </div>
        </div>
      </div>

      <div class="row-actions">
        <button class="btn primary" on:click={save} disabled={saving}>{saving ? 'saving…' : 'save settings'}</button>
        <span class="faint">Settings are written to ~/.config/eko/settings.json</span>
      </div>

      <div class="card">
        <h2>HTTPS certificate</h2>
        <div class="note">
          Eko terminates TLS with its own local CA. Browsers only accept it once the root certificate is
          trusted. Firefox keeps its own trust store, so installing it system-wide is not enough.
        </div>
        <div class="row-actions">
          <a class="btn" href="/api/ca.pem" download>download root CA</a>
          <button class="btn" on:click={importCA} disabled={importing}>
            {importing ? 'importing…' : 'install into Firefox / Chromium (NSS)'}
          </button>
        </div>
        {#if importLog}<div class="import-results">{importLog}</div>{/if}
        <div class="note" style="margin-top:10px">
          After installing, <strong>fully quit and reopen the browser</strong> (a reload is not enough).
          If you prefer manually:
          <code>certutil -d sql:$HOME/.mozilla/firefox/PROFILE -A -t "C,," -n "Eko Root CA" -i ~/.config/eko/ca.pem</code>.
          Apps that pin certificates will reject the proxy — add those hosts to passthrough.
        </div>
        <div class="note" style="margin-top:8px">
          Keep <code>~/.config/eko/ca.pem</code> in place. If it is moved or deleted, Eko creates a new
          root CA and every browser must import it again. Use <strong>download root CA</strong> instead of
          moving the file.
        </div>
      </div>

      <div class="card">
        <h2>Storage</h2>
        <div class="note">
          {store ? `${store.flows} flows stored (limit ${store.maxFlows}), ${fmtBytes(store.dbSize + store.blobSize)} on disk.` : ''}
          <br />
          Data directory: <code>{$stats?.dataDir || ''}</code>
        </div>
        <div class="row-actions">
          <button class="btn danger" on:click={clearHistory}>clear captured history</button>
        </div>
      </div>

      <div class="card">
        <h2>FoxyProxy quick setup</h2>
        <div class="note">
          Add a proxy in FoxyProxy: type <strong>HTTP</strong>, host <code>127.0.0.1</code>, port
          <code>{($stats?.proxyAddr || '127.0.0.1:8080').split(':').pop()}</code>. Use
          <em>proxy by patterns</em> so only in-scope hosts reach Eko. Keep the control UI in a separate
          window.
        </div>
      </div>
    {/if}
  </div>
</div>
