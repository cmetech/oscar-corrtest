(function () {
  'use strict';

  var consoleRoot = document.querySelector('[data-log-console]');
  if (!consoleRoot) return;

  var rows = consoleRoot.querySelector('[data-log-rows]');
  var state = consoleRoot.querySelector('[data-log-state]');
  var pause = consoleRoot.querySelector('[data-log-pause]');
  var clear = consoleRoot.querySelector('[data-log-clear]');
  var sourceFilter = consoleRoot.querySelector('[data-log-source-filter]');
  var levelFilter = consoleRoot.querySelector('[data-log-level-filter]');
  var textFilter = consoleRoot.querySelector('[data-log-text-filter]');
  var paused = false;
  var pending = [];

  function normalized(value) {
    return String(value || '').trim().toLowerCase();
  }

  function displayTime(value) {
    var parsed = new Date(value);
    if (Number.isNaN(parsed.getTime())) return String(value || '');
    return parsed.toISOString().replace('T', ' ');
  }

  function matchesFilters(row) {
    var source = normalized(sourceFilter && sourceFilter.value);
    var level = normalized(levelFilter && levelFilter.value);
    var text = normalized(textFilter && textFilter.value);
    if (source && normalized(row.dataset.logSource) !== source) return false;
    if (level && normalized(row.dataset.logLevel) !== level) return false;
    return !text || normalized(row.textContent).indexOf(text) !== -1;
  }

  function applyFilters() {
    Array.prototype.forEach.call(rows.querySelectorAll('.log-row'), function (row) {
      row.hidden = !matchesFilters(row);
    });
  }

  function addSourceOption(value) {
    if (!sourceFilter || !value) return;
    var exists = Array.prototype.some.call(sourceFilter.options, function (option) {
      return option.value === value;
    });
    if (exists) return;
    var option = document.createElement('option');
    option.value = value;
    option.textContent = value;
    sourceFilter.appendChild(option);
  }

  function append(record) {
    var empty = rows.querySelector('[data-log-empty]');
    if (empty) empty.remove();
    var row = document.createElement('div');
    row.className = 'log-row';
    row.dataset.sequence = String(record.sequence || '');
    row.dataset.logSource = String(record.source || '');
    row.dataset.logLevel = String(record.level || '');
    [displayTime(record.time), record.level, record.source, record.message].forEach(function (value, index) {
      var element = document.createElement(index === 0 ? 'time' : index === 3 ? 'code' : index === 1 ? 'strong' : 'span');
      element.textContent = String(value || '');
      row.appendChild(element);
    });
    rows.appendChild(row);
    addSourceOption(row.dataset.logSource);
    row.hidden = !matchesFilters(row);
    while (rows.children.length > 500) rows.firstElementChild.remove();
    rows.scrollTop = rows.scrollHeight;
  }

  function receive(event) {
    try {
      var record = JSON.parse(event.data);
      if (paused) {
        pending.push(record);
        if (pending.length > 500) pending.shift();
      } else {
        append(record);
      }
    } catch (_) {
      state.textContent = 'A malformed log event was ignored.';
    }
  }

  Array.prototype.forEach.call(rows.querySelectorAll('.log-row'), function (row) {
    addSourceOption(row.dataset.logSource);
  });
  [sourceFilter, levelFilter, textFilter].forEach(function (control) {
    if (control) control.addEventListener(control === textFilter ? 'input' : 'change', applyFilters);
  });

  if (window.EventSource) {
    var stream = new EventSource(consoleRoot.dataset.eventsUrl);
    stream.addEventListener('open', function () { state.textContent = 'Live log stream connected'; });
    stream.addEventListener('error', function () { state.textContent = 'Log stream disconnected; reconnecting'; });
    stream.addEventListener('log', receive);
  } else {
    state.textContent = 'Live log streaming is unavailable in this browser.';
  }

  pause.addEventListener('click', function () {
    paused = !paused;
    pause.textContent = paused ? 'Resume' : 'Pause';
    pause.setAttribute('aria-pressed', String(paused));
    if (!paused) {
      pending.forEach(append);
      pending = [];
    }
  });
  clear.addEventListener('click', function () {
    rows.textContent = '';
  });
}());
