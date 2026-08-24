import { useCallback, useEffect, useRef, useState } from 'react'
import { APIError, api } from '../api/client'
import type { Device } from '../api/types'

interface DeviceSnapshot {
  deviceID: string | undefined
  device: Device | null
  loading: boolean
  error: string | null
}

function errorMessage(error: unknown): string {
  return error instanceof APIError ? error.message : '无法加载设备。'
}

export function useDevice(deviceID: string | undefined, poll = true) {
  const [snapshot, setSnapshot] = useState<DeviceSnapshot>({
    deviceID,
    device: null,
    loading: true,
    error: null,
  })
  const requestSequence = useRef(0)

  const refresh = useCallback(async () => {
    const requestedDeviceID = deviceID
    const requestID = ++requestSequence.current
    if (!requestedDeviceID) {
      setSnapshot({ deviceID: requestedDeviceID, device: null, loading: false, error: '缺少设备 ID。' })
      return
    }
    try {
      const next = await api.device(requestedDeviceID)
      if (requestSequence.current !== requestID) return
      setSnapshot({ deviceID: requestedDeviceID, device: next, loading: false, error: null })
    } catch (nextError) {
      if (requestSequence.current !== requestID) return
      setSnapshot((current) => ({
        deviceID: requestedDeviceID,
        device: current.deviceID === requestedDeviceID ? current.device : null,
        loading: false,
        error: errorMessage(nextError),
      }))
    }
  }, [deviceID])

  useEffect(() => {
    ++requestSequence.current
    setSnapshot({ deviceID, device: null, loading: true, error: null })
    void refresh()
    if (!poll) return () => { ++requestSequence.current }
    const timer = window.setInterval(() => void refresh(), 5_000)
    return () => {
      window.clearInterval(timer)
      ++requestSequence.current
    }
  }, [deviceID, poll, refresh])

  if (snapshot.deviceID !== deviceID) {
    return { device: null, loading: true, error: null, refresh }
  }
  return { device: snapshot.device, loading: snapshot.loading, error: snapshot.error, refresh }
}
