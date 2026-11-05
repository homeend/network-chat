<script>
  import { onMount } from 'svelte'
  import { app } from './lib/state.svelte.js'
  import { layout, dragHandle } from './lib/layout.svelte.js'
  import Sidebar from './lib/Sidebar.svelte'
  import Chat from './lib/Chat.svelte'
  import Settings from './lib/Settings.svelte'

  let w0 = 0

  onMount(() => app.connect())

  $effect(() => {
    document.title = (app.totalUnread ? `(${app.totalUnread}) ` : '') + 'netchat' + (app.self ? ` · ${app.self.name}` : '')
  })
</script>

<div class="layout" style:--side="{layout.side}px">
  <Sidebar />
  <Chat />
  <div
    class="vhandle"
    style:left="{layout.side - 3}px"
    title="Drag to resize"
    role="separator"
    aria-orientation="vertical"
    use:dragHandle={{ onStart: () => (w0 = layout.side), onMove: (dx) => { layout.side = w0 + dx; layout.clamp() }, onEnd: () => layout.save() }}
  ></div>
</div>

{#if app.showSettings}
  <Settings onclose={() => (app.showSettings = false)} />
{/if}
