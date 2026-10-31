<script lang="ts">
  import Button from './Button.svelte';
  import type { HistoryEditorView } from './schedule-history-controller.ts';
  import type {
    HistoryFilter,
    HistoryCounts,
    HistoryEntry,
  } from './schedule-history-api.ts';
  import type { ScheduleDraft } from './schedule-controller.ts';
  import { cycle } from './daily-text.ts';
  import { processingErrors } from './mail-processing-text.ts';
  let {
    view,
    values,
    disabled,
    conflict,
    onToggle,
    onEdit,
    onFetch,
    onAdopt,
  }: {
    view: HistoryEditorView;
    values: ScheduleDraft;
    disabled: boolean;
    conflict: boolean;
    onToggle: (value: boolean) => void;
    onEdit: (patch: Partial<HistoryFilter>) => void;
    onFetch: () => void;
    onAdopt: (index: number) => void;
  } = $props();
  const countLabels: Record<keyof HistoryCounts, string> = {
    total: '対象明細',
    eligible: '有効な支払',
    sampled: '比較に使用',
    older_eligible: '比較対象より古い有効な支払',
    voided: '除外済み明細',
    invoices: '請求情報',
    refunds: '返金の明細',
    other_currency: '別通貨の支払',
    refunded: '返金がある支払',
    referenced_refunds: '対象期間外の参照返金',
  };
  const entryLabels: Record<HistoryEntry['reason'], string> = {
    eligible: '有効な支払',
    voided: '除外済み',
    invoice: '請求情報',
    refund: '返金',
    other_currency: '別通貨',
    refunded: '返金がある支払',
  };
  const statusLabels = {
    ready: '周期候補を確認してください',
    ambiguous: '複数候補・要確認',
    insufficient: '履歴が不足しています',
    duplicate_dates: '同じ日付の支払が複数あります',
    no_match: '完全一致する周期が見つかりません',
    too_many: '対象明細が上限を超えています',
  };
  const validFilter = $derived(
    /^\d{4}-\d{2}-\d{2}$/.test(view.filter.start_date) &&
      /^[A-Z]{3}$/.test(view.filter.currency),
  );
  const hasExistingValues = $derived(
    !!(
      values.interval_unit ||
      values.interval_count ||
      values.anchor_date ||
      values.month_end
    ),
  );
</script>

