import { mount } from 'svelte';
import '../src/lib/brand.css';
import '../src/lib/app.css';

const target = document.getElementById('app')!;
const mode = new URL(location.href).searchParams.get('mode') ?? '';

if (mode.startsWith('push')) {
  const { default: PushFixture } = await import('./PushFixture.svelte');
  window.browserTest = { requests: [], errors: [], saved: null };
  mount(PushFixture, { target, props: { mode } });
} else {
  const { default: Fixture } = await import('./Fixture.svelte');
  mount(Fixture, { target });
}
