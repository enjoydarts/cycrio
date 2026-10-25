import assert from 'node:assert/strict';
import { test } from 'node:test';
import { ApiError, createAPI } from './api.ts';
import { createAuthController, type AuthSDK, type AuthState } from './auth.ts';

function fixture(response: (url: string) => Response, signedIn = true) {
  const states: AuthState[] = [];
  const requests: string[] = [];
  let listener: (() => void) | undefined;
  let unsubscribed = false;
  const sdk: AuthSDK = {
    user: signedIn ? { id: 'user_clerk_self' } : null,
    session: signedIn ? { getToken: async () => 'session' } : null,
    addListener: (callback) => {
      listener = callback;
      return () => {
        unsubscribed = true;
      };
    },
    signOut: async () => {
      sdk.user = null;
      sdk.session = null;
      listener?.();
    },
    mountSignIn: () => {},
    unmountSignIn: () => {},
    mountSignUp: () => {},
    unmountSignUp: () => {},
    handleRedirectCallback: async () => {},
  };
  const api = createAPI('https://api.test', async (url) => {
    requests.push(String(url));
    if (String(url).endsWith('/config')) {
      return Response.json({
        configured: true,
        clerk_publishable_key: 'pk_test_public',
      });
    }
    return response(String(url));
  });
  return {
    sdk,
    api,
    states,
    requests,
    changed: (state: AuthState) => states.push(state),
    get unsubscribed() {
      return unsubscribed;
    },
    listener: () => listener?.(),
  };
}

test('unconfigured installation does not initialize Clerk or call private endpoints', async () => {
  const states: AuthState[] = [];
  const api = createAPI('https://api.test', async () =>
    Response.json({ configured: false, clerk_publishable_key: '' }),
  );
  const controller = createAuthController(
    api,
    'home',
    (state) => states.push(state),
    async () => {
      assert.fail('Clerk must not load before configuration');
    },
  );
  await controller.start();
  assert.equal(states.at(-1)?.status, 'unconfigured');
  controller.dispose();
});

test('signed-out visitor sees sign in without a private API request', async () => {
  const f = fixture(() => {
    assert.fail('private endpoint must not be called');
  }, false);
  const controller = createAuthController(
    f.api,
    'home',
    f.changed,
    async () => f.sdk,
  );
  await controller.start();
  assert.equal(f.states.at(-1)?.status, 'signed_out');
  assert.equal(f.requests.length, 1);
  controller.dispose();
  assert.equal(f.unsubscribed, true);
});

test('allowed user identity comes from the server', async () => {
  const f = fixture(() => Response.json({ id: 'internal-id', role: 'user' }));
  const controller = createAuthController(
    f.api,
    'home',
    f.changed,
    async () => f.sdk,
  );
  await controller.start();
  const state = f.states.at(-1);
  assert.equal(state?.status, 'allowed');
  if (state?.status === 'allowed')
    assert.deepEqual(state.user, { id: 'internal-id', role: 'user' });
  controller.dispose();
});

test('mail route permits ordinary allowed users without checking administrator status', async () => {
  const f = fixture(() => Response.json({ id: 'internal-id', role: 'user' }));
  const controller = createAuthController(
    f.api,
    'mail',
    f.changed,
    async () => f.sdk,
  );
  await controller.start();
  assert.equal(f.states.at(-1)?.status, 'allowed');
  assert.equal(
    f.requests.some((url) => url.includes('/admin/')),
    false,
  );
  controller.dispose();
});

test('403 on the home page is permission pending and shows only own Clerk ID', async () => {
  const f = fixture(() => new Response(null, { status: 403 }));
  const controller = createAuthController(
    f.api,
    'home',
    f.changed,
    async () => f.sdk,
  );
  await controller.start();
  const state = f.states.at(-1);
  assert.equal(state?.status, 'pending');
  if (state?.status === 'pending')
    assert.equal(state.clerkUserId, 'user_clerk_self');
  controller.dispose();
});

test('administrator route checks server status and denies a 403 even after an admin identity', async () => {
  const f = fixture((url) =>
    url.endsWith('/me')
      ? Response.json({ id: 'internal-id', role: 'platform_admin' })
      : new Response(null, { status: 403 }),
  );
  const controller = createAuthController(
    f.api,
    'admin',
    f.changed,
    async () => f.sdk,
  );
  await controller.start();
  assert.equal(
    f.requests.some((url) => url.endsWith('/v1/admin/status')),
    true,
  );
  assert.equal(f.states.at(-1)?.status, 'denied');
  assert.equal(
    f.states.some((state) => state.status === 'allowed'),
    false,
  );
  controller.dispose();
});

