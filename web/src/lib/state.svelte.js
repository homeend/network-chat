import { api, dmChannel } from './api.js'
import { setMyNicks } from './markdown.js'

const store = {
  get: (k, d) => {
    try { return JSON.parse(localStorage.getItem('netchat.' + k)) ?? d } catch { return d }
  },
  set: (k, v) => localStorage.setItem('netchat.' + k, JSON.stringify(v)),
}

class AppState {
  machines = $state([])
  channels = $state([])
  directory = $state([])
  identities = $state([])
  asId = $state(store.get('as', ''))
  current = $state(store.get('current', ''))
  pendingPeer = $state(null) // directory entry for a DM without history yet
  messages = $state([])
  hasOlder = $state(false)
  loading = $state(false)
  lastRead = $state(store.get('lastRead', {}))
  connected = $state(false)
  error = $state('')
  files = $state({}) // id -> LocalFile (offered by us or downloaded)
  progress = $state({}) // id -> FileStatus
  uploading = $state(0)
  showSettings = $state(false)
  newlineKey = $state(store.get('newlineKey', 'ctrl')) // 'ctrl' | 'shift': modifier+Enter = new line

  setNewlineKey(k) {
    this.newlineKey = k
    store.set('newlineKey', k)
  }

  me = $derived(
    this.identities.find((i) => i.id === this.asId) ??
      this.identities.find((i) => i.kind === 'human') ??
      this.identities[0],
  )
  localIds = $derived(new Set(this.identities.map((i) => i.id)))
  self = $derived(this.machines.find((m) => m.self))
  boards = $derived(this.channels.filter((c) => c.kind === 'board'))
  dms = $derived(this.channels.filter((c) => c.kind === 'dm'))
  totalUnread = $derived(this.channels.reduce((n, c) => n + this.unread(c), 0))

  chan = $derived.by(() => {
    const id = this.current
    if (!id) return null
    const c = this.channels.find((c) => c.id === id)
    if (c) return c
    if (id.startsWith('dm:') && this.pendingPeer) {
      const p = this.pendingPeer
      return { id, kind: 'dm', name: '@' + p.nick, peer: p, me: this.me, online: p.online, lastSeq: 0 }
    }
    return null
  })

  #refreshTimer = null

  async refresh() {
    try {
      const [machines, channels, directory, identities, files] = await Promise.all([
        api.machines(), api.channels(), api.directory(), api.identities(), api.files(),
      ])
      Object.assign(this, { machines, channels, directory, identities })
      setMyNicks(identities.map((i) => i.nick))
      this.files = Object.fromEntries(files.map((f) => [f.id, f]))
      if (this.current && !this.chan && !this.current.startsWith('dm:')) this.current = ''
      if (!this.current && this.self) this.open('board:' + this.self.instanceId)
    } catch (e) {
      this.connected = false
    }
  }

