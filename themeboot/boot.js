(function () {
  var root = document.documentElement;
  var theme = root.getAttribute('data-theme') || 'sample';
  var mode = 'system';
  try {
    var raw = localStorage.getItem('govue-theme') /* the key, replaced by WithStorageKey */;
    if (raw) {
      var stored = JSON.parse(raw);
      if (stored && typeof stored.theme === 'string' && stored.theme !== '') {
        theme = stored.theme;
      }
      if (stored && (stored.mode === 'light' || stored.mode === 'dark')) {
        mode = stored.mode;
      }
    }
  } catch (e) {}
  root.setAttribute('data-theme', theme);
  if (mode === 'system') {
    root.removeAttribute('data-mode');
  } else {
    root.setAttribute('data-mode', mode);
  }
})();
