<script>
  import { onMount } from 'svelte'
  import { api } from './api.js'
  import { layout } from './layout.svelte.js'
  import { app } from './state.svelte.js'

  let { onclose } = $props()

  let skills = $state(null) // { version, content, agents: [...] }
  let busy = $state('')
  let msg = $state('')

  async function load() {
    try {
      skills = await api.skills()
    } catch (e) {
      msg = e.message
    }
  }
  onMount(load)

  async function act(id, fn, label) {
    busy = id
    msg = ''
    try {
      await fn()
      msg = label
    } catch (e) {
      msg = e.message
    } finally {
      busy = ''
      await load()
    }
  }

  const detectedTodo = $derived(skills?.agents.filter((a) => a.detected && a.status !== 'current') ?? [])

  async function installAll() {
    await act('*', async () => {
      for (const a of detectedTodo) await api.installSkill(a.id)
    }, `Installed for ${detectedTodo.map((a) => a.name).join(', ')}`)
  }

  function onKey(e) {
    if (e.key === 'Escape') onclose()
  }
</script>

<svelte:window onkeydown={onKey} />

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="backdrop" onclick={(e) => e.target === e.currentTarget && onclose()}>
  <div class="modal" role="dialog" aria-modal="true" aria-label="Settings">
    <header>
      <h2>Settings</h2>
      <button class="x" onclick={onclose} aria-label="Close">×</button>
    </header>
    <div class="content">
      <h3>Agent skills</h3>
      <p>
        Teach coding agents on this machine to use netchat: a skill file explains how to read the inbox, reply,
        post to machine channels and share files with the <code>netchat</code> CLI. Then just ask your agent
        e.g. <i>“check netchat and answer alice”</i>.
      </p>

      {#if !skills}
        <p>{msg || 'Detecting agents…'}</p>
      {:else}
        <table class="agents">
          <thead>
            <tr><th>Agent</th><th>Status</th><th>Skill location</th><th></th></tr>
          </thead>
          <tbody>
            {#each skills.agents as a (a.id)}
              <tr>
                <td>
                  <b>{a.name}</b>
                  <div class="muted" style="font-size:11px">{a.detected ? 'detected' : 'not found'}{a.note ? ' · ' + a.note : ''}</div>
                </td>
                <td>
                  {#if a.status === 'current'}<span class="pill current">installed v{a.installedVersion}</span>
                  {:else if a.status === 'outdated'}<span class="pill outdated">outdated v{a.installedVersion || '?'} → v{skills.version}</span>
                  {:else if a.status === 'foreign'}<span class="pill" title="A 'netchat' skill not written by netchat exists here; it is left alone">other skill</span>
                  {:else}<span class="pill missing">not installed</span>{/if}
                </td>
                <td class="path">{a.path}</td>
                <td class="acts">
                  {#if a.status === 'outdated'}
                    <button class="btn primary" disabled={!!busy} onclick={() => act(a.id, () => api.installSkill(a.id), `Updated for ${a.name}`)}>Update</button>
                  {:else if a.status === 'missing'}
                    <button class="btn" class:primary={a.detected} disabled={!!busy} onclick={() => act(a.id, () => api.installSkill(a.id), `Installed for ${a.name}`)}
                      title={a.detected ? '' : 'Agent not detected; install anyway'}>Install</button>
                  {/if}
                  {#if a.status === 'current' || a.status === 'outdated'}
                    <button class="btn danger" disabled={!!busy} onclick={() => act(a.id, () => api.removeSkill(a.id), `Removed from ${a.name}`)}>Remove</button>
                  {/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
        <div class="row" style="margin-top:10px">
          <button class="btn primary" disabled={!!busy || !detectedTodo.length} onclick={installAll}>
            {detectedTodo.length ? `Install / update for all detected (${detectedTodo.length})` : 'All detected agents are up to date'}
          </button>
          <button class="btn" disabled={!!busy} onclick={load}>Re-detect</button>
          <span class="muted">{busy ? 'Working…' : msg}</span>
        </div>
        <details class="preview">
          <summary class="muted" style="margin-top:10px;cursor:pointer">Preview the skill (v{skills.version})</summary>
          <pre>{skills.content}</pre>
        </details>
      {/if}

      <h3>Message box</h3>
      <p>Enter sends the message. Which key combination starts a new line?</p>
      <div class="row" role="radiogroup" aria-label="New line key">
        <label><input type="radio" name="nl" checked={app.newlineKey === 'ctrl'} onchange={() => app.setNewlineKey('ctrl')} /> Ctrl+Enter (default)</label>
        <label><input type="radio" name="nl" checked={app.newlineKey === 'shift'} onchange={() => app.setNewlineKey('shift')} /> Shift+Enter</label>
      </div>
      <p style="margin-top:6px">Inside a ``` code block, plain Enter also adds a line.</p>

      <h3>Layout</h3>
      <p>Panel widths and the message box height are remembered per window size. Drag the edges to resize.</p>
      <div class="row"><button class="btn" onclick={() => layout.reset()}>Reset sizes</button></div>

      <h3>This machine</h3>
      <p>{app.self?.name} · {app.self?.instanceId}</p>
    </div>
  </div>
</div>