  // Coalesce bursts of events into one refresh.
  refreshSoon() {
    clearTimeout(this.#refreshTimer)
    this.#refreshTimer = setTimeout(() => this.refresh(), 150)
  }

  connect() {
    const es = new EventSource('/v1/events?ui=1') // ui=1: marks this machine's humans as present
    es.onopen = () => {
      this.connected = true
      this.refresh().then(() => this.current && this.load(this.current))
    }
    es.onerror = () => { this.connected = false }
    es.addEventListener('message', (ev) => this.onMessage(JSON.parse(ev.data).message))
    es.addEventListener('deleted', (ev) => this.onDeleted(JSON.parse(ev.data).message))
    es.addEventListener('identities', () => this.refreshSoon())
    es.addEventListener('file', (ev) => {
      const st = JSON.parse(ev.data).file
      this.progress[st.id] = st
      if (st.state === 'done') this.refreshSoon()
    })
    setInterval(() => this.refresh(), 4000)
  }

  onMessage(m) {
    if (!m) return
    if (m.channel === this.current && !this.messages.some((x) => x.id === m.id)) {
      this.messages.push(m)
      this.messages.sort((a, b) => a.seq - b.seq)
      this.markRead(m.channel, m.seq)
    }
    this.refreshSoon()
  }

  onDeleted(t) {
    if (!t) return
    const i = this.messages.findIndex((x) => x.id === t.id)
    if (i >= 0) this.messages[i] = t
  }

  // Own messages anywhere, and anything on this machine's channel (the server checks too).
  canDelete(m) {
    const self = this.self?.instanceId
    return !!self && !m.deleted && (m.fromMachine === self || m.channel === 'board:' + self)
  }

  async deleteMessage(m) {
    try {
      this.onDeleted(await api.deleteMessage(m.id))
      this.error = ''
    } catch (e) {
      this.error = `Could not remove the message: ${e.message}`
    }
  }

  unread(c) {
    if (c.id === this.current) return 0
    return Math.max(0, (c.lastSeq || 0) - (this.lastRead[c.id] || 0))
  }

  markRead(id, seq) {
    if ((this.lastRead[id] || 0) >= seq) return
    this.lastRead[id] = seq
    store.set('lastRead', this.lastRead)
  }

  setAs(id) {
    this.asId = id
    store.set('as', id)
  }

  open(id, peer = null) {
    const c = this.channels.find((c) => c.id === id)
    if (c?.kind === 'dm' && c.me && this.localIds.has(c.me.id)) this.setAs(c.me.id)
    this.current = id
    this.pendingPeer = peer
    this.error = ''
    store.set('current', id)
    this.load(id)
  }

  openDM(entry) {
    if (!this.me || entry.id === this.me.id) return
    this.open(dmChannel(this.me.id, entry.id), entry)
  }

  async load(id) {
    this.messages = []
    this.loading = true
    try {
      const ms = await api.messages(id)
      if (this.current !== id) return
      this.messages = ms
      this.hasOlder = ms.length >= 200
      if (ms.length) this.markRead(id, ms[ms.length - 1].seq)
    } catch (e) {
      this.error = e.message
    } finally {
      if (this.current === id) this.loading = false
    }
  }

  async loadOlder() {
    const first = this.messages[0]
    if (!first) return
    const ms = await api.messages(this.current, { before: first.seq })
    this.messages = [...ms, ...this.messages]
    this.hasOlder = ms.length >= 200
  }

  async send(text) {
    const c = this.chan
    if (!c || !this.me) return false
    try {
      const m = await api.send({ as: this.me.id, to: c.id, text })
      this.onMessage(m)
      this.error = ''
      return true
    } catch (e) {
      this.error = e.message
      return false
    }
  }

  machineOnline(id) {
    return this.machines.some((m) => m.instanceId === id && m.online)
  }

  async pull(id) {
    this.progress[id] = { id, state: 'downloading', bytes: 0, total: 0 }
    try {
      const f = await api.pull(id)
      this.files[id] = f
      this.progress[id] = { id, state: 'done' }
    } catch (e) {
      this.progress[id] = { id, state: 'error', error: e.message }
    }
  }

  async reveal(id) {
    try { await api.reveal(id) } catch (e) { this.error = e.message }
  }

  // Share files with the current channel; the first one carries the caption.
  async upload(fileList, caption = '') {
    const c = this.chan
    if (!c || !this.me) return false
    let ok = true
    for (const [i, file] of [...fileList].entries()) {
      this.uploading++
      try {
        const m = await api.upload(file, { as: this.me.id, to: c.id, text: i === 0 ? caption : '' })
        this.onMessage(m)
        this.error = ''
      } catch (e) {
        this.error = `${file.name}: ${e.message}`
        ok = false
      } finally {
        this.uploading--
      }
    }
    this.refreshSoon()
    return ok
  }

  async createIdentity(nick, kind) {
    const id = await api.createIdentity(nick, kind)
    await this.refresh()
    this.setAs(id.id)
  }

  async deleteIdentity(id) {
    await api.deleteIdentity(id)
    if (this.asId === id) this.setAs('')
    await this.refresh()
  }

  async addPeer(addr) {
    const info = await api.addPeer(addr)
    await this.refresh()
    return info
  }
}

export const app = new AppState()
