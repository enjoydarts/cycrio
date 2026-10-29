<script lang="ts">
  import { getAppSession } from './app-context.ts';
  import { settingsNavigationGroups } from './settings.ts';
  import LibraryHeader from './LibraryHeader.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  const session = getAppSession();
  const descriptions: Record<string, string> = {
    mail: '接続したメールの確認・変更',
    appearance: 'ライト・ダーク・端末の設定に合わせる',
    ai: 'モデルの選択、接続テストと本文の送信許可',
    credentials: 'AI・メールサービスの認証情報',
    integrations: 'アプリからのメール送信と外部イベント',
    catalog: '利用できるモデルと参考料金',
    usage: '月別・使用ユーザー別の実利用原価',
  };
</script>

<section class="library-page settings-index" aria-label="設定一覧">
  <LibraryHeader
    title="設定"
    eyebrow="PREFERENCES"
    description="変更したい項目を選んでください。"
  />
  {#each settingsNavigationGroups(session.user?.role ?? 'user') as group}
    <section class="settings-group" aria-labelledby={'settings-' + group.id}>
      <h2 id={'settings-' + group.id}>{group.title}</h2>
      <LibraryPanel
        ><div class="settings-list">
          {#each group.items as item}<a href={'/settings/' + item.id}
              ><span
                ><strong>{item.title}</strong><small
                  >{descriptions[item.id]}</small
                ></span
              ><span aria-hidden="true">›</span></a
            >{/each}
        </div></LibraryPanel
      >
    </section>
  {/each}
</section>

<style>
  .settings-group {
    margin: 2rem 0;
  }
  .settings-group h2 {
    font-size: 0.8rem;
    font-weight: 600;
    color: var(--color-muted);
    margin: 0 0 0.75rem;
    padding: 0 0.25rem;
  }
  .settings-list {
    padding: 0;
    overflow: hidden;
  }
  .settings-list a {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 1.5rem;
    padding: 1.2rem 1.5rem;
    text-decoration: none;
    color: var(--color-text);
    min-height: 80px;
  }
  .settings-list a + a {
    border-top: 1px solid var(--color-border);
  }
  .settings-list a:hover {
    background: var(--color-surface-soft);
  }
  .settings-list a > span:first-child {
    min-width: 0;
  }
  .settings-list strong {
    font-weight: 600;
    font-size: 0.95rem;
  }
  .settings-list small {
    display: block;
    font-size: 0.8rem;
    color: var(--color-muted);
    margin-top: 0.25rem;
  }
  .settings-list a > span:last-child {
    font-size: 1.4rem;
    color: var(--color-muted);
  }
  @media (max-width: 550px) {
    .settings-list a {
      padding: 1rem;
    }
    .settings-group {
      margin: 1.5rem 0;
    }
  }
</style>