<section class="schedule-history" aria-label="支払履歴から周期候補を確認">
  <Button
    wrap
    type="button"
    class={[
      'schedule-history-toggle',
      'part-ScheduleHistory-schedule-history-toggle',
    ]}
    aria-expanded={view.expanded}
    {disabled}
    onclick={() => onToggle(!view.expanded)}>支払履歴から候補を確認</Button
  >
  {#if view.expanded}
    <p class="muted">
      プランや契約条件を変更した場合は、変更後の対象開始日を選んでください。変更の境界は自動判定しません。通貨を一つ指定し、日本時間の本日までの記録を確認します。
    </p>
    <p class="muted">
      直近3〜5件の日付が完全一致する周期だけを比較します。日付のずれを許容せず、未記録の支払がないことも保証しません。確認済みの0円は含み、金額の大小では判断しません。請求情報・返金・除外済み・未来の明細と、部分返金を含む返金がある支払は比較から除きます。
    </p>
    <fieldset {disabled}>
      <legend class="schedule-legend">履歴の対象条件</legend>
      <div class="form-grid">
        <label
          >対象開始日<input
            type="date"
            required
            min="0001-01-01"
            max="9999-12-31"
            value={view.filter.start_date}
            oninput={(event) =>
              onEdit({ start_date: event.currentTarget.value })}
          /></label
        >
        <label
          >対象通貨<input
            type="text"
            required
            maxlength="3"
            pattern={'[A-Z]{3}'}
            placeholder="例: JPY"
            value={view.filter.currency}
            oninput={(event) =>
              onEdit({ currency: event.currentTarget.value.toUpperCase() })}
          /><small class="muted"
            >大文字の通貨コード3文字。別の通貨と混ぜません。</small
          ></label
        >
      </div>
      <Button
        wrap
        type="button"
        disabled={!validFilter || view.loading}
        onclick={onFetch}
        >{view.loading ? '候補を確認しています…' : '候補を確認する'}</Button
      >
    </fieldset>
    {#if view.loading}<p role="status">支払履歴を確認しています…</p>{/if}
    {#if view.error}<p class="notice error" role="alert">
        {processingErrors[view.error]}
      </p>{/if}
    {#if view.result}{@const result = view.result}
      <div aria-live="polite">
        <h3>{statusLabels[result.status]}</h3>
        <p class="muted">
          対象: {result.filter.start_date}〜{result.as_of}（日本時間）· {result
            .filter.currency}
        </p>
        {#if result.status === 'ambiguous'}<p>
            記録上は区別できない候補です。自動では選びません。契約条件を確認して選んでください。
          </p>
        {:else if result.status === 'insufficient'}<p>
            異なる日付の有効な支払が最低3件必要です。
          </p>
        {:else if result.status === 'duplicate_dates'}<p>
            対象期間内に同日複数の有効な支払があります。直近5件より古い支払も含めて確認してください。一つの周期には確定しません。
          </p>
        {:else if result.status === 'no_match'}<p>
            使用したすべての日付を説明できません。支払の抜けやプラン変更を推測して補いません。
          </p>
        {:else if result.status === 'too_many'}<p>
            対象期間の明細と参照返金は合計1000件までです。対象開始日を絞って、再度確認してください。
          </p>{/if}
        {#if result.reason === 'fixed_term'}<p class="schedule-warning">
            期間限定契約には候補を入力できません。
          </p>
        {:else if result.reason === 'ended'}<p class="schedule-warning">
            契約は終了済みです。履歴は確認できますが、候補は入力できません。
          </p>{/if}
        {#if result.counts}<p>
            対象 {result.counts.total}件 · 有効な支払 {result.counts.eligible}件
            · 比較に使用 {result.counts.sampled}件
          </p>
          {#if result.counts.older_eligible > 0}<p class="muted">
              比較に使用した支払より古い有効な支払が {result.counts
                .older_eligible}件あります。
            </p>{/if}
        {/if}
        <h4>比較に使用した支払</h4>
        {#if result.samples.length}<ul class="schedule-history-samples">
            {#each result.samples as sample (sample.id)}<li>
                {sample.occurred_on} · {sample.amount}
                {sample.currency}
              </li>{/each}
          </ul>{:else}<p class="muted">
            比較に使用できる支払はありません。
          </p>{/if}
        <div class="schedule-history-candidates">
          {#each result.candidates as candidate, index}
            <article class="schedule-history-candidate">
              <h4>
                候補 {index + 1}: {cycle(
                  candidate.rule.interval_unit,
                  candidate.rule.interval_count,
                )}
              </h4>
              <p>
                基準日 {candidate.rule.anchor_date} · {candidate.rule.month_end
                  ? '月末固定'
                  : '日付指定'}
              </p>
              <p>
                次回候補: {candidate.candidate_date ??
                  (candidate.reason === 'fixed_term'
                    ? '期間限定契約'
                    : candidate.reason === 'ended'
                      ? '終了済み'
                      : '扱える日付の範囲を超えています')}
              </p>
              {#if hasExistingValues}<p class="notice">
                  現在の入力（{values.interval_unit
                    ? cycle(values.interval_unit, Number(values.interval_count))
                    : '周期未指定'} · 基準日 {values.anchor_date || '未指定'} · {values.month_end
                    ? '月末固定'
                    : '日付指定'}）を、この候補の周期・基準日・日付指定方法で置き換えます。
                </p>{/if}
              <Button
                wrap
                type="button"
                disabled={disabled ||
                  conflict ||
                  !!result.reason ||
                  !candidate.candidate_date ||
                  !!candidate.reason}
                onclick={() => onAdopt(index)}>この候補を入力する</Button
              >
            </article>
          {/each}
        </div>
        {#if result.counts}<details>
            <summary>対象明細と除外理由を確認</summary>
            <dl class="schedule-history-counts">
              {#each Object.entries(result.counts) as [key, count]}<div>
                  <dt>{countLabels[key as keyof HistoryCounts]}</dt>
                  <dd>{count}件</dd>
                </div>{/each}
            </dl>
            <p class="muted">
              有効な支払のうち直近最大5件を比較しています。対象期間外の参照返金は件数へ別記し、ここには期間内の明細を示します。
            </p>
            {#if result.entries.length}<ul class="schedule-history-samples">
                {#each result.entries as entry (entry.id)}<li>
                    {entry.occurred_on} · {entry.amount}
                    {entry.currency} · {entryLabels[entry.reason]}
                  </li>{/each}
              </ul>{:else}<p class="muted">
                対象期間内の明細はありません。
              </p>{/if}
          </details>{/if}
        <p class="muted">
          候補の入力だけでは保存されません。「計算する」で次回日を確認し、確認チェック後に保存してください。
        </p>
      </div>
    {/if}
  {/if}
</section>
