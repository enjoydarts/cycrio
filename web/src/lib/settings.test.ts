import assert from 'node:assert/strict';
import test from 'node:test';
import {
  settingsSections,
  availableSettings,
  settingsAccess,
} from './settings.ts';
test('ordinary owners see only mail and appearance, administrators retain every separate setting URL', () => {
  assert.deepEqual(
    availableSettings('user').map((section) => section.id),
    ['mail', 'appearance'],
  );
  assert.equal(availableSettings('platform_admin').length, 10);
  assert.equal(new Set(settingsSections.map((section) => section.id)).size, 10);
});
test('direct administrator routes deny owners and unknown sections are unavailable', () => {
  for (const section of settingsSections) {
    assert.equal(settingsAccess(section.id, 'platform_admin'), 'allowed');
    assert.equal(
      settingsAccess(section.id, 'user'),
      section.admin ? 'denied' : 'allowed',
    );
  }
  assert.equal(settingsAccess('does-not-exist', 'platform_admin'), 'not_found');
  assert.equal(settingsAccess('does-not-exist', 'user'), 'not_found');
});
