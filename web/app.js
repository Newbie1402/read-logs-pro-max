'use strict';
/* Log Viewer — frontend vanila JS, virtualized list, JSON hover panel */

// ---------- helpers ----------
const $ = (id) => document.getElementById(id);
const esc = (s) => String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
const p2 = (n) => String(n).padStart(2, '0');

function fmtBytes(n) {
  if (n == null) return '?';
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return (i === 0 ? n : n.toFixed(1)) + ' ' + u[i];
}

async function api(path, opts) {
  const r = await fetch(path, opts);
  let body = null;
  try { body = await r.json(); } catch (_) { /* empty */ }
  if (!r.ok) throw new Error((body && body.error) || `HTTP ${r.status}`);
  return body;
}

let toastTimer = 0;
function toast(msg, isErr) {
  const t = $('toast');
  t.textContent = msg;
  t.classList.toggle('err', !!isErr);
  t.classList.remove('hidden');
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => t.classList.add('hidden'), 2600);
}

// ---------- state ----------
const B = 100; // entries / block
const S = {
  cfg: null,
  scanFiles: [],
  scanFromHour: null,
  scanToHour: null,
  jobTimer: 0,
  session: null,     // {id,total,files,fileCount,totalBytes}
  entries: new Map(), // globalIdx -> entry (chế độ all)
  fetching: new Set(),
  blocks: new Map(),  // blockIdx -> {el, filled}
  heights: [],
  offsets: [],
  offsetsDirty: true,
  maxW: 0,
  wrap: true,
  localTime: true,
  mode: 'all',       // all | filter
  filterEntries: null,
  filterLabel: '',
  level: '',
  q: '',             // từ khóa đang lọc (để highlight)
  jview: null,       // null = tự chọn (bảng cho object phẳng, json cho lồng nhau)
  panelDismissed: false,
  pinned: null,      // {el}
  currentJson: '',
  pendingJump: null,
  colw: null,        // {ln,ts,lv,th,lg}
};

// ---------- độ rộng cột ----------
const COL_MIN = { ln: 34, ts: 80, lv: 34, th: 56, lg: 56 };
const COL_MAX = { ln: 140, ts: 320, lv: 120, th: 600, lg: 800 };

function loadColW() {
  try {
    const w = JSON.parse(localStorage.getItem('lv-colw') || 'null');
    if (w && ['ln', 'ts', 'lv', 'th', 'lg'].every((k) => typeof w[k] === 'number')) return w;
  } catch (_) { /* ignore */ }
  return null;
}

function applyColW() {
  const w = S.colw;
  const root = document.documentElement.style;
  root.setProperty('--c-ln', w.ln + 'px');
  root.setProperty('--c-ts', w.ts + 'px');
  root.setProperty('--c-lv', w.lv + 'px');
  root.setProperty('--c-th', w.th + 'px');
  root.setProperty('--c-lg', w.lg + 'px');
}

function initColumns() {
  S.colw = loadColW() || { ln: 64, ts: 128, lv: 50, th: 180, lg: 220 };
  applyColW();
  document.querySelectorAll('#loghead .grip').forEach((grip) => {
    grip.addEventListener('mousedown', (e) => {
      e.preventDefault();
      const col = grip.dataset.col;
      const startX = e.clientX;
      const startW = S.colw[col];
      grip.classList.add('drag');
      document.body.classList.add('col-drag');
      const onMove = (ev) => {
        const nw = Math.max(COL_MIN[col], Math.min(COL_MAX[col], startW + (ev.clientX - startX)));
        if (nw !== S.colw[col]) {
          S.colw[col] = nw;
          applyColW();
        }
      };
      const onUp = () => {
        grip.classList.remove('drag');
        document.body.classList.remove('col-drag');
        window.removeEventListener('mousemove', onMove);
        window.removeEventListener('mouseup', onUp);
        localStorage.setItem('lv-colw', JSON.stringify(S.colw));
        resetViewData(true); // đo lại chiều cao các block
      };
      window.addEventListener('mousemove', onMove);
      window.addEventListener('mouseup', onUp);
    });
  });
}

function total() { return S.mode === 'filter' ? (S.filterEntries ? S.filterEntries.length : 0) : (S.session ? S.session.total : 0); }
function nBlocks() { return Math.ceil(total() / B); }
function entryAt(pos) {
  if (S.mode === 'filter') return S.filterEntries ? S.filterEntries[pos] : null;
  return S.entries.get(pos);
}
function origIdx(pos) {
  if (S.mode === 'filter') { const e = S.filterEntries && S.filterEntries[pos]; return e ? e.i : pos; }
  return pos;
}

// ---------- virtualized list ----------
// chiều cao ƯỚC LƯỢNG CỦA 1 BLOCK (B entries) — dùng trước khi đo thật
function estimateBlockH() { return (S.wrap ? 30 : 21) * B; }

function resetViewData(keepScroll) {
  for (const [, blk] of S.blocks) blk.el.remove();
  S.blocks.clear();
  S.entries.clear();
  S.fetching.clear();
  S.heights = new Array(nBlocks()).fill(estimateBlockH());
  S.offsets = [];
  S.offsetsDirty = true;
  S.maxW = 0;
  if (!keepScroll) $('viewport').scrollTop = 0;
  $('spacer').style.width = '';
  scheduleRender();
}

function ensureOffsets() {
  if (!S.offsetsDirty) return;
  const n = nBlocks();
  const off = new Array(n + 1);
  off[0] = 0;
  for (let i = 0; i < n; i++) off[i + 1] = off[i] + (S.heights[i] || estimateBlockH());
  S.offsets = off;
  S.offsetsDirty = false;
  $('spacer').style.height = off[n] + 'px';
}

