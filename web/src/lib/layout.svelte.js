// Resizable panel sizes, remembered per viewport size.
// Exact viewport match -> restore exactly; otherwise the closest saved
// viewport's sizes (clamped to fit), else defaults.

const KEY = 'netchat.layouts'
const DEFAULTS = { side: 260, here: 190, composer: 0 } // composer 0 = auto height

const viewportKey = () => `${window.innerWidth}x${window.innerHeight}`

function loadAll() {
  try { return JSON.parse(localStorage.getItem(KEY)) || {} } catch { return {} }
}

class Layout {
  side = $state(DEFAULTS.side)
  here = $state(DEFAULTS.here)
  composer = $state(DEFAULTS.composer)

  constructor() {
    this.restore()
    let t
    window.addEventListener('resize', () => {
      clearTimeout(t)
      t = setTimeout(() => this.restore(), 150)
    })
  }

  limits() {
    const w = window.innerWidth, h = window.innerHeight
    return {
      side: [180, Math.max(180, Math.min(600, w * 0.5))],
      here: [140, Math.max(140, Math.min(480, w * 0.4))],
      composer: [36, Math.max(36, h * 0.6)],
    }
  }

  clamp() {
    const l = this.limits()
    this.side = Math.round(Math.min(Math.max(this.side, l.side[0]), l.side[1]))
    this.here = Math.round(Math.min(Math.max(this.here, l.here[0]), l.here[1]))
    if (this.composer) this.composer = Math.round(Math.min(Math.max(this.composer, l.composer[0]), l.composer[1]))
  }

  restore() {
    const all = loadAll()
    let saved = all[viewportKey()]
    if (!saved) {
      const [w, h] = [window.innerWidth, window.innerHeight]
      let best = Infinity
      for (const [k, v] of Object.entries(all)) {
        const [kw, kh] = k.split('x').map(Number)
        const d = Math.abs(kw - w) + Math.abs(kh - h)
        if (d < best) { best = d; saved = v }
      }
    }
    Object.assign(this, { ...DEFAULTS, ...saved })
    this.clamp()
  }

  save() {
    this.clamp()
    const all = loadAll()
    all[viewportKey()] = { side: this.side, here: this.here, composer: this.composer }
    localStorage.setItem(KEY, JSON.stringify(all))
  }

  reset() {
    localStorage.removeItem(KEY)
    Object.assign(this, DEFAULTS)
  }
}

export const layout = new Layout()

// Svelte action: drag handle. onMove(dx, dy) gets the delta since drag start.
export function dragHandle(node, opts) {
  let start = null
  const down = (e) => {
    if (e.button !== 0) return
    e.preventDefault()
    start = { x: e.clientX, y: e.clientY }
    node.setPointerCapture(e.pointerId)
    opts.onStart?.()
    document.body.classList.add('resizing')
  }
  const move = (e) => start && opts.onMove(e.clientX - start.x, e.clientY - start.y)
  const up = () => {
    if (!start) return
    start = null
    document.body.classList.remove('resizing')
    opts.onEnd?.()
  }
  node.addEventListener('pointerdown', down)
  node.addEventListener('pointermove', move)
  node.addEventListener('pointerup', up)
  node.addEventListener('pointercancel', up)
  return {
    update(o) { opts = o },
    destroy() {
      node.removeEventListener('pointerdown', down)
      node.removeEventListener('pointermove', move)
      node.removeEventListener('pointerup', up)
      node.removeEventListener('pointercancel', up)
    },
  }
}
