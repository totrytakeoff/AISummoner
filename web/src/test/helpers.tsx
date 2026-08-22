import type { ReactElement } from 'react'
import { render } from '@testing-library/react'
import { MemoryRouter } from 'react-router-dom'
import { App } from '../App'

export function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

export function emptyResponse(status = 204): Response {
  return new Response(null, { status })
}

export function renderApp(path = '/'): ReturnType<typeof render> {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <App />
    </MemoryRouter>,
  )
}

export function renderWithRouter(element: ReactElement, path = '/'): ReturnType<typeof render> {
  return render(<MemoryRouter initialEntries={[path]}>{element}</MemoryRouter>)
}

export const onlineDevice = {
  id: 'dev_online',
  name: 'lzr-host',
  platform: 'linux',
  arch: 'amd64',
  client_version: '0.1.0',
  created_at: '2026-08-13T08:00:00Z',
  paired_at: '2026-08-13T08:01:00Z',
  last_seen_at: '2026-08-13T08:02:00Z',
  online: true,
}

export const offlineDevice = {
  ...onlineDevice,
  id: 'dev_offline',
  name: 'archive-host',
  last_seen_at: null,
  online: false,
}
