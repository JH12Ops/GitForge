;(function () {
  try {
    var prefs = null
    var raw =
      localStorage.getItem('gitforge:preferences') ??
      localStorage.getItem('git-light:preferences')
    if (raw) {
      prefs = JSON.parse(raw)
    }
    if (!prefs) {
      document.documentElement.dataset.theme = 'default'
      return
    }
    var theme = prefs.theme || 'default'
    var resolved = theme
    if (theme === 'system') {
      resolved = window.matchMedia('(prefers-color-scheme: dark)').matches
        ? 'default'
        : 'light'
    }
    document.documentElement.dataset.theme = resolved
  } catch (e) {
    document.documentElement.dataset.theme = 'default'
  }
})()
