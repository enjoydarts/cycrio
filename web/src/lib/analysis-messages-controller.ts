import type { TokenProvider } from './api.ts';
import type {
  MessageAPI,
  MessageDetail,
  MessageFilter,
  MessagePage,
} from './analysis-messages-api.ts';
import { createFeed, type FeedView } from './daily-controller.ts';
export const messagePollInterval = 5000;
export function pollMessageHistory(refresh: () => void, ready: () => boolean) {
  const timer = setInterval(() => {
    if (ready()) refresh();
  }, messagePollInterval);
  return () => clearInterval(timer);
}
export type MessageHistoryView = FeedView<MessagePage> & {
  selected: MessageDetail | null;
  detailError: boolean;
  detailLoading: boolean;
};
export function createMessageHistory(
  api: MessageAPI,
  token: TokenProvider,
  changed: (v: MessageHistoryView) => void,
  onDenied: () => void,
  holdSelection: () => boolean = () => false,
) {
  let view: MessageHistoryView = {
      page: null,
      status: 'idle',
      error: null,
      selected: null,
      detailError: false,
      detailLoading: false,
    },
    filter: MessageFilter | undefined,
    selectedId: string | null = null,
    denied = false;
  const publish = (patch: Partial<MessageHistoryView>) => {
    view = { ...view, ...patch };
    changed(view);
  };
  const denial = () => {
    if (denied) return;
    denied = true;
    selectedId = null;
    publish({
      page: null,
      selected: null,
      status: 'denied',
      detailError: false,
      detailLoading: false,
    });
    onDenied();
  };
  const list = createFeed(
    api.messages,
    token,
    (v) => {
      if (!denied) publish(v);
    },
    denial,
  );
  const detail = createFeed(
    async (id: string, t: TokenProvider, s?: AbortSignal) =>
      api.message(id, t, s),
    token,
    (v) => {
      if (denied) return;
      if (v.status === 'denied') {
        denial();
        return;
      }
      if (v.status === 'disposed') return;
      publish({
        detailLoading: v.status === 'loading',
        detailError: !!v.error,
        ...(!holdSelection() &&
        selectedId &&
        v.status === 'ready' &&
        v.page?.message.id === selectedId
          ? { selected: v.page }
          : {}),
      });
    },
    denial,
  );
  return {
    state: () => view,
    async load(f: MessageFilter) {
      if (denied) return;
      filter = f;
      await list.load(f);
    },
    async select(id: string) {
      if (denied) return;
      selectedId = id;
      publish({ selected: null, detailError: false });
      await detail.load(id);
    },
    close() {
      selectedId = null;
      publish({ selected: null, detailError: false });
    },
    updateResult(result: MessageDetail['result']) {
      if (view.selected && result?.id === view.selected.result?.id)
        publish({ selected: { ...view.selected, result } });
    },
    async refresh() {
      if (denied) return;
      if (filter) await list.load(filter);
      if (selectedId && !denied) await detail.load(selectedId);
    },
    dispose() {
      selectedId = null;
      list.dispose();
      detail.dispose();
      publish({ selected: null, page: null, status: 'disposed', error: null });
    },
  };
}
