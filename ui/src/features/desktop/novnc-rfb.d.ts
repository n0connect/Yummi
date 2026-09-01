// @novnc/novnc ships no TypeScript types (a plain JS package) — this is a
// minimal ambient declaration covering only the RFB surface DesktopPanel.tsx
// actually uses (constructor, the two lifecycle events, scaleViewport/
// resizeSession, disconnect). Not a full API surface; extend as needed.
//
// The package's own package.json "exports" field maps ONLY the bare
// specifier `.` to `./core/rfb.js` — a deep import of
// '@novnc/novnc/core/rfb.js' type-checks fine locally (tsc doesn't resolve
// against the real exports map) but fails vite's production build with
// "Package subpath 'undefined' is not defined by exports". Import from the
// bare package name.
declare module '@novnc/novnc' {
  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, url: string, options?: { credentials?: { password?: string } })
    scaleViewport: boolean
    resizeSession: boolean
    viewOnly: boolean
    disconnect(): void
    sendCtrlAltDel(): void
  }
}
