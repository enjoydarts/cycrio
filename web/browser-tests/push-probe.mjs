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
    assert.deepEqual(await js('return window.browserTest.pushCalls'), []);
    await click(
      'document.querySelector(\'input[aria-label="本人の検証済みメールへ通知を送る"]\')',
    );
    assert.deepEqual(await js('return window.browserTest.pushCalls'), []);
    await click(button('この端末でPush通知を有効にする'));
    await wait(button('この端末の通知を許可する') + '!==undefined');
    assert.deepEqual(await js('return window.browserTest.pushCalls'), [
      'prepare',
    ]);
    await screenshot('push-permission-' + width);
    await click(button('この端末の通知を許可する'));
    await wait(
      'document.body.textContent.includes("この端末のPush通知は有効です")',
    );
    assert.equal(
      await js('return window.browserTest.pushStoredBeforeConfirm'),
      true,
    );
    await screenshot('push-settings-' + width);
    await js('window.scrollTo(0,document.body.scrollHeight)');
    await screenshot('push-devices-' + width);
    await layout();
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
    await visit('push-late', width);
    await click(
      'document.querySelector(\'a[href="/settings/notifications"]\')',
    );
    await wait(button('この端末でPush通知を有効にする') + '!==undefined');
    await click(button('この端末でPush通知を有効にする'));
    await wait(button('この端末の通知を許可する') + '!==undefined');
    await click(button('この端末の通知を許可する'));
    await wait('document.body.textContent.includes("受信確認中です")');
    await js('window.browserTest.pushEmit()');
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
      if (mode === 'push-denied') {
        await wait(button('この端末の通知を許可する') + '!==undefined');
        await click(button('この端末の通知を許可する'));
      }
      await wait(
        'document.body.textContent.includes(' + JSON.stringify(message) + ')',
      );
      await layout();
    }
    cases.push({ width, status: 'pass' });
  }
  return { name: 'push', cases };
}
