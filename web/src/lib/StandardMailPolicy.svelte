<script lang="ts">
  import { onDestroy } from 'svelte';
  import type {
    PolicyState,
    PolicyInput,
    StandardPolicyInput,
  } from './mail-processing-api.ts';
  import { validRetention } from './mail-processing-api.ts';
  import { datetime } from './model-catalog.ts';
  import {
    createStandardReview,
    reconcileStandardReview,
    type StandardReview,
    type StandardConsent,
  } from './standard-policy-draft.ts';
  let {
    state: policyState,
    busy,
    reloadRequired = false,
    save,
    saveCustom,
    onDirty = () => {},
  }: {
    state: PolicyState | null;
    busy: boolean;
    reloadRequired?: boolean;
    save: (input: StandardPolicyInput) => Promise<boolean>;
    saveCustom: (input: PolicyInput) => Promise<boolean>;
    onDirty?: (dirty: boolean) => void;
  } = $props();
  let review = $state<StandardReview | null>(null),
    saved = $state(false),
    legacyCustom = $state(false),
    reviewing = $state(false);
  let disposed = false;
  onDestroy(() => {
    disposed = true;
    review = null;
  });
  const form = $derived(review?.draft ?? null),
    conflict = $derived(review?.conflict ?? false);
  const terms = $derived(policyState?.standard_terms),
    route = $derived(terms?.secondary_route);
  const customSecondary = $derived(
    legacyCustom ||
      policyState?.profile?.secondary.provider !== 'openrouter' ||
      terms?.blocked_reason === 'custom_secondary_required',
  );
  const proofCurrent = $derived(
    policyState?.policy?.standard_proof?.primary_mode !== 'standard' ||
      (policyState?.policy?.standard_proof?.primary_terms?.id ===
        terms?.primary.id &&
        policyState?.policy?.standard_proof?.primary_terms?.version ===
          terms?.primary.version),
  );
  const alreadyPermitted = $derived(
    !!policyState?.policy?.enabled && proofCurrent,
  );
  function adopt() {
    const current = policyState;
    if (disposed || !current) return;
    review = createStandardReview(current);
    saved = false;
    legacyCustom = false;
    onDirty(false);
  }
  $effect(() => {
    if (disposed || !policyState) return;
    const next = reconcileStandardReview(review, policyState);
    if (next !== review) review = next;
  });
  function edit(patch: Partial<StandardConsent>) {
    if (!form || !review || busy) return;
    review = {
      ...review,
      draft: { ...form, dirty: true, values: { ...form.values, ...patch } },
    };
    saved = false;
    onDirty(true);
  }
  const valid = $derived(
    !!policyState?.profile &&
      !!form &&
      !reloadRequired &&
      !conflict &&
      form.values.primary_confirmed &&
      form.values.secondary_confirmed &&
      (customSecondary
        ? validRetention(form.values.primary_retention) &&
          validRetention(form.values.secondary_retention)
        : !!terms?.ready &&
          !!route &&
          (form.values.primary_mode === 'standard' ||
            validRetention(form.values.primary_retention))),
  );
  async function submit() {
    if (
      !valid ||
      !form ||
      !policyState?.profile ||
      busy ||
      reloadRequired ||
      disposed
    )
      return;
    const value = form.values;
    const ok = customSecondary
      ? await saveCustom({
          expected_version: form.baseline_version,
          profile_id: policyState.profile.id,
          enabled: true,
          primary_retention: value.primary_retention,
          secondary_retention: value.secondary_retention,
          primary_confirmed: value.primary_confirmed,
          secondary_confirmed: value.secondary_confirmed,
        })
      : await save({
          expected_version: form.baseline_version,
          runtime_version: policyState.runtime_version,
          profile_id: policyState.profile.id,
          enabled: true,
          primary_mode: value.primary_mode,
          primary_terms_id:
            value.primary_mode === 'standard' ? terms!.primary.id : '',
          primary_retention:
            value.primary_mode === 'standard' ? '' : value.primary_retention,
          primary_confirmed: value.primary_confirmed,
          secondary_route_id: route!.id,
          secondary_confirmed: value.secondary_confirmed,
        });
    if (ok && !disposed && policyState) {
      adopt();
      saved = true;
    }
  }
  async function stop() {
    const policy = policyState?.policy;
    if (!policy || busy || disposed) return;
    const ok = await saveCustom({
      expected_version: policy.version,
      profile_id: policy.profile_id,
      enabled: false,
      primary_retention: policy.primary_retention,
      secondary_retention: policy.secondary_retention,
      primary_confirmed: policy.primary_confirmed,
      secondary_confirmed: policy.secondary_confirmed,
    });
    if (ok && !disposed && policyState) adopt();
  }
</script>

