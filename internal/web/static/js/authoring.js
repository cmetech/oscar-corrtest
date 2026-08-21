(function () {
  'use strict';

  function copyBlock(button) {
    var source = document.getElementById(button.dataset.copyTarget);
    if (!source || !navigator.clipboard) return Promise.resolve();
    return navigator.clipboard.writeText(source.textContent).then(function () {
      button.textContent = 'Copied';
    });
  }

  function filterRows(input) {
    var query = input.value.trim().toLowerCase();
    document.querySelectorAll('[data-schema-row]').forEach(function (row) {
      row.hidden = !row.dataset.searchValue.includes(query);
    });
  }

  function submitExampleSelection(select) {
    var form = select.form;
    if (!form) return;
    if (typeof form.requestSubmit === 'function') form.requestSubmit();
    else form.submit();
  }

  function shouldEnhanceViewClick(event) {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey ||
      event.ctrlKey || event.shiftKey || event.altKey) return false;
    return true;
  }

  function selectView(link) {
    var target = document.getElementById(link.dataset.viewTarget);
    if (!target) return false;

    document.querySelectorAll('[data-authoring-view-link]').forEach(function (candidate) {
      var selected = candidate === link;
      if (selected) candidate.setAttribute('aria-current', 'page');
      else candidate.removeAttribute('aria-current');
    });
    document.querySelectorAll('[data-authoring-view-panel]').forEach(function (panel) {
      panel.hidden = panel !== target;
    });

    if (window.history && window.history.replaceState) {
      var destination = new URL(link.href, document.baseURI);
      window.history.replaceState({}, '', destination.pathname + destination.search + destination.hash);
    }
    return true;
  }

  document.querySelectorAll('[data-copy-target]').forEach(function (button) {
    button.addEventListener('click', function () { copyBlock(button); });
  });
  document.querySelectorAll('[data-schema-filter]').forEach(function (input) {
    input.addEventListener('input', function () { filterRows(input); });
  });
  document.querySelectorAll('[data-authoring-example-select]').forEach(function (select) {
    select.addEventListener('change', function () { submitExampleSelection(select); });
  });
  document.querySelectorAll('[data-authoring-view-link]').forEach(function (link) {
    link.addEventListener('click', function (event) {
      if (!shouldEnhanceViewClick(event)) return;
      if (selectView(link)) event.preventDefault();
    });
  });
}());
