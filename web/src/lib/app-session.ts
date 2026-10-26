import type { AuthState } from './auth.ts';
import type { TokenProvider } from './api.ts';
export type SessionSnapshot = {
  scope: number;
  auth: AuthState;
  token: TokenProvider;
};

// A scope is an authorization lifetime, not a token lifetime. Keep it stable
// through SDK refreshes, but never reuse it after a permission/session change.
export function createSessionBoundary(getToken: TokenProvider) {
  let scope = 0;
  let auth: AuthState = { status: 'loading' };
  let token: TokenProvider = async () => null;
  return {
    update(next: AuthState) {
      const sameOwner =
        auth.status === 'allowed' &&
        next.status === 'allowed' &&
        auth.user.id === next.user.id &&
        auth.user.role === next.user.role;
      if (!sameOwner) {
        scope++;
        const captured = scope;
        token = async () => {
          if (captured !== scope || auth.status !== 'allowed') return null;
          const value = await getToken();
          return captured === scope && auth.status === 'allowed' ? value : null;
        };
      }
      auth = next;
    },
    current(): SessionSnapshot {
      return { scope, auth, token };
    },
  };
}
