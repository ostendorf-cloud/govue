(function () {
  var root = document.documentElement;
  var theme = root.getAttribute('data-theme') || 'sample';
  var mode = 'dark';
  try {
    var raw = localStorage.getItem('govue-theme') /* the key, replaced by WithStorageKey */;
    if (raw) {
      var stored = JSON.parse(raw);
      if (stored && typeof stored.theme === 'string' && stored.theme !== '') {
        theme = stored.theme;
      }
      if (stored && (stored.mode === 'light' || stored.mode === 'dark' || stored.mode === 'system')) {
        mode = stored.mode;
      }
    }
  } catch (e) {}
  root.setAttribute('data-theme', theme);
  if (mode === 'system') {
    root.removeAttribute('data-mode');
    return;
  }
  root.setAttribute('data-mode', mode);
  // A stored mode pins the browser bar too: every theme-color tag above this
  // script takes the colour of the tag whose media query names that mode.
  // Each tag keeps its own colour in data-govue-color first, so useTheme can
  // pin another mode or give system back. pinThemeColor in themeColor.ts is
  // the same step, a test runs both.
  var media = mode === 'dark' ? '(prefers-color-scheme: dark)' : '(prefers-color-scheme: light)';
  var tags = document.querySelectorAll('meta[name="theme-color"]');
  var colour = '';
  for (var i = 0; i < tags.length; i++) {
    if (!tags[i].hasAttribute('data-govue-color')) {
      tags[i].setAttribute('data-govue-color', tags[i].getAttribute('content') || '');
    }
    if (tags[i].getAttribute('media') === media) {
      colour = tags[i].getAttribute('data-govue-color');
    }
  }
  if (colour !== '') {
    for (var j = 0; j < tags.length; j++) {
      tags[j].setAttribute('content', colour);
    }
  }
})();
