module.exports = {
  root: true,
  parser: "@typescript-eslint/parser",
  parserOptions: {
    ecmaVersion: 2022,
    sourceType: "module",
    ecmaFeatures: { jsx: true },
  },
  env: {
    browser: true,
    es2022: true,
  },
  extends: [
    "eslint:recommended",
    "plugin:react/recommended",
    "plugin:@typescript-eslint/recommended",
    // Disables ESLint rules that conflict with Prettier formatting.
    // Prettier itself runs as a separate step (`npm run format` /
    // `npx prettier --check` in CI) so formatting violations don't
    // cascade into lint failures.
    "prettier",
  ],
  plugins: ["react", "@typescript-eslint"],
  settings: { react: { version: "detect" } },
  rules: {
    // project preferences
    "react/react-in-jsx-scope": "off",
    "@typescript-eslint/explicit-module-boundary-types": "off",
    // Apostrophes in English copy ("we'll", "don't", "you're") are
    // legible in source; escaping them as &apos; everywhere hurts
    // readability for no real benefit. We don't allow raw < or > so
    // the actual XSS-shaped issues this rule catches don't apply.
    "react/no-unescaped-entities": "off",
  },
};
