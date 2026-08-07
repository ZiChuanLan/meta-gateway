import { renderHook } from '@testing-library/react'
import { act } from 'react'
import { beforeEach, describe, expect, it } from 'vitest'
import type { ReactNode } from 'react'
import { SessionProvider, useSession } from './session'

const wrapper = ({ children }: { children: ReactNode }) => <SessionProvider>{children}</SessionProvider>

describe('admin session', () => {
  beforeEach(() => sessionStorage.clear())

  it('keeps an unremembered token out of storage', () => {
    const { result } = renderHook(() => useSession(), { wrapper })
    act(() => result.current.connect(' transient-token ', false))
    expect(result.current.token).toBe('transient-token')
    expect(sessionStorage.length).toBe(0)
  })
  it('persists only for the current tab and clears on disconnect', () => {
    const { result } = renderHook(() => useSession(), { wrapper })
    act(() => result.current.connect('tab-token', true))
    expect(sessionStorage.getItem('meta-gateway.admin-token')).toBe('tab-token')
    expect(localStorage.length).toBe(0)
    act(() => result.current.disconnect())
    expect(result.current.token).toBeNull()
    expect(sessionStorage.length).toBe(0)
  })
})
