import '@testing-library/jest-dom/vitest'
import { cleanup } from '@testing-library/react'
import { afterEach } from 'vitest'

vi.mock('@xterm/xterm', () => ({
  Terminal: class {
    cols = 80
    rows = 24
    open() {}
    loadAddon() {}
    onData() { return { dispose() {} } }
    write() {}
    dispose() {}
  },
}))

vi.mock('@xterm/addon-fit', () => ({ FitAddon: class { fit() {} } }))

afterEach(() => cleanup())

class TestResizeObserver implements ResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

if (!globalThis.ResizeObserver) {
  globalThis.ResizeObserver = TestResizeObserver
}
