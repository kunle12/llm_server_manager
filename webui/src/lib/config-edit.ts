/**
 * Format-preserving edits to a config document. Used by the proxy switch so it
 * can flip one key without reformatting the user's whole file.
 */

/**
 * Return `text` with the config's "proxy" key set to `enabled`. The rest of the
 * document is left byte-for-byte intact; when the key is absent it is inserted
 * before the closing brace.
 */
export function setProxyInConfigText(text: string, enabled: boolean): string {
  const parsed: unknown = JSON.parse(text)
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error('config must be a JSON object')
  }

  // Replace an existing value (boolean, string, or small object form).
  const re = /("proxy"\s*:\s*)(true|false|"[^"]*"|\{[^{}]*\})/
  if (re.test(text)) {
    return text.replace(re, `$1${enabled}`)
  }

  // The key is absent: insert it, keeping the existing formatting.
  const close = text.lastIndexOf('}')
  if (close === -1) throw new Error('config must be a JSON object')
  const head = text.slice(0, close).replace(/\s*$/, '')
  const empty = head.trimEnd().endsWith('{')
  return `${head}${empty ? '' : ','}\n  "proxy": ${enabled}\n}${text.slice(close + 1)}`
}
