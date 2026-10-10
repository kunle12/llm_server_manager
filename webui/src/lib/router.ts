import { writable } from 'svelte/store'

/** Minimal hash router: routes look like #/fleet, #/machine/{id}, #/machine/{id}/config. */
export const hash = writable(window.location.hash)

window.addEventListener('hashchange', () => hash.set(window.location.hash))

export function navigate(to: string): void {
  window.location.hash = to
}
