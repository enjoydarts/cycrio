import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { createMailController } from './mail-controller.ts';
import type { MailAPI } from './mail-api.ts';
import { account, probe, ids } from './mail-test-fixtures.ts';
function setup(overrides: Partial<MailAPI> = {}) {
  const api: MailAPI = {
    list: async () => [account()],
    save: async () => account({ version: 2 }),
    disconnect: async () => account({ state: 'disconnected', version: 2 }),
    requestProbe: async () => probe(),
    probe: async () => probe(),
    saveFolders: async () => account({ version: 2 }),
    ...overrides,
  };
  let denied = 0;
  const controller = createMailController(
    api,
    async () => 'session',
    () => {},
    () => denied++,
    () => ids.request,
  );
  return { controller, api, denied: () => denied };
}
test('closing connection details clears credentials, selection and confirmation; late reads cannot restore them', async () => {
  const { controller: c } = setup();
  await c.refresh();
  c.select(ids.account);
  c.edit({ login: 'owner@example.com', app_password: 'password' });
  c.confirmDisconnect(true);
  c.close();
  assert.equal(c.state().selected, null);
  assert.equal(c.state().form, null);
  assert.equal(c.state().disconnectConfirmation, null);
  assert.equal(c.state().accounts.length, 1);
  c.select(ids.account);
  assert.equal(c.state().form?.app_password, '');
});
test('load and poll never POST; ordinary refresh preserves dirty credentials and baseline', async () => {
  let posts = 0,
    version = 1;
  const { controller: c } = setup({
    list: async () => [account({ version })],
    requestProbe: async () => {
      posts++;
      return probe();
    },
  });
  await c.refresh();
  c.select(ids.account);
  c.edit({
    label: '編集中',
    login: 'me@example.com',
    app_password: 'password',
  });
  version = 2;
  await c.refresh();
  await c.poll();
  assert.equal(posts, 0);
  assert.equal(c.state().form?.baseline, 1);
  assert.equal(c.state().form?.login, 'me@example.com');
  c.discard();
  assert.equal(c.state().form?.baseline, 2);
  assert.equal(c.state().form?.login, '');
});
test('folder edits and baseline survive refresh; invalid/unavailable folders cannot be selected', async () => {
  const folder = {
    id: ids.folder,
    name: '受信箱',
    delimiter: '/',
    available: true,
    selectable: true,
    selected: false,
  };
  const { controller: c } = setup({
    list: async () => [
      account({
        state: 'ready',
        folders: [folder, { ...folder, id: ids.other, available: false }],
      }),
    ],
  });
  await c.refresh();
  c.select(ids.account);
  c.toggleFolder(ids.folder, true);
  c.toggleFolder(ids.other, true);
  await c.refresh();
  assert.deepEqual(c.state().form?.folder_ids, [ids.folder]);
  assert.equal(c.state().form?.foldersDirty, true);
});
test('uncertain probe acceptance reuses the UUID; new probe after terminal uses a new request', async () => {
  const requests: string[] = [];
  let fails = true;
  const { controller: c } = setup({
    requestProbe: async (_id, input) => {
      requests.push(input.request_id);
      if (fails) {
        fails = false;
        throw new ApiError('dependency_unavailable');
      }
      return probe();
    },
    probe: async () => probe({ state: 'succeeded' }),
    list: async () => [
      account({
        latest_probe: requests.length ? probe({ state: 'succeeded' }) : null,
      }),
    ],
  });
  await c.refresh();
  c.select(ids.account);
  await c.requestProbe();
  assert.equal(c.state().uncertain, true);
  await c.requestProbe();
  await c.poll();
  assert.deepEqual(requests, [ids.request, ids.request]);
});
test('double click saves once and clears secret values after success', async () => {
  let finish!: (value: ReturnType<typeof account>) => void,
    saves = 0;
  const { controller: c } = setup({
    save: async () => {
      saves++;
      return await new Promise((resolve) => {
        finish = resolve;
      });
    },
  });
  await c.refresh();
  c.select(ids.account);
  c.edit({ login: 'me@example.com', app_password: 'password' });
  const first = c.save();
  await c.save();
  assert.equal(saves, 1);
  finish(account({ version: 2 }));
  await first;
  assert.equal(c.state().form?.login, '');
  assert.equal(c.state().form?.app_password, '');
});
test('stale account responses cannot replace another account private state', async () => {
  let finish!: (value: ReturnType<typeof account>) => void;
  const { controller: c } = setup({
    list: async () => [account(), account({ id: ids.other })],
    save: async () =>
      await new Promise((resolve) => {
        finish = resolve;
      }),
  });
  await c.refresh();
  c.select(ids.account);
  c.edit({ login: 'me@example.com', app_password: 'password' });
  const pending = c.save();
  c.select(ids.other);
  finish(account({ version: 2 }));
  await pending;
  assert.equal(c.state().selected, ids.other);
  assert.equal(c.state().form?.login, '');
});
test('403/401 erase all private state, notify parent, and prevent subsequent reads', async () => {
  for (const code of ['access_denied', 'authentication_required'] as const) {
    let calls = 0;
    const f = setup({
      list: async () => {
        calls++;
        if (calls > 1) throw new ApiError(code);
        return [account()];
      },
    });
    await f.controller.refresh();
    f.controller.select(ids.account);
    f.controller.edit({ login: 'secret' });
    await f.controller.refresh();
    assert.equal(f.controller.state().status, 'denied');
    assert.deepEqual(f.controller.state().accounts, []);
    assert.equal(f.controller.state().form, null);
    assert.equal(f.denied(), 1);
    await f.controller.refresh();
    assert.equal(calls, 2);
  }
});
test('disconnect requires explicit confirmation for exact account version', async () => {
  let calls = 0;
  const { controller: c } = setup({
    disconnect: async () => {
      calls++;
      return account({ state: 'disconnected', version: 2 });
    },
  });
  await c.refresh();
  c.select(ids.account);
  await c.disconnect();
  assert.equal(calls, 0);
  c.confirmDisconnect(true);
  await c.disconnect();
  assert.equal(calls, 1);
});
test('new account retry retains browser ID and baseline zero; validation rejects spaces in app password', async () => {
  const inputs: string[] = [];
  const { controller: c } = setup({
    save: async (id) => {
      inputs.push(id);
      throw new ApiError('conflict');
    },
  });
  await c.refresh();
  c.newAccount();
  c.edit({ label: '追加', login: 'me@example.com', app_password: 'bad pass' });
  await c.save();
  assert.equal(inputs.length, 0);
  c.edit({ app_password: 'password' });
  await c.save();
  await c.save();
  assert.deepEqual(inputs, [ids.request, ids.request]);
  assert.equal(c.state().form?.baseline, 0);
  c.dispose();
  assert.equal(c.state().form, null);
});

