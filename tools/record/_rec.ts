// Records every library call the test suite makes, with its arguments and
// results, so the Go port can replay each test and compare the outcomes.
import { createHash } from 'node:crypto';
import { appendFileSync, mkdirSync } from 'node:fs';
import { gzipSync } from 'node:zlib';

const FULL = process.env.REC_FULL === '1';
const OUT_LIMIT = FULL ? Infinity : 64;
// per file: string table and strings not yet written
const strTables = new Map<string, Map<string, string>>();
const pendingStrsByFile = new Map<string, Record<string, string>>();

type Ev = Record<string, unknown> & { k: string };

const OUT = process.env.REC_OUT ?? '/tmp/rec';
mkdirSync(OUT, { recursive: true });

interface FileLog {
  events: Ev[];
}
const files = new Map<string, FileLog>();
let currentFile = '<none>';
// the event list new events go to: a test's list, or a callback's nested list
let sink: Ev[] = [];
let depth = 0;
// innermost recorded API event, for clock values
const active: Ev[] = [];

function fileLog(file: string): FileLog {
  let f = files.get(file);
  if (f === undefined) {
    f = { events: [] };
    files.set(file, f);
  }
  return f;
}

function flush(file: string): void {
  const log = files.get(file);
  if (log === undefined) return;
  const pendingStrs = pendingStrsByFile.get(file);
  if (pendingStrs !== undefined && Object.keys(pendingStrs).length > 0) {
    appendFileSync(`${OUT}/${file}.jsonl`, JSON.stringify({ k: 'strs', table: pendingStrs }) + '\n');
    pendingStrsByFile.set(file, {});
  }
  for (const e of log.events) appendFileSync(`${OUT}/${file}.jsonl`, JSON.stringify(e) + '\n');
  log.events = [];
}

export function setFile(file: string): void {
  flush(currentFile);
  currentFile = file;
  const log = fileLog(file);
  const ev: Ev = { k: 'test', name: '<module>', ev: [] };
  log.events.push(ev);
  sink = ev.ev as Ev[];
}

export function beginTest(file: string, name: string): void {
  flush(currentFile);
  const log = fileLog(file);
  const ev: Ev = { k: 'test', name, ev: [] };
  log.events.push(ev);
  sink = ev.ev as Ev[];
  currentFile = file;
}

export function endTest(ok: boolean, error?: unknown): void {
  const log = fileLog(currentFile);
  const last = log.events[log.events.length - 1];
  last.ok = ok;
  if (!ok) last.error = String((error as Error)?.stack ?? error);
  flush(currentFile);
}

process.on('exit', () => {
  for (const [file, log] of files) {
    const text = log.events.map((e) => JSON.stringify(e)).join('\n') + '\n';
    writeFileSync(`${OUT}/${file}.jsonl.gz`, gzipSync(text, { level: 6 }));
  }
});

// ---- identities ----

const idsByFile = new Map<string, Map<object, string>>();
// the file in which each object was first seen
const firstFile = new WeakMap<object, string>();
let nextId = 1;
function idOf(obj: object, prefix: string, define?: (id: string) => Ev): string {
  // ids are per file so each file replays on its own
  let ids = idsByFile.get(currentFile);
  if (ids === undefined) {
    ids = new Map();
    idsByFile.set(currentFile, ids);
  }
  let id = ids.get(obj);
  if (id === undefined) {
    id = `${prefix}${nextId++}`;
    ids.set(obj, id);
    const first = firstFile.get(obj);
    if (first === undefined) firstFile.set(obj, currentFile);
    if (define !== undefined) {
      const ev = define(id);
      // an instance an earlier file used has memory the replay cannot know
      if (first !== undefined && first !== currentFile) ev.inherited = true;
      sink.push(ev);
    }
  }
  return id;
}

// module sources: transformed code -> transformWat input
export const codeSource = new Map<string, { url: string; content?: string }>();
export const bytesSource = new WeakMap<object, { url: string; content?: string; optimized?: boolean }>();
export const moduleSource = new WeakMap<object, { url: string; content?: string; optimized?: boolean }>();

