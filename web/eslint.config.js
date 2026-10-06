import js from "@eslint/js";
import globals from "globals";
import reactHooks from "eslint-plugin-react-hooks";
import reactRefresh from "eslint-plugin-react-refresh";
import tseslint from "typescript-eslint";

export default tseslint.config(
  { ignores: ["dist"] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ["**/*.{ts,tsx}"],
    languageOptions: { ecmaVersion: 2022, globals: globals.browser },
    plugins: { "react-hooks": reactHooks, "react-refresh": reactRefresh },
    rules: {
      ...reactHooks.configs.recommended.rules,
      "react-refresh/only-export-components": "off",
      "@typescript-eslint/no-explicit-any": "error",
      "no-control-regex": "off",
      "@typescript-eslint/no-unused-vars": [
        "error",
        { argsIgnorePattern: "^_", varsIgnorePattern: "^_" },
      ],
    },
  },
  {
    files: ["src/user/**/*.{ts,tsx}", "src/team/**/*.{ts,tsx}", "src/member/**/*.{ts,tsx}"],
    rules: {
      "no-restricted-imports": [
        "error",
        {
          patterns: [
            // The member app SHARES PAGES with the console on purpose: a list page
            // (keys today, logs and models next) is written once as a renderer that
            // takes an injected data source, so `features/**` is allowed here.
            //
            // What stays forbidden is what would drag the console's identity into
            // the member app: the admin API client, the console session (its
            // token lives in localStorage and it authenticates as an operator), the
            // app shell, and the console-only stylesheet. Those are the things
            // that made a shared page safe to share.
            {
              group: ["**/api/client", "**/session", "**/App", "**/styles.css"],
              message:
                "Team/user modules must not import the admin API client, console session or console stylesheet.",
            },
          ],
        },
      ],
    },
  },
);
