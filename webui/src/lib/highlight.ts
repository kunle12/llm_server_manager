/**
 * Minimal JSON tokenizer for syntax highlighting. Returns escaped HTML with
 * <span class="tok-*"> wrappers, safe to inject with {@html}. No dependencies.
 */

const TOKEN =
  /"(?:\\.|[^"\\])*"(?=\s*:)|"(?:\\.|[^"\\])*"|-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?|\b(?:true|false|null)\b|[{}\[\],:]/g

function escapeHtml(s: string): string {
  return s.replace(/[&<>]/g, (c) => (c === '&' ? '&amp;' : c === '<' ? '&lt;' : '&gt;'))
}

export function highlightJSON(src: string): string {
  let out = ''
  let last = 0

  for (const m of src.matchAll(TOKEN)) {
    const index = m.index ?? 0
    out += escapeHtml(src.slice(last, index))

    const tok = m[0]
    let cls: string
    if (tok.charCodeAt(0) === 34 /* " */) {
      // A string immediately followed by a colon is an object key.
      cls = /^\s*:/.test(src.slice(index + tok.length)) ? 'tok-key' : 'tok-str'
    } else if (/^-?\d/.test(tok)) {
      cls = 'tok-num'
    } else if (tok === 'true' || tok === 'false') {
      cls = 'tok-bool'
    } else if (tok === 'null') {
      cls = 'tok-null'
    } else {
      cls = 'tok-punc'
    }

    out += `<span class="${cls}">${escapeHtml(tok)}</span>`
    last = index + tok.length
  }

  out += escapeHtml(src.slice(last))
  return out
}
