export interface DesktopViewport {
  cssWidth: number
  cssHeight: number
  devicePixelRatio: number
}

export interface DesktopScaleResult {
  scale: number
  effectiveWidth: number
  physicalWidth: number
  useWideToolbar: boolean
}

export function calculateDesktopScale(viewport: DesktopViewport): DesktopScaleResult {
  const ratio = Number.isFinite(viewport.devicePixelRatio)
    ? Math.max(1, viewport.devicePixelRatio)
    : 1
  // WebView2 is per-monitor DPI aware and already exposes a CSS viewport scaled
  // by Windows. Applying a second CSS zoom (and compensating width) creates root
  // overflow and defeats the user's accessibility setting. Responsive layout
  // must therefore use CSS pixels and leave DPI scaling to the native WebView.
  const scale = 1
  const effectiveWidth = Math.max(0, viewport.cssWidth)
  const physicalWidth = Math.max(0, viewport.cssWidth) * ratio

  return {
    scale,
    effectiveWidth,
    physicalWidth,
    useWideToolbar: effectiveWidth >= 1600,
  }
}
