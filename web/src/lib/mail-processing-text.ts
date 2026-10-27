import type { ErrorCode } from './api.ts';
import type {
  ProcessingPreview,
  ProcessingResult,
} from './mail-processing-api.ts';
export const processingErrors: Record<ErrorCode, string> = {
  authentication_required: 'ログインし直してください。',
  access_denied: 'この操作を利用する権限がありません。',
  dependency_unavailable:
    '受付状況を確認できませんでした。最新の状態を確認してください。',
  invalid_request: '入力条件と確認項目を確認してください。',
  conflict:
    '設定または処理状態が変わりました。入力と元の基準は保持しています。最新条件を確認してください。',
  not_found: '対象が見つかりません。最新の状態を確認してください。',
};
export const processingBlocked: Record<
  ProcessingPreview['blocked_reason'],
  string
> = {
  '': '',
  account_not_ready: '接続確認済みのアカウントが必要です。',
  credentials_required: '接続情報を更新して接続確認を行ってください。',
  folders_required: '対象フォルダーを保存してください。',
  runtime_inactive: '管理者が先に AI 設定を有効化する必要があります。',
  policy_disabled: '管理者によるメール解析の送信許可が必要です。',
  full_email_not_allowed: '管理者による本文全体の送信許可が必要です。',
  policy_reconfirmation_required:
    '送信条件が変わりました。管理者が最新の条件を確認して、送信許可を保存してください。',
  processing_active: 'このアカウントの解析が進行中です。',
  configuration_unavailable:
    '現在の AI 設定を利用できません。管理者に確認してください。',
};
export const processingReasons: Record<ProcessingResult['error_code'], string> =
  {
    '': '',
    authentication_failed: 'メール接続の認証に失敗しました。',
    tls_failed: '安全なメール接続を確立できませんでした。',
    connection_failed: 'メールサーバーに接続できませんでした。',
    timeout: '応答が時間切れになりました。',
    invalid_response: '応答の形式を確認できませんでした。',
    cancelled: '処理を取り消しました。',
    credentials_unavailable: '保存済みの接続情報を利用できません。',
    source_missing: '元のメールを取得できませんでした。',
    input_too_large: 'メールのサイズが処理可能な範囲を超えました。',
    unsupported_content: 'このメール形式は解析できません。',
    configuration_unavailable: '現在の AI 設定を利用できません。',
    execution_panic: '処理を継続できませんでした。',
    recording_failed: '結果を記録できませんでした。',
    lease_expired: '処理が中断され、結果を確認できません。',
    invalid_request: '解析要求の条件を確認できませんでした。',
    transport_error: 'AI への接続結果を確認できませんでした。',
    request_cancelled: 'AI への要求が中断されました。',
    response_read: 'AI の応答を読み取れませんでした。',
    response_too_large: '応答が取得上限を超えました。',
    invalid_json: 'AI の応答形式が正しくありません。',
    provider_http: 'AI 配信先が要求を受け付けませんでした。',
    provider_error: 'AI 配信先で処理できませんでした。',
    model_mismatch: '指定モデルと応答モデルが一致しませんでした。',
    invalid_output: '抽出結果の形式を確認できませんでした。',
    refusal: 'AI が解析結果を返しませんでした。',
    incomplete: 'AI の応答が完了しませんでした。',
    generation_mismatch: '応答の識別情報が一致しませんでした。',
    provider_unavailable: 'AI 配信先を利用できませんでした。',
  };
export const processingDate = (value: string) =>
  new Intl.DateTimeFormat('ja-JP', {
    dateStyle: 'medium',
    timeStyle: 'short',
    timeZone: 'Asia/Tokyo',
  }).format(new Date(value));
