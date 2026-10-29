import type { TokenProvider } from './api.ts';
import type { MailAPI } from './mail-api.ts';
import {
  createMailController,
  emptyMailView,
  probePending,
  type MailView,
} from './mail-controller.ts';

export type IntakeView = {
  step: 1 | 2 | 3;
  mail: MailView;
  issue:
    | 'choose_account'
    | 'connect_first'
    | 'select_folder'
    | 'confirm_switch'
    | null;
  folderUncertain: boolean;
  frozen: boolean;
  accepted: string | null;
};
export function createIntakeController(
  api: MailAPI,
  token: TokenProvider,
  changed: (view: IntakeView) => void,
  onDenied: () => void,
  initialAccount?: string,
) {
  let disposed = false,
    initialized = false,
    consentDirty = false;
  let view: IntakeView = {
    step: 1,
    mail: emptyMailView(),
    issue: null,
    folderUncertain: false,
    frozen: false,
    accepted: null,
  };
  const publish = (patch: Partial<IntakeView> = {}) => {
    view = { ...view, ...patch };
    if (!disposed) changed(view);
  };
  const mail = createMailController(
    api,
    token,
    (value) => {
      if (disposed) return;
      publish({
        mail: value,
        ...(value.status === 'denied'
          ? {
              step: 1,
              issue: null,
              folderUncertain: false,
              frozen: false,
              accepted: null,
            }
          : {}),
      });
    },
    onDenied,
  );
  const blocked = () =>
    disposed ||
    view.mail.status === 'denied' ||
    view.mail.status === 'loading' ||
    view.mail.status === 'saving' ||
    view.frozen ||
    view.folderUncertain ||
    view.mail.uncertain;
  const dirty = () =>
    !!(view.mail.form?.credentialsDirty || view.mail.form?.foldersDirty) ||
    consentDirty;
  async function refresh() {
    await mail.refresh();
    if (disposed || view.mail.status !== 'ready' || initialized) return;
    initialized = true;
    const id =
      initialAccount ??
      (view.mail.accounts.length === 1 ? view.mail.accounts[0].id : undefined);
    if (id) mail.select(id);
  }
  return {
    state: () => view,
    refresh,
    async next() {
      if (blocked()) return false;
      const account = view.mail.accounts.find(
        (a) => a.id === view.mail.selected,
      );
      if (!account) {
        publish({ issue: 'choose_account' });
        return false;
      }
      if (view.step === 1) {
        publish({ step: 2, issue: null, accepted: null });
        return true;
      }
      if (view.step !== 2) return false;
      if (account.state !== 'ready' || probePending(account.latest_probe)) {
        publish({ issue: 'connect_first' });
        return false;
      }
      if (
        !view.mail.form?.folder_ids.some((id) =>
          account.folders.some(
            (f) => f.id === id && f.available && f.selectable,
          ),
        )
      ) {
        publish({ issue: 'select_folder' });
        return false;
      }
      if (view.mail.form.foldersDirty) {
        await mail.saveFolders();
        if (disposed || view.mail.status === 'denied') return false;
        if (view.mail.error === 'dependency_unavailable') {
          publish({ folderUncertain: true });
          return false;
        }
        if (view.mail.error || view.mail.form?.foldersDirty) return false;
      }
      publish({ step: 3, issue: null });
      return true;
    },
    back() {
      if (blocked() || view.step === 1) return false;
      publish({ step: (view.step - 1) as 1 | 2, issue: null });
      return true;
    },
    choose(id: string, discard = false) {
      if (blocked() || !view.mail.accounts.some((a) => a.id === id))
        return false;
      if (id !== view.mail.selected && dirty() && !discard) {
        publish({ issue: 'confirm_switch' });
        return false;
      }
      if (id !== view.mail.selected) {
        mail.select(id);
        consentDirty = false;
      }
      publish({ issue: null, accepted: null });
      return true;
    },
    toggleFolder(id: string, checked: boolean) {
      if (blocked() || view.step !== 2) return;
      mail.toggleFolder(id, checked);
      publish({ issue: null });
    },
    async probe() {
      if (!disposed && !view.frozen && !view.folderUncertain)
        await mail.requestProbe();
    },
    poll: () => mail.poll(),
    async adoptStoredFolders() {
      if (disposed || view.frozen || view.mail.status === 'saving') return;
      await mail.refresh();
      if (view.mail.status !== 'ready') return;
      mail.discard();
      publish({ folderUncertain: false, issue: null });
    },
    freeze(frozen: boolean) {
      if (!disposed) publish({ frozen });
    },
    editConsent(dirty: boolean) {
      if (!disposed) consentDirty = dirty;
    },
    started(id: string) {
      if (disposed || view.step !== 3 || id !== view.mail.selected)
        return false;
      publish({ accepted: id });
      return true;
    },
    hasUnsaved: () =>
      dirty() ||
      view.frozen ||
      view.folderUncertain ||
      view.mail.uncertain ||
      view.mail.status === 'saving',
    dispose() {
      disposed = true;
      mail.dispose();
    },
  };
}
