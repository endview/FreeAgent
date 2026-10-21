const DEFAULTS = Object.freeze({ sidebar: 264, column: 768, composer: 92 });
const LIMITS = Object.freeze({
  sidebar: [200, 480],
  column: [560, 1600],
  composer: [92, 420],
});
const STORAGE_PREFIX = 'freeagent:';

function storageKey(environment = '') {
  return `${STORAGE_PREFIX}${environment || 'default'}:layout`;
}

export function clampLayout(name, value, available = Infinity) {
  const [minimum, maximum] = LIMITS[name] || [0, Infinity];
  const upper = Number.isFinite(available) ? Math.max(minimum, Math.min(maximum, available)) : maximum;
  const number = Number(value);
  return Math.round(Math.min(upper, Math.max(minimum, Number.isFinite(number) ? number : DEFAULTS[name])));
}

export function readLayout(storage, environment = '') {
  const result = { ...DEFAULTS };
  if (!storage) return result;
  try {
    const parsed = JSON.parse(storage.getItem(storageKey(environment)) || '{}');
    for (const name of Object.keys(DEFAULTS)) result[name] = clampLayout(name, parsed?.[name]);
  } catch { /* denied or malformed browser storage uses defaults */ }
  return result;
}

export function writeLayout(storage, layout, environment = '') {
  if (!storage) return;
  try { storage.setItem(storageKey(environment), JSON.stringify(layout)); } catch { /* persistence is optional */ }
}

function rootElement(root) {
  return root?.documentElement || root?.ownerDocument?.documentElement || root;
}

function availableColumn(root) {
  const main = root?.querySelector?.('main');
  return main ? Math.max(520, main.getBoundingClientRect().width - 48) : Infinity;
}

function availableSidebar(root) {
  const width = root?.defaultView?.innerWidth || root?.ownerDocument?.defaultView?.innerWidth || Infinity;
  return Number.isFinite(width) ? width * 0.45 : Infinity;
}

function availableComposer(root) {
  const main = root?.querySelector?.('main');
  if (!main) return Infinity;
  const height = main.getBoundingClientRect().height;
  return Math.min(420, height * 0.5, Math.max(92, height - 200));
}

function setValue(root, name, raw) {
  const element = rootElement(root);
  const available = name === 'column' ? availableColumn(root) : name === 'sidebar' ? availableSidebar(root) : availableComposer(root);
  const value = clampLayout(name, raw, available);
  element?.style?.setProperty(`--${name === 'composer' ? 'prompt-height' : `${name}-width`}`, `${value}px`);
  return value;
}

function valueFromCSS(root, name, fallback) {
  const element = rootElement(root);
  const property = name === 'composer' ? '--prompt-height' : `--${name}-width`;
  const raw = element?.ownerDocument?.defaultView?.getComputedStyle(element).getPropertyValue(property);
  const value = Number.parseFloat(raw);
  return Number.isFinite(value) ? value : fallback;
}

export function bindLayoutResizers({ root = globalThis.document, storage = null } = {}) {
  if (!root?.querySelectorAll) return () => {};
  let environment = '';
  const layout = readLayout(storage, environment);
  for (const name of Object.keys(DEFAULTS)) layout[name] = setValue(root, name, layout[name]);
  const handles = [...root.querySelectorAll('[data-layout-resizer]')];
  const cleanups = [];
  let active = null;

  const updateHandle = (handle, value) => {
    const [minimum, maximum] = LIMITS[handle.dataset.layoutResizer];
    handle.setAttribute('aria-valuemin', String(minimum));
    handle.setAttribute('aria-valuemax', String(maximum));
    handle.setAttribute('aria-valuenow', String(value));
    handle.setAttribute('aria-valuetext', `${value} 像素`);
  };
  const update = (name, value) => {
    layout[name] = setValue(root, name, value);
    for (const handle of handles.filter((item) => item.dataset.layoutResizer === name)) updateHandle(handle, layout[name]);
  };
  for (const handle of handles) {
    const name = handle.dataset.layoutResizer;
    if (!DEFAULTS[name]) continue;
    updateHandle(handle, layout[name]);
    const start = (event) => {
      if (event.button !== undefined && event.button !== 0) return;
      event.preventDefault();
      const current = valueFromCSS(root, name, layout[name]);
      active = { handle, name, startX: event.clientX, startY: event.clientY, value: current };
      handle.setPointerCapture?.(event.pointerId);
      root.body?.classList.add('layout-resizing');
    };
    const move = (event) => {
      if (!active || active.handle !== handle) return;
      const delta = name === 'composer' ? active.startY - event.clientY : event.clientX - active.startX;
      if (name === 'column') {
        const direction = handle.dataset.layoutSide === 'start' ? -1 : 1;
        update(name, active.value + delta * direction * 2);
      } else update(name, active.value + delta);
    };
    const end = (event) => {
      if (!active || active.handle !== handle) return;
      handle.releasePointerCapture?.(event.pointerId);
      writeLayout(storage, layout, environment);
      active = null;
      root.body?.classList.remove('layout-resizing');
    };
    const keydown = (event) => {
      const step = event.shiftKey ? (name === 'column' ? 64 : name === 'composer' ? 48 : 32) : name === 'column' ? 32 : name === 'composer' ? 24 : 16;
      let delta = 0;
      if (name === 'composer') delta = event.key === 'ArrowUp' ? step : event.key === 'ArrowDown' ? -step : 0;
      else delta = event.key === 'ArrowRight' ? step : event.key === 'ArrowLeft' ? -step : 0;
      if (!delta) return;
      event.preventDefault();
      update(name, layout[name] + delta);
      writeLayout(storage, layout, environment);
    };
    const reset = (event) => {
      event.preventDefault();
      update(name, DEFAULTS[name]);
      writeLayout(storage, layout, environment);
    };
    handle.addEventListener('pointerdown', start);
    handle.addEventListener('pointermove', move);
    handle.addEventListener('pointerup', end);
    handle.addEventListener('pointercancel', end);
    handle.addEventListener('keydown', keydown);
    handle.addEventListener('dblclick', reset);
    cleanups.push(() => {
      handle.removeEventListener('pointerdown', start);
      handle.removeEventListener('pointermove', move);
      handle.removeEventListener('pointerup', end);
      handle.removeEventListener('pointercancel', end);
      handle.removeEventListener('keydown', keydown);
      handle.removeEventListener('dblclick', reset);
    });
  }
  const resize = () => {
    for (const name of Object.keys(DEFAULTS)) setValue(root, name, layout[name]);
    for (const handle of handles) updateHandle(handle, layout[handle.dataset.layoutResizer]);
  };
  const view = root.defaultView || root.ownerDocument?.defaultView;
  view?.addEventListener('resize', resize);
  const cleanup = () => {
    cleanups.forEach((item) => item());
    view?.removeEventListener('resize', resize);
  };
  cleanup.setEnvironment = (next) => {
    if (next === environment) return;
    writeLayout(storage, layout, environment);
    environment = String(next || '');
    const nextLayout = readLayout(storage, environment);
    for (const name of Object.keys(DEFAULTS)) layout[name] = setValue(root, name, nextLayout[name]);
    for (const handle of handles) updateHandle(handle, layout[handle.dataset.layoutResizer]);
  };
  return cleanup;
}

export { DEFAULTS, LIMITS, storageKey };