{#if policyState}<section aria-labelledby="standard-permission-heading">
    <h2 id="standard-permission-heading">メール本文の送信許可</h2>
    <p>一次判定と二次処理それぞれの保持条件を確認して、送信を許可します。</p>
    <div class="setup-complete">
      <strong
        >現在の許可：{policyState.policy?.enabled
          ? '許可済み'
          : '停止中・未許可'}</strong
      >{#if policyState.policy?.enabled}<button disabled={busy} onclick={stop}
          >送信許可を停止</button
        >{/if}
    </div>
    {#if saved}<p role="status">
        確認した条件で送信許可を保存しました。解析画面で条件を再確認してください。
      </p>{/if}
    {#if !policyState.profile}<p class="setup-feedback">
        先に AI の接続テストと有効化を行ってください。
      </p>{:else if reloadRequired || (!terms && policyState.profile.secondary.provider === 'openrouter')}<p
        class="setup-feedback"
        role="status"
      >
        新しく許可する前に、最新の送信条件を読み直してください。
      </p>{:else if alreadyPermitted && !reviewing}<p>
        現在の有効版について、確認した条件で本文送信を許可しています。解析画面で送信条件を確認して開始できます。
      </p>
      <button
        disabled={busy}
        onclick={() => {
          reviewing = true;
          adopt();
        }}>送信条件を見直す</button
      >{:else if form}
      {#if conflict}<div class="setup-feedback" role="status">
          <p>
            モデル・規約・保存状態が変わりました。入力と編集開始時の版を保持しています。最新条件を確認し直してください。
          </p>
          <button disabled={busy} onclick={adopt}
            >最新の条件で確認し直す（入力を破棄）</button
          >
        </div>{/if}
      <form
        onsubmit={(event) => {
          event.preventDefault();
          void submit();
        }}
      >
        <div class="setup-grid">
          <article class="setup-panel">
            <h3>一次判定 · Liquid AI D1</h3>
            {#if !customSecondary && terms}<label
                >保持条件<select
                  disabled={busy || conflict}
                  value={form.values.primary_mode}
                  onchange={(e) =>
                    edit({
                      primary_mode: e.currentTarget.value as
                        'standard' | 'custom',
                      primary_confirmed: false,
                    })}
                  ><option value="standard">Liquid AI の標準規約を使う</option
                  ><option value="custom">個別契約の条件を使う</option></select
                ></label
              >
              {#if form.values.primary_mode === 'standard'}<p>
                  {terms.primary.summary}
                </p>
                <p class="muted">標準規約：{terms.primary.version}</p>
                <a
                  href={terms.primary.source_url}
                  target="_blank"
                  rel="noopener noreferrer">Liquid AI の標準規約を確認</a
                >{:else}<label
                  >個別契約の保持・学習利用条件<textarea
                    disabled={busy || conflict}
                    maxlength="500"
                    value={form.values.primary_retention}
                    oninput={(e) =>
                      edit({
                        primary_retention: e.currentTarget.value,
                        primary_confirmed: false,
                      })}></textarea></label
                >{/if}
            {:else}<p class="muted">
                個別に確認した保持・学習利用条件を記録します。アカウント固有の契約や
                ZDR を自動で保証するものではありません。
              </p>
              <label
                >一次判定の個別条件<textarea
                  disabled={busy || conflict}
                  maxlength="500"
                  value={form.values.primary_retention}
                  oninput={(e) =>
                    edit({
                      primary_retention: e.currentTarget.value,
                      primary_confirmed: false,
                    })}></textarea></label
              >{/if}
            <label class="check"
              ><input
                type="checkbox"
                checked={form.values.primary_confirmed}
                disabled={busy || conflict}
                onchange={(e) =>
                  edit({ primary_confirmed: e.currentTarget.checked })}
              />一次判定の保持・学習利用条件を確認しました</label
            >
          </article>
          <article class="setup-panel">
            <h3>二次処理 · {policyState.profile.secondary.display_name}</h3>
            {#if customSecondary}<p>
                現在の接続先について、個別に確認した条件を入力してください。
              </p>
              <label
                >二次処理の保持・学習利用条件<textarea
                  disabled={busy || conflict}
                  maxlength="500"
                  value={form.values.secondary_retention}
                  oninput={(e) =>
                    edit({
                      secondary_retention: e.currentTarget.value,
                      secondary_confirmed: false,
                    })}></textarea></label
              >
            {:else if route}<p>
                <strong>{route.provider_name}</strong> · {terms?.ready
                  ? '現在の有効版と照合した ZDR 配信先'
                  : '最新の適合状態の確認が必要です'}
              </p>
              <p>
                本文を保持しない経路を必須にし、データ収集を拒否して別配信先への切り替えを禁止します。
              </p>
              <p class="muted">
                確認日時（日本時間）：{datetime(route.captured_at)}
              </p>
              <a
                href={route.source_url}
                target="_blank"
                rel="noopener noreferrer">OpenRouter の配信先情報を確認</a
              >
            {:else}<p>
                現在の有効版と一致する配信先を確認できません。無料の配信先情報を更新してください。
              </p>{/if}
            <label class="check"
              ><input
                type="checkbox"
                checked={form.values.secondary_confirmed}
                disabled={busy ||
                  conflict ||
                  (!customSecondary && !terms?.ready)}
                onchange={(e) =>
                  edit({ secondary_confirmed: e.currentTarget.checked })}
              />二次処理の配信先と保持条件を確認しました</label
            >
          </article>
        </div>
        {#if !customSecondary && !terms?.ready}<p class="setup-feedback">
            新しい許可を保存するために、現在の有効版に対応する最新の配信先情報が必要です。すでに保存した許可が配信先情報の取得時刻だけで停止することはありません。
          </p>{/if}
        <p class="muted">
          同意はこの操作で新しく確認します。過去のチェック状態は引き継ぎません。料金が不明でも、条件を確認して許可できます。
        </p>
        <div class="setup-actions">
          <button type="submit" class="primary" disabled={busy || !valid}
            >確認した条件で本文送信を許可</button
          ><button type="button" disabled={busy} onclick={adopt}
            >確認をやり直す</button
          >
        </div>
      </form>
      {#if policyState.policy && !policyState.policy.standard_proof && !customSecondary}<details
          class="setup-details"
        >
          <summary>既存の個別条件を編集</summary><button
            disabled={busy}
            onclick={() => {
              legacyCustom = true;
              edit({
                primary_mode: 'custom',
                primary_retention: policyState.policy?.primary_retention ?? '',
                secondary_retention:
                  policyState.policy?.secondary_retention ?? '',
                primary_confirmed: false,
                secondary_confirmed: false,
              });
            }}>個別条件の入力へ切り替える</button
          >
        </details>{/if}
    {/if}
  </section>{/if}
