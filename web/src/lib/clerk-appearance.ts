import type { Clerk } from '@clerk/clerk-js';

// Clerk owns the single login card; Cycrio supplies the surrounding brand.
// Verification steps, errors and attribution stay intact.
export const clerkAppearance = {
  options: { elevation: 'raised', socialButtonsVariant: 'blockButton' },
  variables: {
    colorPrimary: 'var(--color-action)',
    colorPrimaryForeground: 'var(--color-on-action)',
    colorForeground: 'var(--color-text)',
    colorMutedForeground: 'var(--color-muted)',
    colorBackground: 'var(--color-surface)',
    colorInput: 'var(--color-surface)',
    colorInputForeground: 'var(--color-text)',
    colorBorder: 'var(--color-border)',
    colorRing: 'var(--color-action)',
    colorDanger: 'var(--color-danger)',
    colorSuccess: 'var(--color-action)',
    fontFamily: 'inherit',
    fontFamilyButtons: 'inherit',
    fontSize: '14px',
    borderRadius: '6px',
  },
  elements: {
    rootBox: { width: '100%' },
    cardBox: {
      width: '100%',
      boxShadow:
        '0 5px 15px rgba(0,0,0,.08), 0 15px 35px -5px rgba(0,0,0,.2), 0 0 0 1px rgba(0,0,0,.07)',
      border: '0',
      borderRadius: '12px',
    },
    card: {
      boxShadow:
        '0 0 2px rgba(0,0,0,.08), 0 1px 2px rgba(0,0,0,.06), 0 0 0 1px rgba(0,0,0,.03)',
      border: '0',
      padding: '32px 40px',
      gap: '32px',
    },
    header: { textAlign: 'left' },
    headerTitle: {
      color: 'var(--color-text)',
      fontSize: '18px',
      fontWeight: '600',
      textAlign: 'left',
    },
    headerSubtitle: {
      color: 'var(--color-muted)',
      fontSize: '13px',
      lineHeight: '1.7',
      textAlign: 'left',
    },
    socialButtonsBlockButton: {
      minHeight: '48px',
      borderRadius: '6px',
      border: '0',
      boxShadow:
        '0 0 0 1px rgba(0,0,0,.07), 0 2px 3px -1px rgba(0,0,0,.08), 0 1px 0 rgba(0,0,0,.02)',
      background: 'transparent',
      backgroundImage: 'none',
      color: 'var(--color-text)',
      fontSize: '13px',
      fontWeight: '500',
    },
    socialButtonsBlockButtonText: {
      color: 'var(--color-text)',
      fontFamily: 'inherit',
      fontSize: '13px',
      fontWeight: '500',
    },
    formFieldInput: {
      minHeight: '44px',
      boxShadow: 'none',
      border: '1px solid var(--color-border)',
    },
    formButtonPrimary: {
      minHeight: '44px',
      borderRadius: '6px',
      boxShadow: 'none',
    },
    footer: { background: 'var(--brand-background)', padding: '8px 0 0' },
    footerActionText: { color: 'var(--color-muted)', fontSize: '12px' },
    footerActionLink: {
      color: 'var(--color-action)',
      fontSize: '12px',
      fontWeight: '500',
    },
  },
} satisfies NonNullable<Parameters<Clerk['load']>[0]>['appearance'];
