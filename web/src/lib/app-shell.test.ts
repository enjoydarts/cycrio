import './svelte-test-hooks.ts';
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile, access } from 'node:fs/promises';
import { compile, parse } from 'svelte/compiler';
import { render } from 'svelte/server';
import { createRawSnippet } from 'svelte';

test('mobile close restores focus after the background inert state has flushed', async () => {
  const source = await readFile(
    new URL('./AppShell.svelte', import.meta.url),
    'utf8',
  );
  const ast = parse(source, { modern: true }) as any;
  const node = ast.instance.content.body.find(
    (value: any) =>
      value.type === 'FunctionDeclaration' && value.id.name === 'closeMenu',
  );
  let release!: () => void,
    focused = 0;
  const flushed = new Promise<void>((resolve) => {
    release = resolve;
  });
  const fixture = new Function(
    'tick',
    'menuButton',
    `let menuOpen=true; return { close:(${source.slice(node.start, node.end)}), isOpen:()=>menuOpen }`,
  )(() => flushed, {
    focus() {
      focused++;
    },
  });
  const closing = fixture.close();
  assert.equal(fixture.isOpen(), false);
  assert.equal(focused, 0);
  release();
  await closing;
  assert.equal(focused, 1);
});

test('signed-out access places its brand above one compact authentication card', async () => {
  const C = await component('AppAccess');
  const html = render(C, {
    props: {
      state: { status: 'signed_out' },
      retry: () => {},
      signOut: () => {},
      mountAuth: () => () => {},
    },
  }).body;
  assert.ok(
    html.indexOf('class="access-brand') < html.indexOf('class="access-panel'),
  );
  assert.match(html, /サインインして続ける/);
  assert.doesNotMatch(
    html,
    /毎月の支出に、見通しを|メールとつながるサブスクリプション管理/,
  );
  assert.equal((html.match(/<h1\b/g) ?? []).length, 1);
  assert.match(html, /clerk-host/);
});

test('Last used is shown only when Clerk reports an actual previous Google authentication', async () => {
  const C = await component('AppAccess');
  for (const strategy of [undefined, null, 'password', 'oauth_google']) {
    const html = render(C, {
      props: {
        state: { status: 'signed_out', lastAuthenticationStrategy: strategy },
        retry: () => {},
        signOut: () => {},
        mountAuth: () => () => {},
      },
    }).body;
    assert.equal(
      /class="[^"]*\blast-google\b/.test(html),
      strategy === 'oauth_google',
    );
    assert.equal(
      html.includes('前回利用したログイン方法: Google'),
      strategy === 'oauth_google',
    );
  }
});

