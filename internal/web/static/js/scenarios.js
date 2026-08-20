(function () {
  'use strict';

  var workbench = document.querySelector('.scenario-workbench');
  if (!workbench) return;
  workbench.setAttribute('data-enhanced', 'true');

  var search = workbench.querySelector('[data-scenario-search]');
  var items = workbench.querySelectorAll('[data-scenario-item]');
  var empty = workbench.querySelector('[data-scenario-empty]');
  if (search) {
    search.addEventListener('input', function () {
      var query = search.value.trim().toLowerCase();
      var visible = 0;
      Array.prototype.forEach.call(items, function (item) {
        var match = !query || String(item.dataset.searchValue || '').toLowerCase().indexOf(query) !== -1;
        item.hidden = !match;
        if (match) visible += 1;
      });
      if (empty) empty.hidden = visible !== 0;
    });
  }

  var source = workbench.querySelector('[data-scenario-source]');
  var copyStatus = workbench.querySelector('[data-copy-status]');
  function copyText(value, label) {
    var done = function () {
      if (copyStatus) copyStatus.textContent = label + ' copied to the clipboard.';
    };
    if (navigator.clipboard && window.isSecureContext) {
      navigator.clipboard.writeText(value).then(done, function () {
        if (copyStatus) copyStatus.textContent = 'Clipboard access was denied.';
      });
      return;
    }
    var helper = document.createElement('textarea');
    helper.value = value;
    helper.setAttribute('readonly', '');
    helper.style.position = 'fixed';
    helper.style.opacity = '0';
    document.body.appendChild(helper);
    helper.select();
    try {
      document.execCommand('copy');
      done();
    } catch (_) {
      if (copyStatus) copyStatus.textContent = 'Clipboard access was denied.';
    }
    helper.remove();
  }

  var copySource = workbench.querySelector('[data-copy-source]');
  if (copySource && source) {
    copySource.addEventListener('click', function () { copyText(source.value, 'Scenario source'); });
  }

  Array.prototype.forEach.call(workbench.querySelectorAll('[data-copy-value]'), function (button) {
    button.addEventListener('click', function () { copyText(button.dataset.copyValue, 'Inspection filter'); });
  });

  var tabs = workbench.querySelectorAll('[data-case-tab]');
  var panels = workbench.querySelectorAll('[data-case-panel]');
  function activate(code, focus) {
    Array.prototype.forEach.call(tabs, function (tab) {
      var active = tab.dataset.caseTab === code;
      tab.setAttribute('aria-selected', String(active));
      tab.tabIndex = active ? 0 : -1;
      if (active && focus) tab.focus();
    });
    Array.prototype.forEach.call(panels, function (panel) {
      panel.hidden = panel.dataset.caseCode !== code;
    });
  }
  Array.prototype.forEach.call(tabs, function (tab, index) {
    tab.addEventListener('click', function () { activate(tab.dataset.caseTab, false); });
    tab.addEventListener('keydown', function (event) {
      if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
      event.preventDefault();
      var offset = event.key === 'ArrowRight' ? 1 : -1;
      var next = (index + offset + tabs.length) % tabs.length;
      activate(tabs[next].dataset.caseTab, true);
    });
  });
  if (tabs.length) activate(tabs[0].dataset.caseTab, false);
}());
