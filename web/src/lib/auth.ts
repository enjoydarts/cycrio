import type { Clerk } from '@clerk/clerk-js';
import { ApiError, type createAPI, type CurrentUser } from './api.ts';

export type AuthSDK = {
  user: { id: string } | null | undefined;
  session: { getToken: () => Promise<string | null> } | null | undefined;
  addListener: (callback: () => void) => () => void;
  signOut: () => Promise<void>;
  mountSignIn: Clerk['mountSignIn'];
  unmountSignIn: Clerk['unmountSignIn'];
  mountSignUp: Clerk['mountSignUp'];
  unmountSignUp: Clerk['unmountSignUp'];
  handleRedirectCallback: Clerk['handleRedirectCallback'];
};
export type AuthState =
  | {
      status:
        'loading' | 'unconfigured' | 'signed_out' | 'checking' | 'redirecting';
    }
  | { status: 'allowed'; user: CurrentUser }
  | { status: 'pending'; clerkUserId: string }
  | { status: 'denied' }
  | {
      status: 'error';
      code:
        'authentication_required' | 'access_denied' | 'dependency_unavailable';
      signedIn: boolean;
    };

function authError(
  error: unknown,
): 'authentication_required' | 'access_denied' | 'dependency_unavailable' {
  return error instanceof ApiError &&
    (error.code === 'authentication_required' || error.code === 'access_denied')
    ? error.code
    : 'dependency_unavailable';
}

let cachedClerk: { key: string; promise: Promise<AuthSDK> } | undefined;

async function loadClerk(key: string): Promise<AuthSDK> {
  if (cachedClerk?.key === key) return cachedClerk.promise;
  const promise = (async () => {
    const [{ Clerk }, { ClerkUI }, { jaJP }] = await Promise.all([
      import('@clerk/clerk-js'),
      import('@clerk/ui/entry'),
      import('@clerk/localizations/ja-JP'),
    ]);
    const clerk = new Clerk(key);
    await clerk.load({
      ui: { ClerkUI },
      localization: jaJP,
      signInUrl: '/',
      signUpUrl: '/?sign_up=true',
      signInForceRedirectUrl: '/',
      signUpForceRedirectUrl: '/',
      afterSignOutUrl: '/',
      appearance: {
        variables: {
          colorPrimary: 'var(--color-action)',
          colorText: 'var(--color-text)',
          colorTextSecondary: 'var(--color-muted)',
          colorBackground: 'var(--color-surface)',
          colorSuccess: 'var(--color-action)',
          borderRadius: '0.85rem',
        },
        elements: {
          cardBox: { width: '100%', boxShadow: 'none' },
          card: { boxShadow: 'none' },
        },
      },
    });
    return clerk;
  })();
  cachedClerk = { key, promise };
  try {
    return await promise;
  } catch (error) {
    if (cachedClerk?.promise === promise) cachedClerk = undefined;
    throw error;
  }
}

export function createAuthController(
  api: ReturnType<typeof createAPI>,
  mode: 'home' | 'admin' | 'callback' | 'mail',
  changed: (state: AuthState) => void,
  load: (key: string) => Promise<AuthSDK> = loadClerk,
) {
  let disposed = false;
  let sdk: AuthSDK | undefined;
  let unsubscribe: (() => void) | undefined;
  let generation = 0;
  let request = new AbortController();

  function emit(state: AuthState) {
    if (!disposed) changed(state);
  }

  function nextRequest() {
    request.abort();
    request = new AbortController();
    generation++;
    return { id: generation, signal: request.signal };
  }

  async function refresh() {
    if (disposed || !sdk) return;
    const current = nextRequest();
    const user = sdk.user;
    if (!user || !sdk.session) {
      emit({ status: 'signed_out' });
      return;
    }
    emit({ status: 'checking' });
    try {
      const token = async () => (await sdk?.session?.getToken()) ?? null;
      if (mode === 'admin') await api.adminStatus(token, current.signal);
      const identity = await api.me(token, current.signal);
      if (generation === current.id)
        emit({ status: 'allowed', user: identity });
    } catch (error) {
      if (disposed || generation !== current.id) return;
      const code = authError(error);
      if (code === 'access_denied') {
        emit(
          mode === 'admin'
            ? { status: 'denied' }
            : { status: 'pending', clerkUserId: user.id },
        );
      } else {
        emit({ status: 'error', code, signedIn: true });
      }
    }
  }

  async function start() {
    if (disposed) return;
    unsubscribe?.();
    unsubscribe = undefined;
    const current = nextRequest();
    emit({ status: 'loading' });
    try {
      const configuration = await api.configuration(current.signal);
      if (disposed || generation !== current.id) return;
      if (!configuration.configured) {
        emit({ status: 'unconfigured' });
        return;
      }
      const loaded = await load(configuration.clerk_publishable_key);
      if (disposed || generation !== current.id) return;
      sdk = loaded;
      if (mode === 'callback') {
        emit({ status: 'redirecting' });
        await sdk.handleRedirectCallback({
          signInUrl: '/',
          signUpUrl: '/?sign_up=true',
          signInForceRedirectUrl: '/',
          signUpForceRedirectUrl: '/',
        });
        return;
      }
      unsubscribe = sdk.addListener(() => {
        void refresh();
      });
      await refresh();
    } catch (error) {
      if (disposed || current.signal.aborted) return;
      emit({
        status: 'error',
        code: authError(error),
        signedIn: Boolean(sdk?.user && sdk.session),
      });
    }
  }

  return {
    start,
    refresh,
    async getToken() {
      if (disposed || !sdk?.user || !sdk.session) return null;
      const session = sdk.session;
      const token = await session.getToken();
      return !disposed && sdk.session === session && sdk.user ? token : null;
    },
    mountAuth(node: HTMLDivElement, signUp = false) {
      if (!sdk || disposed) return () => {};
      const currentSDK = sdk;
      if (signUp)
        currentSDK.mountSignUp(node, {
          routing: 'hash',
          signInUrl: '/',
          forceRedirectUrl: '/',
        });
      else
        currentSDK.mountSignIn(node, {
          routing: 'hash',
          signUpUrl: '/?sign_up=true',
          forceRedirectUrl: '/',
        });
      return () => {
        if (signUp) currentSDK.unmountSignUp(node);
        else currentSDK.unmountSignIn(node);
      };
    },
    async signOut() {
      if (!sdk || disposed) return;
      nextRequest();
      emit({ status: 'checking' });
      try {
        await sdk.signOut();
        await refresh();
      } catch {
        emit({
          status: 'error',
          code: 'dependency_unavailable',
          signedIn: Boolean(sdk.user && sdk.session),
        });
      }
    },
    dispose() {
      disposed = true;
      request.abort();
      unsubscribe?.();
      unsubscribe = undefined;
    },
  };
}