function visibleBlocks() {
  const vp = $('viewport');
  const y0 = vp.scrollTop, y1 = vp.scrollTop + vp.clientHeight;
  ensureOffsets();
  let lo = 0, hi = nBlocks() - 1, b0 = 0, b1 = -1;
  // binary search block chứa y0
  while (lo <= hi) {
    const m = (lo + hi) >> 1;
    if (S.offsets[m + 1] > y0) { b0 = m; hi = m - 1; } else lo = m + 1;
  }
  lo = b0; hi = nBlocks() - 1;
  while (lo <= hi) {
    const m = (lo + hi) >> 1;
    if (S.offsets[m] < y1) { b1 = m; lo = m + 1; } else hi = m - 1;
  }
  return [b0, Math.max(b1, b0)];
}

let raf = 0;
function scheduleRender() {
  if (!raf) raf = requestAnimationFrame(() => { raf = 0; render(); });
}

// header cột cuộn ngang theo viewport
function syncLogHead() {
  const vp = $('viewport');
  const lh = $('loghead');
  const x = vp.scrollLeft > 0 ? -vp.scrollLeft : 0;
  lh.style.transform = 'translateX(' + x + 'px)';
}

function render() {
  if (!S.session && S.mode !== 'filter') return;
  const n = nBlocks();
  if (n === 0) return;
  ensureOffsets();
  syncLogHead();
  const [v0, v1] = visibleBlocks();
  const a = Math.max(0, v0 - 2), b = Math.min(n - 1, v1 + 2);

  for (const [i, blk] of S.blocks) {
    if (i < a || i > b) { blk.el.remove(); S.blocks.delete(i); }
  }
  for (let i = a; i <= b; i++) {
    let blk = S.blocks.get(i);
    if (!blk) {
      const el = document.createElement('div');
      el.className = 'blk';
      $('blocks').appendChild(el);
      blk = { el, filled: false };
      S.blocks.set(i, blk);
    }
    // cập nhật vị trí mỗi lần render (heights có thể đã đổi)
    blk.el.style.top = S.offsets[i] + 'px';
    if (!blk.filled) fillBlock(i, blk);
  }
  handlePendingJump();
}

function fillBlock(i, blk) {
  const from = i * B;
  const count = Math.min(B, total() - from);
  if (count <= 0) return;
  // chế độ all: cần đủ entries trong map
  if (S.mode === 'all') {
    for (let k = from; k < from + count; k++) {
      if (!S.entries.has(k)) { fetchWindow(i); return; }
    }
  }
  const frag = document.createDocumentFragment();
  for (let k = 0; k < count; k++) {
    const pos = from + k;
    const e = entryAt(pos);
    if (!e) break;
    frag.appendChild(buildEntryEl(e, pos, origIdx(pos)));
  }
  blk.el.textContent = '';
  blk.el.appendChild(frag);
  blk.filled = true;
  requestAnimationFrame(() => measure(blk, i));
}

function measure(blk, i) {
  if (!blk.el.isConnected) return;
  const h = blk.el.offsetHeight;
  if (h > 0 && Math.abs((S.heights[i] || 0) - h) > 0.5) {
    S.heights[i] = h;
    S.offsetsDirty = true;
    scheduleRender();
  }
  if (!S.wrap) {
    const w = blk.el.offsetWidth;
    if (w > S.maxW) {
      S.maxW = Math.min(w + 24, 60000);
      $('spacer').style.width = S.maxW + 'px';
    }
  }
}

function fetchWindow(i) {
  if (!S.session) return;
  const from = i * B;
  const count = Math.min(B, S.session.total - from);
  if (count <= 0 || S.fetching.has(from)) return;
  S.fetching.add(from);
  api(`/api/session/${S.session.id}/entries?from=${from}&count=${count}`)
    .then((r) => {
      for (const e of r.entries) S.entries.set(e.i, e);
      // dọn RAM khi cache phình
      if (S.entries.size > 60000) {
        let k = 0;
        for (const key of S.entries.keys()) {
          if (k++ >= 20000) break;
          S.entries.delete(key);
        }
      }
    })
    .catch((err) => toast('Lỗi đọc log: ' + err.message, true))
    .finally(() => {
      S.fetching.delete(from);
      scheduleRender();
    });
}

function handlePendingJump() {
  const pj = S.pendingJump;
  if (!pj) return;
  if (Date.now() > pj.until) { S.pendingJump = null; return; }
  const b = Math.floor(pj.pos / B);
  const blk = S.blocks.get(b);
  if (blk && blk.filled) {
    const el = blk.el.querySelector(`[data-pos="${pj.pos}"]`);
    if (el) {
      const vp = $('viewport');
      const top = blk.el.offsetTop + el.offsetTop;
      vp.scrollTop = Math.max(0, top - 4);
      el.classList.add('flash');
      setTimeout(() => el.classList.remove('flash'), 1600);
      S.pendingJump = null;
      return;
    }
  }
  // block chưa sẵn sàng: giữ viewport bám gần target trong lúc chờ fetch
  ensureOffsets();
  const target = S.offsets[b];
  const vp = $('viewport');
  if (Math.abs(vp.scrollTop - target) > 300) vp.scrollTop = Math.max(0, target - 4);
}

function gotoPos(pos) {
  if (S.mode === 'filter') setMode('all');
  const b = Math.floor(pos / B);
  ensureOffsets();
  $('viewport').scrollTop = Math.max(0, S.offsets[b] - 4);
  S.pendingJump = { pos, until: Date.now() + 5000 };
  scheduleRender();
}

// ---------- render 1 entry ----------
function fmtTs(ts) {
  if (S.localTime) {
    const d = new Date(ts);
    if (isNaN(d)) return ['', ts];
    return [
      p2(d.getDate()) + '/' + p2(d.getMonth() + 1),
      p2(d.getHours()) + ':' + p2(d.getMinutes()) + ':' + p2(d.getSeconds()) + '.' + String(d.getMilliseconds()).padStart(3, '0'),
    ];
  }
  return [ts.slice(8, 10) + '/' + ts.slice(5, 7), ts.slice(11, 23)];
}

function span(cls, text, title) {
  const s = document.createElement('span');
  s.className = cls;
  s.textContent = text;
  if (title) s.title = title;
  return s;
}

