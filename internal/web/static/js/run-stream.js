(function () {
  'use strict';
  var timeline = document.querySelector('[data-run-timeline]');
  if (!timeline || !window.EventSource) return;
  var runID = timeline.getAttribute('data-run-id');
  var state = document.querySelector('[data-stream-state]');
  var after = 0;
  timeline.querySelectorAll('[data-sequence]').forEach(function (node) {
    after = Math.max(after, Number(node.getAttribute('data-sequence')) || 0);
  });
  var stream = new EventSource('/runs/' + encodeURIComponent(runID) + '/events?after=' + after);
  if (state) state.textContent = 'Live';
  stream.addEventListener('run-event', function (message) {
    var event = JSON.parse(message.data);
    if (timeline.querySelector('[data-sequence="' + event.sequence + '"]')) return;
    var empty = timeline.querySelector('[data-empty]');
    if (empty) empty.remove();
    var item = document.createElement('li');
    item.setAttribute('data-sequence', event.sequence);
    var time = document.createElement('time');
    time.textContent = event.occurredAt;
    var summary = document.createElement('strong');
    summary.textContent = event.summary;
    var type = document.createElement('code');
    type.textContent = event.type;
    item.append(time, summary, type);
    timeline.appendChild(item);
  });
  stream.onerror = function () {
    stream.close();
    if (state) state.textContent = 'Paused — refresh to reconnect';
  };
}());
