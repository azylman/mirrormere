// Root ESLint config for Mirrormere's vanilla JS outside web/ui. web/ui keeps
// its own type-checked config and is linted from there, so it is ignored here.
// CommonJS on purpose: the root package.json must not set "type": "module",
// because scripts/perf tests and sidecars/eink-renderer are CommonJS run by node --test.
const js = require('@eslint/js');
const globals = require('globals');

module.exports = [
  {
    ignores: [
      'web/ui/**',
      'web/static/ui/**',
      '**/node_modules/**',
      '**/dist/**',
      '**/*.min.js',
    ],
  },
  js.configs.recommended,
  {
    rules: {
      eqeqeq: ['error', 'always'],
      // Same `_` convention as web/ui for deliberately unused bindings.
      'no-unused-vars': [
        'error',
        { argsIgnorePattern: '^_', varsIgnorePattern: '^_', caughtErrorsIgnorePattern: '^_' },
      ],
      // Best-effort DOM/storage/media calls on the kiosk swallow failures by
      // design (`catch (_) {}`); other empty blocks are still errors.
      'no-empty': ['error', { allowEmptyCatch: true }],
    },
    linterOptions: { reportUnusedDisableDirectives: 'error' },
  },
  {
    // Classic browser scripts served from web/static/js.
    files: ['web/static/js/**/*.js'],
    languageOptions: {
      sourceType: 'script',
      globals: { ...globals.browser, module: 'readonly' },
    },
  },
  {
    files: ['eslint.config.js', 'sidecars/eink-renderer/**/*.js'],
    languageOptions: {
      sourceType: 'commonjs',
      globals: globals.node,
    },
  },
  {
    files: ['scripts/perf/**/*.test.js'],
    languageOptions: {
      sourceType: 'commonjs',
      globals: { ...globals.node, ...globals.browser },
    },
  },
  {
    files: ['scripts/perf/**/*.mjs'],
    languageOptions: {
      sourceType: 'module',
      globals: globals.node,
    },
  },
];