test('switching accounts retains uncertain acceptance identity; terminal follow-up gets a fresh UUID', async () => {
  let sequence = 0,
    fail = true;
  const requests: string[] = [];
  const api: MailAPI = {
    ...setup().api,
    list: async () => [account(), account({ id: ids.other })],
    requestProbe: async (_id, input) => {
      requests.push(input.request_id);
      if (fail) {
        fail = false;
        throw new ApiError('dependency_unavailable');
      }
      return probe({ state: 'succeeded' });
    },
  };
  const c = createMailController(
    api,
    async () => 's',
    () => {},
    () => {},
    () => `30000000-0000-4000-8000-${String(++sequence).padStart(12, '0')}`,
  );
  await c.refresh();
  c.select(ids.account);
  await c.requestProbe();
  c.select(ids.other);
  c.select(ids.account);
  assert.equal(c.state().uncertain, true);
  await c.requestProbe();
  await c.requestProbe();
  assert.equal(requests[0], requests[1]);
  assert.notEqual(requests[1], requests[2]);
});
test('refreshing a newer account version invalidates previous disconnect confirmation', async () => {
  let version = 1,
    calls = 0;
  const { controller: c } = setup({
    list: async () => [account({ version })],
    disconnect: async () => {
      calls++;
      return account();
    },
  });
  await c.refresh();
  c.select(ids.account);
  c.confirmDisconnect(true);
  version = 2;
  await c.refresh();
  await c.disconnect();
  assert.equal(calls, 0);
});

test('folder save retains original edit revision and permits empty selection', async () => {
  let version = 1;
  const writes: { version: number; ids: string[] }[] = [];
  const folder = {
    id: ids.folder,
    name: '受信箱',
    delimiter: '/',
    selectable: true,
    available: true,
    selected: true,
  };
  const { controller: c } = setup({
    list: async () => [account({ version, state: 'ready', folders: [folder] })],
    saveFolders: async (_id, version, ids) => {
      writes.push({ version, ids });
      return account({
        state: 'ready',
        version: 3,
        folders: [{ ...folder, selected: false }],
      });
    },
  });
  await c.refresh();
  c.select(ids.account);
  c.toggleFolder(ids.folder, false);
  version = 2;
  await c.refresh();
  await c.saveFolders();
  assert.deepEqual(writes, [{ version: 1, ids: [] }]);
  assert.equal(c.state().form?.baseline, 3);
  assert.equal(c.state().form?.foldersDirty, false);
});
test('clicking the current account preserves dirty input', async () => {
  const { controller: c } = setup();
  await c.refresh();
  c.select(ids.account);
  c.edit({ login: 'me@example.com' });
  c.select(ids.account);
  assert.equal(c.state().form?.login, 'me@example.com');
  assert.equal(c.state().form?.credentialsDirty, true);
});

