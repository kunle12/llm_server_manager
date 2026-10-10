import { writable } from 'svelte/store'

export interface Toast {
  id: number
  kind: 'ok' | 'err'
  text: string
}

export const toasts = writable<Toast[]>([])

let nextId = 1

export function toast(kind: Toast['kind'], text: string): void {
  const id = nextId++
  toasts.update((t) => [...t, { id, kind, text }])
  setTimeout(() => toasts.update((t) => t.filter((x) => x.id !== id)), 4000)
}
