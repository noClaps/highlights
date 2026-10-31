# Theme catalogs

`tools/record/ts/test/theme_test.ts` checks the bundled themes against
two catalogs outside the package:

- `shiki-themes.json`: Shiki's `bundledThemesInfo` (id, display name, type),
  taken from the `shiki` package version it names.
- `pierre-theme/`: the Pierre theme files from
  `pierrecomputer/pierre@861ce3c61e1cfeaf283d83e95a3767479e75bfee`,
  `packages/theme/themes`, the commit `NOTICE.md` pins.