test('rail labels and navigation retain AA contrast on the fixed dark surface', async () => {
  const source = await readFile(
    new URL('./brand.css', import.meta.url),
    'utf8',
  );
  const colors = Object.fromEntries(
    [
      ...source.matchAll(/--(color-rail(?:-text|-muted)?):\s*#([a-f0-9]{6})/g),
    ].map((match) => [match[1], match[2]]),
  );
  const luminance = (hex: string) =>
    hex
      .match(/../g)!
      .map((value) => parseInt(value, 16) / 255)
      .map((value) =>
        value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4,
      )
      .reduce(
        (sum, value, index) => sum + value * [0.2126, 0.7152, 0.0722][index],
        0,
      );
  for (const foreground of ['color-rail-text', 'color-rail-muted']) {
    const values = [
      luminance(colors[foreground]),
      luminance(colors['color-rail']),
    ].sort((a, b) => a - b);
    assert.ok((values[1] + 0.05) / (values[0] + 0.05) >= 4.5, foreground);
  }
});

async function component(name: string) {
  return (await import(await componentURL(name))).default;
}
async function componentURL(name: string): Promise<string> {
  const source = await readFile(
    new URL(`./${name}.svelte`, import.meta.url),
    'utf8',
  );
  const { js, warnings } = compile(source, { generate: 'server' });
  assert.deepEqual(warnings, []);
  const nested: Record<string, string> = {};
  for (const match of js.code.matchAll(/from\s+['"]\.\/([^'"]+)\.svelte['"]/g))
    nested[`./${match[1]}.svelte`] = await componentURL(match[1]);
  const code = js.code.replace(
    /(^import[^\n]*?from\s+|^import\s+)(['"])([^'"]+)\2/gm,
    (_m, prefix, quote, path) =>
      `${prefix}${quote}${nested[path] ?? (path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path))}${quote}`,
  );
  return `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`;
}
test('single rail keeps library navigation and a bottom settings link without nested settings', async () => {
  const Component = await component('AppShell');
  const props = {
    user: {
      id: '11111111-1111-4111-8111-111111111111',
      role: 'platform_admin',
    },
    pathname: '/settings/integrations',
    signOut: () => {},
    children: createRawSnippet(() => ({ render: () => '<p>設定本文</p>' })),
  };
  const html = render(Component, { props }).body;
  const sidebar = html.match(/<aside\b[\s\S]*?<\/aside>/)?.[0] ?? '';
  assert.match(sidebar, /<img[^>]*src="\/brand\/cycrio-mark-128.png"/);
  assert.doesNotMatch(sidebar, /brand-symbol/);
  assert.doesNotMatch(sidebar, /aria-label="設定項目"/);
  assert.match(sidebar, /href="\/settings"[^>]*aria-current="page"/);
  assert.equal((html.match(/aria-current="page"/g) ?? []).length, 1);
  assert.doesNotMatch(
    sidebar,
    /href="\/settings\/(?:ai-models|ai-runtime|mail-policy)"/,
  );
  const owner = render(Component, {
    props: {
      ...props,
      user: { ...props.user, role: 'user' },
      pathname: '/settings/mail',
    },
  }).body;
  assert.doesNotMatch(owner, /integrations|credentials|管理者設定/);
  const analysis = render(Component, {
    props: { ...props, pathname: '/analysis' },
  }).body;
  assert.doesNotMatch(analysis, /aria-label="設定項目"/);
  assert.match(analysis, /href="\/settings"/);
  for (const href of [
    '/subscriptions',
    '/analysis?tab=attention',
    '/import?connect=true',
  ]) {
    assert.ok(analysis.includes(`href="${href}"`));
  }
  assert.doesNotMatch(
    analysis,
    /href="\/analysis\?tab=history"|subscriptions\/summary|支払・返金/,
  );
  assert.match(analysis, /aria-label="確認が必要な契約候補"/);
  assert.match(analysis, /aria-controls="workspace-sidebar"/);
  assert.doesNotMatch(analysis, /確認が必要なメール[^<]*0/);
});
test('query navigation selects attention and history and subscription routes', async () => {
  const Component = await component('AppShell');
  const props = {
    user: { id: 'owner', role: 'user' },
    signOut() {},
    children: createRawSnippet(() => ({ render: () => '<p>本文</p>' })),
  };
  for (const [pathname, search, href] of [
    ['/analysis', '', '/analysis?tab=attention'],
    ['/analysis', '?tab=attention', '/analysis?tab=attention'],
    ['/subscriptions', '', '/subscriptions'],
  ]) {
    const html = render(Component, {
      props: { ...props, pathname, search },
    }).body;
    assert.match(
      html,
      new RegExp(
        `href="${href.replace(/[?]/g, '\\?')}"[^>]*aria-current="page"`,
      ),
    );
    assert.equal((html.match(/aria-current="page"/g) ?? []).length, 1);
  }
});
test('legacy AI settings URLs select the shared AI navigation entry', async () => {
  const Component = await component('SettingsNavigation');
  for (const section of ['ai-models', 'ai-runtime', 'mail-policy']) {
    const html = render(Component, {
      props: { role: 'platform_admin', section },
    }).body;
    assert.match(html, /href="\/settings\/ai"[^>]*aria-current="page"/);
  }
});
test('pending access displays the signed-in Clerk user ID for administrator approval', async () => {
  const Component = await component('AppAccess');
  const props = {
    retry: () => {},
    signOut: () => {},
    mountAuth: () => () => {},
  };
  for (const id of ['user_first_account', 'user_second_account']) {
    const html = render(Component, {
      props: { ...props, state: { status: 'pending', clerkUserId: id } },
    }).body;
    assert.ok(
      html.includes(id),
      'approval screen must show the current user ID',
    );
    assert.match(html, /利用許可を再確認/);
  }
  for (const status of ['signed_out', 'denied', 'checking']) {
    const html = render(Component, {
      props: {
        ...props,
        state: { status, clerkUserId: 'user_hidden_account' },
      },
    }).body;
    assert.ok(!html.includes('user_hidden_account'));
  }
});
test('root layout owns one authentication controller and a stable authorization scope for child routes', async () => {
  const source = await readFile(
    new URL('../routes/+layout.svelte', import.meta.url),
    'utf8',
  );
  assert.match(source, /createAuthController\(/);
  assert.match(source, /createSessionBoundary\(/);
  assert.match(source, /#key sessionState.scope/);
  assert.match(source, /sso-callback/);
  for (const route of [
    '+page.svelte',
    'admin/+page.svelte',
    'mail/+page.svelte',
    'sso-callback/+page.svelte',
  ]) {
    const page = await readFile(
      new URL(`../routes/${route}`, import.meta.url),
      'utf8',
    );
    assert.doesNotMatch(page, /AccessPanel|createAuthController/);
  }
});
test('settings navigation renders real routes and withholds administrator names and links from owners', async () => {
  await assert.doesNotReject(
    access(new URL('./SettingsNavigation.svelte', import.meta.url)),
  );
  const Component = await component('SettingsNavigation');
  const owner = render(Component, {
    props: { role: 'user', section: 'mail' },
  }).body;
  assert.match(owner, /href="\/settings\/mail"[^>]*aria-current="page"/);
  assert.match(owner, /href="\/settings\/appearance"/);
  assert.doesNotMatch(owner, /credentials|catalog|AI モデル|管理者設定/);
  const admin = render(Component, {
    props: { role: 'platform_admin', section: 'usage' },
  }).body;
  assert.match(admin, /href="\/settings\/usage"[^>]*aria-current="page"/);
  assert.match(admin, /href="\/settings\/ai"/);
});
test('static settings composition includes permission branches and each settings component', async () => {
  const source = await readFile(
    new URL('../routes/settings/[section]/+page.svelte', import.meta.url),
    'utf8',
  ).catch(() => '');
  assert.match(source, /settingsAccess/);
  assert.match(source, /access === 'denied'/);
  assert.match(source, /access === 'not_found'/);
  for (const name of [
    'MailAccounts',
    'Appearance',
    'AdminCredentials',
    'ModelCatalog',
    'AISetup',
    'AIUsage',
  ])
    assert.match(source, new RegExp(`<${name}\\b`));
});
test('internal model and price UUIDs are available only inside technical disclosure', async () => {
  for (const [filename, expressions] of [
    ['AIModelSettings', ['model.current_price?.id', 'savedPrice']],
    ['ModelCatalog', ['model.id']],
  ] as const) {
    const source = await readFile(
      new URL(`./${filename}.svelte`, import.meta.url),
      'utf8',
    );
    const found = new Set<string>();
    function visit(value: unknown, disclosed = false) {
      if (!value || typeof value !== 'object') return;
      if (Array.isArray(value)) {
        value.forEach((child) => visit(child, disclosed));
        return;
      }
      const node = value as Record<string, unknown>;
      const inside = disclosed || node.name === 'details';
      if (
        node.type === 'ExpressionTag' &&
        node.expression &&
        typeof node.expression === 'object'
      ) {
        const expression = node.expression as { start: number; end: number };
        const text = source.slice(expression.start, expression.end);
        for (const expected of expressions)
          if (text.includes(expected)) {
            found.add(expected);
            assert.equal(inside, true, `${filename}: ${expected}`);
          }
      }
      Object.values(node).forEach((child) => visit(child, inside));
    }
    visit(parse(source, { modern: true }));
    assert.equal(found.size, expressions.length);
  }
});
