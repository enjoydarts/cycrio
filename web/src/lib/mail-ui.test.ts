import assert from 'node:assert/strict';
import test from 'node:test';
import { readFile } from 'node:fs/promises';
import { compile, parse } from 'svelte/compiler';
import { render } from 'svelte/server';
test('wizard closing requires explicit dirty discard and blocks busy or uncertain replay', async () => {
  const source = await readFile(
    new URL('./MailAccounts.svelte', import.meta.url),
    'utf8',
  );
  const ast = parse(source, { modern: true }) as any;
  const fn = (name: string) =>
    ast.instance.content.body.find(
      (node: any) =>
        node.type === 'FunctionDeclaration' && node.id.name === name,
    );
  const request = fn('requestConnectionClose');
  const discard = fn('discardConnection');
  assert.ok(request);
  assert.ok(discard);
  for (const [
    connectOnly,
    busy,
    uncertain,
    credentialsDirty,
    foldersDirty,
    expected,
  ] of [
    [true, false, false, true, false, false],
    [true, false, false, false, true, false],
    [true, true, false, true, false, false],
    [true, false, true, true, false, false],
    [true, false, false, false, false, true],
    [false, false, false, true, false, true],
  ]) {
    const run = new Function(
      'connectOnly',
      'busy',
      'view',
      `let confirmClose=false; const close=(${source.slice(request.start, request.end)}); return { allowed:close(), confirm:confirmClose };`,
    );
    const result = run(connectOnly, busy, {
      uncertain,
      form: { credentialsDirty, foldersDirty },
    });
    assert.equal(result.allowed, expected);
    assert.equal(
      result.confirm,
      connectOnly && !busy && !uncertain && (credentialsDirty || foldersDirty),
    );
  }
  for (const [busy, uncertain] of [
    [false, false],
    [true, false],
    [false, true],
  ]) {
    const calls: string[] = [];
    const run = new Function(
      'busy',
      'view',
      'controller',
      'closeConnection',
      `let confirmClose=true; return (${source.slice(discard.start, discard.end)})`,
    );
    run(busy, { uncertain }, { discard: () => calls.push('discard') }, () =>
      calls.push('close'),
    )();
    assert.deepEqual(calls, busy || uncertain ? [] : ['discard', 'close']);
  }
  assert.match(source, /canClose=\{requestConnectionClose\}/);
  assert.match(source, /入力を破棄して閉じる/);
  assert.match(source, /編集を続ける/);
  const modal = await readFile(
    new URL('./Modal.svelte', import.meta.url),
    'utf8',
  );
  assert.match(modal, /canClose = \(\) => true/);
  assert.match(modal, /busy \|\| !canClose\(\)/);
});
test('wizard connection callback requires a new account save acknowledgement and close respects busy state', async () => {
  const source = await readFile(
    new URL('./MailAccounts.svelte', import.meta.url),
    'utf8',
  );
  assert.match(source, /connectOnly = false/);
  assert.match(source, /onConnected =/);
  assert.match(source, /onConnectClose =/);
  assert.match(source, /onFrozen =/);
  assert.match(source, /\{#if !connectOnly\}/);
  const ast = parse(source, { modern: true }) as any;
  const submit = ast.instance.content.body.find(
    (node: any) =>
      node.type === 'FunctionDeclaration' &&
      node.id.name === 'submitConnection',
  );
  assert.ok(submit);
  const run = new Function(
    'controller',
    'oauth',
    'connectOnly',
    'onConnected',
    'onFrozen',
    `let notifiedClose=false; return (${source.slice(submit.start, submit.end)})`,
  );
  for (const outcome of ['saved', 'error', 'existing', 'oauth']) {
    const saved = { id: 'new-account', version: 1 };
    let view: any = {
      form: { id: saved.id, baseline: outcome === 'existing' ? 1 : 0 },
    };
    const calls: unknown[] = [];
    const controller = {
      state: () => view,
      save: async () => {
        view = {
          status: outcome === 'error' ? 'error' : 'ready',
          success: outcome === 'error' ? null : 'saved',
          accounts: [saved],
        };
      },
      connectOAuth: async () => {
        calls.push('oauth');
      },
      close: () => {
        calls.push('close');
      },
    };
    await run(
      controller,
      outcome === 'oauth',
      true,
      (account: unknown) => calls.push(account),
      (frozen: boolean) => calls.push(frozen),
    )();
    assert.deepEqual(
      calls,
      outcome === 'saved'
        ? ['close', false, saved]
        : outcome === 'oauth'
          ? ['oauth']
          : [],
    );
  }
  const close = ast.instance.content.body.find(
    (node: any) =>
      node.type === 'FunctionDeclaration' && node.id.name === 'closeConnection',
  );
  const closeFactory = new Function(
    'busy',
    'controller',
    'onFrozen',
    'connectOnly',
    'onConnectClose',
    `let editing = true, notifiedClose=false; const requestConnectionClose=()=>!busy; return (${source.slice(close.start, close.end)})`,
  );
  for (const busy of [true, false]) {
    const calls: unknown[] = [];
    closeFactory(
      busy,
      { close: () => calls.push('close'), state: () => ({ form: null }) },
      (value: boolean) => calls.push(value),
      true,
      () => calls.push('exit'),
    )();
    assert.deepEqual(calls, busy ? [] : ['close', false, 'exit']);
  }
  const effect = ast.instance.content.body.find(
    (node: any) =>
      node.type === 'ExpressionStatement' &&
      node.expression.callee?.name === '$effect',
  ).expression.arguments[0];
  for (const [busy, uncertain, credentialsDirty, foldersDirty] of [
    [false, false, false, false],
    [true, false, false, false],
    [false, true, false, false],
    [false, false, true, false],
    [false, false, false, true],
  ]) {
    let frozen: boolean | undefined;
    new Function(
      'busy',
      'view',
      'onFrozen',
      `return (${source.slice(effect.start, effect.end)})`,
    )(
      busy,
      { uncertain, form: { credentialsDirty, foldersDirty } },
      (value: boolean) => {
        frozen = value;
      },
    )();
    assert.equal(frozen, busy || uncertain || credentialsDirty || foldersDirty);
  }
});
test('mail settings begin with a connection list, use a modal for details and keep analysis on its own page', async () => {
  const source = await readFile(
    new URL('./MailAccounts.svelte', import.meta.url),
    'utf8',
  );
  assert.match(source, /<Modal/);
  assert.match(source, /!account \|\| editing/);
  assert.doesNotMatch(source, /MailProcessing|processingAPI/);
  assert.match(source, /メールを接続/);
  assert.match(source, /接続を編集/);
  assert.doesNotMatch(source, /class="mail-columns"|class="mail-accounts"/);
  assert.match(source, /<Modal[\s\S]+?\{@render feedback\(\)\}/);
});
test('actual refresh and discard handlers have separate semantics', async () => {
  const source = await readFile(
    new URL('./MailAccounts.svelte', import.meta.url),
    'utf8',
  );
  const buttons: Record<string, unknown>[] = [];
  function visit(value: unknown) {
    if (!value || typeof value !== 'object') return;
    if (Array.isArray(value)) {
      value.forEach(visit);
      return;
    }
    const node = value as Record<string, unknown>;
    if (node.name === 'button') buttons.push(node);
    Object.values(node).forEach(visit);
  }
  visit(parse(source, { modern: true }));
  for (const [id, expected] of [
    ['mail-refresh', 'refresh'],
    ['mail-discard', 'discard'],
  ]) {
    const button = buttons.find(
      (node) =>
        Array.isArray(node.attributes) &&
        (node.attributes as { name: string; value: { data: string }[] }[]).some(
          (a) => a.name === 'id' && a.value?.[0]?.data === id,
        ),
    );
    assert.ok(button);
    const onclick = (
      button.attributes as {
        name: string;
        value: { expression: { start: number; end: number } };
      }[]
    ).find((a) => a.name === 'onclick')!;
    const expression = (
      Array.isArray(onclick.value) ? onclick.value[0] : onclick.value
    ).expression;
    const calls: string[] = [];
    const handler = new Function(
      'controller',
      `return (${source.slice(expression.start, expression.end)})`,
    )({
      refresh: () => calls.push('refresh'),
      discard: () => calls.push('discard'),
    });
    handler();
    assert.deepEqual(calls, [expected]);
  }
});
test('mail component SSR shows explicit Fastmail boundaries and never includes a preset secret', async () => {
  const source = await readFile(
    new URL('./MailAccounts.svelte', import.meta.url),
    'utf8',
  );
  const { js } = compile(source, { generate: 'server' });
  const syncSource = await readFile(
    new URL('./MailSync.svelte', import.meta.url),
    'utf8',
  );
  const syncCode = compile(syncSource, { generate: 'server' }).js.code.replace(
    /(from\s+|^import\s*)(['"])([^'"]+)\2/gm,
    (_m, prefix, quote, path) =>
      `${prefix}${quote}${path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path)}${quote}`,
  );
  const syncURL = `data:text/javascript;base64,${Buffer.from(syncCode).toString('base64')}`;
  async function compiledURL(name: string): Promise<string> {
    const componentSource = await readFile(
      new URL(`./${name}.svelte`, import.meta.url),
      'utf8',
    );
    const componentCode = compile(componentSource, { generate: 'server' }).js
      .code;
    const nested: Record<string, string> = {};
    for (const match of componentCode.matchAll(
      /from\s+['"]\.\/([^'"]+)\.svelte['"]/g,
    ))
      nested[`./${match[1]}.svelte`] = await compiledURL(match[1]);
    const resolved = componentCode
      .replace(/^import\s+['"][^'"]+\.css['"];?\n/gm, '')
      .replace(
        /(from\s+|^import\s*)(['"])([^'"]+)\2/gm,
        (_m, prefix, quote, path) =>
          `${prefix}${quote}${nested[path] ?? (path.startsWith('.') ? new URL(path, import.meta.url).href : import.meta.resolve(path))}${quote}`,
      );
    return `data:text/javascript;base64,${Buffer.from(resolved).toString('base64')}`;
  }
  const processingURL = await compiledURL('MailProcessing');
  const modalURL = await compiledURL('Modal');
  const headerURL = await compiledURL('LibraryHeader');
  const panelURL = await compiledURL('LibraryPanel');
  const code = js.code.replace(
    /(from\s+|^import\s*)(['"])([^'"]+)\2/gm,
    (_m, prefix, quote, path) =>
      `${prefix}${quote}${path === './LibraryHeader.svelte' ? headerURL : path === './LibraryPanel.svelte' ? panelURL : path === './Modal.svelte' ? modalURL : path === './MailProcessing.svelte' ? processingURL : path === './MailSync.svelte' ? syncURL : path.startsWith('.') ? new URL(path.endsWith('.ts') ? path : `${path}.ts`, import.meta.url).href : import.meta.resolve(path)}${quote}`,
  );
  const Component = (
    await import(
      `data:text/javascript;base64,${Buffer.from(code).toString('base64')}`
    )
  ).default;
  const html = render(Component, {
    props: { api: {}, token: async () => '', onDenied: () => {} },
  }).body;
  for (const text of [
    'imap.fastmail.com:993',
    'アプリパスワード',
    '本文',
    'AI',
    '同期',
    'Google',
  ])
    assert.ok(html.includes(text), text);
  assert.doesNotMatch(html, /localStorage|sessionStorage/);
  const connectHTML = render(Component, {
    props: {
      api: {},
      token: async () => '',
      onDenied: () => {},
      connectOnly: true,
    },
  }).body;
  assert.doesNotMatch(
    connectHTML,
    /メールアカウント一覧|登録済み|連携について/,
  );
  assert.match(connectHTML, /<dialog/);
  for (const oauthResult of ['connected', 'failed', null]) {
    const resultHTML = render(Component, {
      props: {
        api: {},
        token: async () => '',
        onDenied: () => {},
        oauthResult,
      },
    }).body;
    if (oauthResult === 'connected') {
      assert.match(
        resultHTML,
        /href="\/import"[^>]*>\s*取り込みを続ける\s*<\/a>/,
      );
      assert.match(resultHTML, /接続を確認して対象フォルダーを選んでください/);
    } else assert.doesNotMatch(resultHTML, /取り込みを続ける/);
  }
});
