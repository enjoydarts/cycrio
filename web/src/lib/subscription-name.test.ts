import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import {
  createSubscriptionsAPI,
  parseSubscription,
} from './subscriptions-api.ts';
import { id, subscription, values } from './subscriptions-fixtures.ts';
import { createSubscriptionName } from './subscription-name.ts';
import { subscriptionTitle } from './subscription-title.ts';

test('display title uses the saved alias while canonical brand stays available', () => {
  const value = { service: 'Netflix', display_name: '  家族用  ' };
  assert.equal(subscriptionTitle(value), '家族用');
  assert.equal(value.service, 'Netflix');
  for (const display_name of [null, undefined, ' '])
    assert.equal(
      subscriptionTitle({ service: 'Netflix', display_name }),
      'Netflix',
    );
});

test('name controller validates, preserves failed input and reconciles uncertain saves without overwriting newer versions', async () => {
  let denied = 0,
    done = 0,
    calls = 0;
  const saved = { ...subscription(), display_name: '仕事用', version: 2 };
  const controller = createSubscriptionName(
    {
      rename: async () => {
        calls++;
        throw new ApiError('dependency_unavailable');
      },
      get: async () => saved,
    },
    async () => 't',
    subscription(),
    () => {},
    () => {
      done++;
    },
    () => {
      denied++;
    },
  );
  controller.edit('x'.repeat(201));
  await controller.save();
  assert.equal(controller.state().error, 'invalid_request');
  assert.equal(calls, 0);
  controller.edit('仕事用');
  await controller.save();
  assert.equal(controller.state().name, '仕事用');
  assert.equal(controller.state().uncertain, true);
  controller.edit('different');
  assert.equal(controller.state().name, '仕事用');
  await controller.reconcile();
  assert.equal(calls, 1);
  assert.equal(done, 1);
  assert.equal(denied, 0);
});

test('name controller locks stale writes and clears names after permission loss', async () => {
  for (const code of ['conflict', 'access_denied'] as const) {
    let denied = false;
    const controller = createSubscriptionName(
      {
        rename: async () => {
          throw new ApiError(code);
        },
        get: async () => subscription(),
      },
      async () => 't',
      subscription(),
      () => {},
      () => {
        assert.fail('must not save');
      },
      () => {
        denied = true;
      },
    );
    controller.edit('仕事用');
    await controller.save();
    assert.equal(controller.state().error, code);
    if (code === 'access_denied') {
      assert.equal(denied, true);
      assert.equal(controller.state().name, '');
    } else {
      assert.equal(controller.state().name, '仕事用');
      assert.equal(controller.state().conflict, true);
    }
  }
});

test('subscription names survive parsing and dedicated updates only send name and concurrency fields', async () => {
  const calls: { url: string; body: any; method: string | undefined }[] = [];
  const api = createSubscriptionsAPI('', async (url, init) => {
    calls.push({
      url: String(url),
      body: JSON.parse(String(init?.body)),
      method: init?.method,
    });
    return Response.json({
      ...subscription(),
      display_name: '仕事用',
      version: 2,
    });
  });
  assert.equal(
    parseSubscription({ ...subscription(), display_name: '仕事用' })
      .display_name,
    '仕事用',
  );
  assert.equal(
    parseSubscription({ ...subscription(), display_name: null }).display_name,
    null,
  );
  const saved = await api.rename(
    id,
    { display_name: ' 仕事用 ', expected_version: 1, confirmed: true },
    async () => 'token',
  );
  assert.equal(saved.display_name, '仕事用');
  assert.deepEqual(calls[0], {
    url: `/v1/subscriptions/${id}/name`,
    method: 'PUT',
    body: { display_name: '仕事用', expected_version: 1, confirmed: true },
  });
  await api.rename(
    id,
    { display_name: '  ', expected_version: 2, confirmed: true },
    async () => 'token',
  );
  assert.equal(calls[1].body.display_name, null);
  await api.update(
    id,
    {
      ...values,
      display_name: 'omit',
      expected_version: 2,
      confirmed: true,
    } as never,
    async () => 'token',
  );
  assert.equal(calls[2].body.display_name, undefined);
  for (const display_name of ['x'.repeat(201), 'bad\u0000name', 'bad\nname']) {
    await assert.rejects(
      api.rename(
        id,
        { display_name, expected_version: 2, confirmed: true },
        async () => 'token',
      ),
      { code: 'invalid_request' },
    );
  }
  assert.equal(calls.length, 3);
});

test('a rejected name update is never interpreted as success', async () => {
  const api = createSubscriptionsAPI('', async () =>
    Response.json({}, { status: 403 }),
  );
  await assert.rejects(
    api.rename(
      id,
      { display_name: '仕事用', expected_version: 1, confirmed: true },
      async () => 'token',
    ),
    ApiError,
  );
});