test('polling never interrupts an in-flight manual refresh with a pending probe', async () => {
  let reads = 0,
    polls = 0;
  let signal!: AbortSignal;
  let finish!: (accounts: ReturnType<typeof account>[]) => void;
  const terminal = probe({ state: 'succeeded' });
  const { controller: c } = setup({
    list: async (_token, currentSignal) => {
      if (++reads === 1) return [account({ latest_probe: probe() })];
      signal = currentSignal!;
      return await new Promise((resolve) => {
        finish = resolve;
      });
    },
    probe: async () => {
      polls++;
      return probe({ state: 'running' });
    },
  });
  await c.refresh();
  c.select(ids.account);
  c.edit({ label: '編集中', login: 'me@example.com' });
  const refreshing = c.refresh();
  await c.poll();
  assert.equal(signal.aborted, false);
  assert.equal(polls, 0);
  finish([account({ version: 2, state: 'ready', latest_probe: terminal })]);
  await refreshing;
  assert.equal(c.state().status, 'ready');
  assert.equal(c.state().accounts[0].latest_probe?.state, 'succeeded');
  assert.equal(c.state().form?.label, '編集中');
  assert.equal(c.state().form?.baseline, 1);
  c.confirmDisconnect(true);
  await c.disconnect();
  assert.equal(c.state().accounts[0].state, 'disconnected');
});

for (const terminalState of ['succeeded', 'failed'] as const) {
  test(`uncertain replay returning ${terminalState} refreshes account metadata and preserves edits`, async () => {
    let reads = 0;
    let finish!: (accounts: ReturnType<typeof account>[]) => void;
    let started!: () => void;
    const metadataStarted = new Promise<void>((resolve) => {
      started = resolve;
    });
    const requests: string[] = [];
    const terminal = probe({
      state: terminalState,
      error_code: terminalState === 'failed' ? 'authentication_failed' : '',
    });
    const folder = {
      id: ids.folder,
      name: '受信箱',
      delimiter: '/',
      selectable: true,
      available: true,
      selected: false,
    };
    const { controller: c } = setup({
      list: async () => {
        if (++reads === 1) return [account()];
        started();
        return await new Promise((resolve) => {
          finish = resolve;
        });
      },
      requestProbe: async (_id, input) => {
        requests.push(input.request_id);
        if (requests.length === 1) throw new ApiError('dependency_unavailable');
        return terminal;
      },
    });
    await c.refresh();
    c.select(ids.account);
    await c.requestProbe();
    assert.equal(c.state().uncertain, true);
    const replay = c.requestProbe();
    await Promise.race([metadataStarted, replay]);
    assert.equal(reads, 2);
    c.edit({ login: 'edited@example.com', app_password: 'newpassword' });
    finish([
      account({
        version: 2,
        state: terminalState === 'succeeded' ? 'ready' : 'needs_reauth',
        folders: terminalState === 'succeeded' ? [folder] : [],
        last_error: terminal.error_code,
        latest_probe: terminal,
      }),
    ]);
    await replay;
    assert.deepEqual(requests, [ids.request, ids.request]);
    assert.equal(c.state().uncertain, false);
    assert.equal(c.state().status, 'ready');
    assert.equal(
      c.state().accounts[0].state,
      terminalState === 'succeeded' ? 'ready' : 'needs_reauth',
    );
    assert.equal(
      c.state().accounts[0].folders.length,
      terminalState === 'succeeded' ? 1 : 0,
    );
    assert.equal(c.state().form?.login, 'edited@example.com');
    assert.equal(c.state().form?.app_password, 'newpassword');
    assert.equal(c.state().form?.baseline, 1);
  });
}

test('authorization loss while refreshing terminal probe metadata clears private state', async () => {
  for (const code of ['authentication_required', 'access_denied'] as const) {
    let reads = 0;
    const f = setup({
      list: async () => {
        if (++reads > 1) throw new ApiError(code);
        return [account()];
      },
      requestProbe: async () => probe({ state: 'succeeded' }),
    });
    await f.controller.refresh();
    f.controller.select(ids.account);
    await f.controller.requestProbe();
    assert.equal(f.controller.state().status, 'denied');
    assert.deepEqual(f.controller.state().accounts, []);
    assert.equal(f.controller.state().form, null);
    assert.equal(f.denied(), 1);
  }
});