const OrigModule = WebAssembly.Module;
const ModuleProxy = new Proxy(OrigModule, {
  construct(target, args) {
    const m = Reflect.construct(target, args);
    const src = bytesSource.get(args[0]);
    if (src !== undefined) moduleSource.set(m, src);
    return m;
  },
});
(WebAssembly as { Module: unknown }).Module = ModuleProxy;

function moduleId(m: object): string {
  return idOf(m, 'mod', (id) => {
    const src = moduleSource.get(m);
    return { k: 'module', id, src: src ?? null };
  });
}

function themeId(theme: object): string {
  return idOf(theme, 'th', (id) => {
    const t = theme as { themes?: unknown[] };
    if (Array.isArray(t.themes)) {
      return {
        k: 'theme',
        id,
        family: {
          name: (theme as { name?: unknown }).name,
          themes: t.themes.map((m) =>
            m != null && typeof m === 'object' ? { $theme: themeId(m) } : enc(m)
          ),
        },
      };
    }
    if ((theme as { cssVariables?: unknown }).cssVariables === true) {
      // the style of a CSS-variable theme is never read
      const { name, appearance } = theme as { name?: unknown; appearance?: unknown };
      return { k: 'theme', id, json: enc({ name, appearance, cssVariables: true, style: {} }) };
    }
    const text = JSON.stringify(theme);
    const bundled = bundledThemes.get(text);
    if (bundled !== undefined) return { k: 'theme', id, bundled };
    return { k: 'theme', id, json: enc(JSON.parse(text)) };
  });
}

const bundledThemes = new Map<string, string>();
{
  const { readdirSync, readFileSync } = await import('node:fs');
  const dir = new URL('../themes/', import.meta.url);
  for (const f of readdirSync(dir)) {
    if (!f.endsWith('.json')) continue;
    bundledThemes.set(JSON.stringify(JSON.parse(readFileSync(new URL(f, dir), 'utf-8'))), f.slice(0, -5));
  }
}

// ---- value encoding ----

const loneRe = /[\uD800-\uDBFF](?![\uDC00-\uDFFF])|(?:^|[^\uD800-\uDBFF])[\uDC00-\uDFFF]/;

function wtf8(s: string): string {
  const out: number[] = [];
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if (c < 0x80) out.push(c);
    else if (c < 0x800) out.push(0xc0 | (c >> 6), 0x80 | (c & 63));
    else {
      if (c >= 0xd800 && c < 0xdc00) {
        const lo = s.charCodeAt(i + 1);
        if (lo >= 0xdc00 && lo < 0xe000) {
          const cp = 0x10000 + ((c - 0xd800) << 10) + (lo - 0xdc00);
          out.push(0xf0 | (cp >> 18), 0x80 | ((cp >> 12) & 63), 0x80 | ((cp >> 6) & 63), 0x80 | (cp & 63));
          i++;
          continue;
        }
      }
      out.push(0xe0 | (c >> 12), 0x80 | ((c >> 6) & 63), 0x80 | (c & 63));
    }
  }
  return Buffer.from(out).toString('base64');
}

function b64(bytes: Uint8Array): string {
  return Buffer.from(bytes.buffer, bytes.byteOffset, bytes.byteLength).toString('base64');
}

function strId(v: string): string {
  let table = strTables.get(currentFile);
  if (table === undefined) {
    table = new Map();
    strTables.set(currentFile, table);
  }
  let id = table.get(v);
  if (id === undefined) {
    id = `s${table.size}`;
    table.set(v, id);
    let pending = pendingStrsByFile.get(currentFile);
    if (pending === undefined) {
      pending = {};
      pendingStrsByFile.set(currentFile, pending);
    }
    pending[id] = loneRe.test(v) ? ('\u0000wtf8:' + wtf8(v)) : v;
  }
  return id;
}

/** Store an encoded output, or its hash when large. */
function putOut(ev: Ev, key: string, encoded: unknown): void {
  const text = JSON.stringify(encoded);
  if (text.length > OUT_LIMIT) {
    ev[key + 'Hash'] = createHash('sha256').update(text).digest('hex').slice(0, 16);
    ev[key + 'Len'] = text.length;
  } else ev[key] = encoded;
}

