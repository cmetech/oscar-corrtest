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

  document.querySelectorAll('[data-copy-target]').forEach(function (button) {
    button.addEventListener('click', function () { copyBlock(button); });
  });
  document.querySelectorAll('[data-schema-filter]').forEach(function (input) {
    input.addEventListener('input', function () { filterRows(input); });
  });
}());
