import { APIError, api, parseAPIError, setUnauthorizedHandler } from './client'
import { jsonResponse } from '../test/helpers'

describe('API client', () => {
  it('parses the standard error envelope', () => {
    const error = parseAPIError(410, {
      error: { code: 'PAIRING_CODE_EXPIRED', message: 'pairing code has expired', request_id: 'req_1' },
    })
    expect(error).toBeInstanceOf(APIError)
    expect(error).toMatchObject({ status: 410, code: 'PAIRING_CODE_EXPIRED', requestID: 'req_1' })
    expect(error.message).toBe('pairing code has expired')
  })

  it('calls the global unauthorized handler on 401', async () => {
    const unauthorized = vi.fn()
    setUnauthorizedHandler(unauthorized)
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(jsonResponse({
      error: { code: 'UNAUTHENTICATED', message: 'authentication required', request_id: 'req_2' },
    }, 401))

    await expect(api.devices()).rejects.toMatchObject({ status: 401, code: 'UNAUTHENTICATED' })
    expect(unauthorized).toHaveBeenCalledOnce()
    setUnauthorizedHandler(undefined)
  })
})
