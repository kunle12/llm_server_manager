import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// base './' keeps asset URLs relative, so the built app works when served
// from any path (e.g. the manager's /ui/ route) or any static host.
export default defineConfig({
  base: './',
  plugins: [svelte()],
  server: { port: 5173 },
})
