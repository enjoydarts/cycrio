import { defineEnvVars } from '@sveltejs/kit/env';

export const variables = defineEnvVars({
  PUBLIC_API_BASE_URL: {
    public: true,
    static: true,
    schema: (value: string | undefined) => value ?? 'http://localhost:8090',
  },
});