function buildEntryEl(e, pos, orig) {
  const d = document.createElement('div');
  d.className = 'entry';
  d.dataset.pos = pos;
  d.dataset.orig = orig;
  d.appendChild(span('c-ln', String((orig != null ? orig : pos) + 1), 'Bấm để copy nguyên dòng log'));
  if (e.ts) {
    const ts = document.createElement('span');
    ts.className = 'c-ts';
    const [da, ti] = fmtTs(e.ts);
    ts.appendChild(span('ts-d', da));
    ts.appendChild(span('ts-t', ' ' + ti));
    ts.title = e.ts + (S.localTime ? ' (UTC — hiển thị theo giờ máy)' : ' (UTC)');
    d.appendChild(ts);
  } else {
    d.appendChild(span('c-ts', ''));
  }
  d.appendChild(span('c-lv lv-' + (e.lv || 'NONE'), e.lv || '···'));
  d.appendChild(span('c-th', e.th || '', e.th || ''));
  d.appendChild(span('c-lg', e.lg || '', e.lg || ''));
  const msg = document.createElement('span');
  msg.className = 'c-msg';
  fillMsg(msg, e.m || '');
  d.appendChild(msg);
  return d;
}

// ---------- JSON detect & panel ----------
function extractBalanced(text, p) {
  const open = text[p];
  const close = open === '{' ? '}' : ']';
  let depth = 0, inStr = false, escCh = false;
  for (let i = p; i < text.length && i - p < 200000; i++) {
    const ch = text[i];
    if (inStr) {
      if (escCh) escCh = false;
      else if (ch === '\\') escCh = true;
      else if (ch === '"') inStr = false;
      continue;
    }
    if (ch === '"') inStr = true;
    else if (ch === open) depth++;
    else if (ch === close) { depth--; if (depth === 0) return text.slice(p, i + 1); }
  }
  return null;
}

function jsonChunks(text) {
  if (!text || text.length > 50000) return [];
  const out = [];
  let i = 0;
  while (i < text.length && out.length < 3) {
    const ch = text[i];
    if (ch === '{' || ch === '[') {
      const s = extractBalanced(text, i);
      if (s && s.length > 2 && isJson(s)) { out.push({ start: i, text: s }); i += s.length; continue; }
    }
    i++;
  }
  return out;
}

function isJson(s) {
  try {
    const v = JSON.parse(s);
    return v !== null && typeof v === 'object';
  } catch (_) { return false; }
}

function fillMsg(el, text) {
  const chunks = jsonChunks(text);
  const q = (S.mode === 'filter' && S.q) ? S.q : '';
  if (!chunks.length) { appendHighlighted(el, text, q); return; }
  let pos = 0;
  for (const c of chunks) {
    if (c.start > pos) appendHighlighted(el, text.slice(pos, c.start), q);
    const sp = document.createElement('span');
    sp.className = 'jch';
    sp.textContent = c.text;
    sp.dataset.json = c.text;
    sp.title = 'Bấm để mở popup JSON · rê chuột để xem nhanh bên phải';
    el.appendChild(sp);
    pos = c.start + c.text.length;
  }
  if (pos < text.length) appendHighlighted(el, text.slice(pos), q);
}

// tô nổi từ khóa tìm kiếm trong đoạn text thường
function appendHighlighted(parent, text, q) {
  if (!q) { parent.appendChild(document.createTextNode(text)); return; }
  const lower = text.toLowerCase(), ql = q.toLowerCase();
  let i = 0;
  for (;;) {
    const j = lower.indexOf(ql, i);
    if (j < 0) {
      if (i < text.length) parent.appendChild(document.createTextNode(text.slice(i)));
      break;
    }
    if (j > i) parent.appendChild(document.createTextNode(text.slice(i, j)));
    const mk = document.createElement('mark');
    mk.textContent = text.slice(j, j + q.length);
    parent.appendChild(mk);
    i = j + q.length;
  }
}

