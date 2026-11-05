// Thin client for the localhost control API.

async function req(method, path, body) {
  const r = await fetch(path, {
    method,
    headers: body !== undefined ? { 'Content-Type': 'application/json' } : {},
    body: body !== undefined ? JSON.stringify(body) : undefined,
  })
  const data = await r.json().catch(() => null)
  if (!r.ok) throw new Error(data?.error?.message || `HTTP ${r.status}`)
  return data
}

const enc = encodeURIComponent

export const api = {
  info: () => req('GET', '/v1/info'),
  machines: () => req('GET', '/v1/machines'),
  channels: () => req('GET', '/v1/channels'),
  directory: () => req('GET', '/v1/directory'),
  identities: () => req('GET', '/v1/identities'),
  createIdentity: (nick, kind) => req('POST', '/v1/identities', { nick, kind }),
  deleteIdentity: (sel) => req('DELETE', `/v1/identities/${enc(sel)}`),
  messages: (channel, { before = 0, limit = 200 } = {}) =>
    req('GET', `/v1/channels/${enc(channel)}/messages?limit=${limit}${before ? `&before_seq=${before}` : ''}`),
  send: (body) => req('POST', '/v1/send', body),
  deleteMessage: (id) => req('DELETE', `/v1/messages/${enc(id)}`),
  addPeer: (addr) => req('POST', '/v1/peers', { addr }),
  scan: () => req('POST', '/v1/scan'),
  skills: () => req('GET', '/v1/skills'),
  installSkill: (id) => req('POST', `/v1/skills/${enc(id)}`),
  removeSkill: (id) => req('DELETE', `/v1/skills/${enc(id)}`),
  files: () => req('GET', '/v1/files'),
  pull: (id) => req('POST', `/v1/files/${enc(id)}/pull`),
  reveal: (id) => req('POST', `/v1/files/${enc(id)}/reveal`),
  async upload(file, { as, to, text = '' }) {
    const q = new URLSearchParams({ as, to, name: file.name, mime: file.type || '', text })
    const r = await fetch(`/v1/files/upload?${q}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/octet-stream' },
      body: file,
    })
    const data = await r.json().catch(() => null)
    if (!r.ok) throw new Error(data?.error?.message || `HTTP ${r.status}`)
    return data
  },
}

export function humanSize(n) {
  if (n < 1024) return `${n} B`
  const u = ['KB', 'MB', 'GB', 'TB']
  let i = -1
  do { n /= 1024; i++ } while (n >= 1024 && i < u.length - 1)
  return `${n.toFixed(n < 10 ? 1 : 0)} ${u[i]}`
}

export const inlineImage = (mime) => ['image/png', 'image/jpeg', 'image/gif', 'image/webp'].includes(mime)

// Must match protocol.DMChannel in Go.
export function dmChannel(a, b) {
  return a < b ? `dm:${a}:${b}` : `dm:${b}:${a}`
}

export function hhmm(ts) {
  return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

export function dayKey(ts) {
  return new Date(ts).toDateString()
}

export function dayLabel(ts) {
  const d = new Date(ts)
  const today = new Date()
  const y = new Date(Date.now() - 864e5)
  if (d.toDateString() === today.toDateString()) return 'Today'
  if (d.toDateString() === y.toDateString()) return 'Yesterday'
  return d.toLocaleDateString([], { weekday: 'short', year: 'numeric', month: 'short', day: 'numeric' })
}

// Presence of a directory entry: 'active' | 'idle' | 'offline'.
// Older peers send no state: fall back to machine reachability.
export function presence(e) {
  if (!e) return 'offline'
  if (e.state) return e.online === false ? 'offline' : e.state
  return e.online ? 'active' : 'offline'
}

function ago(ts) {
  const s = Math.max(0, (Date.now() - new Date(ts)) / 1000)
  if (s < 90) return 'just now'
  if (s < 3600) return `${Math.round(s / 60)} min ago`
  if (s < 86400) return `${Math.round(s / 3600)} h ago`
  return new Date(ts).toLocaleDateString()
}

export function presenceLabel(e) {
  const p = presence(e)
  const agent = e?.kind === 'agent'
  if (p === 'active') return agent ? 'listening now' : 'here now'
  if (p === 'idle') return `active ${ago(e.lastActive)}`
  if (e?.online === false) return 'machine offline'
  return e?.lastActive ? `not connected · last seen ${ago(e.lastActive)}` : 'not connected'
}

// Stable color per identity id.
export function colorFor(id) {
  let h = 0
  for (const c of id || '') h = (h * 31 + c.charCodeAt(0)) >>> 0
  return `hsl(${h % 360} 55% var(--nick-l))`
}
