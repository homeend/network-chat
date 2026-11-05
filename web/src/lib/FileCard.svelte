<script>
  import { app } from './state.svelte.js'
  import { humanSize, inlineImage } from './api.js'

  let { f } = $props()

  const mine = $derived(f.machine === app.self?.instanceId)
  const local = $derived(app.files[f.id] ?? (mine ? f : null)) // our own offers are always local
  const st = $derived(app.progress[f.id])
  const pct = $derived(st?.total ? Math.round((st.bytes / st.total) * 100) : null)
  const url = $derived(`/v1/files/${encodeURIComponent(f.id)}/content`)
  const path = $derived(app.files[f.id]?.path)

  let copied = $state(false)
  async function copyPath() {
    try {
      await navigator.clipboard.writeText(path)
      copied = true
      setTimeout(() => (copied = false), 1500)
    } catch (e) {
      app.error = 'Copy failed: ' + e.message
    }
  }
</script>

<div class="file">
  {#if local && inlineImage(f.mime)}
    <a href={url} target="_blank" rel="noopener"><img src={url} alt={f.name} loading="lazy" /></a>
  {/if}
  <div class="frow">
    <span class="ficon">📄</span>
    <span class="fname" title={f.name}>{f.name}</span>
    <span class="fsize">{humanSize(f.size)}</span>
    {#if local}
      <a class="fbtn" href={url} download={f.name}>Open</a>
      <button class="fbtn" onclick={() => app.reveal(f.id)}>Show in folder</button>
      {#if path}
        <button class="fbtn" onclick={copyPath} title={path}>{copied ? 'Copied' : 'Copy path'}</button>
      {/if}
      {#if mine}<span class="fnote">shared from this machine</span>{/if}
    {:else if st?.state === 'downloading'}
      <span class="fbar"><span style:width={(pct ?? 5) + '%'}></span></span>
      <span class="fnote">{pct === null ? '…' : pct + '%'}</span>
    {:else}
      <button class="fbtn primary" onclick={() => app.pull(f.id)} disabled={!app.machineOnline(f.machine)}
        title={app.machineOnline(f.machine) ? 'Download to your Downloads/netchat folder' : 'The sender is offline'}
        >Download</button>
    {/if}
  </div>
  {#if st?.state === 'error'}<div class="ferr">{st.error}</div>{/if}
</div>
