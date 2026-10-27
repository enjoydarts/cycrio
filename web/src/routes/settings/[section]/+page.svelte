<script lang="ts">
  import { page } from '$app/state';
  import { getAppSession } from '#lib/app-context.ts';
  import { settingsAccess, settingsSections } from '#lib/settings.ts';
  import MailAccounts from '#lib/MailAccounts.svelte';
  import Appearance from '#lib/Appearance.svelte';
  import AdminCredentials from '#lib/AdminCredentials.svelte';
  import ModelCatalog from '#lib/ModelCatalog.svelte';
  import AIUsage from '#lib/AIUsage.svelte';
  import AISetup from '#lib/AISetup.svelte';
  import IntegrationSettings from '#lib/IntegrationSettings.svelte';
  import { createIntegrationAPI } from '#lib/integrations-api.ts';
  import { PUBLIC_API_BASE_URL } from '$app/env/public';
  const integrationAPI = createIntegrationAPI(PUBLIC_API_BASE_URL);
  const session = getAppSession();
  const section = $derived(page.params.section ?? '');
  const access = $derived(
    session.user ? settingsAccess(section, session.user.role) : 'denied',
  );
  const title = $derived(
    access === 'allowed'
      ? (settingsSections.find((item) => item.id === section)?.title ?? '設定')
      : '設定',
  );
</script>

<svelte:head
  ><title>{title} — Cycrio</title><meta
    name="robots"
    content="noindex"
  /></svelte:head
>
<header class="page-heading">
  <a class="settings-back" href="/settings">← 設定一覧に戻る</a>
  <h1>設定</h1>
</header>
<div class="settings-content">
  {#if access === 'denied'}
    <h2>管理者の権限が必要です</h2>
    <p>この設定を利用する権限がありません。</p>
    <a href="/settings/mail">メール連携へ戻る</a>
  {:else if access === 'not_found'}
    <h2>設定が見つかりません</h2>
    <p>指定された設定ページはありません。</p>
    <a href="/settings/mail">メール連携へ戻る</a>
  {:else}
    {#key section}
      {#if section === 'mail'}
        <MailAccounts
          api={session.mailAPI}
          syncAPI={session.mailSyncAPI}
          token={session.token}
          onDenied={session.onDenied}
          returnTo={page.url.searchParams.get('return_to')}
        />
      {:else if section === 'appearance'}
        <Appearance />
      {:else if section === 'credentials'}
        <AdminCredentials
          api={session.api}
          token={session.token}
          onDenied={session.onDenied}
        />
      {:else if section === 'integrations'}
        <IntegrationSettings
          api={integrationAPI}
          token={session.token}
          onDenied={session.onDenied}
        />
      {:else if section === 'catalog'}
        <ModelCatalog
          api={session.catalogAPI}
          token={session.token}
          onDenied={session.onDenied}
        />
      {:else if ['ai', 'ai-models', 'ai-runtime', 'mail-policy'].includes(section)}
        <AISetup
          initialStage={section === 'ai-models'
            ? 'models'
            : section === 'ai-runtime'
              ? 'test'
              : section === 'mail-policy'
                ? 'permission'
                : undefined}
        />
      {:else if section === 'usage'}
        <AIUsage
          api={session.runtimeAPI}
          token={session.token}
          onDenied={session.onDenied}
        />
      {/if}
    {/key}
  {/if}
</div>

<style>
  .settings-back {
    display: none;
  }
  .settings-content {
    min-width: 0;
    max-width: 1060px;
  }
  @media (max-width: 700px) {
    .settings-back {
      display: block;
      width: fit-content;
      min-height: 44px;
      padding-block: 8px;
      margin-bottom: 8px;
    }
  }
</style>