function colorJson(s) {
  let out = '', i = 0;
  const push = (cls, txt) => { out += cls ? `<span class="${cls}">${esc(txt)}</span>` : esc(txt); };
  while (i < s.length) {
    const ch = s[i];
    if (ch === '"') {
      let j = i + 1;
      while (j < s.length) {
        if (s[j] === '\\') j += 2;
        else if (s[j] === '"') break;
        else j++;
      }
      const tok = s.slice(i, Math.min(j + 1, s.length));
      let k = j + 1;
      while (k < s.length && (s[k] === ' ' || s[k] === '\n' || s[k] === '\t')) k++;
      push(s[k] === ':' ? 'jk' : 'js', tok);
      i = j + 1;
    } else if (ch >= '0' && ch <= '9' || (ch === '-' && s[i + 1] >= '0' && s[i + 1] <= '9')) {
      let j = i + 1;
      while (j < s.length && /[-+.0-9eE]/.test(s[j])) j++;
      push('jn', s.slice(i, j));
      i = j;
    } else if ((ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z')) {
      let j = i;
      while (j < s.length && /[a-zA-Z]/.test(s[j])) j++;
      const w = s.slice(i, j);
      push(/^(true|false|null)$/.test(w) ? 'jb' : '', w);
      i = j;
    } else { push('', ch); i++; }
  }
  return out;
}

function isScalar(v) { return v === null || ['string', 'number', 'boolean'].includes(typeof v); }

// chế độ xem phù hợp: bảng cho object phẳng/mảng phẳng, json cho cấu trúc lồng nhau
function autoJsonView(text) {
  try {
    const v = JSON.parse(text);
    if (Array.isArray(v)) return v.every(isScalar) ? 'table' : 'json';
    if (v && typeof v === 'object') return Object.values(v).every(isScalar) ? 'table' : 'json';
  } catch (_) { /* ignore */ }
  return 'json';
}

function currentJsonView(text) { return S.jview || autoJsonView(text); }

function setSegState(segEl, view) {
  segEl.querySelectorAll('button').forEach((b) => b.classList.toggle('on', b.dataset.v === view));
}

function renderJsonInto(el, text) {
  el.innerHTML = renderJsonBody(text, currentJsonView(text));
}

function renderJsonBody(text, view) {
  let v;
  try { v = JSON.parse(text); } catch (_) { return esc(text); }
  if (view === 'table') {
    if (Array.isArray(v) && v.every(isScalar)) {
      let h = '<table><tr><th>#</th><th>Giá trị</th></tr>';
      v.forEach((x, i) => { h += `<tr><td class="k">${i}</td><td>${colorJson(JSON.stringify(x))}</td></tr>`; });
      return h + '</table>';
    }
    if (v && !Array.isArray(v) && Object.values(v).every(isScalar)) {
      let h = '<table><tr><th>Khóa</th><th>Giá trị</th></tr>';
      for (const [k, x] of Object.entries(v)) {
        h += `<tr><td class="k">${esc(k)}</td><td>${colorJson(JSON.stringify(x))}</td></tr>`;
      }
      return h + '</table>';
    }
  }
  return colorJson(JSON.stringify(v, null, 2));
}

// hover: chỉ cập nhật panel khi panel đang mở (hoặc chưa từng bị tắt)
function hoverJson(text, el) {
  if (S.pinned || S.panelDismissed) return;
  showJson(text, el);
}

function showJson(text, el) {
  if (S.pinned && S.pinned.el !== el) return;
  S.currentJson = text;
  const panel = $('json-panel');
  panel.classList.remove('hidden');
  S.panelDismissed = false;
  $('jp-size').textContent = fmtBytes(text.length) + ' · ' + text.length + ' ký tự';
  setSegState($('jp-seg'), currentJsonView(text));
  renderJsonInto($('jp-body'), text);
}

// click vào đoạn JSON → popup modal
function openJsonModal(text) {
  S.currentJson = text;
  $('jmodal').classList.remove('hidden');
  $('jm-size').textContent = fmtBytes(text.length) + ' · ' + text.length + ' ký tự';
  setSegState($('jm-seg'), currentJsonView(text));
  renderJsonInto($('jm-body'), text);
}

function closeJsonModal() { $('jmodal').classList.add('hidden'); }

function togglePin(el) {
  if (S.pinned && S.pinned.el === el) { unpin(); return; }
  unpin();
  S.pinned = { el };
  el.classList.add('jch-pinned');
  showJson(el.dataset.json, el);
}

function unpin() {
  if (S.pinned) { S.pinned.el.classList.remove('jch-pinned'); S.pinned = null; }
}

// ---------- session / mode ----------
function setMode(mode) {
  if (S.mode === mode) return;
  S.mode = mode;
  if (mode === 'all') $('filter-bar').classList.add('hidden');
  else if (S.filterLabel) $('filter-bar').classList.remove('hidden');
  resetViewData();
}

function applyWrap() {
  const vp = $('viewport');
  vp.classList.toggle('nowrap', !S.wrap);
  vp.classList.toggle('wrap', S.wrap);
  $('btn-wrap').innerHTML = '↵ Ngắt dòng: <b>' + (S.wrap ? 'BẬT' : 'TẮT') + '</b>';
  localStorage.setItem('lv-wrap', S.wrap ? '1' : '0');
  resetViewData(true);
}

function applyTz() {
  $('btn-tz').innerHTML = S.localTime ? '🌍 Giờ máy' : '🌐 UTC';
  localStorage.setItem('lv-tz', S.localTime ? '1' : '0');
  for (const [, blk] of S.blocks) blk.filled = false;
  resetViewData(true);
}

function updateSessInfo() {
  const s = S.session;
  if (!s) { $('sess-info').textContent = ''; return; }
  const dates = [...new Set((s.files || []).map((f) => f.date))].filter((d) => d !== 'imported');
  $('sess-info').textContent =
    `${s.total.toLocaleString('vi-VN')} dòng log · ${s.fileCount} file (${fmtBytes(s.totalBytes)})` +
    (dates.length ? ` · ${dates[0]}${dates.length > 1 ? ' → ' + dates[dates.length - 1] : ''}` : ' · file import local');
}

async function openSession(id) {
  const info = await api(`/api/session/${id}/info`);
  S.session = info;
  S.mode = 'all';
  S.filterEntries = null;
  S.level = '';
  S.q = '';
  $('search').value = '';
  document.querySelectorAll('#level-chips button').forEach((b) => b.classList.toggle('on', b.dataset.level === ''));
  $('viewer').classList.remove('hidden');
  $('picker').classList.add('hidden');
  $('filter-bar').classList.add('hidden');
  updateSessInfo();
  resetViewData();
  location.hash = 'session=' + id;
}

function backToPicker() {
  $('viewer').classList.add('hidden');
  $('picker').classList.remove('hidden');
  location.hash = '';
}

// ---------- scan & load ----------
async function doScan() {
  const from = $('d-from').value, to = $('d-to').value;
  if (!from || !to) { toast('Chọn ngày Từ / Đến trước'); return; }
  const summary = $('scan-summary');
  summary.textContent = 'Đang quét bucket…';
  try {
    const body = { from, to, fromHour: S.scanFromHour, toHour: S.scanToHour };
    const r = await api('/api/scan', { method: 'POST', body: JSON.stringify(body) });
    S.scanFiles = r.files;
    const cached = r.files.filter((f) => f.cached).length;
    summary.innerHTML = r.count
      ? `Tìm thấy <b>${r.count}</b> file log · tổng <b>${fmtBytes(r.totalSize)}</b>` +
        (cached ? ` · <b>${cached}</b> file đã có trong cache (miễn tải lại)` : '') +
        ' — bấm <b>⬇ Tải &amp; mở logs</b>'
      : 'Không có file log nào trong khoảng này.';
    $('btn-load').disabled = r.count === 0;
  } catch (err) {
    summary.textContent = '';
    toast('Quét lỗi: ' + err.message, true);
  }
}

function setQuick(q) {
  const now = new Date();
  const localDate = (x) => `${x.getFullYear()}-${p2(x.getMonth() + 1)}-${p2(x.getDate())}`;
  S.scanFromHour = null;
  S.scanToHour = null;
  if (q === '24h') {
    const s = new Date(now.getTime() - 86400000);
    $('d-from').value = localDate(s);
    $('d-to').value = localDate(now);
    S.scanFromHour = s.getHours();
    S.scanToHour = now.getHours();
  } else if (q === '7d') {
    $('d-from').value = localDate(new Date(now.getTime() - 6 * 86400000));
    $('d-to').value = localDate(now);
  } else {
    $('d-from').value = localDate(new Date(now.getTime() - 29 * 86400000));
    $('d-to').value = localDate(now);
  }
  doScan();
}

async function doLoad() {
  if (!S.scanFiles.length) return;
  const body = { files: S.scanFiles.map((f) => ({ key: f.key, date: f.date, hour: f.hour, size: f.size })) };
  try {
    const r = await api('/api/load', { method: 'POST', body: JSON.stringify(body) });
    pollJob(r.jobId);
  } catch (err) {
    toast('Tải lỗi: ' + err.message, true);
  }
}

function pollJob(id) {
  S.currentJobId = id;
  $('progress').classList.remove('hidden');
  $('pbar-errors').classList.add('hidden');
  clearInterval(S.jobTimer);
  S.jobTimer = setInterval(async () => {
    let j;
    try { j = await api('/api/job/' + id); } catch (_) { return; }
    const pct = j.bytesTotal > 0 ? Math.round((j.bytesDone / j.bytesTotal) * 100) : (j.done / Math.max(1, j.total)) * 100;
    $('pbar-fill').style.width = Math.min(100, pct) + '%';
    const cur = j.current ? j.current.split('/').pop() : '';
    $('pbar-text').textContent =
      (j.phase === 'index' ? '📝 Đang đánh chỉ mục…' : `⬇ Đang tải & giải nén ${j.done}/${j.total}`) +
      ` · ${fmtBytes(j.bytesDone)}/${fmtBytes(j.bytesTotal)}` + (cur ? ` · ${cur}` : '');
    if (j.errors && j.errors.length) {
      $('pbar-errors').classList.remove('hidden');
      $('pbar-error-list').textContent = j.errors.join('\n');
    }
    if (j.status !== 'running') {
      clearInterval(S.jobTimer);
      if (j.status === 'done') {
        $('pbar-text').textContent = '✅ Xong!';
        setTimeout(() => $('progress').classList.add('hidden'), 800);
        openSession(j.sessionId).catch((e) => toast(e.message, true));
      } else if (j.status === 'canceled') {
        toast('Đã hủy tải');
        $('progress').classList.add('hidden');
      } else {
        toast('Tải gặp lỗi — xem chi tiết bên dưới', true);
      }
    }
  }, 700);
}

// ---------- search / filter ----------
async function doSearch() {
  if (!S.session) return;
  const q = $('search').value.trim();
  const level = S.level;
  if (!q && !level) { clearFilter(); return; }
  const fb = $('filter-bar');
  fb.classList.remove('hidden');
  $('filter-text').textContent = 'Đang tìm…';
  try {
    const r = await api(`/api/session/${S.session.id}/search?q=${encodeURIComponent(q)}&level=${encodeURIComponent(level)}&max=20000`);
    S.mode = 'filter';
    S.filterEntries = r.matches;
    S.q = q;
    S.filterLabel =
      `${r.total.toLocaleString('vi-VN')} kết quả` +
      (q ? ` cho "${q}"` : '') + (level ? ` · level=${level}` : '') +
      (r.truncated ? ' — chỉ hiển thị 20.000 dòng đầu (thu hẹp tìm kiếm để xem đủ)' : '');
    $('filter-text').textContent = S.filterLabel;
    resetViewData();
  } catch (err) {
    $('filter-text').textContent = '';
    toast('Tìm kiếm lỗi: ' + err.message, true);
  }
}

function clearFilter() {
  S.mode = 'all';
  S.filterEntries = null;
  S.level = '';
  S.q = '';
  document.querySelectorAll('#level-chips button').forEach((b) => b.classList.toggle('on', b.dataset.level === ''));
  $('filter-bar').classList.add('hidden');
  $('search').value = '';
  resetViewData();
}

// ---------- cache modal ----------
async function openCache() {
  $('modal-cache').classList.remove('hidden');
  await refreshCache();
}

async function refreshCache() {
  const st = await api('/api/cache');
  $('cache-summary').innerHTML =
    `Tổng dung lượng: <b>${fmtBytes(st.totalBytes)}</b> · thư mục: <code>${esc(st.root)}</code>` +
    (st.archBytes > 0 ? ` · file .tar.gz giữ lại: ${fmtBytes(st.archBytes)}` : '') +
    `<br>Tự dọn cache cũ hơn <b>${S.cfg.autoCleanDays || 0}</b> ngày khi mở app${S.cfg.autoCleanDays ? '' : ' (đang tắt)'}`;
  const rows = [...(st.extracted || []), ...(st.imported || [])];
  let h = '';
  if (rows.length) {
    h = '<table><tr><th>Ngày</th><th class="num">File</th><th class="num">Dung lượng</th><th></th></tr>';
    for (const d of rows) {
      h += `<tr><td>${esc(d.date)}</td><td class="num">${d.files}</td><td class="num">${fmtBytes(d.bytes)}</td>` +
        `<td><button class="small danger" data-deldate="${esc(d.date)}">Xóa</button></td></tr>`;
    }
    h += '</table>';
  } else {
    h = '<p class="muted">Cache trống — chưa có gì được tải về.</p>';
  }
  $('cache-table').innerHTML = h;
  $('cache-table').querySelectorAll('[data-deldate]').forEach((btn) => {
    btn.onclick = async () => {
      await api('/api/cache/delete', { method: 'POST', body: JSON.stringify({ date: btn.dataset.deldate }) });
      toast('Đã xóa cache ngày ' + btn.dataset.deldate);
      refreshCache();
    };
  });
}

// ---------- settings ----------
async function openSettings() {
  const st = await api('/api/state');
  S.cfg = st.config;
  $('st-url').value = S.cfg.consoleUrl || '';
  $('st-login').value = S.cfg.loginUrl || '';
  $('st-user').value = S.cfg.username || '';
  $('st-pass').value = '';
  $('st-bucket').value = S.cfg.bucket || '';
  $('st-cachedir').value = S.cfg.cacheDir || '';
  $('st-keep').checked = !!S.cfg.keepArchives;
  $('st-cleandays').value = S.cfg.autoCleanDays != null ? S.cfg.autoCleanDays : 14;
  $('st-msg').textContent = '';
  $('st-envpath').textContent = 'File cấu hình: ' + (S.cfg.envPath || '.env');
  $('modal-settings').classList.remove('hidden');
}

async function saveSettings() {
  const msg = $('st-msg');
  msg.className = 'msg';
  msg.textContent = 'Đang lưu…';
  try {
    const cfgBody = {
      consoleUrl: $('st-url').value.trim(),
      loginUrl: $('st-login').value.trim(),
      username: $('st-user').value.trim(),
      password: $('st-pass').value.trim(),
      bucket: $('st-bucket').value.trim(),
    };
    if (!cfgBody.password) delete cfgBody.password;
    const r = await api('/api/config', { method: 'POST', body: JSON.stringify(cfgBody) });
    S.cfg = r.config;
    fillBucketDatalist('st-buckets', r.buckets || []);
    updateBucketBadge();
    await api('/api/settings', {
      method: 'POST',
      body: JSON.stringify({
        cacheDir: $('st-cachedir').value.trim(),
        keepArchives: $('st-keep').checked ? 'true' : 'false',
        autoCleanDays: $('st-cleandays').value.trim() || '0',
      }),
    });
    msg.className = 'msg ok';
    msg.textContent = 'Đã lưu!';
    const st = await api('/api/state');
    S.cfg = st.config;
    updateBucketBadge();
    setTimeout(() => { $('modal-settings').classList.add('hidden'); }, 700);
  } catch (err) {
    msg.textContent = err.message;
  }
}

async function testConn() {
  const msg = $('st-msg');
  msg.className = 'msg';
  msg.textContent = 'Đang đăng nhập vào MinIO Console…';
  try {
    // lưu tạm các giá trị trên form để backend dùng khi test
    const cfgBody = {
      consoleUrl: $('st-url').value.trim(),
      loginUrl: $('st-login').value.trim(),
      username: $('st-user').value.trim(),
      password: $('st-pass').value.trim(),
      bucket: $('st-bucket').value.trim(),
    };
    if (!cfgBody.password) delete cfgBody.password;
    if (cfgBody.consoleUrl || cfgBody.loginUrl) {
      await api('/api/config', { method: 'POST', body: JSON.stringify(cfgBody) });
    }
    const r = await api('/api/test', { method: 'POST' });
    fillBucketDatalist('st-buckets', r.buckets || []);
    if (!r.bucket && r.buckets && r.buckets.length === 1) $('st-bucket').value = r.buckets[0];
    msg.className = 'msg ok';
    let m;
    if (r.mode === 'console') {
      m = `OK! Kết nối qua MinIO Console: ${r.endpoint} (S3 API không public)`;
    } else {
      m = `OK! S3 API: ${r.endpoint}`;
    }
    if (r.buckets) m += ` · ${r.buckets.length} bucket`;
    if (r.dates != null) m += ` · bucket có ${r.dates} ngày`;
    msg.textContent = m;
  } catch (err) {
    msg.textContent = err.message;
  }
}

// ---------- bucket helpers ----------
function parseBucketFromUrl(u) {
  if (!u) return '';
  let m = u.match(/\/browser\/([^\/?#]+)/i);
  if (m) return decodeURIComponent(m[1]);
  m = u.match(/\/buckets\/([^\/?#]+)/i);
  if (m) return decodeURIComponent(m[1]);
  return '';
}

function fillBucketDatalist(id, buckets) {
  const dl = $(id);
  if (!dl) return;
  dl.innerHTML = '';
  for (const b of buckets || []) {
    const o = document.createElement('option');
    o.value = b;
    dl.appendChild(o);
  }
}

// sau khi lưu cấu hình thành công: gợi ý bucket nếu chưa chọn
async function afterConfigSaved(r) {
  const buckets = r.buckets || [];
  fillBucketDatalist('su-buckets', buckets);
  fillBucketDatalist('st-buckets', buckets);
  updateBucketBadge();
  if (!S.cfg.bucket && buckets.length === 1) {
    try {
      const r2 = await api('/api/config', { method: 'POST', body: JSON.stringify({ bucket: buckets[0] }) });
      S.cfg = r2.config;
      updateBucketBadge();
      toast('Đã chọn bucket: ' + buckets[0]);
    } catch (_) { /* bỏ qua */ }
  } else if (!S.cfg.bucket && buckets.length > 1) {
    toast('Đăng nhập OK — còn ' + buckets.length + ' bucket, hãy chọn 1 bucket trong Cài đặt');
    openSettings();
  }
}

function updateBucketBadge() {
  if (S.cfg.bucket) { $('bucket-badge').textContent = S.cfg.bucket; $('bucket-badge').classList.remove('hidden'); }
  else { $('bucket-badge').classList.add('hidden'); }
}

// ---------- setup screen ----------
async function saveSetup() {
  const msg = $('su-msg');
  msg.className = 'msg';
  msg.textContent = 'Đang lưu & đăng nhập…';
  try {
    const r = await api('/api/config', {
      method: 'POST',
      body: JSON.stringify({
        consoleUrl: $('su-url').value.trim(),
        loginUrl: $('su-login').value.trim(),
        username: $('su-user').value.trim(),
        password: $('su-pass').value.trim(),
        bucket: $('su-bucket').value.trim(),
      }),
    });
    msg.className = 'msg ok';
    msg.textContent = 'Đăng nhập OK!';
    S.cfg = r.config;
    if (r.mode === 'console') toast('Kết nối qua MinIO Console (S3 API không public)');
    await afterConfigSaved(r);
    setTimeout(() => $('setup').classList.add('hidden'), 500);
  } catch (err) {
    msg.textContent = err.message;
  }
}

async function copyJson() {
  try { await navigator.clipboard.writeText(S.currentJson); toast('Đã copy JSON'); }
  catch (_) { toast('Không copy được', true); }
}

// ---------- events ----------
function wire() {
  // picker
  document.querySelectorAll('.quick').forEach((b) => b.addEventListener('click', () => setQuick(b.dataset.q)));
  $('btn-scan').addEventListener('click', doScan);
  $('btn-load').addEventListener('click', doLoad);
  $('btn-cancel').addEventListener('click', () => {
    if (S.currentJobId) api(`/api/job/${S.currentJobId}/cancel`, { method: 'POST' }).catch(() => {});
  });

  // viewer toolbar
  $('btn-back').addEventListener('click', backToPicker);
  $('btn-end').addEventListener('click', () => gotoPos(total() - 1));
  $('btn-wrap').addEventListener('click', () => { S.wrap = !S.wrap; applyWrap(); });
  $('btn-tz').addEventListener('click', () => { S.localTime = !S.localTime; applyTz(); });
  $('btn-clear-filter').addEventListener('click', clearFilter);
  $('search').addEventListener('keydown', (e) => { if (e.key === 'Enter') doSearch(); });
  document.querySelectorAll('#level-chips button').forEach((b) => {
    b.addEventListener('click', () => {
      S.level = b.dataset.level;
      document.querySelectorAll('#level-chips button').forEach((x) => x.classList.toggle('on', x === b));
      if (!S.session) return;
      if (!S.level && !$('search').value.trim()) { clearFilter(); return; }
      doSearch();
    });
  });

  // viewport
  const vp = $('viewport');
  vp.addEventListener('scroll', () => { syncLogHead(); scheduleRender(); });
  window.addEventListener('resize', scheduleRender);
  vp.addEventListener('mouseover', (e) => {
    const ch = e.target.closest && e.target.closest('.jch');
    if (ch) hoverJson(ch.dataset.json, ch);
  });
  vp.addEventListener('click', async (e) => {
    const jch = e.target.closest && e.target.closest('.jch');
    if (jch) {
      // click = mở popup JSON (chủ động, không tự bật/tắt theo chuột)
      openJsonModal(jch.dataset.json);
      return;
    }
    const ln = e.target.closest && e.target.closest('.c-ln');
    if (ln) {
      const entry = ln.closest('.entry');
      const pos = +entry.dataset.pos;
      const eData = entryAt(pos);
      const raw = eData ? (eData.raw || eData.m) : '';
      try { await navigator.clipboard.writeText(raw); toast('Đã copy dòng #' + (origIdx(pos) + 1)); }
      catch (_) { toast('Không copy được (trình duyệt chặn clipboard)', true); }
      return;
    }
    const entry = e.target.closest && e.target.closest('.entry');
    if (!entry) return;
    if (S.mode === 'filter') {
      gotoPos(+entry.dataset.orig);
      return;
    }
    // chọn dòng để soi (không làm gì khác)
    document.querySelectorAll('#viewport .entry.sel').forEach((x) => x.classList.remove('sel'));
    entry.classList.add('sel');
  });

  // json panel
  $('jp-close').addEventListener('click', () => {
    unpin();
    $('json-panel').classList.add('hidden');
    S.panelDismissed = true; // tắt rồi thì hover không tự bật lại
  });
  $('jp-pin').addEventListener('click', () => { unpin(); toast('Đã bỏ ghim'); });
  $('jp-copy').addEventListener('click', copyJson);
  $('jp-seg').querySelectorAll('button').forEach((b) => {
    b.addEventListener('click', () => {
      S.jview = b.dataset.v;
      localStorage.setItem('lv-jview', S.jview);
      setSegState($('jp-seg'), S.jview);
      renderJsonInto($('jp-body'), S.currentJson);
    });
  });

  // popup JSON
  $('jm-close').addEventListener('click', closeJsonModal);
  $('jm-copy').addEventListener('click', copyJson);
  $('jm-seg').querySelectorAll('button').forEach((b) => {
    b.addEventListener('click', () => {
      S.jview = b.dataset.v;
      localStorage.setItem('lv-jview', S.jview);
      setSegState($('jm-seg'), S.jview);
      setSegState($('jp-seg'), S.jview);
      renderJsonInto($('jm-body'), S.currentJson);
      renderJsonInto($('jp-body'), S.currentJson);
    });
  });
  $('jmodal').addEventListener('click', (e) => { if (e.target === $('jmodal')) closeJsonModal(); });

  // bật/tắt khung JSON
  $('btn-json').addEventListener('click', () => {
    const panel = $('json-panel');
    if (panel.classList.contains('hidden')) {
      panel.classList.remove('hidden');
      S.panelDismissed = false;
      if (S.currentJson) renderJsonInto($('jp-body'), S.currentJson);
    } else {
      panel.classList.add('hidden');
      S.panelDismissed = true;
    }
  });

  // nhảy tới dòng
  $('jump-line').addEventListener('keydown', (e) => {
    if (e.key !== 'Enter') return;
    const n = parseInt(e.target.value, 10);
    if (n >= 1 && S.session) gotoPos(n - 1);
    else if (n >= 1) toast('Chưa mở logs');
    e.target.blur();
  });

  // header
  $('btn-settings').addEventListener('click', openSettings);
  $('btn-cache').addEventListener('click', openCache);
  $('btn-import').addEventListener('click', () => $('file-input').click());
  $('file-input').addEventListener('change', async () => {
    const f = $('file-input').files[0];
    if (!f) return;
    toast('Đang giải nén ' + f.name + '…');
    const fd = new FormData();
    fd.append('file', f);
    try {
      const r = await fetch('/api/import-upload', { method: 'POST', body: fd });
      const j = await r.json();
      if (!r.ok) throw new Error(j.error || 'HTTP ' + r.status);
      await openSession(j.sessionId);
      toast('Đã mở ' + f.name);
    } catch (err) {
      toast('Mở file lỗi: ' + err.message, true);
    }
    $('file-input').value = '';
  });

  // modals
  document.querySelectorAll('[data-close]').forEach((b) =>
    b.addEventListener('click', () => $(b.dataset.close).classList.add('hidden')));
  document.querySelectorAll('.overlay').forEach((o) =>
    o.addEventListener('click', (e) => { if (e.target === o) o.classList.add('hidden'); }));
  $('st-save').addEventListener('click', saveSettings);
  $('st-test').addEventListener('click', testConn);
  $('su-save').addEventListener('click', saveSetup);

  // nút lấy danh sách bucket (dùng thông tin đang nhập trên form)
  $('st-buckets-btn').addEventListener('click', async () => {
    const btn = $('st-buckets-btn');
    btn.disabled = true;
    try {
      const cfgBody = {
        consoleUrl: $('st-url').value.trim(),
        loginUrl: $('st-login').value.trim(),
        username: $('st-user').value.trim(),
        password: $('st-pass').value.trim(),
      };
      if (!cfgBody.password) delete cfgBody.password;
      if (cfgBody.consoleUrl || cfgBody.loginUrl) {
        const r0 = await api('/api/config', { method: 'POST', body: JSON.stringify(cfgBody) });
        S.cfg = r0.config;
      }
      const r = await api('/api/buckets');
      fillBucketDatalist('st-buckets', r.buckets || []);
      if (!r.current && r.buckets && r.buckets.length) $('st-bucket').value = r.buckets[0];
      toast(`Có ${r.buckets.length} bucket — chọn 1 trong ô Bucket`);
    } catch (err) {
      toast(err.message, true);
    }
    btn.disabled = false;
  });

  // nút mở dialog chọn thư mục cache của máy
  $('st-pick').addEventListener('click', async () => {
    const btn = $('st-pick');
    btn.disabled = true;
    btn.textContent = '⏳ Đang mở…';
    try {
      const r = await api('/api/pick-folder', { method: 'POST' });
      if (r.path) $('st-cachedir').value = r.path;
    } catch (err) {
      toast(err.message, true);
    }
    btn.disabled = false;
    btn.textContent = '📂 Chọn thư mục...';
  });

  // dán link /browser/<bucket> vào ô URL → tự điền bucket
  for (const [urlId, bucketId] of [['su-url', 'su-bucket'], ['st-url', 'st-bucket']]) {
    $(urlId).addEventListener('change', () => {
      const b = parseBucketFromUrl($(urlId).value);
      if (b && !$(bucketId).value) $(bucketId).value = b;
    });
  }
  $('cache-open').addEventListener('click', () => api('/api/cache/open', { method: 'POST' }).catch((e) => toast(e.message, true)));
  $('cache-clear').addEventListener('click', async () => {
    if (!confirm('Xóa TOÀN BỘ cache log đã tải? (file gốc trên MinIO không bị ảnh hưởng)')) return;
    await api('/api/cache/delete', { method: 'POST', body: JSON.stringify({ all: true }) });
    toast('Đã xóa toàn bộ cache');
    refreshCache();
  });

  // keyboard
  window.addEventListener('keydown', (e) => {
    if (e.altKey && e.code === 'KeyZ') { e.preventDefault(); S.wrap = !S.wrap; applyWrap(); }
    if ((e.ctrlKey || e.metaKey) && e.code === 'KeyF' && !$('viewer').classList.contains('hidden')) {
      e.preventDefault();
      $('search').focus();
      $('search').select();
    }
    if (e.key === 'Escape') {
      document.querySelectorAll('.overlay:not(#setup)').forEach((o) => o.classList.add('hidden'));
      unpin();
    }
  });
}

// ---------- boot ----------
async function boot() {
  S.wrap = localStorage.getItem('lv-wrap') !== '0';
  S.localTime = localStorage.getItem('lv-tz') !== '0';
  const jv = localStorage.getItem('lv-jview');
  S.jview = (jv === 'table' || jv === 'json') ? jv : null;
  applyWrap();
  applyTz();
  initColumns();
  wire();

  const today = new Date();
  const ld = `${today.getFullYear()}-${p2(today.getMonth() + 1)}-${p2(today.getDate())}`;
  $('d-from').value = ld;
  $('d-to').value = ld;

  try {
    const st = await api('/api/state');
    S.cfg = st.config;
    updateBucketBadge();
    // điền sẵn form setup
    $('su-url').value = S.cfg.consoleUrl || '';
    $('su-login').value = S.cfg.loginUrl || '';
    $('su-user').value = S.cfg.username || '';
    $('su-bucket').value = S.cfg.bucket || '';
  } catch (err) {
    toast('Không nói chuyện được với server: ' + err.message, true);
  }

  // mở lại session qua #session=... (dùng khi debug/test)
  const m = location.hash.match(/session=([\w-]+)/);
  if (m) {
    openSession(m[1]).catch(() => { toast('Session cũ đã hết hạn', true); location.hash = ''; });
    return; // đang mở session thì không hiện màn hình setup đè lên
  }

  if (S.cfg) {
    if (!S.cfg.consoleUrl && !S.cfg.loginUrl && !S.cfg.username) {
      $('setup').classList.remove('hidden'); // chưa có cấu hình nào
    } else if (!S.cfg.bucket) {
      // đã đăng nhập được nhưng chưa chọn bucket
      openSettings().catch(() => {});
      toast('Hãy chọn bucket chứa log');
    }
  }
}

boot();
