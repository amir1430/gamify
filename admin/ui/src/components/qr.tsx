import qrcode from 'qrcode-generator'
import { useMemo } from 'react'

// QR as one SVG path (crisp at any size, no canvas), dark modules on a white quiet zone.
export function Qr({ value, size = 184, label }: { value: string; size?: number; label: string }) {
  const { d, n } = useMemo(() => {
    const qr = qrcode(0, 'M')
    qr.addData(value)
    qr.make()
    const n = qr.getModuleCount()
    let d = ''
    for (let r = 0; r < n; r++) for (let c = 0; c < n; c++) if (qr.isDark(r, c)) d += `M${c} ${r}h1v1h-1z`
    return { d, n }
  }, [value])
  return (
    <svg className="rounded-lg" width={size} height={size} viewBox={`-2 -2 ${n + 4} ${n + 4}`} shapeRendering="crispEdges" role="img" aria-label={label}>
      <rect fill="#ffffff" x="-2" y="-2" width={n + 4} height={n + 4} />
      <path fill="#04070e" d={d} />
    </svg>
  )
}
