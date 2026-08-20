(function () {
  'use strict';
  var trigger = document.querySelector('[data-help-open]');
  var drawer = document.querySelector('[data-help-drawer]');
  var overlay = document.querySelector('[data-help-overlay]');
  var closeButton = document.querySelector('[data-help-close]');
  if (!trigger || !drawer || !overlay || !closeButton) return;
  var lastFocus = null;
  function focusable() {
    return Array.prototype.slice.call(drawer.querySelectorAll('a[href],button:not([disabled]),input:not([disabled]),select:not([disabled]),textarea:not([disabled]),[tabindex]:not([tabindex="-1"])'));
  }
  function open() {
    lastFocus = document.activeElement;
    drawer.hidden = false;
    overlay.hidden = false;
    trigger.setAttribute('aria-expanded', 'true');
    document.body.classList.add('help-open');
    closeButton.focus();
  }
  function close() {
    drawer.hidden = true;
    overlay.hidden = true;
    trigger.setAttribute('aria-expanded', 'false');
    document.body.classList.remove('help-open');
    if (lastFocus && lastFocus.focus) lastFocus.focus();
  }
  trigger.addEventListener('click', open);
  closeButton.addEventListener('click', close);
  overlay.addEventListener('click', close);
  document.addEventListener('keydown', function (event) {
    if (drawer.hidden) return;
    if (event.key === 'Escape') { event.preventDefault(); close(); return; }
    if (event.key !== 'Tab') return;
    var items = focusable();
    if (!items.length) return;
    var first = items[0];
    var last = items[items.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  });
}());
