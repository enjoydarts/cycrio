import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import { id, subscription } from './subscriptions-fixtures.ts';
import { changePreview } from './subscription-changes-fixtures.ts';
import {
  canSchedulePreview,
  createScheduledChange,
  createScheduledCancellation,
} from './scheduled-change-controller.ts';
import type {
  ScheduledChange,
  ScheduledInput,
} from './subscription-scheduled-changes-api.ts';

const preview = () => ({
  ...changePreview(),
  scheduled_for: '2027-01-01',
  source: {
    ...changePreview().source,
    original_event: {
      ...changePreview().source.original_event,
      effective_date: '2027-01-01',
    },
  },
});
test('schedule entry requires an explicit future effective date rather than a warning or model inference', () => {
  assert.equal(canSchedulePreview(preview()), true);
  assert.equal(canSchedulePreview(changePreview()), false);
  assert.equal(
    canSchedulePreview({ ...preview(), as_of: '2027-01-01' }),
    false,
  );
  assert.equal(
    canSchedulePreview({
      ...changePreview(),
      warnings: [{ code: 'future_effective' }],
    }),
    false,
  );
});
test('scheduled approval requires separate date consent and preserves the entire request after unknown acceptance', async () => {
  const calls: ScheduledInput[] = [];
  const controller = createScheduledChange(
    {
      preview: async () => preview(),
      get: async () => subscription(),
      apply: async (_, body) => {
        calls.push(structuredClone(body));
        throw new ApiError('dependency_unavailable');
      },
    },
    async () => 't',
    subscription(),
    { source_result_id: id, source_event_index: 0 },
    () => {},
    () => {},
    () => {},
    () => id,
  );
  await controller.preview();
  controller.select('amount', true);
  controller.confirm(true);
  await controller.apply();
  assert.equal(calls.length, 0);
  controller.confirmDate(true);
  controller.confirm(true);
  await controller.apply();
  assert.equal(controller.state().uncertain, true);
  controller.confirmDate(false);
  controller.select('currency', true);
  await controller.preview();
  await controller.retry();
  assert.equal(calls.length, 2);
  assert.deepEqual(calls[1], calls[0]);
  assert.equal(calls[0].effective_on, '2027-01-01');
  assert.equal(controller.state().dateConfirmed, true);
  controller.dispose();
});
test('date consent resets after conflict and refreshed review, denied clears evidence', async () => {
  let denied = 0,
    error = 'conflict';
  const controller = createScheduledChange(
    {
      preview: async () => preview(),
      get: async () => subscription(),
      apply: async () => {
        throw new ApiError(error as 'conflict');
      },
    },
    async () => 't',
    subscription(),
    { source_result_id: id, source_event_index: 0 },
    () => {},
    () => {},
    () => denied++,
    () => id,
  );
  await controller.preview();
  controller.select('amount', true);
  controller.confirmDate(true);
  controller.confirm(true);
  await controller.apply();
  assert.equal(controller.state().dateConfirmed, false);
  await controller.preview();
  error = 'access_denied';
  controller.select('amount', true);
  controller.confirmDate(true);
  controller.confirm(true);
  await controller.apply();
  assert.equal(controller.state().preview, null);
  assert.equal(denied, 1);
  assert.equal(controller.state().dateConfirmed, false);
});
test('uncertain cancellation freezes identity and abort/dispose ignores late completion', async () => {
  const calls: unknown[] = [];
  let count = 0,
    done = 0;
  const cancel = createScheduledCancellation(
    {
      cancel: async (...args) => {
        calls.push(args.slice(0, 3));
        if (++count === 1) throw new ApiError('dependency_unavailable');
        return {} as ScheduledChange;
      },
    },
    async () => 't',
    { id, subscription_id: id, version: 3 } as ScheduledChange,
    () => {},
    () => done++,
    () => {},
  );
  await cancel.submit();
  assert.equal(cancel.state().uncertain, true);
  await cancel.submit();
  assert.deepEqual(calls[0], calls[1]);
  assert.equal(done, 1);
  let signal: AbortSignal | undefined, resolve!: (s: ScheduledChange) => void;
  const late = createScheduledCancellation(
    {
      cancel: async (_id, _schedule, _version, _token, s) => {
        signal = s;
        return new Promise((r) => (resolve = r));
      },
    },
    async () => 't',
    { id, subscription_id: id, version: 3 } as ScheduledChange,
    () => {},
    () => done++,
    () => {},
  );
  const pending = late.submit();
  late.dispose();
  assert.equal(signal?.aborted, true);
  resolve({} as ScheduledChange);
  await pending;
  assert.equal(done, 1);
});
