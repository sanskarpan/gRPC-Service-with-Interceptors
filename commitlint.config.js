module.exports = {
  extends: ['@commitlint/config-conventional'],
  rules: {
    'type-enum': [2, 'always', [
      'feat', 'fix', 'docs', 'style', 'refactor', 'perf', 'test',
      'build', 'ci', 'chore', 'revert'
    ]],
    'scope-case': [2, 'always', 'kebab-case'],
    'subject-case': [2, 'always', ['sentence-case']],
    'subject-empty': [2, 'never'],
    'type-empty': [2, 'never'],
  },
};