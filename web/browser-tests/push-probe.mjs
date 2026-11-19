import assert from 'node:assert/strict';
export async function runPushProbe({
  api,
  session,
  visit,
  js,
  wait,
  click,
  layout,
  screenshot,
}) {
  const button = (text) =>
    `Array.from(document.querySelectorAll('button')).find(e=>e.textContent.trim()===${JSON.stringify(text)})`;
  const cases = [];
  for (const width of [1280, 390, 320]) {
    await api('POST', session + '/goog/cdp/execute', {
      cmd: 'Emulation.setDeviceMetricsOverride',
      params: { width, height: 1100, deviceScaleFactor: 1, mobile: false },
    });
    await visit('push', width);
    await click(
      'document.querySelector(\'a[href="/settings/notifications"]\')',
    );
    await wait(
      'document.querySelector(\'section[aria-label="Push通知の端末設定"]\')!==null',
    );
    await wait(
      'document.querySelector(\'input[aria-label="本人の検証済みメールへ通知を送る"]\')!==null',
    );
    if (width < 700) {
      await wait(button('この端末でPush通知を有効にする') + '!==undefined');
      const settingsLayout = await js(`
        const settings = document.querySelector('section[aria-label="本人の通知設定"]');
        const devices = document.querySelector('section[aria-label="Push通知の端末設定"]');
        const fills = (control, section) => {
          const outer = section.getBoundingClientRect();
          const bounds = control.getBoundingClientRect();
          return Math.abs(bounds.left - outer.left) <= 1 && Math.abs(bounds.right - outer.right) <= 1;
        };
        const timing = [...settings.querySelectorAll('input[type="number"], select')];
        const [enable, refresh] = devices.querySelectorAll('button');
        return {
          timingFullWidth: timing.every(control => fills(control, settings)),
          timingStacked: timing[0].getBoundingClientRect().bottom < timing[1].getBoundingClientRect().top,
          saveFullWidth: fills(settings.querySelector('button[type="submit"]'), settings),
          pushActionsFullWidth: fills(enable, devices) && fills(refresh, devices),
          pushActionsStacked: enable.getBoundingClientRect().bottom < refresh.getBoundingClientRect().top,
        };
      `);
      assert.deepEqual(
        settingsLayout,
        {
          timingFullWidth: true,
          timingStacked: true,
          saveFullWidth: true,
          pushActionsFullWidth: true,
          pushActionsStacked: true,
        },
        `Notification settings at ${width}px`,
      );
    }
    await screenshot(`notification-settings-page-${width}`);
    assert.deepEqual(await js('return window.browserTest.pushCalls'), []);
    await click(
      'document.querySelector(\'input[aria-label="本人の検証済みメールへ通知を送る"]\')',
    );
    assert.deepEqual(await js('return window.browserTest.pushCalls'), []);
    await click(button('この端末でPush通知を有効にする'));
    await wait(
      'document.body.textContent.includes("この端末のPush通知は有効です")',
    );
    assert.deepEqual(await js('return window.browserTest.pushCalls'), [
      'permission',
      'prepare',
      'subscription',
      'register',
      'confirm',
    ]);
    assert.equal(
      await js('return window.browserTest.pushStoredBeforeConfirm'),
      true,
    );
    await screenshot('push-settings-' + width);
    await js('window.scrollTo(0,document.body.scrollHeight)');
    await screenshot('push-devices-' + width);
    await layout();
    if (width < 700) {
      assert.equal(
        await js(`
          const devices = document.querySelector('section[aria-label="Push通知の端末設定"]');
          const table = devices.querySelector('.ui-table-scroll');
          const refresh = devices.querySelector('button');
          const { left, right } = devices.getBoundingClientRect();
          return table.scrollWidth <= table.clientWidth &&
            table.getBoundingClientRect().top > refresh.getBoundingClientRect().bottom &&
            [...devices.querySelectorAll('button')].every(button => {
              const bounds = button.getBoundingClientRect();
              return bounds.left >= left && bounds.right <= right;
            });
        `),
        true,
        `Push device controls fit at ${width}px`,
      );
    }
    assert.equal(
      await js(
        'return document.querySelectorAll("script[src*=onesignal]").length',
      ),
      0,
    );
    assert.equal(
      await js(
        'return performance.getEntriesByType("resource").some(e=>/onesignal|cdn\\.onesignal/.test(e.name))',
      ),
      false,
    );
    await js('window.browserTest.pushRefreshToken()');
    await js('window.browserTest.pushToggleSettings()');
    await js('window.browserTest.pushToggleSettings()');
    await wait(
      'document.body.textContent.includes("この端末のPush通知は有効です")',
    );
    assert.equal(
      await js(
        'return window.browserTest.pushCalls.filter(x=>x==="revoke").length',
      ),
      0,
    );
    await js('window.browserTest.pushLogout()');
    await wait('window.browserTest.pushActive===false');
    await visit('push-settings-on', width);
    await click(
      'document.querySelector(\'a[href="/settings/notifications"]\')',
    );
    const pushToggle =
      'document.querySelector(\'input[aria-label="登録した端末へPush通知を送る"]\')';
    await wait(pushToggle + '!==null');
    await click(pushToggle);
    await click(
      'document.querySelector(\'section[aria-label="本人の通知設定"] form > label:last-of-type input\')',
    );
    await click(button('通知設定を保存'));
    await wait('window.browserTest.pushActive===true');
    await wait(
      'document.body.textContent.includes("この端末のPush通知は有効です")',
    );
    assert.equal(
      await js(
        'return window.browserTest.pushCalls.filter(x=>x==="permission").length',
      ),
      1,
    );
    await visit('push-prompt-wait', width);
    await click(
      'document.querySelector(\'a[href="/settings/notifications"]\')',
    );
    await wait(button('この端末でPush通知を有効にする') + '!==undefined');
    await click(button('この端末でPush通知を有効にする'));
    await wait('window.browserTest.pushCalls.includes("permission")');
    await js('return window.browserTest.pushSetPermission("default")');
    assert.equal(
      await js(
        'return document.body.textContent.includes("この端末の通知を設定しています")',
      ),
      true,
    );
    assert.equal(
      await js('return window.browserTest.pushCalls.includes("register")'),
      false,
    );
    await js('window.browserTest.pushFinishPermission()');
    await wait('window.browserTest.pushActive===true');
    assert.equal(
      await js(
        'return window.browserTest.pushCalls.filter(x=>x==="register").length',
      ),
      1,
    );
    await layout();
    for (const permission of ['denied', 'default']) {
      await visit('push', width);
      await click(
        'document.querySelector(\'a[href="/settings/notifications"]\')',
      );
      await wait(button('この端末でPush通知を有効にする') + '!==undefined');
      await click(button('この端末でPush通知を有効にする'));
      await wait('window.browserTest.pushActive===true');
      await js(
        'window.browserTest.pushSetPermission(' +
          JSON.stringify(permission) +
          ')',
      );
      await wait(
        'document.body.textContent.includes("通知が拒否されています")',
      );
      assert.equal(await js('return window.browserTest.pushActive'), false);
      assert.equal(
        await js(
          'return window.browserTest.pushCalls.filter(x=>x==="permission").length',
        ),
        1,
      );
      await layout();
    }
    await visit('push-refresh', width);
    await click(
      'document.querySelector(\'a[href="/settings/notifications"]\')',
    );
    await wait(button('この端末でPush通知を有効にする') + '!==undefined');
    await click(button('この端末でPush通知を有効にする'));
    await wait(
      'document.body.textContent.includes("この端末のPush通知は有効です")',
    );
    await click(button('端末一覧を再取得'));
    await wait(
      'document.body.textContent.includes("端末一覧を再取得できませんでした。")',
    );
    assert.equal(
      await js(
        'return document.body.textContent.includes("この端末のPush通知は有効です")',
      ),
      true,
    );
    await click(button('端末一覧を再取得'));
    await wait(
      'document.body.textContent.includes("端末一覧を更新しました。")',
    );
    await wait(
      'document.body.textContent.includes("この端末のPush通知は有効です")',
    );
    assert.deepEqual(
      await js(
        'return window.browserTest.pushCalls.filter(call=>["register","confirm"].includes(call))',
      ),
      ['register', 'confirm'],
    );
    await layout();
    for (const mode of ['push-sdk-retry', 'push-sdk-reload']) {
      await visit(mode, width);
      await click(
        'document.querySelector(\'a[href="/settings/notifications"]\')',
      );
      await wait(button('この端末でPush通知を有効にする') + '!==undefined');
      await click(button('この端末でPush通知を有効にする'));
      if (mode === 'push-sdk-retry') {
        await wait(
          'document.body.textContent.includes("端末の通知を設定できませんでした")',
        );
        await click(button('端末一覧を再取得'));
        await click(button('この端末でPush通知を有効にする'));
        await wait('window.browserTest.pushActive===true');
      } else {
        await wait(
          'document.body.textContent.includes("ページを再読み込みしてから操作してください")',
        );
        assert.equal(
          await js(
            'return ' +
              button('この端末でPush通知を有効にする') +
              '===undefined',
          ),
          true,
        );
      }
      await layout();
    }
    await visit('push-late', width);
    await click(
      'document.querySelector(\'a[href="/settings/notifications"]\')',
    );
    await wait(button('この端末でPush通知を有効にする') + '!==undefined');
    await click(button('この端末でPush通知を有効にする'));
    await wait('window.browserTest.pushCalls.includes("confirm")');
    await js('window.browserTest.pushLogout()');
    await js('window.browserTest.pushFinishConfirm()');
    await wait('window.browserTest.pushActive===false');
    for (const [mode, message] of [
      ['push-home', 'ホーム画面に追加'],
      ['push-unsupported', '対応していません'],
      ['push-denied', '通知が拒否されています'],
    ]) {
      await visit(mode, width);
      await click(
        'document.querySelector(\'a[href="/settings/notifications"]\')',
      );
      await wait(button('この端末でPush通知を有効にする') + '!==undefined');
      await click(button('この端末でPush通知を有効にする'));
      await wait(
        'document.body.textContent.includes(' + JSON.stringify(message) + ')',
      );
      await layout();
    }
    await visit('push-subscription-retry', width);
    await click(
      'document.querySelector(\'a[href="/settings/notifications"]\')',
    );
    await wait(button('この端末でPush通知を有効にする') + '!==undefined');
    await click(button('この端末でPush通知を有効にする'));
    await wait(
      'document.body.textContent.includes("端末の通知を設定できませんでした")',
    );
    assert.equal(
      await js(
        'return window.browserTest.pushCalls.filter(x=>x==="register").length',
      ),
      0,
    );
    await click(button('端末一覧を再取得'));
    await click(button('この端末でPush通知を有効にする'));
    await wait(
      'document.body.textContent.includes("この端末のPush通知は有効です")',
    );
    assert.equal(
      await js(
        'return window.browserTest.pushCalls.filter(x=>x==="register").length',
      ),
      1,
    );
    await layout();
    cases.push({ width, status: 'pass' });
  }
  return { name: 'push', cases };
}