export function enc(v: unknown, themeKeys = false): unknown {
  if (v === undefined) return { $undef: 1 };
  if (v === null || typeof v === 'boolean') return v;
  if (typeof v === 'number') {
    if (Number.isFinite(v)) return v;
    return { $num: String(v) };
  }
  if (typeof v === 'bigint') return { $big: v.toString() };
  if (typeof v === 'string') {
    if (v.length > 256) return { $str: strId(v) };
    return loneRe.test(v) ? { $wtf8: wtf8(v) } : v;
  }
  if (typeof v === 'function') return { $fn: 1 };
  if (typeof v === 'symbol') return { $sym: String(v) };
  if (v instanceof Uint8Array) return { $bytes: b64(v) };
  if (v instanceof ArrayBuffer) return { $ab: b64(new Uint8Array(v)) };
  if (v instanceof Uint32Array) return { $u32: Array.from(v) };
  if (ArrayBuffer.isView(v)) return { $view: v.constructor.name };
  if (v instanceof Error) return { $err: { name: v.name, message: v.message } };
  if (v instanceof Map) {
    const entries = [...v];
    if (entries.every(([a]) => typeof a === 'number')) entries.sort((a, b) => (a[0] as number) - (b[0] as number));
    return { $map: entries.map(([a, b]) => [enc(a), enc(b)]) };
  }
  if (v instanceof Set) return { $set: [...v].map((a) => enc(a)) };
  if (Array.isArray(v)) return v.map((x) => enc(x));
  if (typeof v === 'object') {
    const out: Record<string, unknown> = {};
    for (const key of Object.keys(v)) {
      const x = (v as Record<string, unknown>)[key];
      if (x === undefined) continue;
      out[key] = enc(x);
    }
    void themeKeys;
    return out;
  }
  return { $unknown: String(v) };
}

function isThemeLike(v: unknown): v is object {
  return v != null && typeof v === 'object' && !Array.isArray(v);
}

function encTheme(v: unknown): unknown {
  return isThemeLike(v) ? { $theme: themeId(v) } : enc(v);
}

export function encOptions(o: unknown): unknown {
  if (o == null || typeof o !== 'object') return enc(o);
  const out: Record<string, unknown> = {};
  for (const key of Object.keys(o)) {
    const x = (o as Record<string, unknown>)[key];
    if (x === undefined) continue;
    if (key === 'theme') out[key] = encTheme(x);
    else if (key === 'themes' && x != null && typeof x === 'object') {
      const themes: Record<string, unknown> = {};
      for (const name of Object.keys(x)) {
        const t = (x as Record<string, unknown>)[name];
        if (t === undefined) continue;
        themes[name] = encTheme(t);
      }
      out[key] = themes;
    } else out[key] = enc(x);
  }
  return out;
}

// ---- call recording ----

function hlId(hl: object): string {
  return idOf(hl, 'hl', (id) => {
    const m = (hl as { wasmModule?: object }).wasmModule;
    return { k: 'hl', id, module: m === undefined ? null : moduleId(m) };
  });
}

// instances whose memory the tests touch: diff memory before each call
const touched = new WeakSet<object>();
const shadows = new WeakMap<object, Uint8Array>();

export function markTouched(hl: object): void {
  if (depth !== 0 || touched.has(hl)) return;
  // the constructor reads its own fields; that is not test code
  if (/new HighlightsHighlighter/.test(new Error().stack ?? '')) return;
  touched.add(hl);
  // the test is about to read or write memory; later diffs start here
  snapshot(hl, memoryOf(hl));
}

function memoryOf(hl: object): Uint8Array {
  return new Uint8Array((hl as { memory: WebAssembly.Memory }).memory.buffer);
}

