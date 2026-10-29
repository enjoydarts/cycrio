<script lang="ts">
  import { onMount } from 'svelte';
  import { page } from '$app/state';
  import { beforeNavigate, goto } from '$app/navigation';
  import { getAppSession } from './app-context.ts';
  import { getWorkspace } from './workspace-context.ts';
  import {
    createIntakeController,
    type IntakeView,
  } from './intake-controller.ts';
  import {
    emptyMailView,
    mailFolderLabel,
    probePending,
  } from './mail-controller.ts';
  import MailAccounts from './MailAccounts.svelte';
  import MailAutomation from './MailAutomation.svelte';
  import LibraryHeader from './LibraryHeader.svelte';
  import LibraryPanel from './LibraryPanel.svelte';
  import './daily.css';
  const session = getAppSession(),
    workspace = getWorkspace();
  let view = $state<IntakeView>({
    step: 1,
    mail: emptyMailView(),
    issue: null,
    folderUncertain: false,
    frozen: false,
    accepted: null,
  });
  let controller: ReturnType<typeof createIntakeController> | undefined;
  let connecting = $state(false),
    connectionFrozen = $state(false),
    automationVisited = $state(false),
    query = $state(''),
    pendingChoice = $state(''),
    leaveTo = $state(''),
    allowLeave = false;
  const account = $derived(
    view.mail.accounts.find((a) => a.id === view.mail.selected) ?? null,
  );
  const locked = $derived(
    view.frozen ||
      view.folderUncertain ||
      view.mail.uncertain ||
      connectionFrozen ||
      view.mail.status === 'saving' ||
      view.mail.status === 'loading',
  );
  const folders = $derived(
    account?.folders.filter((f) =>
      mailFolderLabel(f)
        .toLocaleLowerCase()
        .includes(query.toLocaleLowerCase()),
    ) ?? [],
  );
  const issues = {
    choose_account: 'メールを選んでください。',
    connect_first: '接続確認が完了してから進んでください。',
    select_folder: '取り込むフォルダを1つ以上選んでください。',
    confirm_switch:
      '別のメールを選ぶ前に、編集中のフォルダ選択を破棄してください。',
  };
  onMount(() => {
    controller = createIntakeController(
      session.mailAPI,
      session.token,
      (next) => {
        view = next;
        if (next.step === 3) automationVisited = true;
      },
      session.onDenied,
      page.url.searchParams.get('account_id') ??
        page.url.searchParams.get('setup_account') ??
        undefined,
    );
    void controller.refresh();
    connecting = page.url.searchParams.get('connect') === 'true';
    const timer = setInterval(() => {
      if (document.visibilityState !== 'hidden') void controller?.poll();
    }, 2500);
    return () => {
      clearInterval(timer);
      controller?.dispose();
    };
  });
  beforeNavigate(({ cancel, to, type }) => {
    if (allowLeave || (!controller?.hasUnsaved() && !connectionFrozen)) return;
    cancel();
    if (!locked && type !== 'leave' && to?.url.origin === page.url.origin)
      leaveTo = to.url.pathname + to.url.search;
  });
  async function connected(id: string) {
    connectionFrozen = false;
    connecting = false;
    await controller?.refresh();
    pendingChoice = id;
    if (controller?.choose(id)) await controller.next();
    void workspace?.refresh();
  }
</script>

