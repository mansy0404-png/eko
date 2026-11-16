async function req(url, opt = {}) {
  const r = await fetch(url, opt)
  if (!r.ok) throw new Error((await r.text()) || r.statusText)
  const ct = r.headers.get('content-type') || ''
  if (ct.includes('application/json')) return r.json()
  return r.text()
}

export const api = {
  stats: () => req('/api/stats'),
  settings: () => req('/api/settings'),
  saveSettings: (s) =>
    req('/api/settings', {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(s),
    }),
  flows: (params) => req('/api/flows?' + new URLSearchParams(params)),
  flow: (id) => req(`/api/flows/${id}`),
  deleteFlow: (id) => req(`/api/flows/${id}`, { method: 'DELETE' }),
  clearFlows: () => req('/api/flows', { method: 'DELETE' }),
  comment: (id, comment) =>
    req(`/api/flows/${id}/comment`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ comment }),
    }),
  bodyURL: (id, kind, raw = false) => `/api/flows/${id}/body/${kind}${raw ? '?raw=1' : ''}`,
  intercept: () => req('/api/intercept'),
  resolve: (id, action, raw) =>
    req(`/api/intercept/${id}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action, raw }),
    }),
  interceptConfig: (request, response) =>
    req('/api/intercept/config', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ request, response }),
    }),
  repeater: (raw) =>
    req('/api/repeater', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ raw }),
    }),
  importCA: () => req('/api/ca/import', { method: 'POST' }),
  shutdown: () => req('/api/shutdown', { method: 'POST' }),
  projects: () => req('/api/projects'),
  createProject: (name, sw = true) =>
    req('/api/projects', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name, switch: sw }),
    }),
  selectProject: (id) =>
    req('/api/projects/select', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id }),
    }),
  updateProject: (id, body) =>
    req('/api/projects/' + id, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    }),
  deleteProject: (id) => req('/api/projects/' + id, { method: 'DELETE' }),
}

export async function fetchBody(id, kind) {
  const r = await fetch(api.bodyURL(id, kind))
  const ct = r.headers.get('content-type') || ''
  const truncated = r.headers.get('X-Eko-Truncated') === '1'
  const decoded = r.headers.get('X-Eko-Decoded') === '1'
  const encoding = r.headers.get('X-Eko-Encoding') || ''
  if (ct.includes('octet-stream')) {
    return { binary: true, size: Number(r.headers.get('X-Eko-Body-Len') || 0), truncated, decoded, encoding }
  }
  return { binary: false, text: await r.text(), truncated, decoded, encoding }
}

export function statusClass(status) {
  if (!status) return 'st-pending'
  if (status < 300) return 'st-2xx'
  if (status < 400) return 'st-3xx'
  if (status < 500) return 'st-4xx'
  return 'st-5xx'
}

export function fmtBytes(n) {
  if (n == null) return ''
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / (1024 * 1024)).toFixed(1)} MB`
}

export function fmtTime(iso) {
  if (!iso) return ''
  const d = new Date(iso)
  return d.toLocaleTimeString([], { hour12: false })
}

export function statusText(code) {
  const map = {
    200: 'OK', 201: 'Created', 202: 'Accepted', 204: 'No Content',
    301: 'Moved Permanently', 302: 'Found', 303: 'See Other', 304: 'Not Modified',
    307: 'Temporary Redirect', 308: 'Permanent Redirect',
    400: 'Bad Request', 401: 'Unauthorized', 403: 'Forbidden', 404: 'Not Found',
    405: 'Method Not Allowed', 409: 'Conflict', 422: 'Unprocessable Entity',
    429: 'Too Many Requests', 500: 'Internal Server Error', 502: 'Bad Gateway',
    503: 'Service Unavailable', 504: 'Gateway Timeout',
  }
  return map[code] || ''
}
