import { useMemo, useState } from 'react'
import { cn } from '@/lib/utils'

export type Bar = { label: string; full: string; value: number }

// A nice ceiling for the y axis: 1, 2, 5 × 10^n at or above max.
function niceMax(max: number) {
  if (max <= 4) return 4
  const p = 10 ** Math.floor(Math.log10(max))
  return ([1, 2, 2.5, 5, 10].map((m) => m * p).find((v) => v >= max) ?? 10 * p)
}

const H = 220 // plot height, px
const GAP = 2 // surface gap between bars

// One series of bars over days (sign-ups): brand colour, 4px rounded tops on the baseline, three
// recessive grid lines, a tooltip per bar (a hit area the full column wide), every nth x label.
// A table view stays available to screen readers.
export function BarChart({ bars, unit, className }: { bars: Bar[]; unit: string; className?: string }) {
  const [hover, setHover] = useState<number | null>(null)
  const max = useMemo(() => niceMax(Math.max(0, ...bars.map((b) => b.value))), [bars])
  const ticks = [0, max / 2, max]
  const every = Math.ceil(bars.length / 8)
  const h = hover == null ? null : bars[hover]
  return (
    <div className={cn('relative', className)}>
      <div className="flex gap-2">
        <div className="relative w-8 shrink-0 text-right text-[11px] text-muted-foreground" style={{ height: H }} aria-hidden>
          {ticks.map((t) => (
            <span key={t} className="num absolute right-0 -translate-y-1/2" style={{ top: H - (t / max) * H }}>
              {Number.isInteger(t) ? t.toLocaleString() : t.toFixed(1)}
            </span>
          ))}
        </div>
        <div className="relative min-w-0 flex-1">
          <div className="relative" style={{ height: H }} onMouseLeave={() => setHover(null)} aria-hidden>
            {ticks.map((t) => (
              <div key={t} className={cn('absolute inset-x-0 border-t', t === 0 ? 'border-border' : 'border-dashed border-border/60')} style={{ top: H - (t / max) * H }} />
            ))}
            <div className="absolute inset-0 flex items-end">
              {bars.map((b, i) => (
                <div key={b.full} className="relative flex h-full flex-1 items-end" style={{ paddingInline: GAP / 2 }} onMouseEnter={() => setHover(i)}>
                  <div
                    className={cn('w-full rounded-t-[4px] bg-brand transition-opacity', hover != null && hover !== i && 'opacity-45')}
                    style={{ height: b.value ? Math.max(2, (b.value / max) * H) : 0 }}
                  />
                </div>
              ))}
            </div>
            {h && hover != null && (
              <div
                className="pointer-events-none absolute z-10 -translate-x-1/2 rounded-md border bg-popover px-2.5 py-1.5 text-xs whitespace-nowrap shadow-md"
                style={{ left: `${((hover + 0.5) / bars.length) * 100}%`, bottom: Math.min(H - 20, (h.value / max) * H + 8) }}
              >
                <div className="text-muted-foreground">{h.full}</div>
                <div className="num font-semibold">
                  {h.value.toLocaleString()} {unit}
                </div>
              </div>
            )}
          </div>
          <div className="relative mt-1.5 h-4 text-[11px] text-muted-foreground" aria-hidden>
            {bars.map((b, i) =>
              (bars.length - 1 - i) % every === 0 ? (
                <span key={b.full} className="absolute -translate-x-1/2 whitespace-nowrap" style={{ left: `${((i + 0.5) / bars.length) * 100}%` }}>
                  {b.label}
                </span>
              ) : null,
            )}
          </div>
        </div>
      </div>
      <table className="sr-only">
        <tbody>
          {bars.map((b) => (
            <tr key={b.full}>
              <th>{b.full}</th>
              <td>{b.value}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