<section class="daily intake library-page">
  <LibraryHeader
    title="メールを取り込む"
    eyebrow="MY LIBRARY"
    description="メールを選んで、サブスクを見つけましょう。"
  >
    {#snippet action()}
      <a class="button" href="/subscriptions">ライブラリに戻る</a>
    {/snippet}
  </LibraryHeader>
  <LibraryPanel>
    <div class="library-content">
      <ol class="steps" aria-label="取り込みの手順">
        {#each ['メールを選ぶ', 'フォルダを選ぶ', '取り込みを始める'] as label, i}<li
            aria-current={view.step === i + 1 ? 'step' : undefined}
          >
            <span>{i + 1}</span>{label}
          </li>{/each}
      </ol>
      {#if view.mail.error}<p class="notice" role="alert">
          メールの状態を確認できませんでした。入力は保持しています。<button
            disabled={locked}
            onclick={() => controller?.refresh()}>再読み込み</button
          >
        </p>{/if}
      {#if view.issue}<div class="notice" role="status">
          <p>{issues[view.issue]}</p>
          {#if view.issue === 'confirm_switch'}<button
              onclick={() => {
                if (controller?.choose(pendingChoice, true)) pendingChoice = '';
              }}>選択を破棄して切り替える</button
            >{/if}
        </div>{/if}
      {#if view.folderUncertain}<div class="notice" role="alert">
          <p>
            フォルダ選択の保存結果を確認できません。保存済みの状態を読み直してから進んでください。
          </p>
          <button onclick={() => controller?.adoptStoredFolders()}
            >保存済みの選択を確認して採用</button
          >
        </div>{/if}
      {#if view.mail.uncertain}<p class="notice" role="status">
          接続確認の受付が不明です。同じ要求で再確認してください。
        </p>{/if}
      {#if leaveTo}<div class="notice" role="alert">
          <p>編集中のフォルダ選択・取り込み条件を破棄して移動しますか？</p>
          <button
            disabled={locked}
            onclick={() => {
              if (locked) return;
              allowLeave = true;
              void goto(leaveTo);
            }}>破棄して移動</button
          ><button onclick={() => (leaveTo = '')}>編集を続ける</button>
        </div>{/if}
      {#if view.step === 1}
        <section class="stage">
          <h2>どのメールから取り込みますか？</h2>
          <p class="muted">ログイン用の Google アカウントとは別に選べます。</p>
          {#if view.mail.status === 'loading'}<p role="status">
              メールを読み込んでいます…
            </p>{/if}
          <div class="accounts">
            {#each view.mail.accounts as a}<button
                class:selected={view.mail.selected === a.id}
                disabled={locked}
                aria-pressed={view.mail.selected === a.id}
                onclick={() => {
                  pendingChoice = a.id;
                  controller?.choose(a.id);
                }}
                ><span class="source-icon" aria-hidden="true">@</span><span
                  ><strong>{a.label}</strong><small
                    >{a.provider === 'gmail'
                      ? 'Gmail / Google Workspace'
                      : a.provider === 'outlook'
                        ? 'Outlook.com / Hotmail'
                        : a.provider === 'fastmail'
                          ? 'Fastmail'
                          : 'IMAP'} · {a.state === 'ready'
                      ? '接続確認済み'
                      : a.state === 'disconnected'
                        ? '解除済み'
                        : '接続確認が必要'}</small
                  ></span
                ><span aria-hidden="true"
                  >{view.mail.selected === a.id ? '✓' : '→'}</span
                ></button
              >{/each}
          </div>
          <button
            class="add"
            disabled={locked || connecting}
            onclick={() => (connecting = true)}>＋ メールを追加する</button
          >
          {#if !view.mail.accounts.length && view.mail.status === 'ready'}<p>
              Gmail・Google Workspace・Outlook.com・Fastmail・IMAP
              に対応しています。
            </p>{/if}
        </section>
      {:else if view.step === 2 && account}
        <section class="stage">
          <h2>取り込むフォルダを選ぶ</h2>
          <p>{account.label}</p>
          {#if account.state !== 'ready' || probePending(account.latest_probe) || view.mail.uncertain}<div
              class="notice"
            >
              <p>
                {probePending(account.latest_probe)
                  ? '接続を確認しています…'
                  : '接続確認を行うとフォルダを取得できます。'}
              </p>
              <button
                disabled={view.mail.status === 'saving' ||
                  probePending(account.latest_probe)}
                onclick={() => controller?.probe()}
                >{view.mail.uncertain
                  ? '同じ要求で受付を確認'
                  : '接続を確認'}</button
              ><a
                href={'/settings/mail?return_to=' +
                  encodeURIComponent('/import?account_id=' + account.id)}
                >接続情報を更新</a
              >
            </div>{/if}
          <label class="search"
            >フォルダを検索<input
              type="search"
              bind:value={query}
              placeholder="フォルダ名で検索"
            /></label
          >
          <div class="folders">
            {#each folders as f}<label
                ><input
                  type="checkbox"
                  checked={view.mail.form?.folder_ids.includes(f.id)}
                  disabled={locked ||
                    !f.available ||
                    !f.selectable ||
                    account.state !== 'ready'}
                  onchange={(e) =>
                    controller?.toggleFolder(f.id, e.currentTarget.checked)}
                /><span
                  >{mailFolderLabel(f)}{#if !f.available}<small
                      >現在利用できません</small
                    >{/if}</span
                ></label
              >{/each}{#if !folders.length}<p>
                表示できるフォルダはありません。
              </p>{/if}
          </div>
          <p class="muted">
            ここではメールを送信しません。次の画面で期間と AI
            への送信条件を確認します。
          </p>
        </section>
      {/if}
      {#if automationVisited}<div hidden={view.step !== 3}>
          <section class="stage">
            <h2>取り込みを始める</h2>
            <p>
              期間・本文の送信先・保持条件を確認して、取り込みを開始してください。
            </p>
            <MailAutomation
              {account}
              active={view.step === 3}
              returnTo={'/import?account_id=' + (account?.id ?? '')}
              onFrozen={(value) => controller?.freeze(value)}
              onDirty={(value) => controller?.editConsent(value)}
              onStarted={(id) => {
                controller?.started(id);
                void workspace?.refresh();
              }}
            />
          </section>
        </div>{/if}
      {#if view.accepted}<div class="notice" role="status">
          <h2>取り込みを受け付けました</h2>
          <p>メールの探索・解析・登録は順次進みます。</p>
          <a
            class="button primary"
            href={'/analysis?tab=history&account_id=' + view.accepted}
            >取り込み履歴を見る</a
          >
        </div>{/if}
      <footer>
        <span
          >{#if view.step > 1}<button
              disabled={locked}
              onclick={() => controller?.back()}>戻る</button
            >{/if}</span
        >{#if view.step < 3}<button
            class="primary"
            disabled={locked || connecting || !account}
            onclick={() => controller?.next()}
            >{view.step === 1
              ? 'フォルダを選ぶ →'
              : '取り込み条件を確認 →'}</button
          >{/if}
      </footer>
    </div>
  </LibraryPanel>
</section>
{#if connecting}<MailAccounts
    api={session.mailAPI}
    token={session.token}
    onDenied={session.onDenied}
    connectOnly
    returnTo="/import"
    onConnected={(a) => void connected(a.id)}
    onConnectClose={() => (connecting = false)}
    onFrozen={(value) => (connectionFrozen = value)}
  />{/if}

<style>
  .steps {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    list-style: none;
    padding: 0;
    margin: 2rem 0;
    border-bottom: 1px solid var(--color-border);
  }
  .steps li {
    display: flex;
    align-items: center;
    gap: 0.65rem;
    padding: 1rem 0.5rem;
    color: var(--color-muted);
    font-size: 0.85rem;
    min-width: 0;
  }
  .steps span {
    display: grid;
    place-items: center;
    border: 1px solid var(--color-border);
    border-radius: 50%;
    width: 1.8rem;
    height: 1.8rem;
    flex: none;
  }
  .steps [aria-current] {
    color: var(--color-action);
    border-bottom: 3px solid var(--color-action);
    font-weight: 600;
  }
  .steps [aria-current] span {
    background: var(--color-action);
    color: var(--color-on-action);
  }
  .stage {
    min-width: 0;
  }
  .stage h2 {
    font-size: 1.2rem;
    margin-top: 0;
  }
  .accounts {
    display: grid;
    gap: 0.75rem;
    margin: 1.5rem 0;
  }
  .accounts button {
    display: flex;
    gap: 1rem;
    align-items: center;
    text-align: left;
    width: 100%;
    padding: 1.1rem;
    border-radius: 0.75rem;
    color: var(--color-text);
  }
  .accounts button.selected {
    border-color: var(--color-action);
    background: var(--color-surface-soft);
  }
  .accounts button > span:nth-child(2) {
    flex: 1;
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .accounts small {
    display: block;
    color: var(--color-muted);
    margin-top: 0.3rem;
  }
  .source-icon {
    background: var(--color-surface-soft);
    padding: 0.5rem;
    border-radius: 0.6rem;
    font-size: 1.4rem;
    color: var(--color-action);
  }
  .add {
    width: 100%;
    border-style: dashed;
    padding: 1rem;
  }
  .muted {
    color: var(--color-muted);
    font-size: 0.85rem;
  }
  .search {
    display: grid;
    gap: 0.5rem;
    margin: 1rem 0;
  }
  .folders {
    max-height: 24rem;
    overflow: auto;
    border: 1px solid var(--color-border);
    border-radius: 0.65rem;
  }
  .folders label {
    display: flex;
    gap: 0.8rem;
    align-items: center;
    padding: 1rem;
    border-bottom: 1px solid var(--color-border);
    overflow-wrap: anywhere;
  }
  .folders small {
    display: block;
  }
  .folders input {
    width: 1.1rem;
    height: 1.1rem;
    flex: none;
  }
  footer {
    display: flex;
    justify-content: space-between;
    gap: 1rem;
    margin-top: 1.5rem;
  }
  [hidden] {
    display: none !important;
  }
  @media (max-width: 600px) {
    .steps li {
      flex-direction: column;
      gap: 0.4rem;
      text-align: center;
      padding: 0.75rem 0.2rem;
      font-size: 0.7rem;
    }
    .steps {
      margin: 1rem 0;
    }
    .accounts button {
      padding: 0.8rem;
      gap: 0.6rem;
    }
    footer button {
      font-size: 0.8rem;
    }
  }
</style>
