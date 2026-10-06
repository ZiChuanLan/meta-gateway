import { act, renderHook } from '@testing-library/react'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { ReactNode } from 'react'
import { SessionProvider, useSession } from './session'

const wrapper = ({ children }: { children: ReactNode }) => <SessionProvider>{children}</SessionProvider>

describe('admin session', () => {
  beforeEach(() => {
    localStorage.clear()
    sessionStorage.clear()
  })

  it('keeps an unremembered token out of storage', () => {
    const { result } = renderHook(() => useSession(), { wrapper })
    act(() => result.current.connect(' transient-token ', false))
    expect(result.current.token).toBe('transient-token')
    expect(localStorage.length).toBe(0)
    expect(sessionStorage.length).toBe(0)
  })
  it('persists a remembered token to localStorage and clears on disconnect', () => {
    const { result } = renderHook(() => useSession(), { wrapper })
    act(() => result.current.connect('tab-token', true))
    expect(localStorage.getItem('meta-gateway.admin-token')).toBe('tab-token')
    expect(sessionStorage.length).toBe(0)
    act(() => result.current.disconnect())
    expect(result.current.token).toBeNull()
    expect(localStorage.length).toBe(0)
    expect(sessionStorage.length).toBe(0)
  })
  it('restores a remembered token from localStorage', () => {
    localStorage.setItem('meta-gateway.admin-token', 'kept-token')
    const { result } = renderHook(() => useSession(), { wrapper })
    expect(result.current.token).toBe('kept-token')
  })
})

it('clears expired member identity locally without a logout request', () => {
 localStorage.clear();sessionStorage.clear();
 const fetcher=vi.fn();vi.stubGlobal('fetch',fetcher);
 const {result,unmount}=renderHook(()=>useSession(),{wrapper});
 act(()=>result.current.connectMember({role:'member',csrf:'test-csrf',remember:true}));
 expect(result.current.role).toBe('member');
 act(()=>window.dispatchEvent(new Event('meta-team-expired')));
 expect(result.current.token).toBeNull();expect(result.current.role).toBeNull();
 expect(localStorage.getItem('meta-gateway.team-console')).toBeNull();
 expect(fetcher).not.toHaveBeenCalled();unmount();vi.unstubAllGlobals();
});
it('does not clear an administrator bearer session on unrelated member expiry', () => {
 localStorage.clear();sessionStorage.clear();
 const {result,unmount}=renderHook(()=>useSession(),{wrapper});
 act(()=>result.current.connect('admin-session',false));
 act(()=>window.dispatchEvent(new Event('meta-team-expired')));
 expect(result.current.token).toBe('admin-session');unmount();
});

it('clears a cookie identity on another tab change without revoking its cookie',()=>{
 localStorage.clear();sessionStorage.clear();
 const fetcher=vi.fn();vi.stubGlobal('fetch',fetcher);
 const {result,unmount}=renderHook(()=>useSession(),{wrapper});
 act(()=>result.current.connectMember({role:'owner',csrf:'owner-csrf',remember:true}));
 act(()=>window.dispatchEvent(new StorageEvent('storage',{key:'meta-gateway.account-change',newValue:'new-account-marker'})));
 expect(result.current.token).toBeNull();expect(result.current.role).toBeNull();
 expect(localStorage.getItem('meta-gateway.team-console')).toBe('1');
 expect(fetcher).not.toHaveBeenCalled();unmount();vi.unstubAllGlobals();
});
it('ignores a cross-tab cookie change for an independent bearer identity',()=>{
 localStorage.clear();sessionStorage.clear();
 const {result,unmount}=renderHook(()=>useSession(),{wrapper});
 act(()=>result.current.connect('bearer-token',false));
 act(()=>window.dispatchEvent(new StorageEvent('storage',{key:'meta-gateway.account-change',newValue:'new-account-marker'})));
 expect(result.current.token).toBe('bearer-token');unmount();
});
