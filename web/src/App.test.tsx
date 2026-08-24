import { screen } from '@testing-library/react'
import { useLocation } from 'react-router-dom'
import { jsonResponse, renderApp } from './test/helpers'

vi.mock('./pages/WorkspacePage', () => ({
  WorkspacePage: () => {
    const location = useLocation()
    return <div data-testid="workspace-route">{location.pathname}{location.search}</div>
  },
}))

describe('workspace route migration', () => {
  beforeEach(() => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValue(jsonResponse({ user: { id: 'usr_1', username: 'admin' } }))
  })

  it.each([
    ['/devices/dev_one', '/devices/dev_one/workspace?settings=device'],
    ['/devices/dev_one/agent', '/devices/dev_one/workspace'],
    ['/devices/dev_one/terminal', '/devices/dev_one/workspace?dock=terminal'],
  ])('redirects %s into the control workspace', async (legacyPath, workspacePath) => {
    renderApp(legacyPath)
    expect(await screen.findByTestId('workspace-route')).toHaveTextContent(workspacePath)
  })
})
