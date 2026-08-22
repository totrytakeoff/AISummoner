import { useEffect, useId, useRef, useState } from 'react'
import type { FormEvent } from 'react'
import { APIError } from '../api/client'

const focusableSelector = [
  'button:not([disabled])',
  'input:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',')

interface DeepSeekSetupDialogProps {
  onCancel: () => void
  onConfigure: (apiKey: string) => Promise<void>
}

export function DeepSeekSetupDialog({ onCancel, onConfigure }: DeepSeekSetupDialogProps) {
  const [apiKey, setAPIKey] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const dialogRef = useRef<HTMLElement>(null)
  const keyRef = useRef<HTMLInputElement>(null)
  const cancelHandler = useRef(onCancel)
  const busyState = useRef(submitting)
  const titleID = useId()
  const descriptionID = useId()

  cancelHandler.current = onCancel
  busyState.current = submitting

  useEffect(() => {
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
    keyRef.current?.focus({ preventScroll: true })

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
      const focusable = Array.from(dialog.querySelectorAll<HTMLElement>(focusableSelector))
      if (focusable.length === 0) return
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

  function cancel() {
    if (submitting) return
    setAPIKey('')
    setError(null)
    onCancel()
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (submitting || !apiKey.trim()) return
    setSubmitting(true)
    setError(null)
    try {
      await onConfigure(apiKey.trim())
      setAPIKey('')
      onCancel()
    } catch (nextError) {
      setError(nextError instanceof APIError ? nextError.message : 'Could not configure DeepSeek.')
      setSubmitting(false)
    }
  }

  return (
    <div className="dialog-backdrop">
      <section
        ref={dialogRef}
        className="confirm-dialog provider-setup-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleID}
        aria-describedby={descriptionID}
        tabIndex={-1}
      >
        <p className="eyebrow">Agent provider</p>
        <h2 id={titleID}>Set up DeepSeek</h2>
        <p id={descriptionID}>
          Paste the API key once. AISummoner keeps it only in Server memory and asks again after a Server restart.
        </p>
        <form className="stack-form provider-setup-form" onSubmit={submit} autoComplete="off">
          <label htmlFor="deepseek-api-key">DeepSeek API key</label>
          <input
            ref={keyRef}
            id="deepseek-api-key"
            name="deepseek-api-key"
            type="password"
            autoComplete="off"
            spellCheck={false}
            maxLength={4096}
            value={apiKey}
            onChange={(event) => setAPIKey(event.target.value)}
            required
          />
          <p className="tiny muted">The default DeepSeek model is selected automatically.</p>
          {error && <div className="notice error compact" role="alert">{error}</div>}
          <div className="button-row">
            <button className="button ghost" type="button" onClick={cancel} disabled={submitting}>Cancel</button>
            <button className="button primary" type="submit" disabled={submitting || !apiKey.trim()}>
              {submitting ? 'Saving…' : 'Use DeepSeek'}
            </button>
          </div>
        </form>
      </section>
    </div>
  )
}
