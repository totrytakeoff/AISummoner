import { useEffect, useId, useRef } from 'react'
import type { ReactNode } from 'react'

interface ConfirmDialogProps {
  title: string
  description: ReactNode
  confirmLabel: string
  busyLabel?: string
  busy?: boolean
  eyebrow?: string
  onCancel: () => void
  onConfirm: () => void
}

const focusableSelector = [
  'a[href]',
  'button:not([disabled])',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

function focusableElements(dialog: HTMLElement): HTMLElement[] {
  return Array.from(dialog.querySelectorAll<HTMLElement>(focusableSelector))
    .filter((element) => element.getAttribute('aria-hidden') !== 'true')
}

export function ConfirmDialog({
  title,
  description,
  confirmLabel,
  busyLabel,
  busy = false,
  eyebrow,
  onCancel,
  onConfirm,
}: ConfirmDialogProps) {
  const dialogRef = useRef<HTMLElement>(null)
  const cancelRef = useRef<HTMLButtonElement>(null)
  const cancelHandler = useRef(onCancel)
  const busyState = useRef(busy)
  const titleID = useId()
  const descriptionID = useId()

  cancelHandler.current = onCancel
  busyState.current = busy

  useEffect(() => {
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    cancelRef.current?.focus({ preventScroll: true })

    function handleKeyDown(event: KeyboardEvent) {
      const dialog = dialogRef.current
      if (!dialog) return
      if (event.key === 'Escape') {
        if (busyState.current) return
        event.preventDefault()
        event.stopPropagation()
        cancelHandler.current()
        return
      }
      if (event.key !== 'Tab') return

      const focusable = focusableElements(dialog)
      if (focusable.length === 0) {
        event.preventDefault()
        dialog.focus({ preventScroll: true })
        return
      }
      const first = focusable[0]
      const last = focusable.at(-1)!
      const active = document.activeElement
      if (event.shiftKey && (active === first || !dialog.contains(active))) {
        event.preventDefault()
        last.focus()
      } else if (!event.shiftKey && (active === last || !dialog.contains(active))) {
        event.preventDefault()
        first.focus()
      }
    }

    document.addEventListener('keydown', handleKeyDown, true)
    return () => {
      document.removeEventListener('keydown', handleKeyDown, true)
      if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true })
    }
  }, [])

  return (
    <div className="dialog-backdrop">
      <section
        ref={dialogRef}
        className="confirm-dialog"
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleID}
        aria-describedby={descriptionID}
        tabIndex={-1}
      >
        {eyebrow && <p className="eyebrow danger-text">{eyebrow}</p>}
        <h2 id={titleID}>{title}</h2>
        <div id={descriptionID}>{description}</div>
        <div className="button-row">
          <button ref={cancelRef} className="button ghost" type="button" onClick={onCancel} disabled={busy}>取消</button>
          <button className="button danger" type="button" onClick={onConfirm} disabled={busy}>
            {busy ? (busyLabel ?? '处理中…') : confirmLabel}
          </button>
        </div>
      </section>
    </div>
  )
}
