import assert from 'node:assert/strict';
export async function runPushInvitationProbe({
  api,
  session,
  visit,
  js,
  wait,
  click,
  layout,
  screenshot,
}) {
  const button = (label) =>
    'Array.from(document.querySelectorAll("dialog[open] button")).find(b=>b.textContent.trim()===' +
    JSON.stringify(label) +
    ')';
  const open = 'document.querySelector("dialog[open]")';
  const cases = [];
  for (const width of [1280, 390, 320]) {
    await api('POST', session + '/goog/cdp/execute', {
      cmd: 'Emulation.setDeviceMetricsOverride',
      params: { width, height: 1100, deviceScaleFactor: 1, mobile: false },
    });
    await visit('push-invitation', width);
    await wait(open + '?.textContent.includes("Push通知を有効にしますか？")');
    assert.deepEqual(await js('return window.browserTest.pushCalls'), []);
    assert.equal(
      await js(
        'return document.querySelector("dialog[open]").getBoundingClientRect().width<=480',
      ),
      true,
    );
    await screenshot('push-invitation-' + width);
    await layout();
    await click(button('あとで'));
    await wait('!' + open);
    await visit('push-invitation-seen', width);
    await wait('document.querySelector("main")');
    assert.equal(
      await js('return !!document.querySelector("dialog[open]")'),
      false,
    );
    assert.deepEqual(await js('return window.browserTest.pushCalls'), []);
    await visit('push-invitation', width);
    await wait(open + '?.textContent.includes("Push通知を有効にしますか？")');
    await click(button('有効にする'));
    await wait(button('この端末の通知を許可する') + '!==undefined');
    assert.deepEqual(await js('return window.browserTest.pushCalls'), [
      'preferences',
      'prepare',
    ]);
    await click(button('この端末の通知を許可する'));
    await wait('window.browserTest.pushActive===true');
    await wait(open + '?.textContent.includes("この端末のPush通知は有効です")');
    assert.equal(
      await js('return window.browserTest.pushStoredBeforeConfirm'),
      true,
    );
    assert.equal(
      await js(
        'return window.browserTest.pushCalls.filter(x=>x==="permission").length',
      ),
      1,
    );
    await screenshot('push-invitation-active-' + width);
    await layout();
    await visit('push-invitation-home', width);
    await wait(open + '?.textContent.includes("Push通知を有効にしますか？")');
    await click(button('有効にする'));
    await wait(open + '?.textContent.includes("Safariの共有メニュー")');
    assert.deepEqual(await js('return window.browserTest.pushCalls'), []);
    await screenshot('push-invitation-home-' + width);
    await layout();
    assert.equal(
      await js(
        'return document.querySelectorAll("script[src*=onesignal]").length',
      ),
      0,
    );
    cases.push({ width, status: 'pass' });
  }
  return { name: 'push-invitation', cases };
}