function diffMemory(owner: object, mem: Uint8Array): unknown[] | undefined {
  const shadow = shadows.get(owner);
  const ranges: unknown[] = [];
  const n = mem.length;
  if (shadow === undefined || shadow.length !== n) {
    // first touch: record all nonzero differences against a fresh instance
    // are unknown, so record every byte range that differs from the
    // previous snapshot only once a snapshot exists
    if (shadow === undefined) return undefined;
  }
  const m = Math.min(n, shadow!.length);
  let i = 0;
  while (i < m) {
    if (mem[i] === shadow![i]) {
      i++;
      continue;
    }
    let j = i + 1;
    let same = 0;
    while (j < m && same < 16) {
      if (mem[j] === shadow![j]) same++;
      else same = 0;
      j++;
    }
    j -= same;
    ranges.push([i, b64(mem.subarray(i, j))]);
    i = j;
  }
  if (n > shadow!.length) ranges.push([shadow!.length, b64(mem.subarray(shadow!.length))]);
  return ranges;
}

function snapshot(owner: object, mem: Uint8Array): void {
  shadows.set(owner, mem.slice());
}

export function call<T>(
  ev: Ev,
  run: () => T,
  encodeResult: (r: T) => unknown = (r) => enc(r)
): T {
  if (depth > 0) return run();
  sink.push(ev);
  depth++;
  active.push(ev);
  try {
    const r = run();
    putOut(ev, 'out', encodeResult(r));
    return r;
  } catch (e) {
    ev.err = { name: (e as Error)?.name, message: (e as Error)?.message ?? String(e) };
    throw e;
  } finally {
    active.pop();
    depth--;
  }
}

export function now(): number {
  const t = performance.now();
  const ev = active[active.length - 1];
  if (ev !== undefined) ((ev.clock ??= []) as number[]).push(t);
  return t;
}

/** Run a user callback from inside an API call, collecting its events. */
export function callback<T>(ev: Ev, run: () => T): T {
  const savedSink = sink;
  const savedDepth = depth;
  const savedActive = active.splice(0);
  if ('lines' in ev) {
    const lines = ev.lines;
    delete ev.lines;
    putOut(ev, 'lines', lines);
  }
  ev.ev = [];
  sink.push(ev);
  sink = ev.ev as Ev[];
  depth = 0;
  try {
    return run();
  } finally {
    sink = savedSink;
    depth = savedDepth;
    active.push(...savedActive);
  }
}

export function fn<A extends unknown[], R>(
  name: string,
  impl: (...args: A) => R,
  encodeArgs: (...args: A) => unknown[] = (...args) => args.map((a) => enc(a))
): (...args: A) => R {
  return (...args: A) => {
    if (depth > 0) return impl(...args);
    return call({ k: 'fn', name, args: encodeArgs(...args) }, () => impl(...args));
  };
}

// HighlightsHighlighter methods
export function hlMethod(name: string, self: object, args: unknown[], run: () => unknown, encodeResult?: (r: unknown) => unknown): unknown {
  if (depth > 0) return run();
  const id = hlId(self);
  const ev: Ev = { k: 'hl.call', hl: id, name, args: name === 'codeToHtml' || name === 'codeToTokens' ? [enc(args[0]), encOptions(args[1])] : args.map((a) => enc(a)) };
  const wasTouched = touched.has(self);
  if (wasTouched) {
    const mem = memoryOf(self);
    const d = diffMemory(self, mem);
    ev.mem = d ?? { fullgz: b64(gzipSync(mem)) };
    snapshot(self, mem);
  }
  try {
    return call(ev, run, encodeResult);
  } finally {
    if (wasTouched) {
      const mem = memoryOf(self);
      putOut(ev, 'memAfter', diffMemory(self, mem));
      snapshot(self, mem);
    } else if (touched.has(self)) snapshot(self, memoryOf(self));
  }
}

// stream and live objects
export function objId(obj: object, prefix: string): string {
  return idOf(obj, prefix);
}

export function construct(kind: string, obj: () => object, options: unknown, encOpts: (o: unknown) => unknown): object {
  if (depth > 0) return obj();
  const ev: Ev = { k: `${kind}.new`, opts: encOpts(options) };
  return call(ev, () => {
    const o = obj();
    ev.id = objId(o, kind);
    return o;
  }, () => null);
}

