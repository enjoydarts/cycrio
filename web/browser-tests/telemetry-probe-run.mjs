import {
  runTelemetryProbe,
  runPageTelemetryProbe,
} from './telemetry-probe.mjs';
import { readFile } from 'node:fs/promises';

const configPath = process.argv.slice(2).find((value) => value !== '--pages');
const config = configPath
  ? JSON.parse(await readFile(configPath, 'utf8'))
  : null;
const options = config
  ? { projectToken: config.project_token, host: config.host, live: true }
  : {};

async function api(method, path, body) {
  const response = await fetch('http://browser:4444' + path, {
    method,
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(40_000),
  });
  const { value } = await response.json();
  if (!response.ok || value?.error) throw Error(JSON.stringify(value));
  return value;
}
let session;
try {
  session =
    '/session/' +
    (
      await api('POST', '/session', {
        capabilities: {
          alwaysMatch: {
            browserName: 'chrome',
            'goog:chromeOptions': {
              args: [
                '--headless=new',
                '--no-sandbox',
                '--disable-dev-shm-usage',
                '--unsafely-treat-insecure-origin-as-secure=http://fixture:5199',
              ],
            },
          },
        },
      })
    ).sessionId;
  await api('POST', session + '/url', {
    url: 'http://fixture:5199/browser-tests/index.html?mode=healthchecks',
  });
  for (let i = 0; i < 150; i++) {
    if (
      await api('POST', session + '/execute/sync', {
        script: 'return window.browserTest!==undefined',
        args: [],
      })
    )
      break;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  const probe = process.argv.includes('--pages')
    ? runPageTelemetryProbe
    : runTelemetryProbe;
  console.log(JSON.stringify(await probe(api, session, options)));
} finally {
  if (session) await api('DELETE', session);
}
