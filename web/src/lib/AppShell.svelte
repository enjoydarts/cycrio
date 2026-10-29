<script lang="ts">
  import { onMount, tick, type Snippet } from 'svelte';
  import type { CurrentUser, TokenProvider } from './api.ts';
  import type { MailAPI } from './mail-api.ts';
  import type { createOverviewAPI } from './overview-api.ts';
  import type { ThemePreference } from './theme.ts';
  import { provideWorkspace } from './workspace-context.ts';
  import {
    createWorkspaceController,
    emptyWorkspace,
  } from './workspace-controller.ts';
  let {
    user,
    pathname,
    search = '',
    signOut,
    children,
    token,
    mailAPI,
    overviewAPI,
    onDenied = () => {},
    theme = 'system',
    setTheme = () => {},
  }: {
    user: CurrentUser;
    pathname: string;
    search?: string;
    signOut: () => void;
    children: Snippet;
    token?: TokenProvider;
    mailAPI?: Pick<MailAPI, 'list'>;
    overviewAPI?: Pick<ReturnType<typeof createOverviewAPI>, 'read'>;
    onDenied?: () => void;
    theme?: ThemePreference;
    setTheme?: (value: ThemePreference) => void;
  } = $props();
  let workspaceView = $state(emptyWorkspace());
  let controller: ReturnType<typeof createWorkspaceController> | undefined;
  provideWorkspace({
    get view() {
      return workspaceView;
    },
    async refresh() {
      await controller?.refresh();
    },
  });
  let mobile = $state(false),
    menuOpen = $state(false);
  let sidebar: HTMLElement, menuButton: HTMLButtonElement;
  let lastNavigation: string | undefined;
  let lastRefreshKey: string | undefined;
  const navigation = [
    {
      key: 'subscriptions',
      href: '/subscriptions',
      title: 'サブスク',
      icon: 'M4 5h16v14H4z M4 10h16 M9 10v9',
    },
    {
      key: 'attention',
      href: '/analysis?tab=attention',
      title: '確認が必要',
      icon: 'M12 3 2 21h20L12 3z M12 9v5 M12 17v1',
    },
    {
      key: 'billing',
      href: '/subscriptions/summary',
      title: '支払・返金',
      icon: 'M5 3h14v18l-3-2-4 2-4-2-3 2V3z M8 8h8 M8 12h5',
    },
    {
      key: 'history',
      href: '/analysis?tab=history',
      title: '取り込み履歴',
      icon: 'M4 11a8 8 0 1 1 2 7 M4 4v7h7 M12 7v5l3 2',
    },
  ];
  const selected = $derived(
    pathname.startsWith('/settings') ||
      pathname === '/admin' ||
      pathname === '/mail'
      ? 'settings'
      : pathname === '/subscriptions/summary'
        ? 'billing'
        : pathname.startsWith('/analysis')
          ? new URLSearchParams(search).get('tab') === 'attention'
            ? 'attention'
            : 'history'
          : pathname.startsWith('/subscriptions')
            ? 'subscriptions'
            : '',
  );
  const current = $derived(
    selected === 'settings'
      ? '設定'
      : pathname === '/import'
        ? 'メールから取り込む'
        : (navigation.find((item) => item.key === selected)?.title ?? 'ホーム'),
  );
  const accountID = $derived(new URLSearchParams(search).get('account_id'));
  const overview = $derived(
    workspaceView.overview.status === 'ready'
      ? workspaceView.overview.value
      : null,
  );
  const providers = {
    gmail: 'Gmail',
    outlook: 'Outlook',
    fastmail: 'Fastmail',
    imap: 'IMAP',
  };
  async function openMenu() {
    menuOpen = true;
    await tick();
    sidebar?.querySelector<HTMLElement>('a,button')?.focus();
  }
  async function closeMenu(restore = true) {
    menuOpen = false;
    if (restore) {
      await tick();
      menuButton?.focus();
    }
  }
  function keyboard(event: KeyboardEvent) {
    if (!mobile || !menuOpen) return;
    if (event.key === 'Escape') {
      event.preventDefault();
      closeMenu();
    }
    if (event.key !== 'Tab') return;
    const elements = [
      ...sidebar.querySelectorAll<HTMLElement>('a[href],button:not(:disabled)'),
    ];
    const first = elements[0],
      last = elements.at(-1);
    if (
      event.shiftKey &&
      (document.activeElement === first ||
        !sidebar.contains(document.activeElement))
    ) {
      event.preventDefault();
      last?.focus();
    } else if (!event.shiftKey && document.activeElement === last) {
      event.preventDefault();
      first?.focus();
    }
  }
  function toggleTheme() {
    const effective =
      theme === 'system' ? document.documentElement.dataset.theme : theme;
    setTheme(effective === 'dark' ? 'light' : 'dark');
  }
  $effect(() => {
    const location = `${pathname}${search}`;
    // List search, pagination and drawer state do not invalidate global counts.
    const next = pathname.startsWith('/analysis')
      ? `${pathname}?tab=${new URLSearchParams(search).get('tab') ?? 'history'}`
      : pathname;
    if (lastNavigation !== undefined && location !== lastNavigation) {
      closeMenu(false);
      if (next !== lastRefreshKey) void controller?.refresh();
    }
    lastNavigation = location;
    lastRefreshKey = next;
  });
  onMount(() => {
    const media = window.matchMedia('(max-width: 700px)');
    const resize = () => {
      mobile = media.matches;
      if (!mobile) closeMenu(false);
    };
    resize();
    media.addEventListener('change', resize);
    if (token && mailAPI && overviewAPI) {
      controller = createWorkspaceController(
        mailAPI,
        overviewAPI,
        token,
        (value) => {
          workspaceView = value;
        },
        onDenied,
      );
      void controller.refresh();
    }
    return () => {
      media.removeEventListener('change', resize);
      controller?.dispose();
    };
  });
  $effect(() => {
    if (!mobile || !menuOpen) return;
    const previous = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
    return () => {
      document.body.style.overflow = previous;
    };
  });
