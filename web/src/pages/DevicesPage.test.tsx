import { screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { jsonResponse, offlineDevice, onlineDevice, renderApp } from '../test/helpers'

describe('devices page', () => {
  it('shows online and offline states', async () => {
    vi.spyOn(globalThis, 'fetch')
      .mockResolvedValueOnce(jsonResponse({ user: { id: 'usr_1', username: 'admin' } }))
      .mockResolvedValueOnce(jsonResponse({ devices: [onlineDevice, offlineDevice] }))
    renderApp('/devices')

    expect(await screen.findByText('lzr-host')).toBeInTheDocument()
    expect(screen.getByText('archive-host')).toBeInTheDocument()
    expect(screen.getByText('Online')).toBeInTheDocument()
    expect(screen.getByText('Offline')).toBeInTheDocument()
  })

  it('normalizes a pairing code and refreshes the list after success', async () => {
    const paired = { ...onlineDevice, id: 'dev_paired', name: 'new-host' }
    const fetchMock = vi.spyOn(globalThis, 'fetch')
    fetchMock
      .mockResolvedValueOnce(jsonResponse({ user: { id: 'usr_1', username: 'admin' } }))
      .mockResolvedValueOnce(jsonResponse({ devices: [] }))
      .mockResolvedValueOnce(jsonResponse({ device: paired }))
      .mockResolvedValueOnce(jsonResponse({ devices: [paired] }))
    renderApp('/devices')

    const field = await screen.findByLabelText('Pairing code')
    await userEvent.type(field, 'k7hf 92pq')
    expect(field).toHaveValue('K7HF-92PQ')
    await userEvent.click(screen.getByRole('button', { name: 'Pair device' }))

    expect(await screen.findByText('new-host')).toBeInTheDocument()
    expect(screen.getByRole('status')).toHaveTextContent('new-host is now paired')
    await waitFor(() => {
      const pairingCall = fetchMock.mock.calls.find(([url]) => url === '/api/v1/pairings/claim')
      expect(pairingCall?.[1]?.body).toBe(JSON.stringify({ code: 'K7HF-92PQ' }))
    })
  })
})
