import type { CSSProperties } from 'react'
import logoUrl from '../assets/yummi_logo.svg'
import './YummiLogo.css'

export interface YummiLogoProps {
  /** Height in pixels or CSS string (e.g. 20, '1.5rem'). Defaults to 20. */
  height?: number | string
  /** Width in pixels or CSS string. Defaults to 'auto' to preserve the ~3.07:1 aspect ratio. */
  width?: number | string
  /** Optional extra CSS classes. */
  className?: string
  /** Accessibility alt text. Defaults to 'YUMMI'. */
  alt?: string
  /** Inline style overrides. */
  style?: CSSProperties
}

/**
 * Reusable, brand-consistent Yummi wordmark component backed by yummi_logo.svg.
 * Preserves the exact voxel typography, depth shadow layer, and theme styling.
 */
export function YummiLogo({
  height = 20,
  width,
  className = '',
  alt = 'YUMMI',
  style
}: YummiLogoProps): JSX.Element {
  return (
    <img
      src={logoUrl}
      alt={alt}
      className={`yummi-logo ${className}`.trim()}
      style={{
        height: typeof height === 'number' ? `${height}px` : height,
        width: width !== undefined ? (typeof width === 'number' ? `${width}px` : width) : 'auto',
        ...style
      }}
      loading="eager"
      decoding="async"
    />
  )
}