export function method(kind: string, self: object, name: string, args: unknown[], run: () => unknown, encArgs?: unknown[]): unknown {
  if (depth > 0) return run();
  const ev: Ev = { k: `${kind}.call`, id: objId(self, kind), name, args: encArgs ?? args.map((a) => enc(a)) };
  return call(ev, run);
}

export function slice(self: object, generation: number, run: () => void): void {
  if (depth > 0) {
    run();
    return;
  }
  call({ k: 'lv.slice', id: objId(self, 'lv'), gen: generation }, run, () => null);
}

// ---- raw instances ----

const OrigInstance = WebAssembly.Instance;
const InstanceProxy = new Proxy(OrigInstance, {
  construct(target, args) {
    const [mod, imports] = args as [object, Record<string, Record<string, unknown>> | undefined];
    if (depth > 0) return Reflect.construct(target, args);
    // wrap env imports so their results are recorded
    let wrappedImports = imports;
    const envLog: { ev: Ev | undefined } = { ev: undefined };
    if (imports !== undefined) {
      wrappedImports = {};
      for (const ns of Object.keys(imports)) {
        const table: Record<string, unknown> = {};
        for (const name of Object.keys(imports[ns])) {
          const f = imports[ns][name];
          table[name] =
            typeof f === 'function'
              ? (...a: unknown[]) => {
                  let r: unknown;
                  let threw: unknown;
                  try {
                    r = (f as (...x: unknown[]) => unknown)(...a);
                  } catch (e) {
                    threw = e;
                  }
                  const ev = envLog.ev;
                  if (ev !== undefined)
                    ((ev.env ??= []) as unknown[]).push(threw === undefined ? [name, enc(r)] : [name, { $throw: String(threw) }]);
                  if (threw !== undefined) throw threw;
                  return r;
                }
              : f;
        }
        wrappedImports[ns] = table;
      }
    }
    const inst = Reflect.construct(target, [mod, wrappedImports]) as WebAssembly.Instance;
    const id = objId(inst, 'raw');
    const initial = inst.exports.memory as WebAssembly.Memory | undefined;
    if (initial !== undefined) snapshot(inst, new Uint8Array(initial.buffer));
    sink.push({ k: 'raw.new', id, module: moduleId(mod), imports: imports === undefined ? null : Object.keys(imports.env ?? {}) });
    const exports: Record<string, unknown> = {};
    const mem = inst.exports.memory as WebAssembly.Memory | undefined;
    for (const name of Object.keys(inst.exports)) {
      const x = inst.exports[name];
      if (typeof x !== 'function') {
        exports[name] = x;
        continue;
      }
      exports[name] = (...a: unknown[]) => {
        if (depth > 0) return (x as (...y: unknown[]) => unknown)(...a);
        const ev: Ev = { k: 'raw.call', id, name, args: a.map((v) => enc(v)) };
        if (mem !== undefined) {
          const bytes = new Uint8Array(mem.buffer);
          ev.mem = diffMemory(inst, bytes) ?? { fullgz: b64(gzipSync(bytes)) };
          snapshot(inst, bytes);
        }
        envLog.ev = ev;
        try {
          return call(ev, () => (x as (...y: unknown[]) => unknown)(...a));
        } finally {
          envLog.ev = undefined;
          if (mem !== undefined) {
            const bytes = new Uint8Array(mem.buffer);
            putOut(ev, 'memAfter', diffMemory(inst, bytes));
            snapshot(inst, bytes);
          }
        }
      };
    }
    return new Proxy(inst, {
      get(t, p) {
        if (p === 'exports') return exports;
        return Reflect.get(t, p);
      },
    });
  },
});
(WebAssembly as { Instance: unknown }).Instance = InstanceProxy;

export function depthNow(): number {
  return depth;
}
export function enter(): void {
  depth++;
}
export function leave(): void {
  depth--;
}

(globalThis as Record<string, unknown>).__rec = {
  setFile, beginTest, endTest, enc, encOptions, call, now, callback, fn, hlMethod,
  construct, method, slice, objId, markTouched, codeSource, bytesSource, moduleSource,
  enter, leave, depthNow, themeId,
};
