(function () {
  'use strict';

  var key = 'corrtest-theme';
  var root = document.documentElement;
  var toggle = document.querySelector('[data-theme-toggle]');
  if (!toggle) return;

  var icon = toggle.querySelector('[data-theme-icon]');

  function apply(theme) {
    var light = theme === 'light';
    document.documentElement.dataset.theme = theme;
    document.documentElement.style.colorScheme = theme;
    toggle.setAttribute('aria-label', 'Light theme');
    toggle.setAttribute('aria-pressed', light ? 'true' : 'false');
    toggle.title = light ? 'Switch to dark theme' : 'Switch to light theme';
    if (icon) icon.textContent = light ? '☀' : '☾';
  }

  apply(root.dataset.theme === 'light' ? 'light' : 'dark');

  toggle.addEventListener('click', function () {
    var next = root.dataset.theme === 'light' ? 'dark' : 'light';
    apply(next);
    try {
      localStorage.setItem('corrtest-theme', next);
    } catch (_) {
      // Storage is optional; the in-page toggle must continue to work.
    }
  });
})();
