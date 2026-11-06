const redacted = '[redacted]';
const emailPattern =
  /[\p{L}\p{N}.!#$%&'*+/=?^_`{|}~-]+@(?:[\p{L}\p{N}-]+\.)+[\p{L}][\p{L}\p{N}-]*/giu;
const secretPattern =
  /\b(?:sk[-_][A-Za-z0-9_-]{8,}|ph[cxs]_[A-Za-z0-9_-]{8,}|gh[pousr]_[A-Za-z0-9_]{8,}|github_pat_[A-Za-z0-9_]{8,}|xox[baprs]-[A-Za-z0-9-]{8,}|(?:AKIA|ASIA)[A-Z0-9]{16}|AIza[A-Za-z0-9_-]{20,}|re_[A-Za-z0-9_-]{16,}|whsec_[A-Za-z0-9_-]{8,}|ya29\.[A-Za-z0-9._-]{8,}|GOCSPX-[A-Za-z0-9_-]{8,}|user_[A-Za-z0-9_-]{12,})\b/g;
const jwtPattern = /\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b/g;
const sensitiveField =
  /\b(?:authorization|proxy-authorization|cookie|set-cookie|password|secret|token|api[-_ ]?key|x-api-key|access[-_ ]?token|refresh[-_ ]?token|id[-_ ]?token|client[-_ ]?secret|app[-_ ]?password|login|username|user[-_ ]?name|user|email|body|request[-_]body|response[-_]body|subject|from|to|cc|bcc|sender|recipient|recipients|payload|prompt|content|input|headers|analysis[-_]result|parsed[-_]result)["']?\s*[:=]\s*/gi;

function scalarSecrets(text: string): string {
  return text
    .replace(emailPattern, redacted)
    .replace(secretPattern, redacted)
    .replace(jwtPattern, redacted)
    .replace(/\bBearer\s+[^\s,;"'<>]+/gi, 'Bearer ' + redacted);
}

function quotedEnd(text: string, start: number): number {
  const quote = text[start];
  for (let i = start + 1; i < text.length; i++) {
    if (text[i] === '\\') {
      i++;
      continue;
    }
    if (text[i] === quote) return i + 1;
  }
  return text.length;
}
function valueEnd(text: string, start: number): number {
  const first = text[start];
  if (first === '"' || first === "'" || first === '`')
    return quotedEnd(text, start);
  if (first === '{' || first === '[') {
    const closing: string[] = [];
    for (let i = start; i < text.length; i++) {
      const char = text[i];
      if (char === '"' || char === "'" || char === '`') {
        i = quotedEnd(text, i) - 1;
        continue;
      }
      if (char === '{' || char === '[') closing.push(char === '{' ? '}' : ']');
      else if (char === closing.at(-1)) {
        closing.pop();
        if (!closing.length) return i + 1;
      }
    }
    return text.length;
  }
  const rest = text.slice(start);
  const end = rest.search(/[\n,;]|\s+(?=[A-Za-z_]+\s*[:=])/);
  return end < 0 ? text.length : start + end;
}

function redactFields(text: string): string {
  let result = '',
    cursor = 0;
  sensitiveField.lastIndex = 0;
  for (
    let match = sensitiveField.exec(text);
    match;
    match = sensitiveField.exec(text)
  ) {
    const start = match.index + match[0].length;
    // Cookie headers contain multiple unquoted key/value pairs on the same line.
    const cookie = /^(?:set-)?cookie/i.test(match[0]);
    const end = cookie
      ? (text.indexOf('\n', start) + 1 || text.length + 1) - 1
      : valueEnd(text, start);
    result += text.slice(cursor, start) + redacted;
    cursor = Math.max(start, end);
    sensitiveField.lastIndex = cursor;
  }
  return result + text.slice(cursor);
}

function redactEmbeddedPayloads(text: string): string {
  // Quoted keys distinguish JSON data from ordinary technical interface{} text.
  const pattern = /\{\s*["'][^"'\n]+["']\s*:|\[\s*(?:\{\s*["']|["'])/g;
  let result = '',
    cursor = 0;
  for (let match = pattern.exec(text); match; match = pattern.exec(text)) {
    result += text.slice(cursor, match.index) + redacted;
    cursor = valueEnd(text, match.index);
    pattern.lastIndex = cursor;
  }
  return result + text.slice(cursor);
}
function redactInvalidInput(text: string): string {
  const pattern =
    /\b(?:invalid|unexpected)\s+(?:input|value|syntax|json)\b[^\n"'`]{0,160}(?=["'`])/gi;
  let result = '',
    cursor = 0;
  for (let match = pattern.exec(text); match; match = pattern.exec(text)) {
    const start = match.index + match[0].length;
    result += text.slice(cursor, start) + redacted;
    cursor = quotedEnd(text, start);
    pattern.lastIndex = cursor;
  }
  return result + text.slice(cursor);
}

function redactMailBlock(text: string): string {
  const headers =
    '(?:Subject|From|To|Cc|Bcc|Date|Message-ID|MIME-Version|Content-Type|Reply-To)';
  const block = new RegExp(
    '^' +
      headers +
      ':[^\\r\\n]*(?:\\r?\\n(?:' +
      headers +
      ':[^\\r\\n]*|[ \\t]+[^\\r\\n]*))*\\r?\\n\\r?\\n',
    'gim',
  );
  for (let match = block.exec(text); match; match = block.exec(text)) {
    const count =
      match[0].match(new RegExp('^' + headers + ':', 'gim'))?.length ?? 0;
    const hasMailAnchor =
      /^(?:Subject|MIME-Version|Message-ID|Content-Type):|^From:.*@/im.test(
        match[0],
      );
    if (
      hasMailAnchor &&
      (count >= 2 || /^(?:MIME-Version|Content-Type):/im.test(match[0]))
    )
      return text.slice(0, match.index) + redacted;
  }
  return text;
}

function scrubURL(value: string): string {
  const suffix = value.match(/[:,;.)\]}]+$/)?.[0] ?? '';
  const source = suffix ? value.slice(0, -suffix.length) : value;
  try {
    const url = new URL(source);
    if (!url.hostname || /[;=\\]/.test(url.hostname)) return redacted + suffix;
    if (url.hostname === 'hc-ping.com')
      return `${url.protocol}//${url.host}/${redacted}${suffix}`;
    const segments = url.pathname.split('/').map((segment, index, all) => {
      let decoded = segment;
      try {
        decoded = decodeURIComponent(segment);
      } catch {
        /* Keep an opaque malformed path. */
      }
      if (
        index > 0 &&
        /^(?:users?|accounts?|profiles?|people|recipients?|senders?|emails?)$/i.test(
          all[index - 1],
        ) &&
        segment &&
        segment !== 'me'
      )
        return redacted;
      return scalarSecrets(decoded) !== decoded ? redacted : segment;
    });
    return `${url.protocol}//${url.host}${segments.join('/')}${suffix}`;
  } catch {
    return redacted + suffix;
  }
}

// Preserve technical text and quoted identifiers. Only values with a sensitive
// field label, credential pattern, or personal URL/email component are removed.
export function scrubText(value: string): string {
  const bounded = redactMailBlock(value.slice(0, 32_768));
  const urls = bounded.replace(
    /(?:https?|postgres(?:ql)?|imaps?|smtps?|mysql|rediss?|mongodb(?:\+srv)?|sqlserver):\/\/[^\s<>"']+/gi,
    scrubURL,
  );
  const fields = redactInvalidInput(redactEmbeddedPayloads(redactFields(urls)));
  return scalarSecrets(fields)
    .replace(
      /\b(Unexpected token[^\n]*?,\s*)(["'])(.*?)\2(?=\s+is not valid JSON)/gi,
      '$1' + redacted,
    )
    .replace(/(?:\/Users\/|\/home\/)[^\s"']+/g, redacted)
    .replace(/[A-Z]:\\Users\\[^\s"']+/gi, redacted)
    .slice(0, 2048);
}
