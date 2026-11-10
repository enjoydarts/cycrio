import { ApiError, type TokenProvider } from './api.ts';
import {
  deriveRevocationToken,
  pushSecret,
  type PushAPI,
  type PushCapability,
  type PushPage,
  type PushRegistration,
} from './push-api.ts';
import type { PushBrowser } from './push-browser.ts';
import { uuid } from './daily-api.ts';
export type PushStorage = Pick<
  Storage,
  'length' | 'key' | 'getItem' | 'setItem' | 'removeItem'
>;
type Receipt = PushCapability & {
  owner: string;
  operation: string;
  stop: boolean;
};
export type PushView = {
  page: PushPage | null;
  status:
    | 'idle'
    | 'preparing'
    | 'permission_ready'
    | 'registering'
    | 'waiting'
    | 'confirming'
    | 'active'
    | 'disabled'
    | 'unsupported'
    | 'home_screen'
    | 'denied'
    | 'error'
    | 'uncertain'
    | 'reload';
  busy: boolean;
  cleanupPending: boolean;
  currentDevice: string | null;
};
const ownerKey = 'cycrio.push.owner.v1',
  prefix = 'cycrio.push.receipt.v1.';
export function createPushController({
  api,
  sdk,
  storage,
  now = Date.now,
  denied = () => {},
}: {
  api: PushAPI;
  sdk: PushBrowser;
  storage: PushStorage;
  now?: () => number;
  denied?: () => void;
}) {
  let owner: string | null = null,
    token: TokenProvider = async () => null,
    epoch = '',
    generation = 0;
  let bound = false;
  let endScope: () => Promise<void> = async () => {};
  async function rejectAccess(error: unknown) {
    if (
      error instanceof ApiError &&
      ['authentication_required', 'access_denied'].includes(error.code)
    ) {
      await endScope();
      denied();
      return true;
    }
    return false;
  }
  let view: PushView = {
    page: null,
    status: 'idle',
    busy: false,
    cleanupPending: false,
    currentDevice: null,
  };
  const listeners = new Set<(view: PushView) => void>();
  type Attempt = {
    owner: string;
    epoch: string;
    generation: number;
    operation: string;
    token: TokenProvider;
    started: number;
    registration?: PushRegistration;
    confirming: boolean;
    sent: boolean;
  };
  let attempt: Attempt | undefined;
  const buffer: {
    data: Record<string, unknown>;
    at: number;
    attempt: Attempt;
  }[] = [];
  const publish = (patch: Partial<PushView>) => {
    view = { ...view, ...patch };
    listeners.forEach((fn) => fn(view));
  };
  function sharedOwner(): { owner: string | null; epoch: string } | null {
    const raw = storage.getItem(ownerKey);
    if (!raw) return null;
    try {
      return JSON.parse(raw);
    } catch {
      return null;
    }
  }
  function current(a: Attempt) {
    const s = sharedOwner();
    return (
      owner === a.owner &&
      generation === a.generation &&
      s?.owner === a.owner &&
      s?.epoch === a.epoch
    );
  }
  function receipts(): Receipt[] {
    const result: Receipt[] = [];
    for (let i = 0; i < storage.length; i++) {
      const key = storage.key(i);
      if (!key?.startsWith(prefix)) continue;
      try {
        const v = JSON.parse(storage.getItem(key) ?? 'null');
        uuid(v.owner);
        uuid(v.operation);
        uuid(v.device_id);
        pushSecret(v.revocation_token);
        if (typeof v.stop === 'boolean') result.push(v);
      } catch {
        /* 壊れた記録を解除証明として使わない。 */
      }
    }
    return result;
  }
  function save(r: Receipt) {
    storage.setItem(prefix + r.operation, JSON.stringify(r));
  }
  async function revoke(r: Receipt, keep = false) {
    const stopped = { ...r, stop: true };
    save(stopped);
    try {
      await api.revoke(stopped);
      if (!keep) storage.removeItem(prefix + r.operation);
      return true;
    } catch {
      publish({ cleanupPending: true });
      return false;
    }
  }
  async function cleanup() {
    const shared = sharedOwner();
    const old = receipts().filter((r) => r.stop || r.owner !== shared?.owner);
    const results = await Promise.all(
      old.map((r) =>
        revoke(r, attempt?.operation === r.operation && attempt.confirming),
      ),
    );
    publish({ cleanupPending: results.some((ok) => !ok) });
  }
  async function load() {
    if (!owner) return;
    const expected = owner,
      g = generation;
    try {
      await cleanup();
      const page = await api.list(token);
      if (
        owner !== expected ||
        g !== generation ||
        sharedOwner()?.owner !== owner ||
        sharedOwner()?.epoch !== epoch
      )
        return;
      const local = receipts()
        .filter((r) => r.owner === owner && !r.stop)
        .find((r) =>
          page.devices.some(
            (d) => d.id === r.device_id && d.state === 'active',
          ),
        );
      publish({
        page,
        currentDevice: local?.device_id ?? null,
        ...(local
          ? { status: 'active' as const }
          : view.status === 'active'
            ? { status: 'disabled' as const }
            : {}),
      });
    } catch (error) {
      if (owner === expected && g === generation) await rejectAccess(error);
      if (owner === expected && g === generation)
        publish({ status: 'error', busy: false });
    }
  }
  async function receive(value: unknown) {
    const a = attempt;
    if (
      !a ||
      !current(a) ||
      now() - a.started > 120000 ||
      typeof value !== 'object' ||
      value === null
    )
      return;
    const data = value as Record<string, unknown>;
    try {
      uuid(data.cycrio_challenge_id);
      pushSecret(data.cycrio_challenge_secret);
    } catch {
      return;
    }
    if (!a.registration) {
      if (buffer.length >= 8) buffer.shift();
      buffer.push({ data, at: now(), attempt: a });
      return;
    }
    if (
      a.confirming ||
      a.sent ||
      data.cycrio_challenge_id !== a.registration.challenge_id
    )
      return;
    a.confirming = true;
    let receipt: Receipt | undefined;
    try {
      const revocation_token = await deriveRevocationToken(
        data.cycrio_challenge_secret as string,
      );
      receipt = {
        owner: a.owner,
        operation: a.operation,
        device_id: a.registration.device_id,
        revocation_token,
        stop: false,
      };
      // 成功応答が失われても、確認前から解除専用の証明だけを保持する。
      save(receipt);
      if (!current(a)) {
        await revoke(receipt);
        return;
      }
      publish({
        status: 'confirming',
        busy: true,
        currentDevice: receipt.device_id,
      });
      a.sent = true;
      const confirmed = await api.confirm(
        {
          challenge_id: a.registration.challenge_id,
          secret: data.cycrio_challenge_secret as string,
        },
        a.token,
      );
      if (
        confirmed.device.id !== receipt.device_id ||
        confirmed.revocation_token !== revocation_token
      )
        throw Error('invalid confirmation');
      if (!current(a)) {
        await revoke(receipt);
        return;
      }
      publish({ status: 'active', busy: false });
      await load();
    } catch (error) {
      if (current(a)) await rejectAccess(error);
      if (receipt && !current(a)) await revoke(receipt);
      else if (current(a)) {
        // 再登録せず、GETで成功済みの確認と照合する。
        try {
          const page = await api.list(a.token);
          if (!current(a)) {
            if (receipt) await revoke(receipt);
            return;
          }
          const active = page.devices.some(
            (d) => d.id === receipt?.device_id && d.state === 'active',
          );
          publish({
            page,
            status: active ? 'active' : 'uncertain',
            busy: false,
          });
        } catch (error) {
          if (current(a)) await rejectAccess(error);
          if (current(a)) publish({ status: 'uncertain', busy: false });
        }
      }
    } finally {
      a.confirming = false;
      if (receipt && !current(a)) await revoke(receipt);
    }
  }
  sdk.listen((value) => {
    void receive(value);
  });
  const controller = {
    state: () => view,
    subscribe(fn: (view: PushView) => void) {
      listeners.add(fn);
      fn(view);
      return () => {
        listeners.delete(fn);
      };
    },
    async bind(
      next: string | null,
      nextToken: TokenProvider = async () => null,
    ) {
      const shared = sharedOwner();
      if (bound && next === owner) {
        token = nextToken;
        if (shared?.owner !== owner || shared?.epoch !== epoch) {
          generation++;
          buffer.length = 0;
          if (shared?.owner === owner) epoch = shared.epoch;
          publish({
            status: 'disabled',
            busy: false,
            page: null,
            currentDevice: null,
          });
        }
        await cleanup();
        return;
      }
      const mayPublishOwner =
        !bound || shared?.owner === owner || next !== null;
      bound = true;
      generation++;
      owner = next;
      token = nextToken;
      buffer.length = 0;
      if (shared?.owner !== next && mayPublishOwner) {
        epoch = crypto.randomUUID();
        storage.setItem(ownerKey, JSON.stringify({ owner: next, epoch }));
      } else epoch = shared?.epoch ?? crypto.randomUUID();
      publish({ status: 'idle', busy: false, page: null, currentDevice: null });
      await cleanup();
    },
    async synchronize() {
      const shared = sharedOwner();
      if (shared?.owner !== owner || shared?.epoch !== epoch) {
        generation++;
        buffer.length = 0;
        publish({
          status: 'disabled',
          busy: false,
          page: null,
          currentDevice: null,
        });
      }
      await cleanup();
    },
    load,
    async prepare() {
      if (
        !owner ||
        view.busy ||
        ['uncertain', 'waiting'].includes(view.status) ||
        sharedOwner()?.owner !== owner ||
        sharedOwner()?.epoch !== epoch
      )
        return;
      const g = generation,
        expected = owner;
      await cleanup();
      if (view.cleanupPending) {
        publish({ status: 'error' });
        return;
      }
      if (!view.page) await load();
      if (!view.page?.available) return;
      if (
        g !== generation ||
        owner !== expected ||
        sharedOwner()?.owner !== owner ||
        sharedOwner()?.epoch !== epoch
      )
        return;
      const availability = sdk.availability();
      if (availability !== 'supported') {
        publish({ status: availability });
        return;
      }
      publish({ status: 'preparing', busy: true });
      try {
        await sdk.prepare(view.page.app_id);
        if (g === generation && owner === expected)
          publish({ status: 'permission_ready', busy: false });
      } catch (error) {
        if (g === generation)
          publish({
            status:
              error instanceof Error && error.message === 'reload'
                ? 'reload'
                : 'error',
            busy: false,
          });
      }
    },
    async allow() {
      if (!owner || view.status !== 'permission_ready' || view.busy) return;
      const a: Attempt = {
        owner,
        epoch,
        generation,
        operation: crypto.randomUUID(),
        token,
        started: now(),
        confirming: false,
        sent: false,
      };
      if (!current(a)) return;
      attempt = a;
      publish({ status: 'registering', busy: true });
      let registrationStarted = false;
      try {
        // この呼出しより前にawaitを置かず、明示クリックのuser activationを使う。
        const permission = await sdk.permission();
        if (!current(a)) return;
        if (permission !== 'granted') {
          publish({
            status: permission === 'denied' ? 'denied' : 'permission_ready',
            busy: false,
          });
          return;
        }
        const subscription_id = await sdk.subscription();
        if (!current(a)) return;
        registrationStarted = true;
        const reg = await api.register(
          { subscription_id, request_id: a.operation },
          a.token,
        );
        a.registration = reg;
        if (!current(a)) {
          // 証明のない旧登録は未確認のまま失効させ、再利用された端末IDを停止しない。
          return;
        }
        publish({
          status: reg.state === 'failed' ? 'error' : 'waiting',
          busy: false,
          currentDevice: reg.device_id,
        });
        for (const buffered of buffer.splice(0))
          if (buffered.attempt === a && now() - buffered.at < 120000)
            void receive(buffered.data);
      } catch (error) {
        if (current(a)) await rejectAccess(error);
        if (current(a))
          publish({
            status: registrationStarted ? 'uncertain' : 'error',
            busy: false,
          });
      }
    },
    async disable(id: string) {
      if (!owner) return;
      const g = generation,
        expected = owner;
      try {
        await api.disable(id, token);
        for (const r of receipts().filter(
          (r) => r.device_id === id && r.owner === expected,
        ))
          await revoke(r);
        if (g === generation) {
          if (attempt?.registration?.device_id === id) {
            generation++;
            buffer.length = 0;
          }
          publish({ status: 'disabled', busy: false, currentDevice: null });
          await load();
        }
      } catch (error) {
        if (g === generation) await rejectAccess(error);
        if (g === generation) publish({ status: 'error', busy: false });
      }
    },
    retryCleanup: cleanup,
  };
  endScope = () => controller.bind(null);
  return controller;
}
export type PushController = ReturnType<typeof createPushController>;
