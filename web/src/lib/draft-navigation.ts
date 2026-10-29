type RetainedDraft = {
  view: { dirty: boolean; uncertain: boolean; status: string; saved?: unknown };
  pending?: unknown;
};
export function hasDraftChanges(...stores: Iterable<RetainedDraft>[]) {
  return stores.some((store) =>
    [...store].some(
      (draft) =>
        !draft.view.saved &&
        (draft.view.dirty ||
          draft.view.uncertain ||
          draft.view.status === 'saving' ||
          !!draft.pending),
    ),
  );
}
export function createDraftNavigation(
  hasUnsaved: () => boolean,
  frozen: () => boolean,
  discard: () => void,
  navigate: (target: string) => unknown,
  changed: (target: string) => void,
) {
  let target = '',
    leaving = false;
  const set = (value: string) => {
    target = value;
    changed(value);
  };
  return {
    target: () => target,
    request(current: string, destination?: string) {
      if (leaving) {
        leaving = false;
        return true;
      }
      if (!frozen() && !hasUnsaved()) return true;
      if (frozen()) return false;
      if (destination) {
        const source = new URL(current),
          next = new URL(destination, source);
        if (next.origin === source.origin)
          set(next.pathname + next.search + next.hash);
      }
      return false;
    },
    stay() {
      if (!frozen()) set('');
    },
    discardAndLeave() {
      if (frozen() || !target) return;
      const destination = target;
      discard();
      leaving = true;
      set('');
      void navigate(destination);
    },
  };
}
