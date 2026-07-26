// Flat config (ESLint 9 requirement — .eslintrc.cjs is no longer read by
// eslint@9+). Mirrors the old .eslintrc.cjs ruleset (eslint:recommended +
// react + react-hooks) plus TS-aware parsing via typescript-eslint, which
// the old config never had (espree can't parse .ts/.tsx syntax at all, so
// `npm run lint` would fail on every file without it).
//
// Requires the `typescript-eslint` devDependency — run `npm install` once
// before `npm run lint` / CI's lint step will work.
import js from '@eslint/js'
import tseslint from 'typescript-eslint'
import react from 'eslint-plugin-react'
import reactHooks from 'eslint-plugin-react-hooks'
import globals from 'globals'

export default tseslint.config(
  { ignores: ['dist'] },
  {
    files: ['**/*.{ts,tsx}'],
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    languageOptions: {
      ecmaVersion: 2022,
      globals: { ...globals.browser, ...globals.node },
    },
    plugins: {
      react,
      'react-hooks': reactHooks,
    },
    settings: {
      react: { version: 'detect' },
    },
    rules: {
      ...react.configs.recommended.rules,
      ...reactHooks.configs.recommended.rules,
      'react/react-in-jsx-scope': 'off',
      // A leading underscore is the project's convention for an intentionally
      // unused binding (e.g. a mock callback param kept only so a later
      // assertion can index into the mock's recorded call args) — recognize it
      // instead of flagging it as dead code.
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_', varsIgnorePattern: '^_' }],
    },
  },
)
