"""Copy the assembled TypeScript package and instrument it for recording."""
import os
import re
import shutil
import sys

SRC = sys.argv[1]
DST = sys.argv[2]
REC = os.path.dirname(os.path.abspath(__file__))
NODE_MODULES = sys.argv[3]

if os.path.exists(DST):
    shutil.rmtree(DST)
shutil.copytree(SRC, DST)
os.symlink(NODE_MODULES, os.path.join(DST, 'node_modules'))
shutil.copy(os.path.join(REC, '_rec.ts'), os.path.join(DST, 'test', '_rec.ts'))
shutil.copy(os.path.join(REC, '_shim.ts'), os.path.join(DST, 'test', '_shim.ts'))


def edit(path, fn):
    p = os.path.join(DST, path)
    with open(p) as f:
        s = f.read()
    t = fn(s)
    with open(p, 'w') as f:
        f.write(t)


R = "const __R = (globalThis as any).__rec;\n"


def wrap_functions(s, names, enc_args=None):
    enc_args = enc_args or {}
    tail = ''
    for name in names:
        pat = re.compile(r'export function ' + name + r'\(')
        if not pat.search(s):
            raise SystemExit(f'missing function {name}')
        s = pat.sub(f'function __impl_{name}(', s, count=1)
        args = enc_args.get(name)
        if args:
            tail += f"export const {name} = __R.fn('{name}', __impl_{name}, {args}) as typeof __impl_{name};\n"
        else:
            tail += f"export const {name} = __R.fn('{name}', __impl_{name}) as typeof __impl_{name};\n"
    return s, tail


# ---- scripts/build.ts ----
def build(s):
    s = s.replace("""  if (!(url instanceof URL)) url = pathToFileURL(url);
  content ??= readFileSync(url, 'utf-8');""", """  if (!(url instanceof URL)) url = pathToFileURL(url);
  const __content = content;
  content ??= readFileSync(url, 'utf-8');""")
    s = s.replace("""  code = markSimdReachers(code);
  return {
    code,""", """  code = markSimdReachers(code);
  (globalThis as any).__rec?.codeSource.set(code, { url: url.pathname, content: __content });
  return {
    code,""")
    s = s.replace("""  try {
    return wasmModule.toBinary({}).buffer;
  } finally {
    wasmModule.destroy();
  }
}""", """  try {
    const out = wasmModule.toBinary({}).buffer;
    const src = (globalThis as any).__rec?.codeSource.get(text);
    if (src !== undefined) (globalThis as any).__rec.bytesSource.set(out, src);
    return out;
  } finally {
    wasmModule.destroy();
  }
}""")
    s = s.replace("export function optimizeWasm(wasmBytes: Uint8Array): Uint8Array {", """export function optimizeWasm(wasmBytes: Uint8Array): Uint8Array {
  const src = (globalThis as any).__rec?.bytesSource.get(wasmBytes);
  const out = optimizeWasmImpl(wasmBytes);
  if (src !== undefined) (globalThis as any).__rec.bytesSource.set(out, { ...src, optimized: true });
  return out;
}
function optimizeWasmImpl(wasmBytes: Uint8Array): Uint8Array {""")
    return s


edit('scripts/build.ts', build)


# ---- lib/highlighter.ts ----
def highlighter(s):
    for field in ['instance', 'memory', 'buffer', 'dv']:
        s = re.sub(r'^  ' + field + r': ', f'  declare {field}: ', s, count=1, flags=re.M)
    s = s.replace('const instance = new WebAssembly.Instance(wasmModule, { env });', 'const __r = (globalThis as any).__rec;\n    __r.enter();\n    let instance: WebAssembly.Instance;\n    try {\n      instance = new WebAssembly.Instance(wasmModule, { env });\n    } finally {\n      __r.leave();\n    }')
    s, tail = wrap_functions(s, ['isSupportedLanguage', 'langIdOf'])
    s = s.replace('export class StreamTokenizer extends', 'class __StreamTokenizer extends')
    s += '\n' + R + tail + """
for (const field of ['instance', 'memory', 'buffer', 'dv']) {
  const key = Symbol(field);
  Object.defineProperty(HighlightsHighlighter.prototype, field, {
    configurable: true,
    get(this: any) {
      __R.markTouched(this);
      return this[key];
    },
    set(this: any, v: unknown) {
      this[key] = v;
    },
  });
}
for (const name of ['codeToHtml', 'codeToTokens', 'tokenizeLineRecords', 'writeInput']) {
  const orig = (HighlightsHighlighter.prototype as any)[name];
  (HighlightsHighlighter.prototype as any)[name] = function (this: any, ...args: unknown[]) {
    return __R.hlMethod(name, this, args, () => orig.apply(this, args),
      name === 'codeToHtml' ? (r: Uint8Array) => __R.enc(r.slice()) :
      name === 'tokenizeLineRecords' ? (r: Uint32Array) => __R.enc(r.slice()) : undefined);
  };
}
for (const name of ['pushCode', 'end', 'dispose']) {
  const orig = (__StreamTokenizer.prototype as any)[name];
  (__StreamTokenizer.prototype as any)[name] = function (this: any, ...args: unknown[]) {
    return __R.method('st', this, name, args, () => orig.apply(this, args));
  };
}
export const StreamTokenizer = new Proxy(__StreamTokenizer, {
  construct(target, args, newTarget) {
    return __R.construct('st', () => Reflect.construct(target, args, newTarget), args[0], __R.encOptions);
  },
}) as typeof __StreamTokenizer;
"""
    return s


