import assert from 'node:assert/strict';
import test from 'node:test';
import {
  settingsSections,
  availableSettings,
  settingsAccess,
} from './settings.ts';
test('ordinary owners see personal settings including account, administrators retain every separate setting URL', () => {
  assert.deepEqual(
    availableSettings('user').map((section) => section.id),
    ['mail', 'appearance', 'account'],
  );
  assert.equal(availableSettings('platform_admin').length, 11);
  assert.equal(new Set(settingsSections.map((section) => section.id)).size, 11);
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