</script>

<svelte:window onkeydown={keyboard} />
<a class="skip-link" href="#app-main">本文に移動</a>
<div class="application">
  {#if mobile && menuOpen}<button
      class="sidebar-shade"
      aria-label="メニューを閉じる"
      onclick={() => closeMenu()}
    ></button>{/if}
  <aside
    id="workspace-sidebar"
    class="sidebar"
    class:open={menuOpen}
    bind:this={sidebar}
    inert={mobile && !menuOpen}
    role={mobile && menuOpen ? 'dialog' : undefined}
    aria-modal={mobile && menuOpen ? true : undefined}
    aria-label="ナビゲーション"
  >
    <a
      class="brand"
      href="/"
      aria-label="Cycrio ホーム"
      onclick={() => closeMenu(false)}
      ><img
        class="brand-logo"
        src="/brand/cycrio-mark-128.png"
        width="36"
        height="36"
        alt=""
      />Cycrio</a
    >
    <div class="sidebar-navigation">
      <p class="rail-label">ライブラリ</p>
      <nav aria-label="メイン">
        {#each navigation as item}
          <a
            href={item.href}
            onclick={() => closeMenu(false)}
            class:active={selected === item.key}
            aria-current={selected === item.key ? 'page' : undefined}
            aria-label={item.key === 'attention'
              ? '確認が必要なメール'
              : undefined}
          >
            <svg
              width="19"
              height="19"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.6"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"><path d={item.icon} /></svg
            ><span>{item.title}</span>
            {#if overview && item.key === 'subscriptions'}<span
                class="nav-count"
                aria-label={`${overview.subscriptions.total}件のサブスク`}
                >{overview.subscriptions.total}</span
              >{/if}
            {#if overview && item.key === 'attention'}<span
                class="nav-count attention"
                aria-label={`${overview.attention_messages}通のメール`}
                >{overview.attention_messages}</span
              >{/if}
          </a>
        {/each}
      </nav>
      <div class="sources">
        <p class="rail-label">接続したメール</p>
        {#if workspaceView.accounts.status === 'ready' && workspaceView.accounts.value}
          {#each workspaceView.accounts.value as account (account.id)}
            <a
              class="source-link"
              onclick={() => closeMenu(false)}
              class:active={selected === 'subscriptions' &&
                accountID === account.id}
              href={`/subscriptions?account_id=${account.id}`}
              aria-current={selected === 'subscriptions' &&
              accountID === account.id
                ? 'location'
                : undefined}
            >
              <span
                class="mail-logo"
                class:fastmail={account.provider === 'fastmail'}
                aria-hidden="true"
                >{providers[account.provider].slice(0, 1)}</span
              >
              <span class="source-meta"
                ><strong>{providers[account.provider]}</strong><small
                  >{account.label}</small
                ></span
              >
              <span
                class="source-state"
                role="img"
                class:ready={account.state === 'ready'}
                aria-label={account.state === 'ready'
                  ? '接続済み'
                  : account.state === 'needs_reauth'
                    ? '再認証が必要'
                    : account.state === 'disconnected'
                      ? '切断済み'
                      : '接続未確認'}
              ></span>
            </a>
          {:else}<p class="rail-note">メールはまだ接続されていません</p>{/each}
        {:else if workspaceView.accounts.status === 'error'}
          <p class="rail-note" role="status">接続を取得できませんでした</p>
          <button class="rail-retry" onclick={() => controller?.refresh()}
            >再試行</button
          >
        {:else if workspaceView.accounts.status === 'loading'}<p
            class="rail-note"
            role="status"
          >
            接続を読み込み中…
          </p>{/if}
        <a
          class="add-source"
          href="/import?connect=true"
          onclick={() => closeMenu(false)}
          ><span aria-hidden="true">＋</span>メールを追加</a
        >
      </div>
    </div>
    <div class="rail-bottom">
      <a
        class="settings-link"
        class:active={selected === 'settings'}
        href="/settings"
        onclick={() => closeMenu(false)}
        aria-current={selected === 'settings' ? 'page' : undefined}
        ><span aria-hidden="true">⚙</span>設定</a
      >
      <div class="account">
        <span class="avatar" aria-hidden="true">個</span>
        <div>
          個人アカウント<small
            >{user.role === 'platform_admin' ? '管理者' : '一般ユーザー'}</small
          >
        </div>
        <button aria-label="ログアウト" onclick={signOut}>↪</button>
      </div>
    </div>
  </aside>
  <div class="application-content" inert={mobile && menuOpen}>
    <header class="topbar">
      <div class="breadcrumb">
        <button
          class="mobile-menu"
          bind:this={menuButton}
          aria-label="メニューを開く"
          aria-controls="workspace-sidebar"
          aria-expanded={menuOpen}
          onclick={openMenu}>☰</button
        ><span class="desktop-crumb"
          >ライブラリ<span aria-hidden="true"> / </span></span
        ><strong>{current}</strong>
      </div>
      <button
        class="theme-button"
        aria-label="表示テーマを切り替える"
        title="表示テーマを切り替える"
        onclick={toggleTheme}
        ><svg
          width="19"
          height="19"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.6"
          aria-hidden="true"
          ><path d="M21 13A9 9 0 0 1 11 3a9 9 0 1 0 10 10Z" /></svg
        ></button
      >
    </header>
    <main id="app-main" tabindex="-1">{@render children()}</main>
  </div>
</div>

<style>
  .application {
    display: grid;
    grid-template-columns: 230px minmax(0, 1fr);
    min-height: 100svh;
  }
  .sidebar {
    position: sticky;
    top: 0;
    height: 100svh;
    padding: 30px 17px 19px;
    background: var(--color-rail);
    color: var(--color-rail-text);
    display: flex;
    flex-direction: column;
    gap: 28px;
  }
  .brand {
    display: flex;
    align-items: center;
    gap: 11px;
    min-height: 44px;
    padding: 0 14px;
    color: #fff;
    text-decoration: none;
    font-size: 25px;
    font-weight: 750;
    letter-spacing: -1px;
  }
  .brand-logo {
    display: block;
    width: 36px;
    height: 36px;
    flex: none;
    object-fit: contain;
  }
  .sidebar-navigation {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
  }
  .rail-label {
    padding: 0 14px;
    margin: 0 0 10px;
    color: var(--color-rail-muted);
    font-size: 10px;
    letter-spacing: 1px;
    font-weight: 650;
  }
  nav {
    display: grid;
    gap: 5px;
  }
  nav a,
  .settings-link,
  .source-link {
    display: flex;
    align-items: center;
    gap: 11px;
    padding: 11px 13px;
    min-height: 44px;
    border-radius: 9px;
    color: var(--color-rail-text);
    text-decoration: none;
    font-size: 13px;
  }
  nav a:hover,
  .settings-link:hover,
  .source-link:hover {
    background: #ffffff0d;
    color: #fff;
  }
  nav a.active,
  .settings-link.active {
    background: #7463e929;
    color: #e4dfff;
    font-weight: 650;
  }
  nav svg {
    flex-shrink: 0;
  }
  .nav-count {
    margin-left: auto;
    background: #ffffff12;
    border-radius: 5px;
    padding: 0 7px;
    font-size: 11px;
    font-variant-numeric: tabular-nums;
  }
  .nav-count.attention {
    color: #efc17c;
    background: #f3ba611c;
  }
  .sources {
    margin-top: 30px;
  }
  .source-link.active {
    background: #ffffff0d;
    color: #fff;
  }
  .source-meta {
    display: grid;
    min-width: 0;
    font-size: 12px;
  }
  .source-meta strong {
    font-weight: 500;
  }
  .source-meta small {
    color: var(--color-rail-muted);
    font-size: 10px;
    overflow-wrap: anywhere;
  }
  .mail-logo {
    display: grid;
    place-items: center;
    background: #fff;
    color: #4269ba;
    width: 29px;
    height: 29px;
    border-radius: 8px;
    flex-shrink: 0;
    font-weight: 800;
  }
  .mail-logo.fastmail {
    color: white;
    background: #6657df;
  }
  .source-state {
    width: 6px;
    height: 6px;
    flex-shrink: 0;
    border-radius: 50%;
    background: #efc17c;
    margin-left: auto;
  }
  .source-state.ready {
    background: #6cd1a4;
  }
  .add-source {
    margin-top: 7px;
    color: #c5baff;
    border: 1px dashed #786aa1;
    padding: 9px 12px;
    border-radius: 9px;
    font-size: 12px;
    display: flex;
    align-items: center;
    gap: 8px;
    min-height: 44px;
    text-decoration: none;
  }
  .rail-note {
    color: var(--color-rail-muted);
    font-size: 11px;
    padding-inline: 14px;
  }
  .rail-retry {
    color: #c5baff;
    padding-inline: 14px;
  }
  .rail-bottom {
    margin-top: auto;
    display: grid;
    gap: 12px;
  }
  .account {
    border-top: 1px solid #ffffff14;
    padding: 17px 11px 0;
    display: flex;
    gap: 9px;
    align-items: center;
    font-size: 12px;
  }
  .account small {
    display: block;
    color: var(--color-rail-muted);
    font-size: 10px;
  }
  .avatar {
    width: 30px;
    height: 30px;
    background: #47405e;
    border-radius: 50%;
    display: grid;
    place-items: center;
    color: #e7dcff;
    font-size: 11px;
  }
  .account button {
    margin-left: auto;
    color: var(--color-rail-text);
    font-size: 22px;
    min-width: 36px;
  }
  button {
    border: 0;
    background: transparent;
    color: inherit;
  }
  .application-content {
    min-width: 0;
  }
  .topbar {
    height: 74px;
    border-bottom: 1px solid var(--color-border);
    background: var(--color-surface);
    display: flex;
    justify-content: space-between;
    align-items: center;
    padding: 0 40px;
    gap: 16px;
  }
  .breadcrumb {
    display: flex;
    align-items: center;
    gap: 12px;
    color: var(--color-muted);
    font-size: 12px;
  }
  .desktop-crumb span {
    margin-left: 12px;
  }
  .breadcrumb strong {
    color: var(--color-text);
    font-weight: 500;
  }
  .theme-button {
    display: grid;
    place-items: center;
    min-width: 44px;
    color: var(--color-muted);
    border-radius: 7px;
  }
  .theme-button:hover {
    background: var(--brand-background);
  }
  .mobile-menu {
    display: none;
  }
  main {
    padding: 35px 40px 28px;
    max-width: 1550px;
    margin: 0 auto;
    min-width: 0;
  }
  .skip-link {
    position: fixed;
    top: 8px;
    left: 8px;
    padding: 12px;
    z-index: 100;
    background: var(--color-surface);
    transform: translateY(-150%);
  }
  .skip-link:focus {
    transform: none;
  }
  .sidebar-shade {
    position: fixed;
    inset: 0;
    z-index: 20;
    background: #15182c88;
  }
  @media (max-width: 1150px) {
    .application {
      grid-template-columns: 205px minmax(0, 1fr);
    }
    .sidebar {
      padding-inline: 13px;
    }
    main {
      padding: 28px 25px;
    }
    .topbar {
      padding-inline: 25px;
    }
  }
  @media (max-width: 850px) {
    .application {
      grid-template-columns: 185px minmax(0, 1fr);
    }
    .sidebar {
      padding-inline: 10px;
    }
    .brand {
      font-size: 23px;
      padding-left: 9px;
    }
    main {
      padding: 24px 19px;
    }
    .topbar {
      padding-inline: 19px;
    }
  }
  @media (max-width: 700px) {
    .application {
      display: block;
    }
    .sidebar {
      position: fixed;
      z-index: 21;
      inset: 0 auto 0 0;
      width: min(280px, calc(100vw - 36px));
      transform: translateX(-100%);
      transition: transform 0.2s;
      padding: 27px 18px;
    }
    .sidebar.open {
      transform: translateX(0);
    }
    .mobile-menu {
      display: grid;
      place-items: center;
      min-width: 44px;
      font-size: 22px;
    }
    .topbar {
      height: 59px;
      padding: 0 12px;
      gap: 8px;
    }
    .breadcrumb {
      gap: 7px;
      font-size: 11px;
    }
    .desktop-crumb {
      display: none;
    }
    main {
      padding: 24px 16px;
    }
  }
</style>
