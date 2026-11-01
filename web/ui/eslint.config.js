import js from '@eslint/js';
import globals from 'globals';
import reactHooks from 'eslint-plugin-react-hooks';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  { ignores: ['../static/ui/**', 'node_modules/**'] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
    },
    plugins: {
      'react-hooks': reactHooks,
    },
    rules: {
      'react-hooks/rules-of-hooks': 'error',
      'react-hooks/exhaustive-deps': 'warn',
      'max-lines': ['error', { max: 1500, skipBlankLines: true, skipComments: true }],
    },
  },
  {
    files: ['src/**/*.{ts,tsx}'],
    ignores: ['src/**/*.test.{ts,tsx}'],
    rules: {
      'max-lines-per-function': ['error', { max: 450, skipBlankLines: true, skipComments: true }],
    },
  },
  {
    files: ['eslint.config.js', 'vite.config.ts'],
    languageOptions: {
      globals: globals.node,
    },
  },
);
