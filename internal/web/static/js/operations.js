(function () {
  'use strict';
  var consoleRoot = document.querySelector('[data-log-console]');
  if (!consoleRoot || !window.EventSource) return;
  var rows = consoleRoot.querySelector('[data-log-rows]');
  var state = consoleRoot.querySelector('[data-log-state]');
  var pause = consoleRoot.querySelector('[data-log-pause]');
  var clear = consoleRoot.querySelector('[data-log-clear]');
  var paused = false;
  var pending = [];
  function append(record) {
    var empty = rows.querySelector('[data-log-empty]');
    if (empty) empty.remove();
    var row = document.createElement('div');
    row.className = 'log-row';
    row.dataset.sequence = String(record.sequence || '');
    [record.time, record.level, record.source, record.message].forEach(function (value, index) {
      var element = document.createElement(index === 0 ? 'time' : index === 3 ? 'code' : index === 1 ? 'strong' : 'span');
      element.textContent = String(value || '');
      row.appendChild(element);
    });
    rows.appendChild(row);
    while (rows.children.length > 500) rows.firstElementChild.remove();
    rows.scrollTop = rows.scrollHeight;
  }
  function receive(event) {
    try {
      var record = JSON.parse(event.data);
      if (paused) { pending.push(record); if (pending.length > 500) pending.shift(); }
      else append(record);
    } catch (_) { state.textContent = 'A malformed log event was ignored.'; }
  }
  var source = new EventSource(consoleRoot.dataset.eventsUrl);
  source.addEventListener('open', function () { state.textContent = 'Live log stream connected'; });
  source.addEventListener('error', function () { state.textContent = 'Log stream disconnected; reconnecting'; });
  source.addEventListener('log', receive);
  pause.addEventListener('click', function () {
    paused = !paused;
    pause.textContent = paused ? 'Resume' : 'Pause';
    if (!paused) { pending.forEach(append); pending = []; }
  });
  clear.addEventListener('click', function () { rows.textContent = ''; });
}());
