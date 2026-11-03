import assert from 'node:assert/strict';
import { mkdir, writeFile } from 'node:fs/promises';

const out = '/artifacts';
await mkdir(out, { recursive: true });
async function api(method, path, body) {
  const response = await fetch('http://browser:4444' + path, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(40000),
  });
  const { value } = await response.json();
  if (!response.ok || value?.error) throw Error(JSON.stringify(value));
  return value;
}
let session;
let scenario = 'startup';
const results = [];
const js = (script) =>
  api('POST', session + '/execute/sync', { script, args: [] });
async function wait(expression) {
  for (let i = 0; i < 150; i++) {
    if (await js('return ' + expression)) return;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw Error(
    'Timeout: ' +
      expression +
      '\n' +
      (await js('return document.body.innerText')),
  );
}
async function click(expression) {
  const element = await js('return ' + expression);
  assert.ok(element, 'Missing control: ' + expression);
  await api(
    'POST',
    session +
      '/element/' +
      element['element-6066-11e4-a52e-4f735466cecf'] +
      '/click',
    {},
  );
}
async function closeMobileMenu() {
  // The full-screen shade's center sits underneath the open sidebar. Wait for
  // its transition, then hit-test and click the exposed shade using WebDriver.
  await wait(
    'document.querySelector("#workspace-sidebar").getBoundingClientRect().left===0',
  );
  const point = await js(`
    const shade=document.querySelector('button[aria-label="メニューを閉じる"]');
    const sidebar=document.querySelector('#workspace-sidebar');
    const bounds=shade.getBoundingClientRect(), panel=sidebar.getBoundingClientRect();
    const left=Math.max(0,bounds.left,panel.right), right=Math.min(innerWidth,bounds.right);
    const x=Math.floor((left+right)/2);
    const y=Math.floor((Math.max(0,bounds.top)+Math.min(innerHeight,bounds.bottom))/2);
    return {x,y,exposedWidth:right-left,hitShade:document.elementFromPoint(x,y)===shade};
  `);
  assert.ok(
    point.exposedWidth > 0,
    'Mobile sidebar must leave an exposed shade',
  );
  assert.equal(
    point.hitShade,
    true,
    'Close-menu click point must hit the exposed shade',
  );
  await api('POST', session + '/actions', {
    actions: [
      {
        type: 'pointer',
        id: 'sidebar-shade-pointer',
        parameters: { pointerType: 'mouse' },
        actions: [
          {
            type: 'pointerMove',
            origin: 'viewport',
            duration: 0,
            x: point.x,
            y: point.y,
          },
          { type: 'pointerDown', button: 0 },
          { type: 'pointerUp', button: 0 },
        ],
      },
    ],
  });
  await wait(
    `document.querySelector('button[aria-label="メニューを開く"]').getAttribute('aria-expanded')==='false' && document.querySelector('button[aria-label="メニューを閉じる"]')===null && document.activeElement===document.querySelector('button[aria-label="メニューを開く"]')`,
  );
  await wait(
    `document.querySelector('#workspace-sidebar').inert && document.querySelector('#workspace-sidebar').getBoundingClientRect().right<=0`,
  );
}
const button = (label) =>
  `Array.from(document.querySelectorAll('button')).find(e=>e.textContent.trim()===${JSON.stringify(label)})`;
async function input(expression, value, event = 'input') {
  await js(
    `const e=${expression};e.value=${JSON.stringify(value)};e.dispatchEvent(new Event('${event}',{bubbles:true}));`,
  );
}
const field = (label, type = 'input') =>
  `Array.from(document.querySelectorAll('label')).find(e=>e.textContent.includes(${JSON.stringify(label)})).querySelector('${type}')`;
async function visit(mode, width) {
  scenario = `${width}-${mode}`;
  await api('POST', session + '/url', {
    url: `http://fixture:5199/browser-tests/index.html?mode=${mode}`,
  });
  await wait(
    'window.browserTest!==undefined && document.querySelector("main,dialog")!==null',
  );
}
async function layout() {
  assert.equal(
    await js('return document.documentElement.scrollWidth>innerWidth'),
    false,
    scenario + ': page overflow',
  );
  assert.equal(
    await js(
      'const d=document.querySelector("dialog[open]");return !!d&&d.scrollWidth>d.clientWidth',
    ),
    false,
    scenario + ': modal overflow',
  );
  assert.deepEqual(
    await js('return window.browserTest.errors'),
    [],
    scenario + ': fake API contract failed',
  );
}
async function saved() {
  await wait(
    'window.browserTest.saved!==null || window.browserTest.errors.length>0',
  );
  await layout();
  assert.ok(
    await js('return window.browserTest.saved'),
    scenario + ': save callback missing',
  );
}
try {
  session =
    '/session/' +
    (
      await api('POST', '/session', {
        capabilities: {
          alwaysMatch: {
            browserName: 'chrome',
            'goog:chromeOptions': {
              args: [
                '--headless=new',
                '--no-sandbox',
                '--disable-dev-shm-usage',
                '--unsafely-treat-insecure-origin-as-secure=http://fixture:5199',
              ],
            },
            'goog:loggingPrefs': { browser: 'ALL' },
          },
        },
      })
    ).sessionId;
  for (const width of [1280, 390, 320]) {
    await api('POST', session + '/goog/cdp/execute', {
      cmd: 'Emulation.setDeviceMetricsOverride',
      params: { width, height: 1100, deviceScaleFactor: 1, mobile: false },
    });
    for (const mode of ['register', 'edit']) {
      await visit(mode, width);
      await wait('document.querySelector("form input")!==null');
      if (mode === 'register') await input(field('サービス名'), 'Example Mail');
      await input(field('周期の単位', 'select'), 'year', 'change');
      await input(field('請求周期の回数'), '');
      assert.equal(
        await js('return ' + field('請求周期の回数') + '.value'),
        '',
        'Cycle count should remain optional',
      );
      await click('document.querySelector("input[type=checkbox]")');
      const submit = button(mode === 'edit' ? '変更を保存' : 'サブスクを登録');
      await wait(submit + '.disabled===false');
      await click(submit);
      await saved();
      assert.equal(
        await js('return window.browserTest.saved.interval_count'),
        1,
      );
      assert.equal(
        await js('return window.browserTest.saved.interval_unit'),
        'year',
      );
    }
    for (const mode of ['partial-amount', 'partial-currency', 'transaction']) {
      await visit(mode, width);
      const amount = field('通常継続料金');
      await wait('document.querySelector("select[aria-label=通貨]")!==null');
      assert.equal(
        await js('return ' + amount + '.value'),
        mode === 'partial-amount' ? '9' : '',
      );
      assert.equal(
        await js(
          'return document.querySelector("select[aria-label=通貨]").value',
        ),
        mode === 'partial-currency' ? 'USD' : '',
      );
      assert.equal(
        await js(
          'return document.querySelector("button[type=submit]").disabled',
        ),
        true,
      );
      if (mode === 'transaction') {
        await click('document.querySelector("input[type=checkbox]")');
        await click(button('抽出された請求額を使う'));
        await wait(amount + '.value==="9"');
        assert.equal(
          await js(
            'return document.querySelector("input[type=checkbox]").checked',
          ),
          false,
          'Applying transaction amount must reset confirmation',
        );
      } else if (mode === 'partial-amount') {
        assert.ok(
          await js(
            'return document.body.textContent.includes("通貨を選択してください")',
          ),
        );
        await input(
          'document.querySelector("select[aria-label=通貨]")',
          'USD',
          'change',
        );
      } else await input(amount, '9');
      await click('document.querySelector("input[type=checkbox]")');
      await click(button('サブスクを登録'));
      await saved();
    }
    await visit('name', width);
    await wait('document.querySelector("dialog input")!==null');
    await input('document.querySelector("dialog input")', '仕事用のメール');
    await click(button('保存'));
    await saved();
    assert.equal(
      await js('return document.querySelector("[data-title]").textContent'),
      '仕事用のメール',
    );
    assert.equal(
      await js('return window.browserTest.saved.service'),
      'Example Mail',
    );
    await click(button('名前を再変更'));
    await wait('document.querySelector("dialog input")!==null');
    await js('window.browserTest.saved=null');
    await input('document.querySelector("dialog input")', '');
    await click(button('保存'));
    await saved();
    assert.equal(
      await js('return document.querySelector("[data-title]").textContent'),
      'Example Mail',
    );
    assert.equal(
      await js('return window.browserTest.saved.service'),
      'Example Mail',
    );

    await visit('attach', width);
    await wait('document.querySelector("input[type=radio]")!==null');
    assert.equal(
      await js(
        'return document.querySelector("input[type=checkbox]").disabled',
      ),
      true,
    );
    await click(button('次へ'));
    await wait(
      'window.browserTest.requests.some(r=>r.url.includes("offset=20"))',
    );
    await input('document.querySelector("input[type=search]")', 'Example');
    await click(button('検索'));
    await wait(
      'window.browserTest.requests.some(r=>r.url.includes("q=Example")&&r.url.includes("offset=0"))',
    );
    await wait('document.querySelector("input[type=radio]")!==null');
    await click('document.querySelector("input[type=radio]")');
    await wait(
      'document.querySelector("input[type=checkbox]").disabled===false',
    );
    assert.equal(
      await js('return ' + button('確認したサブスクへ紐付ける') + '.disabled'),
      true,
    );
    const metrics = await js(
      'const r=document.querySelector("input[type=radio]"),c=document.querySelector("input[type=checkbox]"),a=c.getBoundingClientRect(),b=c.nextElementSibling.getBoundingClientRect();return {radio:r.getBoundingClientRect().width,direction:getComputedStyle(c.closest("label")).flexDirection,aligned:a.right<=b.left&&Math.abs(a.top-b.top)<8}',
    );
    assert.equal(metrics.radio, 20, 'Radio should keep its 20px width');
    assert.equal(metrics.direction, 'row');
    assert.ok(
      metrics.aligned,
      'Confirmation text should align beside checkbox',
    );
    await layout();
    await click('document.querySelector("input[type=checkbox]")');
    await click(button('確認したサブスクへ紐付ける'));
    await saved();
    assert.equal(
      await js(
        'return window.browserTest.requests.filter(r=>r.method==="POST").length',
      ),
      1,
    );

    await visit('status', width);
    await wait('document.querySelectorAll(".source-state").length===2');
    if (width < 700) {
      await click(
        'document.querySelector("button[aria-label=メニューを開く]")',
      );
      await wait(
        'document.querySelector("button[aria-label=メニューを開く]").getAttribute("aria-expanded")==="true"',
      );
    }
    assert.equal(
      await js(
        'return document.querySelectorAll(".source-state.error").length',
      ),
      1,
    );
    assert.equal(
      await js(
        'return document.querySelectorAll(".source-state.ready").length',
      ),
      1,
    );
    const colors = await js(
      'return ["error","ready"].map(c=>getComputedStyle(document.querySelector(".source-state."+c)).backgroundColor)',
    );
    assert.deepEqual(
      colors,
      ['rgb(255, 146, 155)', 'rgb(108, 209, 164)'],
      'Stopped must be red and waiting must be green',
    );
    if (width < 700) await closeMobileMenu();
    assert.equal(
      await js(
        'return Array.from(document.querySelectorAll("dt")).filter(e=>e.textContent==="最終実行開始").length',
      ),
      2,
    );
    assert.equal(
      await js(
        'return document.querySelectorAll(".automation-activity time").length',
      ),
      5,
    );
    const activity = await js(
      'return Array.from(document.querySelectorAll(".automation-activity")).map(e=>Array.from(e.querySelectorAll("time")).map(t=>({at:t.dateTime,text:t.textContent})))',
    );
    for (const times of activity) {
      assert.deepEqual(times.slice(0, 2), [
        { at: '2026-11-01T12:00:00.000Z', text: '2026/11/01 21:00' },
        { at: '2026-11-01T12:05:00.000Z', text: '2026/11/01 21:05' },
      ]);
    }
    assert.equal(
      activity[0].length,
      2,
      'Stopped import must omit its stale next-check timestamp',
    );
    assert.equal(
      activity[1].length,
      3,
      'Waiting import must show its next-check timestamp',
    );
    assert.ok(
      await js(
        'return document.body.textContent.includes("停止中（予定なし）")',
      ),
    );
    await layout();
    for (const mode of [
      'change',
      'change-conflict',
      'change-uncertain',
      'change-source-conflict',
    ]) {
      await visit(mode, width);
      await wait('document.querySelector("input[type=radio]")!==null');
      await click('document.querySelector("input[type=radio]")');
      await click(button('契約の変更を確認'));
      await wait(
        'document.querySelectorAll(".change-review input[type=checkbox]").length===4',
      );
      const boxes =
        'document.querySelectorAll(".change-review input[type=checkbox]")';
      assert.equal(
        await js(`return Array.from(${boxes}).some(e=>e.checked)`),
        false,
      );
      await click(boxes + '[0]');
      await click(boxes + '[3]');
      await click(boxes + '[1]');
      assert.equal(
        await js(`return ${boxes}[3].checked`),
        false,
        'Changing selected fields must reset confirmation',
      );
      await click(boxes + '[1]');
      await click(boxes + '[3]');
      await layout();
      await click(button('選択した変更を反映'));
      if (mode === 'change-source-conflict') {
        await wait(button('一覧に戻って最新の抽出結果を確認') + '!==undefined');
        await click(button('最新の差分を確認し直す'));
        await wait(
          'window.browserTest.requests.filter(r=>r.url.endsWith("change-preview")).length===2 && Array.from(document.querySelectorAll("button")).some(e=>e.textContent.trim()==="一覧に戻って最新の抽出結果を確認")',
        );
        await click(button('一覧に戻って最新の抽出結果を確認'));
        assert.equal(await js('return window.browserTest.back'), true);
        await layout();
        continue;
      }
      if (mode === 'change-conflict') {
        await wait(button('最新の差分を確認し直す') + '!==undefined');
        assert.equal(
          await js(`return Array.from(${boxes}).some(e=>e.checked)`),
          false,
        );
        await click(button('最新の差分を確認し直す'));
        await wait(
          'window.browserTest.requests.filter(r=>r.url.endsWith("change-preview")).length===2 && document.querySelectorAll(".change-review input[type=checkbox]").length===4',
        );
        assert.equal(
          await js(`return Array.from(${boxes}).some(e=>e.checked)`),
          false,
        );
        await click(boxes + '[0]');
        await click(boxes + '[3]');
        await click(button('選択した変更を反映'));
      } else if (mode === 'change-uncertain') {
        await wait(button('同じ要求で変更を確認') + '!==undefined');
        assert.equal(
          await js(`return Array.from(${boxes}).every(e=>e.disabled)`),
          true,
        );
        assert.equal(
          await js(
            'return document.querySelector("input[type=radio]").disabled',
          ),
          true,
        );
        await click(button('同じ要求で変更を確認'));
      }
      await saved();
      assert.deepEqual(
        await js(
          'const s=window.browserTest.saved;return [s.amount,s.currency,s.interval_unit,s.interval_count,s.next_billing_date]',
        ),
        ['19', 'USD', 'month', 1, '2027-11-01'],
      );
      await wait('document.querySelector(".change-history details")!==null');
      await click('document.querySelector(".change-history summary")');
      assert.ok(
        await js(
          'return document.querySelector(".change-history").textContent.includes("変更前")&&document.querySelector(".change-history").textContent.includes("変更後")',
        ),
      );
      assert.equal(
        await js(
          'return document.querySelector(".change-history [data-label=変更後]").textContent',
        ),
        '19',
      );
      await layout();
    }
    await visit('lifecycle-create', width);
    await wait('document.querySelector("form input")!==null');
    await input(field('サービス名'), 'Example Mail');
    assert.equal(
      await js('return ' + field('自動更新（任意）', 'select') + '.value'),
      '',
    );
    await input(field('自動更新（任意）', 'select'), 'on', 'change');
    assert.equal(
      await js('return ' + field('自動更新（任意）', 'select') + '.value'),
      'on',
    );
    await input(field('自動更新（任意）', 'select'), '', 'change');
    await input(field('自動更新（任意）', 'select'), 'off', 'change');
    await input(field('無料期間終了日'), '2028-02-29');
    await input(field('利用終了日'), '2028-03-01');
    assert.equal(
      await js('return ' + field('通常継続料金') + '.value'),
      '',
      'Trial must not invent a recurring charge',
    );
    await click('document.querySelector("input[type=checkbox]")');
    await click(button('サブスクを登録'));
    await saved();

    await visit('lifecycle-raw-edit', width);
    await wait('document.querySelector("input[type=date]")!==null');
    assert.equal(
      await js('return ' + field('次回請求日（任意）') + '.value'),
      '2027-11-01',
    );
    await input(field('プラン（任意）'), '仕事用');
    await click('document.querySelector("input[type=checkbox]")');
    await click(button('変更を保存'));
    await saved();
    await js('window.browserTest.saved=null');
    await input(field('次回請求日（任意）'), '');
    await click('document.querySelector("input[type=checkbox]")');
    await click(button('変更を保存'));
    await saved();

    await visit('change-lifecycle', width);
    await wait('document.querySelector("input[type=radio]")!==null');
    await click('document.querySelector("input[type=radio]")');
    await click(button('契約の変更を確認'));
    await wait(
      'document.querySelectorAll(".change-review input[type=checkbox]").length===5',
    );
    await click(
      'document.querySelectorAll(".change-review input[type=checkbox]")[0]',
    );
    await click(
      'document.querySelectorAll(".change-review input[type=checkbox]")[4]',
    );
    await click(button('選択した変更を反映'));
    await saved();
    assert.equal(await js('return window.browserTest.saved.auto_renew'), false);
    assert.equal(await js('return window.browserTest.saved.status'), 'active');
    await wait('document.querySelector(".change-history summary")!==null');
    await click('document.querySelector(".change-history summary")');
    assert.equal(
      await js(
        'return document.querySelector(".change-history [data-label=変更後]").textContent',
      ),
      '停止',
    );
    await layout();

    await visit('lifecycle-labels', width);
    await wait('document.body.textContent.includes("自動更新停止")');
    assert.ok(
      await js(
        'return document.body.textContent.includes("利用終了予定 2040-03-01")&&document.body.textContent.includes("利用終了 2020-03-01")&&document.body.textContent.includes("利用中")',
      ),
    );
    await layout();
    await visit('lifecycle-report', width);
    await wait(
      'document.body.textContent.includes("集計できる登録料金がありません")',
    );
    assert.ok(
      await js('return document.body.textContent.includes("利用終了 1")'),
    );
    await layout();
    const logs = await api('POST', session + '/log', { type: 'browser' });
    assert.deepEqual(
      logs.filter((entry) => ['SEVERE', 'WARNING'].includes(entry.level)),
      [],
    );
    results.push({
      width,
      cases: [
        'register',
        'edit',
        'partial-amount',
        'partial-currency',
        'transaction',
        'name',
        'attach',
        'status',
        'change',
        'change-conflict',
        'change-uncertain',
        'change-source-conflict',
        'lifecycle-create',
        'lifecycle-raw-edit',
        'change-lifecycle',
        'lifecycle-labels',
        'lifecycle-report',
      ],
      status: 'pass',
    });
  }
  console.log(JSON.stringify(results));
} catch (error) {
  if (session) {
    const capture = async (operation) => {
      try {
        return await operation();
      } catch (failure) {
        return { captureError: String(failure) };
      }
    };
    await capture(async () =>
      writeFile(
        out + '/failure.png',
        Buffer.from(await api('GET', session + '/screenshot'), 'base64'),
      ),
    );
    await writeFile(
      out + '/diagnostics.json',
      JSON.stringify(
        {
          scenario,
          error: String(error),
          text: await capture(() => js('return document.body.innerText')),
          trace: await capture(() => js('return window.browserTest')),
          logs: await capture(() =>
            api('POST', session + '/log', { type: 'browser' }),
          ),
        },
        null,
        2,
      ),
    ).catch(() => {});
  }
  throw error;
} finally {
  await writeFile(out + '/results.json', JSON.stringify(results, null, 2));
  if (session) await api('DELETE', session);
}
