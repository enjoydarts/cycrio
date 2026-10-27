<script lang="ts">
  import { onMount } from 'svelte';
  import { getAppSession } from './app-context.ts';
  import { ApiError, type CredentialSummary } from './api.ts';
  import type { MailAccount } from './mail-api.ts';
  import type {
    ProcessingPreview,
    PolicyState,
  } from './mail-processing-api.ts';
  import type { RuntimeState } from './airuntime-api.ts';
  import type { SettingsState } from './catalog-api.ts';
  import type { RoutesResult } from './ai-routes-api.ts';
  import { setupReadiness } from './setup-readiness.ts';
  import SettingsNavigation from './SettingsNavigation.svelte';
  import './setup.css';
  const session = getAppSession();
  let accounts = $state<MailAccount[]>([]),
    previews = $state<ProcessingPreview[]>([]),
    loaded = $state(false),
    error = $state(false),
    busy = $state(false),
    denied = $state(false);
  let runtime = $state<RuntimeState | null>(null),
    policy = $state<PolicyState | null>(null),
    settings = $state<SettingsState | null>(null),
    routes = $state<RoutesResult | null>(null),
    keys = $state<CredentialSummary[]>([]);
  let abort: AbortController | undefined,
    disposed = false,
    generation = 0;
  const admin = $derived(session.user?.role === 'platform_admin');
  const best = $derived(
    previews.find((p) => p.ready && p.full_email_allowed) ??
      previews[0] ??
      null,
  );
  const readiness = $derived(
    setupReadiness({
      loaded,
      accounts: accounts.length,
      preview: best,
      admin,
      error,
    }),
  );
  const permission = $derived(
    !!policy?.policy?.enabled &&
      !!policy.policy.full_email_allowed &&
      !!policy.policy.primary_retention &&
      !!policy.policy.secondary_retention &&
      (policy.policy.standard_proof?.primary_mode !== 'standard' ||
        (policy.policy.standard_proof.primary_terms?.id ===
          policy.standard_terms?.primary.id &&
          policy.policy.standard_proof.primary_terms?.version ===
            policy.standard_terms?.primary.version)),
  );
  const readyAccounts = $derived(
    accounts.filter(
      (a) =>
        a.state === 'ready' && a.folders.some((f) => f.selected && f.available),
    ).length,
  );
  const missingKey = $derived(
    keys.find(
      (k) =>
        (k.provider === 'liquid' ||
          k.provider ===
            (runtime?.draft ?? runtime?.active)?.secondary.provider) &&
        k.state !== 'configured',
    ),
  );
  const rowStatus = $derived(
    error ? '確認できません' : !loaded ? '確認中' : '',
  );
  async function load() {
    if (busy || disposed || denied) return;
    abort?.abort();
    abort = new AbortController();
    const signal = abort.signal,
      seq = ++generation;
    busy = true;
    error = false;
    try {
      const owner = async () => {
        const items = await session.mailAPI.list(session.token, signal);
        return {
          accounts: items,
          previews: await Promise.all(
            items.map((a) =>
              session.mailProcessingAPI.preview(a.id, session.token, signal),
            ),
          ),
        };
      };
      const management = admin
        ? Promise.all([
            session.runtimeAPI.state(session.token, signal),
            session.mailProcessingAPI.policy(session.token, signal),
            session.catalogAPI.settings(session.token, signal),
            session.routesAPI.list({}, session.token, signal),
            session.api.credentials(session.token, signal),
          ])
        : null;
      const [mine, global] = await Promise.all([owner(), management]);
      if (disposed || seq !== generation) return;
      accounts = mine.accounts;
      previews = mine.previews;
      if (global) [runtime, policy, settings, routes, keys] = global;
      loaded = true;
    } catch (e) {
      if (disposed || seq !== generation) return;
      if (
        e instanceof ApiError &&
        ['authentication_required', 'access_denied'].includes(e.code)
      ) {
        accounts = [];
        previews = [];
        runtime = null;
        policy = null;
        settings = null;
        routes = null;
        keys = [];
        loaded = false;
        denied = true;
        session.onDenied();
      } else error = true;
    } finally {
      if (seq === generation) busy = false;
    }
  }
  onMount(() => {
    void load();
    return () => {
      disposed = true;
      generation++;
      abort?.abort();
      accounts = [];
      previews = [];
      runtime = null;
      policy = null;
      settings = null;
      routes = null;
      keys = [];
    };
  });
