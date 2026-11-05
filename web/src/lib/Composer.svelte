<script>
  import { tick } from 'svelte'
  import { app } from './state.svelte.js'
  import { layout, dragHandle } from './layout.svelte.js'
  import { LANGUAGE_HINTS, insideCodeFence } from './markdown.js'
  import { presence } from './api.js'

  let { onsent } = $props()

  let draft = $state('')
  let ta = $state()
  let picker = $state()
  let h0 = 0

  // Popup hints: ```lang (code fence language) and @nick (mention).
  let hint = $state(null) // { kind: 'lang'|'mention', start, items, sel }

  async function submit() {
    const text = draft.trim()
    if (!text) return
    onsent?.()
    if (await app.send(text)) draft = ''
  }

  export async function share(files) {
    if (!files?.length) return
    onsent?.()
    if (await app.upload(files, draft.trim())) draft = ''
  }

  function mentionCandidates(q) {
    q = q.toLowerCase()
    return app.directory
      .filter((d) => d.id !== app.me?.id)
      .map((d) => ({ d, i: d.nick.toLowerCase().indexOf(q) }))
      .filter((x) => x.i >= 0)
      .sort((a, b) => (a.i === 0 ? 0 : 1) - (b.i === 0 ? 0 : 1) || Number(b.d.online) - Number(a.d.online) || a.d.nick.localeCompare(b.d.nick))
      .map((x) => x.d)
      .slice(0, 8)
  }

  function updateHint() {
    const pos = ta.selectionStart
    if (pos !== ta.selectionEnd) return (hint = null)
    const lineStart = draft.lastIndexOf('\n', pos - 1) + 1
    const line = draft.slice(lineStart, pos)
    const inFence = insideCodeFence(draft, lineStart)

    // ``` + language, only on an opening fence
    const f = /^(\s*```)([\w+#.-]*)$/.exec(line)
    if (f && !inFence) {
      const prefix = f[2].toLowerCase()
      const items = LANGUAGE_HINTS.filter((l) => l.startsWith(prefix) && l !== prefix).slice(0, 8)
      return (hint = items.length ? { kind: 'lang', start: lineStart + f[1].length, items, sel: 0 } : null)
    }
    // @nick, at a word start, not inside code
    const m = /(^|[^\p{L}\p{N}_@.-])@([\p{L}\p{N}_.-]{0,32})$/u.exec(line)
    if (m && !inFence && (line.split('`').length - 1) % 2 === 0) {
      const items = mentionCandidates(m[2])
      const keep = hint?.kind === 'mention' ? Math.min(hint.sel, items.length - 1) : 0
      return (hint = items.length ? { kind: 'mention', start: pos - m[2].length - 1, items, sel: Math.max(0, keep) } : null)
    }
    hint = null
  }

  async function accept(item) {
    const pos = ta.selectionStart
    let before, after
    if (hint.kind === 'mention') {
      before = draft.slice(0, hint.start) + '@' + item.nick + ' '
      after = draft.slice(pos).replace(/^[\p{L}\p{N}_.-]+/u, '') // replace the rest of a partly typed nick
    } else {
      before = draft.slice(0, hint.start) + item + '\n'
      const rest = draft.slice(pos)
      after = (/^\s*\n?```/.test(rest) ? '' : '\n```') + rest
    }
    draft = before + after
    hint = null
    await tick()
    ta.focus()
    ta.setSelectionRange(before.length, before.length)
  }

  function onKey(e) {
    if (hint) {
      if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
        e.preventDefault()
        const n = hint.items.length
        hint.sel = (hint.sel + (e.key === 'ArrowDown' ? 1 : n - 1)) % n
        return
      }
      if (e.key === 'Tab' || (e.key === 'Enter' && !e.shiftKey && !e.ctrlKey && !e.metaKey)) {
        e.preventDefault()
        accept(hint.items[hint.sel])
        return
      }
      if (e.key === 'Escape') {
        hint = null
        return
      }
    }
    if (e.key !== 'Enter' || e.isComposing) return
    // New line: the configured modifier (Ctrl/Cmd+Enter by default, or Shift+Enter),
    // or plain Enter inside an unclosed ``` block (like Slack). Otherwise Enter sends.
    const ctrl = e.ctrlKey || e.metaKey
    const newline = app.newlineKey === 'shift' ? e.shiftKey && !ctrl : ctrl && !e.shiftKey
    e.preventDefault()
    if (newline || (!ctrl && !e.shiftKey && insideCodeFence(draft, ta.selectionStart))) {
      insertAtCursor('\n')
    } else {
      submit()
    }
  }

  function insertAtCursor(text) {
    const { selectionStart: s, selectionEnd: en } = ta
    draft = draft.slice(0, s) + text + draft.slice(en)
    tick().then(() => {
      ta.setSelectionRange(s + text.length, s + text.length)
      updateHint()
    })
  }

  const newlineLabel = $derived(app.newlineKey === 'shift' ? 'Shift+Enter' : 'Ctrl+Enter')

  function onPaste(e) {
    const files = e.clipboardData?.files
    if (files?.length) {
      e.preventDefault()
      share(files)
    }
  }

  function wrap(left, right = left) {
    const { selectionStart: s, selectionEnd: en } = ta
    draft = draft.slice(0, s) + left + draft.slice(s, en) + right + draft.slice(en)
    tick().then(() => {
      ta.focus()
      ta.setSelectionRange(s + left.length, en + left.length)
    })
  }
