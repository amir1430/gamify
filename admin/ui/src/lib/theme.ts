import { useSyncExternalStore } from 'react'

// Light, dark or System (follows prefers-color-scheme, live). The choice lives in localStorage;
// index.html's inline script applies it before first paint with the same key and rule.

export type Theme = 'system' | 'light' | 'dark'

const KEY = 'gamify.admin.theme'
const media = matchMedia('(prefers-color-scheme: dark)')
const listeners = new Set<() => void>()

function read(): Theme {
  try {
    const t = localStorage.getItem(KEY)
    return t === 'light' || t === 'dark' ? t : 'system'
  } catch {
    return 'system' // storage blocked
  }
}

let theme = read()

const isDark = () => theme === 'dark' || (theme === 'system' && media.matches)

function apply() {
  document.documentElement.classList.toggle('dark', isDark())
  listeners.forEach((l) => l())
}

media.addEventListener('change', apply)
// Another tab changed it.
window.addEventListener('storage', (e) => {
  if (e.key !== KEY) return
  theme = read()
  apply()
})

export function setTheme(t: Theme) {
  theme = t
  try {
    t === 'system' ? localStorage.removeItem(KEY) : localStorage.setItem(KEY, t)
  } catch {
    // storage blocked: this tab only
  }
  apply()
}

const subscribe = (l: () => void) => (listeners.add(l), () => void listeners.delete(l))

// [the choice, the theme in effect]
export function useTheme(): [Theme, 'light' | 'dark'] {
  const t = useSyncExternalStore(subscribe, () => theme)
  const resolved = useSyncExternalStore(subscribe, () => (isDark() ? 'dark' : 'light'))
  return [t, resolved]
}

// Brand.Accent (any CSS colour) as --brand, --ring and --primary on <html>, with near-black or
// white, whichever reads better on it, as --primary-foreground. '' or an invalid colour restores
// each theme's own.
const ACCENT_VARS = ['--brand', '--ring', '--primary', '--primary-foreground']

export function setAccent(color: string) {
  const s = document.documentElement.style
  if (!color || !CSS.supports('color', color)) {
    ACCENT_VARS.forEach((v) => s.removeProperty(v))
    return
  }
  s.setProperty('--brand', color)
  s.setProperty('--ring', color)
  s.setProperty('--primary', color)
  s.setProperty('--primary-foreground', luminance(color) > 0.179 ? '#09090b' : '#ffffff')
}

// Relative luminance (WCAG) of a CSS colour: painted on a 1×1 canvas, so any syntax the browser
// knows comes back as sRGB.
function luminance(color: string): number {
  const ctx = document.createElement('canvas').getContext('2d', { willReadFrequently: true })
  if (!ctx) return 0
  ctx.fillStyle = color
  ctx.fillRect(0, 0, 1, 1)
  const [r, g, b] = Array.from(ctx.getImageData(0, 0, 1, 1).data.slice(0, 3), (c) => {
    const v = c / 255
    return v <= 0.04045 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4
  })
  return 0.2126 * r + 0.7152 * g + 0.0722 * b
}
