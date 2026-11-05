// Chat message rendering: GitHub-flavoured Markdown (like Slack/Discord),
// fenced code blocks with syntax highlighting, sanitized output.
import { Marked } from 'marked'
import DOMPurify from 'dompurify'
import hljs from 'highlight.js/lib/core'
import bash from 'highlight.js/lib/languages/bash'
import c from 'highlight.js/lib/languages/c'
import cpp from 'highlight.js/lib/languages/cpp'
import csharp from 'highlight.js/lib/languages/csharp'
import css from 'highlight.js/lib/languages/css'
import diff from 'highlight.js/lib/languages/diff'
import dockerfile from 'highlight.js/lib/languages/dockerfile'
import go from 'highlight.js/lib/languages/go'
import ini from 'highlight.js/lib/languages/ini'
import java from 'highlight.js/lib/languages/java'
import javascript from 'highlight.js/lib/languages/javascript'
import json from 'highlight.js/lib/languages/json'
import kotlin from 'highlight.js/lib/languages/kotlin'
import markdown from 'highlight.js/lib/languages/markdown'
import php from 'highlight.js/lib/languages/php'
import plaintext from 'highlight.js/lib/languages/plaintext'
import powershell from 'highlight.js/lib/languages/powershell'
import python from 'highlight.js/lib/languages/python'
import ruby from 'highlight.js/lib/languages/ruby'
import rust from 'highlight.js/lib/languages/rust'
import shell from 'highlight.js/lib/languages/shell'
import sql from 'highlight.js/lib/languages/sql'
import swift from 'highlight.js/lib/languages/swift'
import typescript from 'highlight.js/lib/languages/typescript'
import xml from 'highlight.js/lib/languages/xml'
import yaml from 'highlight.js/lib/languages/yaml'

const langs = { bash, c, cpp, csharp, css, diff, dockerfile, go, ini, java, javascript, json, kotlin, markdown, php, plaintext, powershell, python, ruby, rust, shell, sql, swift, typescript, xml, yaml }
for (const [name, def] of Object.entries(langs)) hljs.registerLanguage(name, def)
hljs.registerAliases(['toml'], { languageName: 'ini' })
hljs.registerAliases(['html', 'svg'], { languageName: 'xml' })

// Suggestions shown after typing ``` in the composer.
export const LANGUAGE_HINTS = [
  'js', 'ts', 'json', 'bash', 'sh', 'powershell', 'python', 'go', 'rust', 'java', 'kotlin', 'csharp',
  'cpp', 'c', 'sql', 'yaml', 'toml', 'html', 'css', 'xml', 'diff', 'dockerfile', 'markdown', 'php', 'ruby', 'swift', 'text',
]

const esc = (s) => s.replace(/[&<>"']/g, (ch) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[ch])

// Nicks of this machine's identities: their mentions are highlighted strongly.
let myNicks = new Set()
export function setMyNicks(nicks) {
  const next = new Set(nicks.map((n) => n.toLowerCase()))
  if ([...next].join() !== [...myNicks].join()) {
    myNicks = next
    cache.clear()
  }
}

// @nick -> <span class="mention">; not inside words or e-mail addresses.
const mention = {
  name: 'mention',
  level: 'inline',
  start(src) {
    const i = src.search(/@[\p{L}\p{N}_]/u)
    return i < 0 ? undefined : i
  },
  tokenizer(src, tokens) {
    const m = /^@([\p{L}\p{N}_.-]*[\p{L}\p{N}_])/u.exec(src)
    if (!m) return
    const prev = tokens.at(-1)?.raw?.slice(-1) ?? ''
    if (prev && /[\p{L}\p{N}_@.-]/u.test(prev)) return
    return { type: 'mention', raw: m[0], nick: m[1] }
  },
  renderer(t) {
    const me = myNicks.has(t.nick.toLowerCase()) ? ' me' : ''
    return `<span class="mention${me}" data-nick="${esc(t.nick)}">@${esc(t.nick)}</span>`
  },
}

const md = new Marked({
  extensions: [mention],
  gfm: true,
  breaks: true, // chat-style: a newline is a line break
  renderer: {
    code({ text, lang }) {
      const language = (lang || '').trim().split(/\s+/)[0].toLowerCase()
      const known = language && hljs.getLanguage(language)
      const body = known ? hljs.highlight(text, { language, ignoreIllegals: true }).value : esc(text)
      return `<div class="codeblock"><div class="codehead"><span>${esc(language || 'code')}</span><button type="button" class="copy" data-copy>Copy</button></div><pre><code class="hljs">${body}</code></pre></div>`
    },
    html({ text }) {
      return esc(text) // raw HTML is shown as text, never rendered
    },
    image({ href, text }) {
      return `<a href="${esc(href || '')}">${esc(text || href || 'image')}</a>` // no remote images
    },
  },
})

DOMPurify.addHook('afterSanitizeAttributes', (node) => {
  if (node.tagName === 'A') {
    node.setAttribute('target', '_blank')
    node.setAttribute('rel', 'noopener noreferrer')
  }
})

const cache = new Map()

export function renderMarkdown(text) {
  if (!text) return ''
  let html = cache.get(text)
  if (html === undefined) {
    html = DOMPurify.sanitize(md.parse(text), {
      ADD_ATTR: ['target'],
      FORBID_TAGS: ['style', 'img', 'iframe', 'form', 'input', 'video', 'audio'],
    })
    if (cache.size > 2000) cache.clear()
    cache.set(text, html)
  }
  return html
}

// Is the cursor inside an unclosed ``` block? (Enter then inserts a newline.)
export function insideCodeFence(text, cursor) {
  const before = text.slice(0, cursor)
  return ((before.match(/^\s*```/gm) || []).length % 2) === 1
}
