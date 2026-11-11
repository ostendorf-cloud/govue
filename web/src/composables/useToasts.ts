import { readonly, ref, type Ref } from 'vue'

export type ToastKind = 'success' | 'info' | 'error'

export interface Toast {
  id: number
  kind: ToastKind
  title: string
  /** The second line, for a detail or a request id. */
  message?: string
  /** 0 stays until it is dismissed. */
  timeoutMs: number
}

export interface ToastInput {
  kind?: ToastKind
  title: string
  message?: string
  timeoutMs?: number
}

/** maxToasts keeps a run of failures from covering the page. */
const maxToasts = 4

const defaultTimeouts: Record<ToastKind, number> = {
  success: 4000,
  info: 6000,
  // A failure has to be readable, so it waits for the user.
  error: 0,
}

// Shared, because a toast is app wide: anything can raise one and one host
// renders them.
const toasts = ref<Toast[]>([])
const timers = new Map<number, ReturnType<typeof setTimeout>>()
let nextId = 1

export interface ToastState {
  toasts: Readonly<Ref<Toast[]>>
  /** notify raises a toast and returns its id. */
  notify: (input: ToastInput) => number
  dismiss: (id: number) => void
  dismissAll: () => void
}

export function useToasts(): ToastState {
  return {
    toasts: readonly(toasts) as Readonly<Ref<Toast[]>>,
    notify: (input) => {
      const kind = input.kind ?? 'info'
      const toast: Toast = {
        id: nextId++,
        kind,
        title: input.title,
        message: input.message,
        timeoutMs: input.timeoutMs ?? defaultTimeouts[kind],
      }
      toasts.value = [...toasts.value, toast].slice(-maxToasts)
      if (toast.timeoutMs > 0) {
        timers.set(
          toast.id,
          setTimeout(() => dismiss(toast.id), toast.timeoutMs),
        )
      }
      return toast.id
    },
    dismiss,
    dismissAll: () => {
      for (const timer of timers.values()) {
        clearTimeout(timer)
      }
      timers.clear()
      toasts.value = []
    },
  }
}

function dismiss(id: number): void {
  const timer = timers.get(id)
  if (timer) {
    clearTimeout(timer)
    timers.delete(id)
  }
  toasts.value = toasts.value.filter((toast) => toast.id !== id)
}
