import assert from 'node:assert/strict';
import test from 'node:test';
import { createSessionBoundary } from './app-session.ts';

test('verified owner and role keep their scope and token provider during background refresh', async () => {
  const boundary = createSessionBoundary(async () => 'fresh');
  boundary.update({ status: 'allowed', user: { id: 'one', role: 'user' } });
  const first = boundary.current();
  boundary.update({ status: 'allowed', user: { id: 'one', role: 'user' } });
  assert.equal(boundary.current().scope, first.scope);
  assert.equal(boundary.current().token, first.token);
  assert.equal(await first.token(), 'fresh');
});

test('owner, role loss and explicit checking invalidate old token providers immediately', async () => {
  for (const next of [
    { status: 'allowed', user: { id: 'two', role: 'platform_admin' } },
    { status: 'allowed', user: { id: 'one', role: 'user' } },
    { status: 'checking' },
    { status: 'signed_out' },
  ] as const) {
    const boundary = createSessionBoundary(async () => 'secret');
    boundary.update({
      status: 'allowed',
      user: { id: 'one', role: 'platform_admin' },
    });
    const old = boundary.current();
    boundary.update(next);
    assert.notEqual(boundary.current().scope, old.scope);
    assert.equal(await old.token(), null);
  }
});

test('late token response cannot cross session boundary or restore a previous scope', async () => {
  let resolve!: (token: string) => void;
  const boundary = createSessionBoundary(
    () =>
      new Promise<string>((done) => {
        resolve = done;
      }),
  );
  const allowed = {
    status: 'allowed',
    user: { id: 'one', role: 'user' },
  } as const;
  boundary.update(allowed);
  const first = boundary.current();
  const pending = first.token();
  boundary.update({ status: 'checking' });
  boundary.update(allowed);
  resolve('old');
  assert.equal(await pending, null);
  assert.notEqual(boundary.current().scope, first.scope);
});
