<script lang="ts">
  import type { MailAccountCost } from './airuntime-api';
  import { stageNames } from './ai-runtime-controller';
  import ActionGroup from './ActionGroup.svelte';
  import BrandIcon from './BrandIcon.svelte';
  import Button from './Button.svelte';
  import DataTable from './DataTable.svelte';
  import UsageAmounts from './UsageAmounts.svelte';

  let {
    accounts,
    busy,
    selectedAccount,
    onSelect,
  }: {
    accounts: MailAccountCost[];
    busy: boolean;
    selectedAccount?: string;
    onSelect: (account: MailAccountCost) => void;
  } = $props();
</script>

<DataTable responsive="cards">
  <caption class="sr-only">メール接続別の月次原価</caption>
  <thead>
    <tr>
      <th scope="col">メール接続</th>
      <th scope="col">集計済みコスト / 処理段階</th>
      <th scope="col">呼出し件数</th>
      <th scope="col">操作</th>
    </tr>
  </thead>
  <tbody>
    {#each accounts as account}
      <tr
        class:active-row={selectedAccount ===
          (account.account_id ?? 'unattributed')}
      >
        <td data-primary data-label="メール接続">
          <div class="account-name">
            <BrandIcon provider={account.provider} size="small" />
            <div>
              <strong
                >{account.account_id === null
                  ? '接続先不明'
                  : account.label || '名称未設定のメール接続'}</strong
              >
              {#if account.provider}<small>{account.provider}</small>{/if}
            </div>
          </div>
        </td>
        <td data-label="集計済みコスト / 処理段階">
          <UsageAmounts totals={account.totals} counts={false} />
          <details>
            <summary>一次判定・二次処理の内訳</summary>
            {#each account.stages as stage}
              <div class="stage-cost">
                <strong
                  >{stage.stage === 'classification'
                    ? '一次判定'
                    : `二次処理 · ${stageNames[stage.stage]}`}</strong
                >
                <UsageAmounts totals={stage.totals} />
              </div>
            {:else}<p class="muted">処理段階の内訳はありません。</p>{/each}
          </details>
        </td>
        <td data-label="呼出し件数">
          <div>{account.calls} 件</div>
          <small>原価不明 {account.unknown_calls} 件</small>
          <small>失敗・結果不明 {account.failures} 件</small>
        </td>
        <td data-actions data-label="操作">
          <ActionGroup>
            <Button
              type="button"
              wrap
              disabled={busy}
              onclick={() => onSelect(account)}>このメールの呼出し履歴</Button
            >
          </ActionGroup>
        </td>
      </tr>
    {:else}
      <tr><td colspan="4">対象のメール接続の呼出しはありません。</td></tr>
    {/each}
  </tbody>
</DataTable>

<style>
  .account-name {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    min-width: 0;
  }
  .account-name > div {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  small {
    display: block;
    color: var(--color-muted);
  }
  .stage-cost {
    margin-top: 1rem;
  }
</style>
