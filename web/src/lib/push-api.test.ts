import assert from 'node:assert/strict';
import test from 'node:test';
import {
  createPushAPI,
  parsePushPage,
  deriveRevocationToken,
} from './push-api.ts';
const id = '11111111-1111-4111-8111-111111111111';
test('端末一覧は公開契約と準備理由を厳密に検証する', () => {
  assert.equal(
    parsePushPage({ app_id: id, available: true, reason: '', devices: [] })
      .app_id,
    id,
  );
  for (const patch of [
    { available: 'true' },
    { reason: 'unknown' },
    { app_id: '' },
    {
      devices: [
        {
          id,
          state: 'unknown',
          created_at: '2026-11-10T00:00:00Z',
          updated_at: '2026-11-10T00:00:00Z',
        },
      ],
    },
  ])
    assert.throws(() =>
      parsePushPage({
        app_id: id,
        available: true,
        reason: '',
        devices: [],
        ...patch,
      }),
    );
});
test('解除tokenはGoと同じ領域分離・正規base64urlで導出する', async () => {
  const secret = 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA';
  const { createHash } = await import('node:crypto');
  assert.equal(
    await deriveRevocationToken(secret),
    createHash('sha256')
      .update('cycrio-push-revocation/v1\x00' + secret)
      .digest('base64url'),
  );
  await assert.rejects(deriveRevocationToken(secret + '='));
});
test('解除capabilityの送信は認証やcookieを使わない', async () => {
  const calls: any[] = [];
  const api = createPushAPI('/api', async (url, init) => {
    calls.push({ url, init });
    return new Response(null, { status: 204 });
  });
  await api.revoke({
    device_id: id,
    revocation_token: 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA',
  });
  assert.equal(calls[0].url, '/api/v1/push-device-revocations');
  assert.equal(calls[0].init.credentials, 'omit');
  assert.equal(calls[0].init.headers.Authorization, undefined);
  assert.deepEqual(JSON.parse(calls[0].init.body), {
    device_id: id,
    revocation_token: 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA',
  });
});
