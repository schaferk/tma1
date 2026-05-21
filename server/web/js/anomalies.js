// Anomalies view — lists tma1 anomaly detections across recent sessions.
// Polls /api/anomalies (no SSE; the Detector itself caches with a 30s TTL
// so polling at 10s gives near-real-time updates without server push).
//
// All visual styling lives in style.css under the .anom-* class family —
// no inline styles, so the view follows the dashboard's theme variables.

var anom_state = {
  items: [],
  filterSeverity: '',
  autorefreshTimer: null,
};

async function anom_load() {
  try {
    var resp = await fetch('/api/anomalies?limit=200');
    if (!resp.ok) {
      anom_state.items = [];
      anom_render();
      return false;
    }
    var data = await resp.json();
    anom_state.items = data.anomalies || [];
    anom_render();
    return true;
  } catch (err) {
    console.error('anom_load failed', err);
    return false;
  }
}

function anom_render() {
  var sevFilter = (document.getElementById('anom-filter-sev') || {}).value || '';
  anom_state.filterSeverity = sevFilter;
  var items = anom_state.items;

  // Stats use the full set; the filter only narrows the list below.
  var counts = { total: items.length, high: 0, medium: 0, low: 0 };
  var sessions = {};
  items.forEach(function (a) {
    counts[a.severity] = (counts[a.severity] || 0) + 1;
    if (a.session_id) sessions[a.session_id] = true;
  });
  document.getElementById('anom-val-total').textContent = counts.total;
  document.getElementById('anom-val-high').textContent = counts.high;
  document.getElementById('anom-val-medium').textContent = counts.medium;
  document.getElementById('anom-val-sessions').textContent = Object.keys(sessions).length;

  var listEl = document.getElementById('anom-list');
  if (!listEl) return;
  var shown = sevFilter ? items.filter(function (a) { return a.severity === sevFilter; }) : items;

  if (shown.length === 0) {
    listEl.innerHTML = '<div style="color:var(--text-muted);padding:24px;text-align:center;font-size:13px">No anomalies in the active window.</div>';
    return;
  }
  listEl.innerHTML = shown.map(function (a) { return anom_renderRow(a, true); }).join('');
}

// anom_renderRow returns the compact, click-to-expand row used in both
// the top-level Anomalies tab and the session-detail insights panel.
// includeSession adds the session id chip (useful only in the top view —
// the session-detail panel already knows which session it's in).
function anom_renderRow(a, includeSession) {
  var sev = a.severity || 'low';
  var pill = '<span class="anom-pill sev-' + sev + '">' + sev + '</span>';
  var sess = '';
  if (includeSession && a.session_id) {
    var sid = (a.session_id + '').substring(0, 8);
    // event.stopPropagation prevents the parent row's toggle from firing
    // when the user is trying to jump into the session detail overlay.
    // sess_openDetail floats an overlay above the current view, so we
    // don't need to switchView('sessions') first.
    sess =
      '<a class="anom-sess-link" title="Open session detail" ' +
      'onclick="event.stopPropagation();sess_openDetail(\x27' +
      anom_jsLit(a.session_id) + '\x27,\x27\x27)">' + anom_escape(sid) + '</a>';
  }

  var detail = '';
  if (a.suggestion || (a.related_files && a.related_files.length > 0)) {
    detail += '<div class="anom-detail">';
    if (a.suggestion) {
      detail += '<div class="anom-suggestion">→ ' + anom_escape(a.suggestion) + '</div>';
    }
    if (a.related_files && a.related_files.length > 0) {
      detail += '<div class="anom-files">' + a.related_files.slice(0, 6).map(function (f) {
        return '<code>' + anom_escape(anom_shortPath(f)) + '</code>';
      }).join('') + (a.related_files.length > 6 ? '<span style="color:var(--text-dim)">+' + (a.related_files.length - 6) + ' more</span>' : '') + '</div>';
    }
    detail += '</div>';
  }

  return (
    '<div class="anom-row sev-' + sev + '" onclick="this.classList.toggle(\x27open\x27)">' +
    '<div class="anom-row-head">' + pill +
    '<span class="anom-kind">' + anom_escape(a.kind) + '</span>' + sess +
    '</div>' +
    '<div class="anom-evidence">' + anom_escape(a.evidence || '') + '</div>' +
    detail +
    '</div>'
  );
}

function anom_shortPath(p) {
  if (!p) return '';
  if (p.length <= 50) return p;
  var parts = p.split('/');
  if (parts.length <= 3) return p;
  return '.../' + parts.slice(-3).join('/');
}

function anom_escape(s) {
  if (s == null) return '';
  return String(s)
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;')
    // Single quote escape is required because some callers embed the
    // result inside JS single-quoted strings inside HTML double-quoted
    // attributes (e.g. onclick="…sess_openDetail('SID', '')"). Without
    // this, a stray apostrophe in the data breaks out of the JS string.
    .replace(/'/g, '&#39;');
}

// anom_jsLit escapes a value for safe embedding inside a JS single-quoted
// string literal that itself lives inside an HTML double-quoted attribute
// (e.g. onclick="…'<here>'…"). HTML attribute parsing happens FIRST, so
// `&#39;` decodes to `'` before the JS source is evaluated — meaning
// anom_escape alone is insufficient for this context. Backslash escapes
// survive HTML attribute parsing untouched, so JS sees the intended
// escape sequence.
function anom_jsLit(s) {
  if (s == null) return '';
  return String(s)
    .replace(/\\/g, '\\\\')
    .replace(/'/g, "\\'")
    // Defence in depth: prevent script breakout if the value contains
    // </script>, and escape any HTML metacharacters that could otherwise
    // confuse the HTML attribute parser.
    .replace(/</g, '\\x3c')
    .replace(/>/g, '\\x3e')
    .replace(/&/g, '\\x26')
    .replace(/"/g, '\\x22');
}

function anom_startAutorefresh() {
  if (anom_state.autorefreshTimer) return;
  anom_state.autorefreshTimer = setInterval(function () {
    var cb = document.getElementById('anom-autorefresh');
    if (cb && !cb.checked) return;
    if (currentView !== 'anomalies') {
      anom_stopAutorefresh();
      return;
    }
    anom_load();
  }, 10000);
}

function anom_stopAutorefresh() {
  if (anom_state.autorefreshTimer) {
    clearInterval(anom_state.autorefreshTimer);
    anom_state.autorefreshTimer = null;
  }
}
