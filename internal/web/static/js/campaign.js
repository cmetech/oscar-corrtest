(function () {
  'use strict';
  var wait = document.querySelector('[data-autoreload]');
  if (wait) setTimeout(function () { location.reload(); }, Number(wait.getAttribute('data-autoreload')) || 3000);
  var list = document.querySelector('[data-campaign-timeline]');
  if (!list || !window.EventSource || list.hasAttribute('data-terminal')) return;
  var state = document.querySelector('[data-stream-state]');
  var after = 0;
  list.querySelectorAll('[data-sequence]').forEach(function (n) { after = Math.max(after, Number(n.getAttribute('data-sequence')) || 0); });
  var stream = new EventSource('/campaigns/' + encodeURIComponent(list.getAttribute('data-campaign-id')) + '/events?after=' + after);
  if (state) state.textContent = 'Live';
  stream.addEventListener('campaign-event', function (m) {
    var e = JSON.parse(m.data);
    if (list.querySelector('[data-sequence="' + e.sequence + '"]')) return;
    var li = document.createElement('li');
    li.setAttribute('data-sequence', e.sequence);
    var t = document.createElement('time'); t.textContent = e.occurredAt;
    var s = document.createElement('strong'); s.textContent = ' ' + e.summary + ' ';
    var c = document.createElement('code'); c.textContent = e.type;
    li.append(t, s, c);
    list.appendChild(li);
  });
  stream.addEventListener('campaign-done', function () { stream.close(); location.reload(); });
  stream.onerror = function () { stream.close(); if (state) state.textContent = 'Paused, refresh to reconnect'; };
}());
