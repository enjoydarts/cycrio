import assert from 'node:assert/strict';
import test from 'node:test';
import { clerkAppearance } from './clerk-appearance.ts';
test('Clerk renders the single Sifto-style login card using the application theme', () => {
  assert.equal(clerkAppearance.options.elevation, 'raised');
  assert.equal(clerkAppearance.variables.colorPrimary, 'var(--color-action)');
  assert.equal(
    clerkAppearance.variables.colorPrimaryForeground,
    'var(--color-on-action)',
  );
  assert.equal(clerkAppearance.variables.colorForeground, 'var(--color-text)');
  assert.equal(
    clerkAppearance.variables.colorMutedForeground,
    'var(--color-muted)',
  );
  assert.equal(clerkAppearance.variables.colorBorder, 'var(--color-border)');
  assert.equal(clerkAppearance.variables.fontFamily, 'inherit');
  assert.equal(
    clerkAppearance.elements.socialButtonsBlockButton.minHeight,
    '48px',
  );
  assert.equal(
    clerkAppearance.elements.socialButtonsBlockButton.borderRadius,
    '6px',
  );
  assert.equal(clerkAppearance.elements.headerTitle.textAlign, 'left');
  assert.equal(clerkAppearance.elements.card.padding, '32px 40px');
  assert.equal(clerkAppearance.elements.cardBox.borderRadius, '12px');
  assert.equal(
    clerkAppearance.elements.socialButtonsBlockButton.background,
    'transparent',
  );
  assert.equal(
    clerkAppearance.elements.socialButtonsBlockButton.backgroundImage,
    'none',
  );
  assert.equal(clerkAppearance.elements.socialButtonsBlockButton.border, '0');
  assert.equal(
    clerkAppearance.elements.socialButtonsBlockButtonText.color,
    'var(--color-text)',
  );
  for (const name of ['footer', 'formFieldErrorText', 'alertText', 'header'])
    assert.notEqual(
      (clerkAppearance.elements as Record<string, unknown>)[name] &&
        (clerkAppearance.elements as Record<string, any>)[name].display,
      'none',
    );
});
