export type ThemePreference = 'light' | 'dark' | 'system';
type ThemeEnvironment = {
  root: { dataset: Record<string, string | undefined> };
  storage: Pick<Storage, 'getItem' | 'setItem'>;
  media: {
    readonly matches: boolean;
    addEventListener: (type: 'change', listener: () => void) => void;
    removeEventListener: (type: 'change', listener: () => void) => void;
  };
};
export function createThemeController({
  root,
  storage,
  media,
}: ThemeEnvironment) {
  let preference: ThemePreference = 'system';
  try {
    const stored = storage.getItem('cycrio-theme');
    if (stored === 'light' || stored === 'dark') preference = stored;
  } catch {
    /* Appearance remains usable when browser storage is unavailable. */
  }
  function apply() {
    root.dataset.theme =
      preference === 'system' ? (media.matches ? 'dark' : 'light') : preference;
  }
  apply();
  media.addEventListener('change', apply);
  return {
    preference: () => preference,
    set(value: ThemePreference) {
      preference = value;
      try {
        storage.setItem('cycrio-theme', value);
      } catch {
        /* Optional persistence. */
      }
      apply();
    },
    dispose() {
      media.removeEventListener('change', apply);
    },
  };
}