test('administrator is allowed only after server administrator status succeeds', async () => {
  const f = fixture((url) =>
    Response.json(
      url.endsWith('/me')
        ? { id: 'internal-id', role: 'platform_admin' }
        : { role: 'platform_admin' },
    ),
  );
  const controller = createAuthController(
    f.api,
    'admin',
    f.changed,
    async () => f.sdk,
  );
  await controller.start();
  assert.equal(f.states.at(-1)?.status, 'allowed');
  controller.dispose();
});

test('administrator route checks its endpoint even when the user has no application access', async () => {
  const f = fixture(() => new Response(null, { status: 403 }));
  const controller = createAuthController(
    f.api,
    'admin',
    f.changed,
    async () => f.sdk,
  );
  await controller.start();
  assert.equal(f.requests[1], 'https://api.test/v1/admin/status');
  assert.equal(f.states.at(-1)?.status, 'denied');
  controller.dispose();
});

test('SSO callback uses fixed application destinations and does not request protected data', async () => {
  const f = fixture(() => {
    assert.fail('callback must not load private data');
  }, false);
  let called = false;
  f.sdk.handleRedirectCallback = async (options) => {
    called = true;
    assert.equal(options?.signInForceRedirectUrl, '/');
    assert.equal(options?.signUpForceRedirectUrl, '/');
  };
  const controller = createAuthController(
    f.api,
    'callback',
    f.changed,
    async () => f.sdk,
  );
  await controller.start();
  assert.equal(called, true);
  assert.equal(f.states.at(-1)?.status, 'redirecting');
  controller.dispose();
});

test('sign-out immediately removes the authorized panel and clears the session', async () => {
  const f = fixture(() => Response.json({ id: 'internal-id', role: 'user' }));
  const controller = createAuthController(
    f.api,
    'home',
    f.changed,
    async () => f.sdk,
  );
  await controller.start();
  await controller.signOut();
  assert.equal(f.states.at(-1)?.status, 'signed_out');
  controller.dispose();
});

test('late API response after disposal cannot update the view', async () => {
  let release: ((value: Response) => void) | undefined;
  const f = fixture(() => Response.json({ id: 'internal-id', role: 'user' }));
  const api = createAPI('https://api.test', async (url) =>
    String(url).endsWith('/config')
      ? Response.json({
          configured: true,
          clerk_publishable_key: 'pk_test_public',
        })
      : new Promise<Response>((resolve) => {
          release = resolve;
        }),
  );
  const controller = createAuthController(
    api,
    'home',
    f.changed,
    async () => f.sdk,
  );
  const running = controller.start();
  for (let attempt = 0; attempt < 20 && !release; attempt++) {
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  assert.ok(release, 'protected request must start');
  controller.dispose();
  const count = f.states.length;
  release(Response.json({ id: 'internal-id', role: 'user' }));
  await running;
  assert.equal(f.states.length, count);
  assert.equal(f.unsubscribed, true);
});

test('Clerk load errors expose a fixed dependency error', async () => {
  const f = fixture(() => {
    assert.fail('must not call private API');
  });
  const controller = createAuthController(
    f.api,
    'home',
    f.changed,
    async () => {
      throw new Error('secret vendor detail');
    },
  );
  await controller.start();
  assert.deepEqual(f.states.at(-1), {
    status: 'error',
    code: 'dependency_unavailable',
    signedIn: false,
  });
  controller.dispose();
});

test('management uses the existing current Clerk session and cannot get a token after disposal', async () => {
  const f = fixture(() => Response.json({ id: 'internal-id', role: 'user' }));
  const controller = createAuthController(
    f.api,
    'home',
    f.changed,
    async () => f.sdk,
  );
  assert.equal(await controller.getToken(), null);
  await controller.start();
  f.sdk.session = { getToken: async () => 'fresh-session' };
  assert.equal(await controller.getToken(), 'fresh-session');
  controller.dispose();
  assert.equal(await controller.getToken(), null);
});

test('auth views normalize management-only error codes', async () => {
  const f = fixture(() => Response.json({ id: 'internal-id', role: 'user' }));
  f.api.me = async () => {
    throw new ApiError('conflict');
  };
  const controller = createAuthController(
    f.api,
    'home',
    f.changed,
    async () => f.sdk,
  );
  await controller.start();
  assert.deepEqual(f.states.at(-1), {
    status: 'error',
    code: 'dependency_unavailable',
    signedIn: true,
  });
  controller.dispose();
});
