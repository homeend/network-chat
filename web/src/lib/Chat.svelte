<script>
  import { tick } from 'svelte'
  import { app } from './state.svelte.js'
  import { layout, dragHandle } from './layout.svelte.js'
  import { colorFor, dayKey, dayLabel, hhmm, presence, presenceLabel } from './api.js'
  import { renderMarkdown } from './markdown.js'
  import FileCard from './FileCard.svelte'
  import Composer from './Composer.svelte'

  let listEl = $state()
  let composer = $state()
  let stick = true // follow new messages while scrolled to the bottom
  let w0 = 0

  const here = $derived(
    app.chan?.kind === 'board' ? app.directory.filter((d) => d.machineId === app.chan.machineId) : [],
  )
  const subtitle = $derived.by(() => {
    const c = app.chan
    if (!c) return ''
    if (c.kind === 'board') {
      const m = app.machines.find((m) => m.instanceId === c.machineId)
      return m?.self ? 'your machine channel — anyone on the network can post here' : `machine channel on ${m?.addr ?? '?'}`
    }
    return `${c.peer?.kind ?? ''} on ${c.peer?.machineName ?? '?'} · ${presenceLabel(c.peer)} · you are ${c.me?.nick ?? app.me?.nick}`
  })

  // Tell the sender when the other side is not listening (the message still waits in its inbox).
  const dmNotice = $derived.by(() => {
    const c = app.chan
    if (c?.kind !== 'dm' || !c.peer) return ''
    const p = presence(c.peer)
    if (c.peer.online === false) return `${c.peer.nick}'s machine is offline — DMs cannot be delivered until it is back.`
    if (p === 'offline') return `${c.peer.nick} is not connected — your message will wait in its inbox until it checks netchat.`
    if (p === 'idle') return `${c.peer.nick} was ${presenceLabel(c.peer)} — it will see your message when it next checks netchat.`
    return ''
  })

  $effect(() => {
    app.messages.length
    if (stick) tick().then(() => listEl && (listEl.scrollTop = listEl.scrollHeight))
  })
  $effect(() => {
    app.current
    stick = true
  })

  function onScroll() {
    stick = listEl.scrollHeight - listEl.scrollTop - listEl.clientHeight < 60
  }

  // Copy buttons inside rendered code blocks.
  async function onMessagesClick(e) {
    // @mention -> open a DM with that identity (online ones first)
    const men = e.target.closest?.('.mention')
    if (men) {
      const nick = men.dataset.nick.toLowerCase()
      const hits = app.directory.filter((d) => d.nick.toLowerCase() === nick && d.id !== app.me?.id)
      hits.sort((a, b) => Number(b.online) - Number(a.online))
      if (hits[0]) app.openDM(hits[0])
      return
    }
    const btn = e.target.closest?.('[data-copy]')
    if (!btn) return
    const code = btn.closest('.codeblock')?.querySelector('code')?.innerText ?? ''
    try {
      await navigator.clipboard.writeText(code)
      btn.textContent = 'Copied'
      setTimeout(() => (btn.textContent = 'Copy'), 1200)
    } catch {
      btn.textContent = 'Copy failed'
    }
  }

  // ---- files: drag & drop ----
  let dragDepth = $state(0)
  const hasFiles = (e) => [...(e.dataTransfer?.types ?? [])].includes('Files')
  function onDragEnter(e) {
    if (!hasFiles(e)) return
    e.preventDefault()
    dragDepth++
  }
  function onDragOver(e) {
    if (hasFiles(e)) e.preventDefault()
  }
  function onDragLeave(e) {
    if (hasFiles(e)) dragDepth = Math.max(0, dragDepth - 1)
  }
  function onDrop(e) {
    if (!hasFiles(e)) return
    e.preventDefault()
    dragDepth = 0
    composer?.share(e.dataTransfer.files)
  }
</script>

