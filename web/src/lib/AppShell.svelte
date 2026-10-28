<script lang="ts">
  import type { Snippet } from 'svelte';
  import type { CurrentUser } from './api.ts';
  import SettingsNavigation from './SettingsNavigation.svelte';
  let {
    user,
    pathname,
    signOut,
    children,
  }: {
    user: CurrentUser;
    pathname: string;
    signOut: () => void;
    children: Snippet;
  } = $props();
  const navigation = [
    { href: '/', title: 'ホーム', icon: 'M3 10l9-7 9 7v11H3z M9 21v-8h6v8' },
    {
      href: '/subscriptions',
      title: 'サブスク',
      icon: 'M4 5h16v14H4z M4 10h16 M9 10v9',
    },
    {
      href: '/analysis',
      title: 'メール解析',
      icon: 'M3 5h18v14H3z M3 6l9 7 9-7',
    },
    {
      href: '/settings',
      title: '設定',
      icon: 'M4 7h16 M4 17h16 M8 4v6 M16 14v6',
    },
  ];
  const current = $derived(
    pathname.startsWith('/settings') ||
      pathname === '/admin' ||
      pathname === '/mail'
      ? '設定'
      : pathname.startsWith('/analysis')
        ? 'メール解析'
        : pathname === '/'
          ? 'ホーム'
          : 'サブスク',
  );
</script>

<a class="skip-link" href="#app-main">本文に移動</a>
<div class="application">
  <aside class="sidebar">
    <a class="brand" href="/" aria-label="Cycrio ホーム"
      ><img
        src="/brand/cycrio-mark-128.png"
        width="32"
        height="32"
        alt=""
      />Cycrio</a
    >
    <div class="sidebar-navigation">
      <nav aria-label="メイン">
        {#each navigation as item}
          <a
            href={item.href}
            class:active={current === item.title}
            aria-current={current === item.title
              ? item.title === '設定'
                ? 'location'
                : 'page'
              : undefined}
          >
            <svg
              width="20"
              height="20"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              stroke-width="1.6"
              stroke-linecap="round"
              stroke-linejoin="round"
              aria-hidden="true"><path d={item.icon} /></svg
            >
            <span>{item.title}</span>
          </a>
        {/each}
      </nav>
      {#if current === '設定'}
        <SettingsNavigation
          role={user.role}
          section={pathname.startsWith('/settings/')
            ? pathname.slice('/settings/'.length).split('/')[0]
            : ''}
        />
      {/if}
    </div>
    <div class="account">
      <span class="account-icon" aria-hidden="true"
        ><svg
          width="20"
          height="20"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.6"
          ><circle cx="12" cy="8" r="3" /><path
            d="M5 21v-2a7 7 0 0 1 14 0v2"
          /></svg
        ></span
      >
      <div>
        個人アカウント<small
          >{user.role === 'platform_admin' ? '管理者' : '一般ユーザー'}</small
        >
      </div>
      <button aria-label="ログアウト" onclick={signOut}
        ><svg
          width="18"
          height="18"
          viewBox="0 0 24 24"
          fill="none"
          stroke="currentColor"
          stroke-width="1.6"
          aria-hidden="true"
          ><path d="M9 4H4v16h5 M9 12h12 M17 8l4 4-4 4" /></svg
        ></button
      >
    </div>
  </aside>
  <div class="application-content">
    <header class="topbar">
      <span class="breadcrumb">{current}</span><a
        class="mobile-brand brand"
        href="/"
        ><img
          src="/brand/cycrio-mark-128.png"
          width="26"
          height="26"
          alt=""
        />Cycrio</a
      ><button class="mobile-signout" onclick={signOut}>ログアウト</button>
    </header>
    <main id="app-main" tabindex="-1">{@render children()}</main>
  </div>
</div>

<style>
  .application {
    display: grid;
    grid-template-columns: 224px minmax(0, 1fr);
    min-height: 100svh;
  }
  .sidebar {
    position: sticky;
    top: 0;
    height: 100svh;
    background: var(--color-surface);
    border-right: 1px solid var(--color-border);
    padding: 28px 18px;
    display: flex;
    flex-direction: column;
  }
  .brand {
    display: flex;
    min-height: 44px;
    align-items: center;
    gap: 9px;
    color: var(--color-text);
    text-decoration: none;
    font-size: 25px;
    font-weight: 750;
    letter-spacing: -1px;
  }
  .sidebar > .brand {
    padding: 0 12px 36px;
  }
  .sidebar-navigation {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 0 3px 12px;
  }
  nav {
    display: grid;
    gap: 6px;
  }
  nav a {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 12px 14px;
    min-height: 46px;
    border-radius: 7px;
    color: var(--color-muted);
    text-decoration: none;
    font-weight: 600;
  }
  nav a.active {
    background: var(--color-surface-soft);
    color: var(--color-action);
  }
  nav svg {
    flex-shrink: 0;
  }
  .account {
    flex-shrink: 0;
    margin-top: auto;
    border-top: 1px solid var(--color-border);
    padding: 18px 0 0;
    display: flex;
    align-items: center;
    gap: 8px;
    font-size: 12px;
  }
  .account small {
    display: block;
    color: var(--color-muted);
    margin-top: 3px;
    font-size: 12px;
  }
  .account-icon {
    display: grid;
    place-items: center;
    color: var(--color-action);
    background: var(--color-surface-soft);
    width: 32px;
    height: 32px;
    border-radius: 50%;
    flex-shrink: 0;
  }
  .account button {
    margin-left: auto;
    min-width: 44px;
    padding: 8px;
  }
  button {
    display: grid;
    place-items: center;
    color: var(--color-muted);
    background: transparent;
    border: 0;
  }
  .application-content {
    min-width: 0;
  }
  .topbar {
    height: 72px;
    display: flex;
    align-items: center;
    padding: 0 40px;
    border-bottom: 1px solid var(--color-border);
    background: var(--color-surface);
  }
  .breadcrumb {
    color: var(--color-muted);
    font-size: 13px;
  }
  .mobile-brand,
  .mobile-signout {
    display: none;
  }
  main {
    padding: 36px 40px 48px;
    max-width: 1500px;
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
  @media (max-width: 1050px) {
    main {
      padding: 30px 26px;
    }
    .topbar {
      padding: 0 26px;
    }
  }
  @media (max-width: 700px) {
    .application {
      display: block;
      padding-bottom: calc(72px + env(safe-area-inset-bottom));
    }
    .sidebar {
      position: fixed;
      z-index: 20;
      top: auto;
      bottom: 0;
      left: 0;
      right: 0;
      height: auto;
      border-right: 0;
      border-top: 1px solid var(--color-border);
      padding: 0 0 env(safe-area-inset-bottom);
    }
    .sidebar > .brand,
    .account,
    .breadcrumb {
      display: none;
    }
    .sidebar-navigation {
      display: contents;
    }
    nav {
      grid-template-columns: repeat(4, minmax(0, 1fr));
      gap: 0;
      min-height: 65px;
    }
    nav a {
      flex-direction: column;
      align-items: center;
      justify-content: center;
      gap: 3px;
      padding: 7px 4px;
      border-radius: 0;
      font-size: 12px;
    }
    .topbar {
      height: 60px;
      padding: 0 20px;
      justify-content: space-between;
    }
    .mobile-brand {
      display: flex;
      font-size: 21px;
    }
    .mobile-signout {
      display: block;
      font-size: 12px;
    }
    main {
      padding: 26px 20px 36px;
    }
  }
  @media (max-width: 360px) {
    main {
      padding-inline: 16px;
    }
    .topbar {
      padding-inline: 16px;
    }
  }
</style>
