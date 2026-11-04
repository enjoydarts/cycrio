<script lang="ts">
  import {
    notificationKinds,
    type NotificationPreferences,
    type NotificationSettings,
  } from './notifications-api.ts';
  import {
    notificationKindNames,
    deliveryReason,
  } from './notification-text.ts';
  import LibraryPanel from './LibraryPanel.svelte';
  import DataTable from './DataTable.svelte';
  let {
    values,
    email,
    disabled = false,
    onchange,
  }: {
    values: NotificationPreferences;
    email: NotificationSettings['email'];
    disabled?: boolean;
    onchange: (patch: Partial<NotificationPreferences>) => void;
  } = $props();
</script>

<LibraryPanel
  ><DataTable responsive="compact"
    ><thead><tr><th>通知の種類</th><th>アプリ内</th><th>メール</th></tr></thead
    ><tbody>
      {#each notificationKinds as kind}<tr
          ><td data-primary>{notificationKindNames[kind]}</td><td
            data-label="アプリ内"
            ><input
              aria-label={`${notificationKindNames[kind]}をアプリ内で通知`}
              type="checkbox"
              checked={values.kinds[kind].in_app}
              {disabled}
              onchange={(event) =>
                onchange({
                  kinds: {
                    ...values.kinds,
                    [kind]: {
                      ...values.kinds[kind],
                      in_app: event.currentTarget.checked,
                    },
                  },
                })}
            /></td
          ><td data-label="メール"
            ><input
              aria-label={`${notificationKindNames[kind]}をメールで通知`}
              type="checkbox"
              checked={values.kinds[kind].email}
              disabled={disabled ||
                (!email.available && !values.kinds[kind].email)}
              onchange={(event) =>
                onchange({
                  kinds: {
                    ...values.kinds,
                    [kind]: {
                      ...values.kinds[kind],
                      email: event.currentTarget.checked,
                    },
                  },
                })}
            /></td
          ></tr
        >{/each}
    </tbody></DataTable
  ></LibraryPanel
>
<div class="form-grid timing">
  <label
    >何日前から通知するか<input
      type="number"
      min="0"
      max="30"
      required
      value={values.lead_days}
      {disabled}
      oninput={(event) =>
        onchange({
          lead_days:
            event.currentTarget.value === ''
              ? NaN
              : Number(event.currentTarget.value),
        })}
    /><small class="muted"
      >更新・年払い・無料期間終了の通知に使います（0〜30日前）。</small
    ></label
  ><label
    >通知する時刻<select
      value={values.send_hour}
      {disabled}
      onchange={(event) =>
        onchange({ send_hour: Number(event.currentTarget.value) })}
      >{#each Array.from({ length: 24 }, (_, hour) => hour) as hour}<option
          value={hour}>{hour}:00</option
        >{/each}</select
    ><small class="muted"
      >すべての種類に適用します。日本時間（Asia/Tokyo）です。</small
    ></label
  >
</div>
<p class="muted">メールの送信前にも最新の設定と元情報を確認します。</p>
<p class="muted">
  アプリ内のチェックを外すと、新しい通知を停止します。これまでの履歴は「すべて」「既読」から確認できます。
</p>
<label class="choice"
  ><input
    type="checkbox"
    aria-label="本人の検証済みメールへ通知を送る"
    checked={values.email_opt_in}
    disabled={disabled || (!email.available && !values.email_opt_in)}
    onchange={(event) =>
      onchange({ email_opt_in: event.currentTarget.checked })}
  /><span>本人の検証済みメールへ通知を送る</span></label
>
{#if !email.available}<p class="notice">
    {deliveryReason(email.reason)} アプリ内通知の設定とメール通知の停止は可能です。
  </p>{/if}

<style>
  .timing {
    margin-top: 16px;
  }
  .choice {
    display: flex;
    flex-direction: row;
    gap: 10px;
    align-items: flex-start;
    margin-block: 16px;
  }
  input[type='checkbox'] {
    width: 20px;
    height: 20px;
    min-height: 20px;
    flex: 0 0 20px;
    margin: 2px 0 0;
    padding: 0;
  }
  td {
    overflow-wrap: anywhere;
  }
</style>