</script>

<div class="composer-wrap">
  <div
    class="hhandle"
    title="Drag to resize · double-click to reset"
    use:dragHandle={{
      onStart: () => (h0 = ta.offsetHeight),
      onMove: (_, dy) => { layout.composer = h0 - dy; layout.clamp() },
      onEnd: () => layout.save(),
    }}
    ondblclick={() => { layout.composer = 0; layout.save() }}
    role="separator"
    aria-orientation="horizontal"
  ></div>

  {#if hint}
    <div class="hints" class:mentions={hint.kind === 'mention'} role="listbox">
      {#each hint.items as it, i (hint.kind === 'mention' ? it.id + it.machineId : it)}
        <button type="button" role="option" aria-selected={i === hint.sel} class:sel={i === hint.sel}
          onmousedown={(e) => { e.preventDefault(); accept(it) }}>
          {#if hint.kind === 'mention'}
            <span class="dot {presence(it)}"></span>
            <span class="hn">{it.kind === 'agent' ? '🤖 ' : ''}{it.nick}</span>
            <span class="hm">@{it.machineName}</span>
          {:else}{it}{/if}
        </button>
      {/each}
      <span class="hk">↑↓ · Tab ↹</span>
    </div>
  {/if}
  <form class="composer" onsubmit={(e) => { e.preventDefault(); submit() }}>
    <span class="as" title="Sending as">{app.me?.nick}</span>
    <button type="button" class="attach" title="Share files (or drag & drop / paste)" onclick={() => picker.click()}
      disabled={app.uploading > 0}>{app.uploading ? '…' : '📎'}</button>
    <input type="file" multiple hidden bind:this={picker} onchange={(e) => { share(e.target.files); e.target.value = '' }} />
    <textarea
      bind:this={ta}
      bind:value={draft}
      onkeydown={onKey}
      oninput={updateHint}
      onclick={updateHint}
      onblur={() => (hint = null)}
      onpaste={onPaste}
      rows="1"
      class:fixed={layout.composer > 0}
      style:height={layout.composer > 0 ? layout.composer + 'px' : null}
      placeholder={`Message ${app.chan?.name ?? ''}`}
    ></textarea>
    <button disabled={!draft.trim()}>Send</button>
  </form>
  <div class="fmt">
    <button type="button" onclick={() => wrap('**')} title="Bold"><b>B</b></button>
    <button type="button" onclick={() => wrap('_')} title="Italic"><i>I</i></button>
    <button type="button" onclick={() => wrap('~~')} title="Strikethrough"><s>S</s></button>
    <button type="button" onclick={() => wrap('`')} title="Inline code"><code>`code`</code></button>
    <button type="button" onclick={() => wrap('```\n', '\n```')} title="Code block (type ``` plus a language for highlighting)"><code>```block```</code></button>
    <span class="tip">Markdown · ```js for highlighting · Enter send · {newlineLabel} new line · drop files to share</span>
  </div>
</div>
