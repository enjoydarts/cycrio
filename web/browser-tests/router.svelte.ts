import { onDestroy } from 'svelte';
const mode = new URL(location.href).searchParams.get('mode') ?? '';
export const page = $state({ url: new URL('/subscriptions', location.origin) });
type Navigation = { cancel: () => void; to: { url: URL } };
const guards = new Set<(navigation: Navigation) => void>();
export function beforeNavigate(guard: (navigation: Navigation) => void) {
  guards.add(guard);
  onDestroy(() => guards.delete(guard));
}
export async function goto(
  path: string | URL,
  options: {
    replaceState?: boolean;
    replace?: boolean;
    reset?: boolean;
    keepFocus?: boolean;
    noScroll?: boolean;
  } = {},
) {
  const target = new URL(path, location.origin);
  let cancelled = false;
  for (const guard of guards)
    guard({
      cancel: () => {
        cancelled = true;
      },
      to: { url: target },
    });
  if (cancelled) return;
  page.url = target;
  const fixture = new URL('/browser-tests/index.html', location.origin);
  fixture.search = target.search;
  fixture.searchParams.set('mode', mode);
  history[
    options.replace || options.replaceState ? 'replaceState' : 'pushState'
  ]({}, '', fixture);
}
