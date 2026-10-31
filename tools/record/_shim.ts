// Wraps bun:test so the recorder knows which test is running.
import { beforeAll, expect, test as bunTest } from 'bun:test';
import { beginTest, endTest, setFile } from './_rec';

type Fn = (...a: unknown[]) => unknown;

export function makeTest(file: string) {
  setFile(file);
  const wrap = (name: string, fn: Fn) => async (...a: unknown[]) => {
    beginTest(file, name);
    try {
      const r = await fn(...a);
      endTest(true);
      return r;
    } catch (e) {
      endTest(false, e);
      throw e;
    }
  };
  const test = (name: string, optsOrFn: unknown, maybeFn?: unknown) => {
    const fn = (typeof optsOrFn === 'function' ? optsOrFn : maybeFn) as Fn;
    const opts = typeof optsOrFn === 'object' && optsOrFn !== null ? (optsOrFn as { timeout?: number }) : {};
    return bunTest(name, wrap(name, fn), opts.timeout ?? 120000);
  };
  const before = (fn: Fn) =>
    beforeAll(async () => {
      beginTest(file, '<before>');
      try {
        await fn();
        endTest(true);
      } catch (e) {
        endTest(false, e);
        throw e;
      }
    });
  const t = Object.assign(test, { test, before });
  return { t, test, expect };
}
