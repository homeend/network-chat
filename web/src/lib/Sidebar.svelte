<script>
  import { app } from './state.svelte.js'
  import { colorFor, presence, presenceLabel } from './api.js'

  let newNick = $state('')
  let newAgent = $state(false)
  let showNew = $state(false)
  let peerAddr = $state('')
  let peerMsg = $state('')
  let busy = $state(false)

  const rank = { active: 0, idle: 1, offline: 2 }
  const people = $derived(
    app.directory
      .filter((d) => d.id !== app.me?.id)
      .sort((a, b) => rank[presence(a)] - rank[presence(b)] || a.nick.localeCompare(b.nick)),
  )
  const connected = $derived(people.filter((p) => presence(p) !== 'offline'))
  const disconnected = $derived(people.filter((p) => presence(p) === 'offline'))
  let showOffline = $state(localStorage.getItem('netchat.showOffline') === '1')
  function toggleOffline() {
    showOffline = !showOffline
    localStorage.setItem('netchat.showOffline', showOffline ? '1' : '0')
  }

  async function createId(e) {
    e.preventDefault()
    try {
      await app.createIdentity(newNick.trim(), newAgent ? 'agent' : 'human')
      newNick = ''
      newAgent = false
      showNew = false
    } catch (err) {
      app.error = err.message
    }
  }

  async function removeId(i) {
    if (!confirm(`Delete identity "${i.nick}"? Others will no longer be able to reach it.`)) return
    try { await app.deleteIdentity(i.id) } catch (err) { app.error = err.message }
  }

  async function addPeer(e) {
    e.preventDefault()
    if (!peerAddr.trim()) return
    busy = true
    peerMsg = ''
    try {
      const info = await app.addPeer(peerAddr.trim())
      peerMsg = `Added ${info.machine}`
      peerAddr = ''
    } catch (err) {
      peerMsg = err.message
    } finally {
      busy = false
    }
  }
</script>

<aside class="sidebar">
  <header>
    <div class="brand">netchat <span class="dot" class:on={app.connected} title={app.connected ? 'connected' : 'service not reachable'}></span></div>
    <div class="machine">{app.self?.name ?? '…'}</div>
  </header>

  <section>
    <h3>You are <button class="link" onclick={() => (showNew = !showNew)} title="New identity">＋</button></h3>
    <div class="ids">
      {#each app.identities as i (i.id)}
        <span class="chip" class:active={i.id === app.me?.id}>
          <button class="chipbtn" onclick={() => app.setAs(i.id)} title={i.id}>
            {i.kind === 'agent' ? '🤖 ' : ''}{i.nick}
          </button>
          {#if app.identities.length > 1}
            <button class="x" onclick={() => removeId(i)} title="Delete identity">×</button>
          {/if}
        </span>
      {/each}
    </div>
    {#if showNew}
      <form class="inline" onsubmit={createId}>
        <input placeholder="nick" bind:value={newNick} maxlength="32" required />
        <label title="Agent identity"><input type="checkbox" bind:checked={newAgent} /> agent</label>
        <button>Add</button>
      </form>
    {/if}
  </section>

  <nav>
    <h3>Machines</h3>
    {#each app.boards as c (c.id)}
      <button class="item" class:sel={c.id === app.current} onclick={() => app.open(c.id)}>
        <span class="dot" class:on={c.online}></span>
        <span class="name">{c.name}</span>
        {#if c.machineId === app.self?.instanceId}<span class="tag">you</span>{/if}
        {#if app.unread(c)}<span class="badge">{app.unread(c)}</span>{/if}
      </button>
    {/each}
    <form class="inline" onsubmit={addPeer}>
      <input placeholder="add machine: ip[:port]" bind:value={peerAddr} disabled={busy} />
      <button disabled={busy}>{busy ? '…' : 'Add'}</button>
    </form>
    {#if peerMsg}<div class="hint">{peerMsg}</div>{/if}

    {#if app.dms.length}
      <h3>Conversations</h3>
      {#each app.dms as c (c.id)}
        <button class="item" class:sel={c.id === app.current} class:offline={presence(c.peer) === 'offline'}
          onclick={() => app.open(c.id)} title={presenceLabel(c.peer)}>
          <span class="dot {presence(c.peer)}"></span>
          <span class="name" style:color={colorFor(c.peer?.id)}>{c.peer?.kind === 'agent' ? '🤖 ' : ''}{c.peer?.nick}</span>
          {#if c.me && c.me.id !== app.me?.id}<span class="tag">as {c.me.nick}</span>{/if}
          {#if app.unread(c)}<span class="badge">{app.unread(c)}</span>{/if}
        </button>
      {/each}
    {/if}

    <h3>People &amp; agents</h3>
    {#snippet person(p)}
      <button class="item" class:offline={presence(p) === 'offline'} onclick={() => app.openDM(p)}
        title="{p.nick}@{p.machineName} · {presenceLabel(p)}">
        <span class="dot {presence(p)}"></span>
        <span class="name" style:color={colorFor(p.id)}>{p.kind === 'agent' ? '🤖 ' : ''}{p.nick}</span>
        <span class="sub">@{p.machineName}</span>
      </button>
    {/snippet}
    {#each connected as p (p.id + p.machineId)}
      {@render person(p)}
    {:else}
      <div class="hint">{people.length ? 'Nobody else is connected right now.' : 'Nobody else yet. Start netchat on another machine in this network.'}</div>
    {/each}
    {#if disconnected.length}
      <button class="togglelink" onclick={toggleOffline}>{showOffline ? '▾' : '▸'} Not connected ({disconnected.length})</button>
      {#if showOffline}
        {#each disconnected as p (p.id + p.machineId)}
          {@render person(p)}
        {/each}
      {/if}
    {/if}
  </nav>
</aside>