</script>

<section class="setup-ui" aria-label="設定の準備状況" aria-busy={busy}>
  <div class="setup-heading">
    <div>
      <p class="eyebrow">SETTINGS</p>
      <h1>設定</h1>
      <p>解析までに必要な準備を確認できます。</p>
    </div>
    <button disabled={busy || denied} onclick={load}>準備状況を更新</button>
  </div>
  <div class="mobile-settings-index">
    <SettingsNavigation
      role={session.user?.role ?? 'user'}
      section=""
      variant="list"
    />
  </div>
  <article class="setup-next" aria-live="polite">
    <p class="eyebrow">次のステップ</p>
    <h2>{readiness.title}</h2>
    {#if error}<p>通信状態を確認して読み直してください。</p>
      <button disabled={busy} onclick={load}>もう一度確認</button
      >{:else if readiness.href}<a
        class="setup-button primary"
        href={readiness.href}>{readiness.label}</a
      >{/if}
  </article>
  <div class="setup-status-list">
    <div class="setup-status-row">
      <div>
        <strong>メール連携</strong>
        <p class="muted">
          {rowStatus ||
            (accounts.length
              ? `${accounts.length} 件の接続 · ${readyAccounts} 件が接続・対象フォルダーの準備済み`
              : '未接続')}
        </p>
      </div>
      <a href="/settings/mail">確認・変更</a>
    </div>
    {#if admin}<div class="setup-status-row">
        <div>
          <strong>AI の接続</strong>
          <p class="muted">
            {rowStatus ||
              (runtime?.active_ready
                ? `${runtime.active!.primary.display_name} → ${runtime.active!.secondary.display_name} · 有効・接続確認済み`
                : missingKey
                  ? 'API キーの登録が必要です'
                  : runtime?.draft
                    ? '下書きあり・接続テストと有効化を確認してください'
                    : settings?.id
                      ? 'モデル選択済み・実行設定の準備が必要です'
                      : 'モデルを選択してください')}
          </p>
        </div>
        <a href={missingKey ? '/settings/credentials' : '/settings/ai'}
          >{missingKey ? 'キーを登録' : '確認・変更'}</a
        >
      </div>
      <div class="setup-status-row">
        <div>
          <strong>メール本文の送信許可</strong>
          <p class="muted">
            {rowStatus ||
              (permission
                ? '確認した条件で許可済み'
                : policy?.policy?.enabled
                  ? '最新の送信条件を確認し直してください'
                  : '保持・学習利用条件の確認と許可が必要です')}
          </p>
        </div>
        <a href="/settings/ai?stage=permission">条件を確認</a>
      </div>
    {:else}<div class="setup-status-row">
        <div>
          <strong>メール解析</strong>
          <p class="muted">
            {rowStatus ||
              (best?.ready && best.full_email_allowed
                ? 'モデルと送信許可を確認済み'
                : accounts.length
                  ? '管理者の準備が必要な場合は、管理者へお問い合わせください'
                  : 'メール接続後に準備状況を確認します')}
          </p>
        </div>
      </div>{/if}
    <div class="setup-status-row">
      <div>
        <strong>表示</strong>
        <p class="muted">ライト・ダーク・システム</p>
      </div>
      <a href="/settings/appearance">変更</a>
    </div>
  </div>
  {#if admin}<details class="setup-details">
      <summary>管理の詳細</summary>
      <p class="muted">
        {rowStatus ||
          (routes?.key_available
            ? 'OpenRouter の API キーあり'
            : 'OpenRouter の API キー未登録')}。配信先情報の鮮度は新しい設定・許可の保存時に確認します。
      </p>
      <div class="setup-links">
        <a href="/settings/credentials">API キー</a><a href="/settings/catalog"
          >モデルカタログ・価格履歴</a
        ><a href="/settings/usage">AI 利用原価</a>
      </div>
    </details>{/if}
</section>

<style>
  .mobile-settings-index {
    display: none;
  }
  @media (max-width: 700px) {
    .mobile-settings-index {
      display: block;
      margin-bottom: 28px;
    }
  }
</style>
