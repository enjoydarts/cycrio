<script lang="ts">
  import type { Notification } from './notifications-api.ts';
  import {
    notificationKindNames,
    notificationAction,
  } from './notification-text.ts';
  import { processingDate } from './mail-processing-text.ts';
  import { money } from './daily-text.ts';
  import LibraryPanel from './LibraryPanel.svelte';
  import DataTable from './DataTable.svelte';
  import Button from './Button.svelte';
  import ActionLink from './ActionLink.svelte';
  import ActionGroup from './ActionGroup.svelte';
  let {
    notifications,
    busy = false,
    onRead,
    onDelivery,
    onNavigate = () => {},
  }: {
    notifications: Notification[];
    busy?: boolean;
    onRead: (id: string) => void;
    onDelivery: (id: string) => void;
    onNavigate?: () => void;
  } = $props();
</script>

<LibraryPanel
  ><DataTable responsive="cards"
    ><thead><tr><th>通知</th><th>日付・内容</th><th>操作</th></tr></thead><tbody
    >
      {#each notifications as notification}{@const f = notification.facts}<tr>
          <td data-primary
            ><strong>{notificationKindNames[f.kind]}</strong>
            <p>
              {f.service ||
                (f.kind === 'pending_review'
                  ? 'メール解析の確認待ち'
                  : 'メールの取り込み')}
            </p>
            <small>{processingDate(notification.created_at)}（日本時間）</small
            >{#if notification.superseded_at}<p class="muted">
                現在は対象外の通知です。
              </p>{/if}</td
          >
          <td data-primary class="notification-body"
            >{#if f.due_date}<p>
                {f.kind === 'trial_end'
                  ? '無料期間終了日'
                  : '次回請求日'}：{f.due_date}{f.date_source ===
                'receipt_estimate'
                  ? '（メール受信日を起点にした推定）'
                  : ''}
              </p>{/if}
            {#if f.kind === 'price_increase'}<p>
                通知作成時の登録料金：{money(
                  f.old_amount ?? null,
                  f.currency ?? null,
                )} → 候補：{money(f.new_amount ?? null, f.currency ?? null)}
              </p>
              <p class="muted">
                契約の一致は未確定です。元の抽出情報を確認してください。承認まで契約内容は変更されません。
              </p>
            {:else if f.kind === 'pending_review'}<p>
                通知作成時点で {f.review_count} 件の新しい確認待ちがありました。現在の内容を確認してください。
              </p>
            {:else if f.kind === 'import_error'}<p>
                接続設定と取り込み状況を確認してください。
              </p>
            {:else if f.kind === 'trial_end'}<p class="muted">
                終了後の料金・請求日は自動で補完していません。
              </p>
            {:else}<p>{money(f.amount ?? null, f.currency ?? null)}</p>{/if}
          </td>
          <td data-actions
            ><ActionGroup
              ><ActionLink
                wrap
                href={notificationAction(notification)}
                aria-disabled={busy}
                onclick={(event) => {
                  if (busy) event.preventDefault();
                  else onNavigate();
                }}
                >{f.kind === 'price_increase'
                  ? '変更候補を確認'
                  : f.kind === 'import_error'
                    ? '取り込みを確認'
                    : '契約を確認'}</ActionLink
              >{#if notification.read_at}<span>既読</span>{:else}<Button
                  type="button"
                  wrap
                  disabled={busy}
                  onclick={() => onRead(notification.id)}>既読にする</Button
                >{/if}<Button
                type="button"
                wrap
                disabled={busy}
                onclick={() => onDelivery(notification.id)}
                >通知状態を確認</Button
              ></ActionGroup
            ></td
          >
        </tr>{:else}<tr><td colspan="3">通知はありません。</td></tr>{/each}
    </tbody></DataTable
  ></LibraryPanel
>

<style>
  p {
    margin: 8px 0;
  }
  td,
  p,
  small {
    overflow-wrap: anywhere;
  }
  small {
    color: var(--color-muted);
  }
</style>