<main class="chat" ondragenter={onDragEnter} ondragover={onDragOver} ondragleave={onDragLeave} ondrop={onDrop}>
  {#if app.chan && dragDepth > 0}
    <div class="dropzone">Drop to share with <b>{app.chan.name}</b></div>
  {/if}

  <header>
    <div class="title">
      {#if app.chan}
        <span class="dot {app.chan.kind === 'dm' ? presence(app.chan.peer) : app.chan.online ? 'on' : ''}"></span>
        <h2>{app.chan.name}</h2>
        {#if app.chan.kind === 'dm' ? presence(app.chan.peer) === 'offline' : !app.chan.online}<span class="tag warn">{app.chan.kind === 'dm' ? 'not connected' : 'offline'}</span>{/if}
      {:else}
        <h2>netchat</h2>
      {/if}
      <button class="gear" title="Settings" aria-label="Settings" onclick={() => (app.showSettings = true)}>⚙</button>
    </div>
    <div class="subtitle">{subtitle}</div>
  </header>

  {#if app.chan}
    <div class="body">
      <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
      <div class="messages" bind:this={listEl} onscroll={onScroll} onclick={onMessagesClick}>
        {#if app.hasOlder}
          <button class="older" onclick={() => app.loadOlder()}>Load older messages</button>
        {/if}
        {#each app.messages as m, i (m.id)}
          {#if i === 0 || dayKey(app.messages[i - 1].ts) !== dayKey(m.ts)}
            <div class="day"><span>{dayLabel(m.ts)}</span></div>
          {/if}
          <div class="msg" class:mine={app.localIds.has(m.from.id)}>
            <span class="time" title={new Date(m.ts).toLocaleString()}>{hhmm(m.ts)}</span>
            <span class="who">
              <span class="nick" style:color={colorFor(m.from.id)} title={m.from.id}
                >{m.from.kind === 'agent' ? '🤖 ' : ''}{m.from.nick}</span
              ><span class="at">@{m.fromMachineName}</span>
            </span>
            <div class="text">
              {#if m.deleted}
                <span class="removed">message removed</span>
              {:else}
                {#if m.intent}<span class="intent {m.intent}">{m.intent}</span>{/if}
                <div class="md">{@html renderMarkdown(m.text)}</div>
                {#each m.files ?? [] as f (f.id)}<FileCard {f} />{/each}
              {/if}
            </div>
            {#if app.canDelete(m)}
              <button class="del" title="Remove this message for everyone" aria-label="Remove message"
                onclick={(e) => { e.stopPropagation(); if (confirm('Remove this message for everyone?')) app.deleteMessage(m) }}>🗑 Remove</button>
            {/if}
          </div>
        {:else}
          <div class="empty">{app.loading ? 'Loading…' : 'No messages yet. Say something.'}</div>
        {/each}
      </div>

      {#if here.length}
        <aside class="here" style:width={layout.here + 'px'}>
          <div
            class="vhandle left"
            title="Drag to resize"
            role="separator"
            aria-orientation="vertical"
            use:dragHandle={{ onStart: () => (w0 = layout.here), onMove: (dx) => { layout.here = w0 - dx; layout.clamp() }, onEnd: () => layout.save() }}
          ></div>
          <h3>On this machine</h3>
          {#each here as p (p.id)}
            <button class="item" onclick={() => app.openDM(p)} disabled={p.id === app.me?.id}
              title={p.id === app.me?.id ? 'you' : `Message ${p.nick}`}>
              <span class="dot {presence(p)}" title={presenceLabel(p)}></span>
              <span class="name" style:color={colorFor(p.id)}>{p.kind === 'agent' ? '🤖 ' : ''}{p.nick}</span>
            </button>
          {/each}
        </aside>
      {/if}
    </div>

    {#if app.error}<div class="error" role="alert">{app.error}<button class="x" onclick={() => (app.error = '')}>×</button></div>{/if}

    {#if dmNotice}<div class="notice">{dmNotice}</div>{/if}
    <Composer bind:this={composer} onsent={() => (stick = true)} />
  {:else}
    <div class="empty center">{app.connected ? 'Pick a machine or a person.' : 'Connecting to the netchat service…'}</div>
  {/if}
</main>
