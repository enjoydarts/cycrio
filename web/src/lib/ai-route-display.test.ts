import assert from 'node:assert/strict';
import test from 'node:test';
import { routeDisplayName, routeQuantization } from './ai-route-display.ts';

test('Google routes show their actual endpoint region instead of unknown precision', () => {
  for (const [region, name] of [
    ['europe', '欧州'],
    ['global', 'グローバル'],
    ['us', '米国'],
  ]) {
    assert.equal(
      routeDisplayName({
        provider_name: 'Google',
        tag: `google-vertex/${region}`,
        quantization: 'unknown',
      }),
      `Google Vertex AI · ${name}`,
    );
  }
});
test('other endpoint variants and known precision remain distinguishable', () => {
  assert.equal(
    routeDisplayName({
      provider_name: 'Amazon Bedrock',
      tag: 'amazon-bedrock/eu-west-1',
      quantization: 'unknown',
    }),
    'Amazon Bedrock · eu-west-1',
  );
  assert.equal(
    routeDisplayName({
      provider_name: 'DeepInfra',
      tag: 'deepinfra/fp4',
      quantization: 'fp4',
    }),
    'DeepInfra · fp4',
  );
  assert.equal(
    routeDisplayName({
      provider_name: 'Example',
      tag: 'example',
      quantization: 'FP8',
    }),
    'Example · FP8',
  );
  for (const quantization of ['', 'unknown', 'Unknown', ' UNKNOWN ']) {
    assert.equal(routeQuantization({ quantization }), null);
    assert.equal(
      routeDisplayName({
        provider_name: 'Example',
        tag: 'example',
        quantization,
      }),
      'Example',
    );
  }
});
