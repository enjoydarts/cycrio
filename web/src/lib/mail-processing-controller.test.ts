import assert from 'node:assert/strict';
import test from 'node:test';
import { ApiError } from './api.ts';
import type {
  MailProcessingAPI,
  ProcessingInput,
  ProcessingRetryInput,
} from './mail-processing-api.ts';
import {
  createMailProcessingController,
  type ProcessingRequests,
} from './mail-processing-controller.ts';
import {
  processingIDs as ids,
  processingPreview,
  processingRun,
  processingResult,
} from './mail-processing-fixtures.ts';
const deferred = <T>() => {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
};
function setup(
  patch: Partial<MailProcessingAPI> = {},
  pending: ProcessingRequests = new Map(),
) {
  const calls: {
    operation: string;
    input?: ProcessingInput | ProcessingRetryInput;
  }[] = [];
  let denied = 0;
  const api: MailProcessingAPI = {
    preview: async () => {
      calls.push({ operation: 'preview' });
      return processingPreview();
    },
    runs: async () => [],
    results: async () => [processingResult()],
    start: async (_id, input) => {
      calls.push({ operation: 'start', input });
      return processingRun();
    },
    retry: async (_id, _message, input) => {
      calls.push({ operation: 'retry', input });
      return processingRun();
    },
    cancel: async () => {
      calls.push({ operation: 'cancel' });
      return processingRun({ state: 'cancelled' });
    },
    policy: async () => {
      throw Error();
    },
    savePolicy: async () => {
      throw Error();
    },
    ...patch,
  };
  const controller = createMailProcessingController(
    api,
    async () => 'token',
    () => {},
    () => {
      denied++;
    },
    () => ids.request,
    pending,
  );
  controller.setAccount(ids.account);
  return { controller, calls, pending, denied: () => denied };
}
test('explicit selected result and run work beyond legacy latest100/latest20 collections', async () => {
  const s = setup({ results: async () => [], runs: async () => [] });
  await s.controller.refresh();
  await s.controller.retrySelected(
    processingResult({ state: 'unknown' }),
    true,
    false,
  );
  assert.equal(s.calls.filter((x) => x.operation === 'retry').length, 0);
  await s.controller.retrySelected(
    processingResult({ state: 'unknown' }),
    true,
    true,
  );
  assert.equal(s.calls.filter((x) => x.operation === 'retry').length, 1);
  await s.controller.cancelSelected(processingRun());
  assert.equal(s.calls.filter((x) => x.operation === 'cancel').length, 1);
});
test('result-first preview mode never reads legacy capped collections or automatically posts', async () => {
  const calls: string[] = [];
  const s = setup();
  const api = {
    preview: async () => {
      calls.push('preview');
      return processingPreview();
    },
    runs: async () => {
      throw Error('legacy runs forbidden');
    },
    results: async () => {
      throw Error('legacy results forbidden');
    },
  } as unknown as MailProcessingAPI;
  const c = createMailProcessingController(
    api,
    async () => 't',
    () => {},
    () => {},
    () => ids.request,
    new Map(),
    true,
  );
  c.setAccount(ids.account);
  await c.refresh();
  assert.equal(c.state().status, 'ready');
  assert.deepEqual(calls, ['preview']);
  s.controller.dispose();
  c.dispose();
});
test('clearing the account removes consent and aborts a preview that cannot later restore its data', async () => {
  const delayed = deferred<ReturnType<typeof processingPreview>>();
  let reads = 0,
    signal: AbortSignal | undefined;
  const s = setup({
    preview: async (_account, _token, currentSignal) => {
      if (++reads === 1) return processingPreview();
      signal = currentSignal;
      return delayed.promise;
    },
  });
  await s.controller.refresh();
  s.controller.edit({ max_messages: '1', confirmed: true });
  s.controller.setAccount('');
  assert.equal(s.controller.state().preview, null);
  assert.equal(s.controller.state().form.confirmed, false);
  assert.equal(s.controller.state().form.max_messages, '');
  await s.controller.start();
  assert.equal(s.calls.filter((c) => c.operation === 'start').length, 0);
  s.controller.setAccount(ids.account);
  const pending = s.controller.refresh();
  s.controller.setAccount('');
  assert.equal(signal?.aborted, true);
  delayed.resolve(processingPreview());
  await pending;
  assert.equal(s.controller.state().account, '');
  assert.equal(s.controller.state().preview, null);
  assert.equal(s.controller.state().form.confirmed, false);
  await s.controller.start();
  assert.equal(s.calls.filter((c) => c.operation === 'start').length, 0);
  s.controller.dispose();
});
test('restoring an uncertain request can replay its frozen body when the fresh preview is unavailable', async () => {
  const requests: ProcessingRequests = new Map();
  let original: ProcessingInput | undefined;
  const first = setup(
    {
      start: async (_account, input) => {
        original = input;
        throw new ApiError('dependency_unavailable');
      },
    },
    requests,
  );
  await first.controller.refresh();
  first.controller.edit({ max_messages: '1', confirmed: true });
  await first.controller.start();
  first.controller.dispose();
  const restored = setup(
    {
      preview: async () => {
        throw new ApiError('dependency_unavailable');
      },
    },
    requests,
  );
  await restored.controller.refresh();
  assert.equal(restored.controller.state().preview, null);
  assert.equal(restored.controller.state().uncertain, true);
  await restored.controller.start();
  assert.equal(restored.calls.filter((c) => c.operation === 'start').length, 0);
  await restored.controller.replay();
  assert.deepEqual(
    restored.calls.filter((c) => c.operation === 'start').map((c) => c.input),
    [original],
  );
  assert.equal(requests.size, 0);
  restored.controller.dispose();
});
test('mount GETs only; explicit confirmed start suppresses double click and polling only GETs', async () => {
  const wait = deferred<ReturnType<typeof processingRun>>();
  let accepted = false;
  const s = setup({
    runs: async () => (accepted ? [processingRun()] : []),
    start: async (_id, input) => {
      s.calls.push({ operation: 'start', input });
      const run = await wait.promise;
      accepted = true;
      return run;
    },
  });
  await s.controller.refresh();
  assert.deepEqual(
    s.calls.map((x) => x.operation),
    ['preview'],
  );
  s.controller.edit({ max_messages: '2' });
  await s.controller.start();
  assert.equal(s.calls.length, 1);
  s.controller.edit({ confirmed: true });
  const first = s.controller.start();
  await s.controller.start();
  assert.equal(s.calls.filter((x) => x.operation === 'start').length, 1);
  wait.resolve(processingRun());
  await first;
  await s.controller.poll();
  assert.equal(s.calls.filter((x) => x.operation === 'start').length, 1);
  await s.controller.cancel(ids.run);
  assert.equal(
    s.calls.some((x) => x.operation === 'cancel'),
    true,
  );
});
test('response loss retains the complete input and UUID through account switching and only explicit replay resends', async () => {
  let lose = true;
  const pending: ProcessingRequests = new Map();
  const s = setup(
    {
      start: async (_id, input) => {
        s.calls.push({ operation: 'start', input: { ...input } });
        if (lose) throw new ApiError('dependency_unavailable');
        return processingRun();
      },
    },
    pending,
  );
  await s.controller.refresh();
  s.controller.edit({ max_messages: '2', confirmed: true });
  await s.controller.start();
  assert.equal(s.controller.state().uncertain, true);
  const original = s.calls.at(-1)?.input;
  await s.controller.start();
  await s.controller.poll();
  assert.equal(s.calls.filter((x) => x.operation === 'start').length, 1);
  s.controller.setAccount(ids.profile);
  s.controller.setAccount(ids.account);
  assert.equal(s.controller.state().uncertain, true);
  lose = false;
  await s.controller.replay();
  assert.deepEqual(
    s.calls.filter((x) => x.operation === 'start').map((x) => x.input),
    [original, original],
  );
  assert.equal(pending.size, 0);
});
test('409 and background profile changes retain form and original basis until explicit discard; clear old confirmation', async () => {
  let version = 2;
  const s = setup({
    preview: async () => processingPreview({ runtime_version: version }),
    start: async () => {
      throw new ApiError('conflict');
    },
  });
  await s.controller.refresh();
  s.controller.edit({ max_messages: '2', confirmed: true });
  await s.controller.start();
  version = 8;
  await s.controller.refresh();
  assert.equal(s.controller.state().form.max_messages, '2');
  assert.equal(s.controller.state().form.basis?.runtime_version, 2);
  assert.equal(s.controller.state().conflict, true);
  s.controller.discard();
  assert.equal(s.controller.state().form.basis?.runtime_version, 8);
  assert.equal(s.controller.state().form.confirmed, false);
});
test('individual unknown retry requires renewed charge and unknown confirmations and binds latest result', async () => {
  const s = setup();
  await s.controller.refresh();
  await s.controller.retryResult(ids.result, true, false);
  assert.equal(
    s.calls.some((x) => x.operation === 'retry'),
    false,
  );
  await s.controller.retryResult(ids.result, true, true);
  const input = s.calls.at(-1)?.input as ProcessingRetryInput;
  assert.equal(input.expected_result_id, ids.result);
  assert.equal(input.max_messages, 1);
  assert.equal(input.confirm_unknown, true);
});
test('401/403 erase form, preview, results and all scope requests; dispose rejects late GET', async () => {
  for (const code of ['authentication_required', 'access_denied'] as const) {
    const s = setup({
      start: async () => {
        throw new ApiError(code);
      },
    });
    await s.controller.refresh();
    s.controller.edit({ max_messages: '2', confirmed: true });
    await s.controller.start();
    assert.equal(s.denied(), 1);
    assert.equal(s.controller.state().preview, null);
    assert.deepEqual(s.controller.state().results, []);
    assert.equal(s.controller.state().form.max_messages, '');
    assert.equal(s.pending.size, 0);
  }
  const wait = deferred<ReturnType<typeof processingPreview>>();
  const s = setup({ preview: async () => wait.promise });
  const loading = s.controller.refresh();
  s.controller.dispose();
  wait.resolve(processingPreview());
  await loading;
  assert.equal(s.controller.state().status, 'disposed');
  assert.equal(s.controller.state().preview, null);
});
test('older account reads cannot restore private state and polling never interrupts an in-flight manual read', async () => {
  const wait = deferred<ReturnType<typeof processingPreview>>();
  let signals: (AbortSignal | undefined)[] = [];
  const s = setup({
    preview: async (_id, _token, signal) => {
      signals.push(signal);
      return signals.length === 1
        ? wait.promise
        : processingPreview({ account_id: ids.profile });
    },
  });
  const read = s.controller.refresh();
  await s.controller.poll();
  assert.equal(signals.length, 1);
  s.controller.setAccount(ids.profile);
  assert.equal(signals[0]?.aborted, true);
  await s.controller.refresh();
  wait.resolve(processingPreview());
  await read;
  assert.equal(s.controller.state().preview?.account_id, ids.profile);
  assert.equal(s.controller.state().account, ids.profile);
});
test('definitive rejection releases pending identity while response loss from individual retry replays its exact latest-result body', async () => {
  for (const code of ['invalid_request', 'not_found'] as const) {
    const s = setup({
      start: async () => {
        throw new ApiError(code);
      },
    });
    await s.controller.refresh();
    s.controller.edit({ max_messages: '1', confirmed: true });
    await s.controller.start();
    assert.equal(s.pending.size, 0);
    assert.equal(s.controller.state().uncertain, false);
  }
  let first = true;
  const seen: ProcessingRetryInput[] = [];
  const s = setup({
    retry: async (_id, _message, input) => {
      seen.push({ ...input });
      if (first) {
        first = false;
        throw new ApiError('dependency_unavailable');
      }
      return processingRun();
    },
  });
  await s.controller.refresh();
  await s.controller.retryResult(ids.result, true, true);
  await s.controller.replay();
  assert.equal(seen.length, 2);
  assert.deepEqual(seen[0], seen[1]);
  assert.equal(seen[0].expected_result_id, ids.result);
});
test('a changed condition invalidates the prior charge confirmation and a completed stop refreshes preview and results', async () => {
  let version = 2,
    reads = 0;
  const s = setup({
    preview: async () => {
      reads++;
      return processingPreview({ runtime_version: version });
    },
    runs: async () => (reads > 1 ? [processingRun()] : []),
  });
  await s.controller.refresh();
  s.controller.edit({ max_messages: '1', confirmed: true });
  version = 3;
  await s.controller.refresh();
  assert.equal(s.controller.state().form.confirmed, false);
  assert.equal(s.controller.state().conflict, true);
  const prior = reads;
  await s.controller.cancel(ids.run);
  assert.equal(reads, prior + 1);
});
test('switching during POST retains uncertain acceptance across disposal/remount and ignores late success from the old account', async () => {
  const pending: ProcessingRequests = new Map(),
    wait = deferred<ReturnType<typeof processingRun>>();
  const s = setup({ start: async () => wait.promise }, pending);
  await s.controller.refresh();
  s.controller.edit({ max_messages: '2', confirmed: true });
  const posting = s.controller.start();
  s.controller.setAccount(ids.profile);
  wait.resolve(processingRun());
  await posting;
  assert.deepEqual(s.controller.state().runs, []);
  assert.equal(s.controller.state().account, ids.profile);
  assert.equal(pending.size, 1);
  s.controller.dispose();
  const remount = setup({}, pending);
  assert.equal(remount.controller.state().uncertain, true);
  await remount.controller.replay();
  assert.equal(pending.size, 0);
  assert.equal(remount.calls.filter((x) => x.operation === 'start').length, 1);
});
