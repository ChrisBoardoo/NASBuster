// SPDX-License-Identifier: AGPL-3.0-or-later
// Applies the saved theme on <html> before the first paint, to avoid a flash of the
// default theme. Must stay in sync with STORAGE_KEY and THEMES in src/lib/theme.ts.
;(function () {
  try {
    var theme = window.localStorage.getItem('cwnsb.theme')
    if (theme === 'bold' || theme === 'paper') {
      document.documentElement.setAttribute('data-theme', theme)
    }
  } catch (e) {
    /* storage unavailable: keep the default theme */
  }
})()
