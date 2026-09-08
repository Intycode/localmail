/**
 * Minimal observable store — no Redux, no framework. Just a value plus
 * subscribers. This is the entire state architecture.
 *
 * All methods are arrow-bound class fields: a method reference may be handed
 * to React (e.g. useSyncExternalStore(store.subscribe, ...)) or stored in a
 * callback without losing `this`.
 */
export class Store<T> {
  private value: T
  private listeners = new Set<(v: T) => void>()

  constructor(initial: T) {
    this.value = initial
  }

  get = (): T => this.value

  set = (next: T): void => {
    this.value = next
    for (const fn of this.listeners) fn(next)
  }

  /** mutate in place via a producer fn, then notify */
  update = (fn: (prev: T) => T): void => {
    this.set(fn(this.value))
  }

  subscribe = (fn: (v: T) => void): (() => void) => {
    this.listeners.add(fn)
    fn(this.value)
    return () => this.listeners.delete(fn)
  }
}
