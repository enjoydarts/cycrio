import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { account, ids, probe } from './mail-test-fixtures.ts';
import type { MailAPI, MailAccount } from './mail-api.ts';
import { createIntakeController } from './intake-controller.ts';

const folder = {
  id: ids.folder,
  name: 'INBOX',
  display_name: '受信トレイ',
  delimiter: '/',
  selectable: true,
  available: true,
  selected: false,
};
function fixture(
  patch: Partial<MailAPI> = {},
  initial: MailAccount = account({ state: 'ready', folders: [folder] }),
) {
  const api: MailAPI = {
    list: async () => [initial],
    save: async () => initial,
    disconnect: async () => initial,
    requestProbe: async () => probe(),
    probe: async () => probe(),
    saveFolders: async () =>
      account({
        ...initial,
        version: 2,
        folders: [{ ...folder, selected: true }],
      }),
    ...patch,
  };
  const published: unknown[] = [];
  let denied = 0;
  const controller = createIntakeController(
    api,
    async () => 'token',
    (v) => published.push(v),
    () => denied++,
    ids.account,
  );
  return { controller, published, denied: () => denied };
}

test('intake reads connections without probing or sending mail, and never skips an unready connection', async () => {
  let probes = 0,
    saves = 0;
  const f = fixture(
    {
      requestProbe: async () => {
        probes++;
        return probe();
      },
      saveFolders: async () => {
        saves++;
        return account();
      },
    },
    account(),
  );
  assert.equal(f.published.length, 0);
  await f.controller.refresh();
  assert.equal(f.controller.state().step, 1);
  assert.equal(await f.controller.next(), true);
  assert.equal(f.controller.state().step, 2);
  assert.equal(await f.controller.next(), false);
  assert.equal(f.controller.state().issue, 'connect_first');
  assert.equal(probes, 0);
  assert.equal(saves, 0);
});

test('intake saves the selected folders at their original version before exposing consent', async () => {
  let received: unknown = null;
  const f = fixture({
    saveFolders: async (id, version, folders) => {
      received = { id, version, folders };
      return account({
        state: 'ready',
        version: 2,
        folders: [{ ...folder, selected: true }],
      });
    },
  });
  await f.controller.refresh();
  await f.controller.next();
  assert.equal(await f.controller.next(), false);
  assert.equal(f.controller.state().issue, 'select_folder');
  f.controller.toggleFolder(ids.folder, true);
  assert.equal(await f.controller.next(), true);
  assert.deepEqual(received, {
    id: ids.account,
    version: 1,
    folders: [ids.folder],
  });
  assert.equal(f.controller.state().step, 3);
  f.controller.freeze(true);
  assert.equal(f.controller.back(), false);
  assert.equal(f.controller.choose(ids.other), false);
  assert.equal(f.controller.hasUnsaved(), true);
  f.controller.freeze(false);
  assert.equal(f.controller.started(ids.other), false);
  assert.equal(f.controller.started(ids.account), true);
  assert.equal(f.controller.state().accepted, ids.account);
});

test('an unknown folder write remains blocked until the owner explicitly adopts a fresh stored state', async () => {
  let reads = 0;
  const fresh = account({
    state: 'ready',
    version: 2,
    folders: [{ ...folder, selected: true }],
  });
  const f = fixture({
    list: async () => [
      ++reads === 1 ? account({ state: 'ready', folders: [folder] }) : fresh,
    ],
    saveFolders: async () => {
      throw new ApiError('dependency_unavailable');
    },
  });
  await f.controller.refresh();
  await f.controller.next();
  f.controller.toggleFolder(ids.folder, true);
  assert.equal(await f.controller.next(), false);
  assert.equal(f.controller.state().folderUncertain, true);
  assert.equal(f.controller.state().mail.form?.baseline, 1);
  assert.equal(f.controller.back(), false);
  await f.controller.refresh();
  assert.equal(f.controller.state().folderUncertain, true);
  assert.equal(f.controller.state().mail.form?.baseline, 1);
  await f.controller.adoptStoredFolders();
  assert.equal(f.controller.state().folderUncertain, false);
  assert.equal(f.controller.state().mail.form?.baseline, 2);
  assert.equal(await f.controller.next(), true);
});

test('dirty folder selection survives back and requires explicit discard before switching connection', async () => {
  const other = account({
    id: ids.other,
    label: '別のメール',
    state: 'ready',
    folders: [],
  });
  const f = fixture({
    list: async () => [account({ state: 'ready', folders: [folder] }), other],
  });
  await f.controller.refresh();
  await f.controller.next();
  f.controller.toggleFolder(ids.folder, true);
  assert.equal(f.controller.back(), true);
  assert.equal(f.controller.choose(ids.other), false);
  assert.equal(f.controller.state().issue, 'confirm_switch');
  assert.deepEqual(f.controller.state().mail.form?.folder_ids, [ids.folder]);
  assert.equal(f.controller.choose(ids.other, true), true);
  assert.equal(f.controller.state().mail.selected, ids.other);
  assert.equal(f.controller.state().mail.form?.foldersDirty, false);
});

test('an ambiguous connection probe replays the same request and cannot be replaced by another operation', async () => {
  const requests: unknown[] = [];
  const f = fixture(
    {
      requestProbe: async (_id, input) => {
        requests.push(input);
        if (requests.length === 1) throw new ApiError('dependency_unavailable');
        return probe();
      },
    },
    account(),
  );
  await f.controller.refresh();
  await f.controller.next();
  await f.controller.probe();
  assert.equal(f.controller.state().mail.uncertain, true);
  assert.equal(f.controller.back(), false);
  await f.controller.probe();
  assert.equal(requests.length, 2);
  assert.deepEqual(requests[0], requests[1]);
});

test('denial purges connection state and ignores disposed or late reads', async () => {
  const f = fixture({
    list: async () => {
      throw new ApiError('access_denied');
    },
  });
  await f.controller.refresh();
  assert.equal(f.controller.state().mail.status, 'denied');
  assert.deepEqual(f.controller.state().mail.accounts, []);
  assert.equal(f.denied(), 1);
  let resolve!: (value: MailAccount[]) => void;
  const g = fixture({ list: () => new Promise((r) => (resolve = r)) });
  const pending = g.controller.refresh();
  g.controller.dispose();
  const count = g.published.length;
  resolve([account()]);
  await pending;
  assert.equal(g.published.length, count);
});

test('edited consent survives back and requires explicit discard before switching accounts', async () => {
  const other = account({ id: ids.other, state: 'ready' });
  const f = fixture({
    list: async () => [
      account({ state: 'ready', folders: [{ ...folder, selected: true }] }),
      other,
    ],
  });
  await f.controller.refresh();
  await f.controller.next();
  await f.controller.next();
  f.controller.editConsent(true);
  assert.equal(f.controller.hasUnsaved(), true);
  assert.equal(f.controller.back(), true);
  assert.equal(f.controller.back(), true);
  assert.equal(f.controller.choose(ids.account), true);
  assert.equal(f.controller.hasUnsaved(), true);
  assert.equal(f.controller.choose(ids.other), false);
  assert.equal(f.controller.choose(ids.other, true), true);
  assert.equal(f.controller.hasUnsaved(), false);
});
