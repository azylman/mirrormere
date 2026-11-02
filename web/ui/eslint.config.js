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
      'react-hooks/exhaustive-deps': 'error',
      '@typescript-eslint/no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_' },
      ],
      'max-lines': ['error', { max: 1500, skipBlankLines: true, skipComments: true }],
    },
  },
  {
    files: ['src/**/*.{ts,tsx}'],
    ignores: ['src/**/*.test.{ts,tsx}'],
    rules: {
      'max-lines-per-function': ['error', { max: 450, skipBlankLines: true, skipComments: true }],
      'complexity': ['error', { max: 35 }],
      'max-depth': ['error', { max: 5 }],
    },
  },
  {
    // Grandfathered legacy widgets and formatters that exceed complexity / depth bounds.
    files: [
      'src/widgets/**',
      'src/shell/NextUp.tsx',
      'src/store/index.ts',
      'src/video/Hud.tsx',
      'src/video/api.ts',
    ],
    rules: {
      'complexity': 'off',
      'max-depth': 'off',
    },
  },
  {
    files: ['eslint.config.js', 'vite.config.ts'],
    languageOptions: {
      globals: globals.node,
    },
  },
);
