import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import type { MailProcessingAPI, PolicyInput } from './mail-processing-api.ts';
import { createProcessingPolicyController } from './mail-processing-policy-controller.ts';
import {
  processingIDs as ids,
  processingPolicy,
} from './mail-processing-fixtures.ts';
function setup() {
  let state = processingPolicy(),
    denied = 0,
    failure: string | null = null;
  const writes: PolicyInput[] = [];
  const api: Pick<MailProcessingAPI, 'policy' | 'savePolicy'> = {
    policy: async () => state,
    savePolicy: async (input: PolicyInput) => {
      writes.push(input);
      if (failure) throw new ApiError(failure as 'conflict');
      state = processingPolicy({
        policy: { ...input, version: input.expected_version + 1 },
      });
      return state;
    },
  };
  const controller = createProcessingPolicyController(
    api,
    async () => '',
    () => {},
    () => {
      denied++;
    },
  );
  return {
    controller,
    writes,
    setState: (next: typeof state) => {
      state = next;
    },
    fail: (code: string) => {
      failure = code;
    },
    denied: () => denied,
  };
}
test('policy loads read only and initial missing policy is disabled until both retentions are confirmed', async () => {
  const s = setup();
  s.setState(processingPolicy({ policy: null }));
  await s.controller.refresh();
  assert.equal(s.writes.length, 0);
  assert.equal(s.controller.state().form?.enabled, false);
  s.controller.edit({ enabled: true });
  await s.controller.save();
  assert.equal(s.writes.length, 0);
  s.controller.edit({
    primary_retention: '保持条件',
    secondary_retention: '保持条件',
    primary_confirmed: true,
    secondary_confirmed: true,
  });
  await s.controller.save();
  assert.equal(s.writes[0].expected_version, 0);
  assert.equal(s.writes[0].enabled, true);
});
test('dirty policy retains CAS version and profile across refresh and 409; discard explicitly adopts current active profile', async () => {
  const s = setup();
  await s.controller.refresh();
  s.controller.edit({ primary_retention: '編集中' });
  s.setState(
    processingPolicy({
      profile: { ...processingPolicy().profile!, id: ids.account },
      policy: null,
      runtime_version: 10,
    }),
  );
  await s.controller.refresh();
  assert.equal(s.controller.state().form?.primary_retention, '編集中');
  assert.equal(s.controller.state().form?.profile_id, ids.profile);
  assert.equal(s.controller.state().form?.expected_version, 3);
  assert.equal(s.controller.state().conflict, true);
  await s.controller.save();
  assert.equal(s.writes.length, 0);
  s.controller.discard();
  assert.equal(s.controller.state().form?.profile_id, ids.account);
  assert.equal(s.controller.state().form?.primary_retention, '');
  assert.equal(s.controller.state().form?.primary_confirmed, false);
});
test('stop uses saved policy and preserves dirty draft with its old CAS basis', async () => {
  const s = setup();
  const original = processingPolicy().policy!;
  s.setState(
    processingPolicy({
      policy: {
        ...original,
        enabled: true,
        primary_confirmed: true,
        secondary_confirmed: true,
      },
    }),
  );
  await s.controller.refresh();
  s.controller.edit({ primary_retention: '保存していない条件' });
  await s.controller.stop();
  assert.equal(s.writes[0].enabled, false);
  assert.equal(s.writes[0].primary_retention, original.primary_retention);
  assert.equal(
    s.controller.state().form?.primary_retention,
    '保存していない条件',
  );
  assert.equal(s.controller.state().form?.expected_version, 3);
  assert.equal(s.controller.state().conflict, true);
});
test('policy rejection keeps drafts, role loss clears everything and blocks future requests', async () => {
  const s = setup();
  await s.controller.refresh();
  s.controller.edit({ primary_retention: '編集中' });
  s.fail('conflict');
  await s.controller.save();
  assert.equal(s.controller.state().form?.primary_retention, '編集中');
  assert.equal(s.controller.state().conflict, true);
  for (const code of ['authentication_required', 'access_denied']) {
    const s = setup();
    await s.controller.refresh();
    s.fail(code);
    await s.controller.save();
    assert.equal(s.controller.state().current, null);
    assert.equal(s.controller.state().form, null);
    assert.equal(s.denied(), 1);
    await s.controller.refresh();
    assert.equal(s.controller.state().current, null);
  }
});
test('a policy conflict cannot silently disappear during a refresh of the same visible revision', async () => {
  const s = setup();
  await s.controller.refresh();
  s.controller.edit({ primary_retention: '編集中' });
  s.fail('conflict');
  await s.controller.save();
  await s.controller.refresh();
  assert.equal(s.controller.state().conflict, true);
  assert.equal(s.controller.state().form?.expected_version, 3);
});
test('policy writes deduplicate clicks, disposal aborts mutation and late success cannot restore private data', async () => {
  let resolve!: (value: ReturnType<typeof processingPolicy>) => void,
    writes = 0,
    signal: AbortSignal | undefined;
  const api = {
    policy: async () => processingPolicy(),
    savePolicy: async (
      _input: PolicyInput,
      _token: unknown,
      requestSignal?: AbortSignal,
    ) => {
      writes++;
      signal = requestSignal;
      return new Promise<ReturnType<typeof processingPolicy>>((r) => {
        resolve = r;
      });
    },
  };
  const controller = createProcessingPolicyController(
    api,
    async () => 'token',
    () => {},
    () => {},
  );
  await controller.refresh();
  const write = controller.save();
  await controller.save();
  assert.equal(writes, 1);
  controller.dispose();
  assert.equal(signal?.aborted, true);
  resolve(processingPolicy());
  await write;
  assert.equal(controller.state().status, 'disposed');
  assert.equal(controller.state().current, null);
});
