import { mount } from 'svelte'
import { get } from 'svelte/store'
import './app.css'
import App from './App.svelte'
import { settings } from './lib/store'

// Apply the persisted theme before the first paint, otherwise a dark-theme
// reload flashes the light console until App's effect runs.
document.documentElement.dataset.theme = get(settings).theme

const app = mount(App, {
  target: document.getElementById('app')!,
})

export default app
