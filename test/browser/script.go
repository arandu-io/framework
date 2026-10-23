package browser

// pageScript is installed in every document before the document's own
// scripts. It is the page's half of the kit, and it is the same for every
// driver: what a driver adds is a way to run it.
//
// It keeps three things:
//
//   - the htmx requests in flight, from htmx:beforeRequest until the request
//     settled (htmx:afterSettle) or ended with nothing to swap;
//   - a document id, so a wait can tell a new document from the old one;
//   - the element the last lookup found, so the action that follows acts on
//     exactly that element.
//
// A Content-Security-Policy violation is reported through console.error with
// a fixed prefix, which reaches the test through the console events every
// driver already delivers.
const pageScript = `(() => {
if (window.__arandu) return;
const norm = s => String(s == null ? '' : s).replace(/\s+/g, ' ').trim();
const requests = new Map();
const st = { doc: Math.random().toString(36).slice(2) + Date.now().toString(36), started: 0, settled: 0, resumed: false, target: null, control: null, filled: null, leaving: 0 };
window.addEventListener("beforeunload", () => { st.leaving = Date.now(); });
const on = (name, fn) => document.addEventListener(name, fn, true);
const xhrOf = e => e && e.detail && e.detail.xhr;
on('htmx:beforeRequest', e => { st.started++; const x = xhrOf(e); if (x) requests.set(x, { phase: 'request', at: Date.now() }); });
on('htmx:afterSwap', e => { const x = xhrOf(e); if (x && requests.has(x)) requests.set(x, { phase: 'swapped', at: Date.now() }); });
on('htmx:afterRequest', e => {
  const x = xhrOf(e); if (!x || !requests.has(x)) return;
  const t = e.detail.target;
  if (requests.get(x).phase === 'swapped' || (t && t.classList && t.classList.contains('htmx-swapping'))) requests.set(x, { phase: 'settling', at: Date.now() });
  else requests.delete(x);
});
on('htmx:afterSettle', e => { st.settled++; const x = xhrOf(e); if (x) requests.delete(x); });
document.addEventListener('securitypolicyviolation', e => {
  console.error('` + cspPrefix + `' + (e.blockedURI || 'inline') + ' (' + (e.effectiveDirective || e.violatedDirective) + ')');
}, true);

const busySelector = '.htmx-request,.htmx-swapping,.htmx-settling';
const htmxQuiet = () => {
  const busy = !!document.querySelector(busySelector);
  for (const [x, r] of requests) {
    // A swap whose settle never reached the document (its elements were
    // replaced before they settled) is done once nothing is settling.
    if (r.phase === 'settling' && !busy && Date.now() - r.at > 1000) requests.delete(x);
  }
  return requests.size === 0 && !busy;
};
const finite = a => { try { return a.effect && a.effect.getComputedTiming().iterations !== Infinity; } catch (e) { return true; } };
const running = list => list.some(a => a.playState === 'running' && finite(a));
const animating = () => document.getAnimations ? running(document.getAnimations()) : false;

const visible = el => {
  if (!el || !el.isConnected) return false;
  const r = el.getBoundingClientRect();
  if (r.width <= 0 || r.height <= 0) return false;
  if (el.checkVisibility) return el.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true });
  const s = getComputedStyle(el);
  return s.display !== 'none' && s.visibility !== 'hidden' && parseFloat(s.opacity) > 0;
};

const implicitRole = el => {
  const t = el.tagName.toLowerCase();
  const type = (el.getAttribute('type') || '').toLowerCase();
  switch (t) {
    case 'a': case 'area': return el.hasAttribute('href') ? 'link' : null;
    case 'button': case 'summary': return 'button';
    case 'input':
      if (['button', 'submit', 'reset', 'image'].includes(type)) return 'button';
      if (type === 'checkbox') return el.hasAttribute('switch') ? 'switch' : 'checkbox';
      if (type === 'radio') return 'radio';
      if (type === 'range') return 'slider';
      if (type === 'number') return 'spinbutton';
      if (type === 'search') return el.hasAttribute('list') ? 'combobox' : 'searchbox';
      if (['', 'text', 'email', 'tel', 'url', 'password'].includes(type)) return el.hasAttribute('list') ? 'combobox' : 'textbox';
      return null;
    case 'select': return (el.multiple || el.size > 1) ? 'listbox' : 'combobox';
    case 'textarea': return 'textbox';
    case 'h1': case 'h2': case 'h3': case 'h4': case 'h5': case 'h6': return 'heading';
    case 'dialog': return 'dialog';
    case 'ul': case 'ol': case 'menu': return 'list';
    case 'li': return 'listitem';
    case 'nav': return 'navigation';
    case 'main': return 'main';
    case 'aside': return 'complementary';
    case 'header': return el.closest('article,aside,main,nav,section') ? null : 'banner';
    case 'footer': return el.closest('article,aside,main,nav,section') ? null : 'contentinfo';
    case 'form': return 'form';
    case 'table': return 'table';
    case 'tr': return 'row';
    case 'td': return 'cell';
    case 'th': return el.getAttribute('scope') === 'row' ? 'rowheader' : 'columnheader';
    case 'img': return el.getAttribute('alt') === '' ? 'presentation' : 'img';
    case 'option': return 'option';
    case 'section': return 'region';
    case 'article': return 'article';
    case 'fieldset': return 'group';
    case 'progress': return 'progressbar';
    case 'hr': return 'separator';
    case 'output': return 'status';
  }
  if (el.isContentEditable && !(el.parentElement && el.parentElement.isContentEditable)) return 'textbox';
  return null;
};
const roleOf = el => { const r = el.getAttribute('role'); return r ? r.trim().split(/\s+/)[0] : implicitRole(el); };

const hiddenForName = el => el.getAttribute('aria-hidden') === 'true' || el.hidden || getComputedStyle(el).display === 'none';
const textOf = (node, skip) => {
  if (node === skip) return '';
  if (node.nodeType === 3) return node.nodeValue;
  if (node.nodeType !== 1) return '';
  if (hiddenForName(node)) return '';
  const t = node.tagName.toLowerCase();
  if (t === 'script' || t === 'style' || t === 'template') return '';
  if (t === 'img') return ' ' + (node.getAttribute('alt') || '') + ' ';
  if (t === 'input' && ['button', 'submit', 'reset'].includes((node.type || '').toLowerCase())) return ' ' + node.value + ' ';
  let s = '';
  for (const c of node.childNodes) s += textOf(c, skip);
  return getComputedStyle(node).display.startsWith('inline') ? s : ' ' + s + ' ';
};
const byIds = ids => ids.split(/\s+/).map(id => document.getElementById(id)).filter(Boolean).map(e => textOf(e)).join(' ');
const isControl = el => ['input', 'select', 'textarea', 'button', 'meter', 'output', 'progress'].includes(el.tagName.toLowerCase());
const labelName = el => {
  const lb = el.getAttribute('aria-labelledby');
  if (lb) { const s = norm(byIds(lb)); if (s) return s; }
  const al = norm(el.getAttribute('aria-label'));
  if (al) return al;
  if (el.labels && el.labels.length) return norm(Array.from(el.labels).map(l => textOf(l, el)).join(' '));
  return '';
};
const contentRoles = new Set(['button', 'link', 'heading', 'cell', 'columnheader', 'rowheader', 'option', 'listitem', 'tab', 'menuitem', 'menuitemcheckbox', 'menuitemradio', 'checkbox', 'radio', 'switch', 'tooltip', 'treeitem', 'row', 'gridcell']);
const nameOf = el => {
  const t = el.tagName.toLowerCase();
  const label = labelName(el);
  if (label) return label;
  if (t === 'input') {
    const type = (el.type || '').toLowerCase();
    if (['button', 'submit', 'reset'].includes(type)) return norm(el.value || (type === 'submit' ? 'Submit' : type === 'reset' ? 'Reset' : ''));
    if (type === 'image') return norm(el.getAttribute('alt'));
  }
  if (isControl(el) && t !== 'button') { const ph = norm(el.getAttribute('placeholder')); if (ph) return ph; }
  if (t === 'img' || t === 'area') { const a = norm(el.getAttribute('alt')); if (a) return a; }
  if (t === 'fieldset') { const lg = el.querySelector(':scope > legend'); if (lg) return norm(textOf(lg)); }
  if (t === 'table') { const c = el.querySelector(':scope > caption'); if (c) return norm(textOf(c)); }
  const role = roleOf(el);
  if (role && contentRoles.has(role)) { const s = norm(textOf(el)); if (s) return s; }
  if (role === 'dialog' || role === 'alertdialog') { const h = el.querySelector('h1,h2,h3,h4,h5,h6,[role=heading]'); if (h) return norm(textOf(h)); }
  return norm(el.getAttribute('title'));
};
const sameName = (have, want) => have === want || have.replace(/\s*\*$/, '') === want;

const describeLoc = l => {
  const own = l.kind === 'role' ? 'role=' + l.a + (l.b ? ' name="' + l.b + '"' : '')
    : l.kind === 'modal' ? 'modal' + (l.a ? ' name="' + l.a + '"' : '')
    : l.kind + '="' + l.a + '"';
  return l.parent ? describeLoc(l.parent) + ' >> ' + own : own;
};
const describe = el => {
  if (!el) return 'nothing';
  if (el.nodeType !== 1) return el.nodeName;
  let s = el.tagName.toLowerCase();
  if (el.id) s += '#' + el.id;
  if (typeof el.className === 'string' && el.className.trim()) s += '.' + el.className.trim().split(/\s+/).slice(0, 2).join('.');
  const role = roleOf(el); if (role) s += '[role=' + role + ']';
  const name = nameOf(el); if (name) s += ' "' + (name.length > 40 ? name.slice(0, 40) + '…' : name) + '"';
  return s;
};

const controlSelector = 'input:not([type=hidden]),select,textarea,[contenteditable=""],[contenteditable=true],[role=textbox],[role=combobox],[role=checkbox],[role=radio],[role=switch],[role=slider],[role=spinbutton],[role=searchbox],[role=listbox]';
const within = (root, sel) => Array.from(root.querySelectorAll(sel));
const deepest = els => els.filter(el => !els.some(o => o !== el && el.contains(o)));
const resolve = l => {
  let root = document;
  if (l.parent) {
    const p = resolve(l.parent);
    if (p.error) return p;
    const vis = p.els.filter(visible);
    if (vis.length !== 1) return { error: (vis.length === 0 ? 'no visible element matches ' : vis.length + ' visible elements match ') + describeLoc(l.parent) };
    root = vis[0];
  }
  const all = root === document ? within(document, '*') : [root, ...within(root, '*')];
  let els;
  switch (l.kind) {
    case 'role': els = all.filter(el => roleOf(el) === l.a && (!l.b || sameName(nameOf(el), l.b))); break;
    case 'modal': els = all.filter(el => { const r = roleOf(el); return (r === 'dialog' || r === 'alertdialog') && (el.matches(':modal') || el.getAttribute('aria-modal') === 'true') && (!l.a || sameName(nameOf(el), l.a)); }); break;
    case 'label': els = within(root === document ? document : root, controlSelector).filter(el => sameName(labelName(el), l.a)); break;
    case 'text': els = deepest(all.filter(el => el !== document.documentElement && el !== document.body && el.tagName !== 'SCRIPT' && el.tagName !== 'STYLE' && (norm(el.innerText) === l.a || (el.tagName === 'INPUT' && ['button', 'submit', 'reset'].includes(el.type) && norm(el.value) === l.a)))); break;
    case 'testid': els = all.filter(el => el.getAttribute('data-testid') === l.a); break;
    case 'css': try { els = within(root === document ? document : root, l.a); } catch (e) { return { error: 'invalid selector ' + l.a + ': ' + e.message }; } break;
    default: return { error: 'unknown locator ' + l.kind };
  }
  return { els };
};

const frames = () => Promise.race([
  new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))),
  new Promise(r => setTimeout(r, 100)),
]);
const rectOf = el => { const r = el.getBoundingClientRect(); return [r.x, r.y, r.width, r.height].map(v => Math.round(v * 10) / 10); };
const disabled = el => el.disabled === true || !!el.closest('fieldset[disabled]') || el.getAttribute('aria-disabled') === 'true';
const pointOf = el => {
  const rects = el.getClientRects();
  const r = rects.length ? rects[0] : el.getBoundingClientRect();
  const x = Math.min(Math.max(r.x + r.width / 2, 0), innerWidth - 1);
  const y = Math.min(Math.max(r.y + r.height / 2, 0), innerHeight - 1);
  return { x, y };
};
const reaches = (el, hit) => !!hit && (el === hit || el.contains(hit) || hit.control === el || (hit.closest && hit.closest('label') && hit.closest('label').control === el));

window.__arandu = {
  state() {
    return { doc: st.doc, ready: document.readyState, htmx: htmxQuiet(), animating: animating(), url: location.href, started: st.started, settled: st.settled, leaving: st.leaving ? Date.now() - st.leaving : -1 };
  },
  async find(l, action) {
    const res = resolve(l);
    if (res.error) return { status: 'none', detail: res.error };
    const els = res.els;
    const vis = els.filter(visible);
    if (action === 'hidden') return vis.length === 0 ? { status: 'ok' } : { status: 'visible', detail: describeLoc(l) + ' is visible: ' + describe(vis[0]) };
    if (els.length === 0) return { status: 'none', detail: 'no element matches ' + describeLoc(l) };
    // A styled checkbox or radio hides the native control and shows its
    // label; a person clicks the label, and so does the check.
    let control = vis.length === 1 ? vis[0] : null, el = control;
    if (action === 'check' && vis.length === 0 && els.length === 1 && els[0].labels) {
      const label = Array.from(els[0].labels).find(visible);
      if (label) { control = els[0]; el = label; }
    }
    if (!el && vis.length === 0) return { status: 'hidden', detail: els.length + ' element(s) match ' + describeLoc(l) + ' and none is visible: ' + els.slice(0, 3).map(describe).join(', ') };
    if (vis.length > 1) return { status: 'many', detail: vis.length + ' visible elements match ' + describeLoc(l) + ': ' + vis.slice(0, 5).map(describe).join(', ') };
    st.target = el; st.control = control;
    if (action === 'visible') return { status: 'ok', description: describe(el), text: norm(el.innerText || el.value || '') };
    if (disabled(control)) return { status: 'disabled', detail: describe(control) + ' is disabled' };
    if (action === 'fill' && (el.readOnly || (!isControl(el) && !el.isContentEditable))) return { status: 'readonly', detail: describe(el) + ' cannot be typed into' };
    el.scrollIntoView({ block: 'center', inline: 'center', behavior: 'instant' });
    const before = rectOf(el);
    await frames();
    const after = rectOf(el);
    if (before.join() !== after.join() || (el.getAnimations && running(el.getAnimations({ subtree: true })))) return { status: 'moving', detail: describe(el) + ' is still moving' };
    const p = pointOf(el);
    const hit = document.elementFromPoint(p.x, p.y);
    if (!reaches(el, hit)) return { status: 'covered', detail: describe(el) + ' is covered by ' + describe(hit) + ' at ' + Math.round(p.x) + ',' + Math.round(p.y) };
    return { status: 'ok', x: p.x, y: p.y, description: describe(el) };
  },
  focusTarget() {
    const el = st.target;
    if (!el || !el.isConnected) return { ok: false, detail: 'the element went away' };
    if (document.activeElement !== el) el.focus();
    st.filled = el;
    const type = (el.type || '').toLowerCase();
    const native = ['date', 'datetime-local', 'month', 'week', 'time', 'color', 'range'].includes(type);
    if (!native) {
      if (el.isContentEditable) { const r = document.createRange(); r.selectNodeContents(el); const s = getSelection(); s.removeAllRanges(); s.addRange(r); }
      else if (el.select) { try { el.select(); } catch (e) {} }
    }
    return { ok: document.activeElement === el, native, detail: document.activeElement === el ? '' : 'focus went to ' + describe(document.activeElement) };
  },
  setTargetValue(v) {
    const el = st.target;
    const proto = Object.getPrototypeOf(el);
    const desc = Object.getOwnPropertyDescriptor(proto, 'value');
    if (desc && desc.set) desc.set.call(el, v); else el.value = v;
    el.dispatchEvent(new Event('input', { bubbles: true }));
    el.dispatchEvent(new Event('change', { bubbles: true }));
    return norm(el.value);
  },
  targetValue() { const el = st.target; return el ? (el.isContentEditable ? norm(el.innerText) : String(el.value)) : ''; },
  controlChecked() { const el = st.control; return !!(el && (el.checked || el.getAttribute('aria-checked') === 'true')); },
  selectOption(want) {
    const el = st.target;
    if (!el || el.tagName !== 'SELECT') return { ok: false, detail: describe(el) + ' is not a select' };
    const options = Array.from(el.options);
    const match = options.find(o => norm(o.label || o.text) === want) || options.find(o => o.value === want);
    if (!match) return { ok: false, detail: 'no option "' + want + '" in ' + describe(el) + '; options: ' + options.map(o => norm(o.label || o.text)).join(', ') };
    if (match.disabled) return { ok: false, detail: 'option "' + want + '" is disabled' };
    match.selected = true;
    el.dispatchEvent(new Event('input', { bubbles: true }));
    el.dispatchEvent(new Event('change', { bubbles: true }));
    return { ok: true };
  },
  submit() {
    let form = st.filled && st.filled.isConnected ? st.filled.form : null;
    if (!form) {
      const forms = Array.from(document.forms).filter(visible);
      if (forms.length !== 1) return { ok: false, detail: forms.length === 0 ? 'there is no visible form to submit' : forms.length + ' visible forms and no field filled in one of them; click its button instead' };
      form = forms[0];
    }
    form.requestSubmit();
    return { ok: true };
  },
  text() { return norm(document.body ? document.body.innerText : ''); },
  dom() { return '<!doctype html>\n' + document.documentElement.outerHTML; },
  overflow() {
    const root = document.documentElement;
    if (root.scrollWidth <= root.clientWidth) return null;
    const box = el => { const r = el.getBoundingClientRect(), s = getComputedStyle(el); return { element: describe(el), left: r.left, right: r.right, width: r.width, scrollWidth: el.scrollWidth, clientWidth: el.clientWidth, display: s.display, position: s.position, minWidth: s.minWidth, whiteSpace: s.whiteSpace, overflowX: s.overflowX }; };
    const candidates = Array.from(document.querySelectorAll('body *')).filter(el => { const r = el.getBoundingClientRect(); return r.width > 0 && r.right > root.clientWidth + 1; });
    return {
      viewport: root.clientWidth, documentWidth: root.scrollWidth, body: box(document.body),
      candidates: deepest(candidates).slice(0, 40).map(el => { const item = box(el); item.clippingAncestors = []; for (let p = el.parentElement; p; p = p.parentElement) if (getComputedStyle(p).overflowX !== 'visible') item.clippingAncestors.push(box(p)); return item; }),
    };
  },
  resume() { st.resumed = true; return 'resumed'; },
  resumed() { return st.resumed; },
};
})();`

// cspPrefix starts the console error the page script writes for every
// Content-Security-Policy violation.
const cspPrefix = "arandu: content security policy blocked "
