// Shared helpers for the raw request editors (Repeater, 403 Bypass, History).

// Headers recorded for a flow, ready to show or to seed a raw request.
// Framing headers are dropped because the body shown in the editor may be
// decoded; Host is added back because Go keeps it outside http.Header.
export function requestHeaders(flow, decoded = false) {
  const out = []
  let hasHost = false
  for (const [k, v] of flow?.req?.headers || []) {
    const lk = (k || '').toLowerCase()
    if (lk === 'host') hasHost = true
    if (lk === 'content-length' || lk === 'transfer-encoding') continue
    if (lk === 'content-encoding' && decoded) continue
    out.push([k, v])
  }
  if (!hasHost && flow?.host) {
    const port = flow.port || ''
    const isDefault = !port || port === (flow.scheme === 'https' ? '443' : '80')
    out.unshift(['Host', isDefault ? flow.host : `${flow.host}:${port}`])
  }
  return out
}

export function headersText(headers) {
  return headers.map(([k, v]) => `${k}: ${v}`).join('\n')
}

// Parse the header block out of a raw request editor value.
export function parseRawHeaders(raw) {
  const text = typeof raw === 'string' ? raw : ''
  const end = text.indexOf('\n\n')
  const head = end >= 0 ? text.slice(0, end) : text
  const out = []
  for (const line of head.split(/\r?\n/).slice(1)) {
    const i = line.indexOf(':')
    if (i > 0) out.push([line.slice(0, i).trim(), line.slice(i + 1).trim()])
  }
  return out
}

// Apply URL encode/decode to the selected slice of a textarea.
// Returns the replacement text plus the new selection so callers can restore
// the highlight after Svelte updates the bound value.
export function applyUrlTransform(text, start, end, mode) {
  if (start == null || end == null || start === end) {
    return { error: 'Select text in the request first' }
  }
  const selected = text.slice(start, end)
  let replacement
  try {
    replacement = mode === 'encode' ? encodeURIComponent(selected) : decodeURIComponent(selected)
  } catch {
    return { error: 'Invalid percent-encoding in selection' }
  }
  const preview = selected.length > 18 ? `${selected.slice(0, 18)}…` : selected
  return {
    text: text.slice(0, start) + replacement + text.slice(end),
    start,
    end: start + replacement.length,
    message: `${mode === 'encode' ? 'Encoded' : 'Decoded'} “${preview}”`,
  }
}
