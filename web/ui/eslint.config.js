import js from '@eslint/js';
import globals from 'globals';
import reactHooks from 'eslint-plugin-react-hooks';
import tseslint from 'typescript-eslint';

export default tseslint.config(
  { ignores: ['../static/ui/**', 'node_modules/**'] },
  {
    extends: [
      js.configs.recommended,
      ...tseslint.configs.strictTypeChecked,
      ...tseslint.configs.stylisticTypeChecked,
      reactHooks.configs.flat.recommended,
    ],
    files: ['**/*.{ts,tsx}'],
    languageOptions: {
      ecmaVersion: 2022,
      globals: globals.browser,
      parserOptions: {
        projectService: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
    rules: {
      '@typescript-eslint/consistent-type-imports': 'error',
      eqeqeq: ['error', 'always'],
      'no-console': ['error', { allow: ['warn', 'error'] }],
      // Numbers in template literals (px offsets, HTTP status, indices) are
      // intentional; keep the rule for objects, nullish and other unions.
      '@typescript-eslint/restrict-template-expressions': ['error', { allowNumber: true }],
      // Stylistic: 229 findings, none autofixable (only suggestions), and
      // swapping `||` for `??` changes behaviour for '' / 0 / false.
      '@typescript-eslint/prefer-nullish-coalescing': 'off',
      // Stylistic: its autofix rewrites `querySelector(...) as HTMLDivElement`
      // to `querySelector(...)!`, but the cast is what drives querySelector's
      // generic inference, so the fix yields Element and breaks tsc (293
      // findings, all such casts in tests).
      '@typescript-eslint/non-nullable-type-assertion-style': 'off',
      // 76 findings, all String()/template coercion of loosely typed widget
      // JSON (`unknown` / `{}`) or fetch-mock `RequestInfo | URL` inputs; none
      // stringify a real object, so the rule only adds noise here.
      '@typescript-eslint/no-base-to-string': 'off',
      // 14 findings, all `delete record[id]` on id-keyed Record copies, the
      // intended pattern for these maps rather than a defect.
      '@typescript-eslint/no-dynamic-delete': 'off',
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
    files: ['src/**/*.{ts,tsx}'],
    ignores: ['src/**/*.test.{ts,tsx}', 'src/test/**'],
    rules: {
      'max-lines': ['error', { max: 500, skipBlankLines: false, skipComments: false }],
    },
  },
  {
    files: ['eslint.config.js', 'vite.config.ts', 'playwright.config.ts'],
    languageOptions: {
      globals: globals.node,
    },
  },
  {
    files: ['src/**/*.test.{ts,tsx}', 'src/test/**/*.{ts,tsx}'],
    rules: {
      // Tests deliberately assert presence with `!`; a wrong assertion fails
      // the test loudly, so these findings are not defects.
      '@typescript-eslint/no-non-null-assertion': 'off',
      // `expect(mock.method).toHaveBeenCalled()` is the standard Vitest idiom
      // and is a known false positive for this rule.
      '@typescript-eslint/unbound-method': 'off',
      // Async stubs satisfy Promise-returning interfaces without awaiting.
      '@typescript-eslint/require-await': 'off',
      // No-op stubs (`() => {}`) are intentional in mocks.
      '@typescript-eslint/no-empty-function': 'off',
      // Mock fetch bodies are JSON.parse'd and Vitest asymmetric matchers
      // (expect.any / objectContaining) are typed `any` by design.
      '@typescript-eslint/no-unsafe-member-access': 'off',
      '@typescript-eslint/no-unsafe-assignment': 'off',
      '@typescript-eslint/no-unsafe-argument': 'off',
      '@typescript-eslint/no-unsafe-call': 'off',
      '@typescript-eslint/no-unsafe-return': 'off',
    },
  },
  {
    // Playwright config and specs sit outside tsconfig.json (they run under
    // Playwright's own TS loader), so the project service cannot type them.
    files: ['playwright.config.ts', 'e2e/**/*.{ts,tsx}'],
    extends: [tseslint.configs.disableTypeChecked],
  },
  {
    files: ['e2e/**/*.{ts,tsx}'],
    languageOptions: {
      globals: {
        ...globals.node,
        ...globals.browser,
      },
    },
  },
);
