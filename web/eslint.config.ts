// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

import prettier from '@vue/eslint-config-prettier'
import { defineConfigWithVueTs, vueTsConfigs } from '@vue/eslint-config-typescript'
import pluginVue from 'eslint-plugin-vue'

// The annotation is what keeps this file inside the TypeScript project in
// tsconfig.node.json: without one its inferred type could only be named through
// a transitive dependency, and the type aware rules would then have no project
// for the file itself.
const config: ReturnType<typeof defineConfigWithVueTs> = defineConfigWithVueTs(
  {
    name: 'web-kit/files',
    files: ['**/*.ts', '**/*.vue'],
  },
  {
    name: 'web-kit/ignores',
    ignores: ['dist/**', 'node_modules/**'],
  },
  pluginVue.configs['flat/recommended'],
  vueTsConfigs.recommendedTypeChecked,
  prettier,
  {
    name: 'web-kit/rules',
    rules: {
      // The component files carry the prefix, the tags do not: Wk is the
      // namespace of this library.
      'vue/multi-word-component-names': 'off',
      '@typescript-eslint/no-explicit-any': 'error',
      '@typescript-eslint/no-non-null-assertion': 'error',
      '@typescript-eslint/consistent-type-imports': 'error',
    },
  },
  {
    name: 'web-kit/tests',
    files: ['**/*.test.ts'],
    rules: {
      // Testing Library's render returns an object of closures, so taking one
      // out of it is safe. The rule is about methods that read `this`.
      '@typescript-eslint/unbound-method': 'off',
      // A test composes wrapper components to render a component in context.
      'vue/one-component-per-file': 'off',
    },
  },
)

export default config
