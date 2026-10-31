// Native Node tests compile nested Svelte components through the same server compiler.
import { registerHooks } from 'node:module';
import { readFileSync } from 'node:fs';
import { compile } from 'svelte/compiler';

registerHooks({
  load(url, context, nextLoad) {
    if (url.endsWith('.css'))
      return { format: 'module', source: '', shortCircuit: true };
    if (url.endsWith('.svelte')) {
      const { js, warnings } = compile(readFileSync(new URL(url), 'utf8'), {
        generate: 'server',
        filename: new URL(url).pathname,
      });
      if (warnings.length)
        throw new Error(warnings.map((warning) => warning.message).join('\n'));
      return { format: 'module', source: js.code, shortCircuit: true };
    }
    return nextLoad(url, context);
  },
});