edit('lib/highlighter.ts', highlighter)


# ---- lib/live.ts ----
def live(s):
    s = s.replace('export class LiveTokenizer {', 'class __LiveTokenizer {')
    s = s.replace('performance.now()', '__R.now()')
    s = s.replace('LiveTokenizer.#createStaged', '__LiveTokenizer.#createStaged')
    s = s.replace("""  #deferSlice(generation: number): void {""", """  #deferSlice(generation: number): void {
    __R.slice(this, generation, () => this.#deferSliceImpl(generation));
  }

  #deferSliceImpl(generation: number): void {""")
    s = R + s + """
for (const name of ['flush', 'pause', 'resume', 'dispose', 'getLineLength', 'getLineText', 'getText', 'getLineRecords', 'getLineTokens', 'applyEdits', 'reset']) {
  const orig = (__LiveTokenizer.prototype as any)[name];
  (__LiveTokenizer.prototype as any)[name] = function (this: any, ...args: unknown[]) {
    return __R.method('lv', this, name, args, () => orig.apply(this, args));
  };
}
for (const name of ['revision', 'lineCount', 'pendingTokenization']) {
  const desc = Object.getOwnPropertyDescriptor(__LiveTokenizer.prototype, name)!;
  Object.defineProperty(__LiveTokenizer.prototype, name, {
    configurable: true,
    get(this: any) {
      return __R.method('lv', this, name, [], () => desc.get!.call(this));
    },
  });
}
export const LiveTokenizer = new Proxy(__LiveTokenizer, {
  construct(target, args, newTarget) {
    const options = args[0];
    let wrapped = options;
    if (options != null && typeof options === 'object' && typeof options.onDeferTokenize === 'function') {
      const cb = options.onDeferTokenize;
      wrapped = { ...options, onDeferTokenize: (lines: unknown) =>
        __R.callback({ k: 'lv.defer', lines: __R.enc(lines) }, () => cb(lines)) };
    }
    return __R.construct('lv', () => Reflect.construct(target, [wrapped, ...args.slice(1)], newTarget), options, __R.encOptions);
  },
}) as typeof __LiveTokenizer;
"""
    return s


edit('lib/live.ts', live)


# ---- lib/theme.ts ----
def theme(s):
    s, tail = wrap_functions(s, ['isThemeColor', 'themeForeground', 'themeBackground', 'resolveThemeStyle', 'prepareTheme', 'resolveTheme'], {
        'prepareTheme': '(theme: unknown, prefix?: unknown) => [__R.encOptions({ theme }).theme, __R.enc(prefix)]',
        'resolveTheme': '(theme: unknown) => [__R.encOptions({ theme }).theme]',
    })
    return s + '\n' + R + tail


edit('lib/theme.ts', theme)


def themes_index(s):
    s, tail = wrap_functions(s, ['toCSS'], {
        'toCSS': '(theme: unknown, opts?: unknown) => [__R.encOptions({ theme }).theme, __R.enc(opts)]',
    })
    return s + '\n' + R + tail


edit('themes/index.ts', themes_index)

# ---- tests ----
for name in sorted(os.listdir(os.path.join(DST, 'test'))):
    if not name.endswith('_test.ts'):
        continue
    file = name[:-3]

    def tests(s, file=file):
        n = 0
        for pat, rep in [
            (r"import t from 'node:test';", "const { t } = __mk('{f}');"),
            (r"import test from 'node:test';", "const { test } = __mk('{f}');"),
            (r"import \{ expect, test \} from 'bun:test';", "const { expect, test } = __mk('{f}');"),
            (r"import \{ test \} from 'bun:test';", "const { test } = __mk('{f}');"),
        ]:
            s, c = re.subn(pat, rep.replace('{f}', file), s)
            n += c
        if n != 1:
            raise SystemExit(f'{file}: {n} test imports')
        return "import './_rec';\nimport { makeTest as __mk } from './_shim';\n" + s

    edit(os.path.join('test', name), tests)
print('instrumented', DST)
