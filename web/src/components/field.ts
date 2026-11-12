import type { ComputedRef, InjectionKey } from 'vue'

/**
 * What a surrounding field hands to the control it wraps, so a control never
 * has to be told its own id, how it is described or whether it is invalid: it
 * reads this instead. A control that renders outside a field falls back to its
 * own `invalid`/`disabled` props and whatever label the caller supplies.
 */
export interface FieldContext {
  /** The id the field's label points at. */
  id: string
  /** The ids of the hint and the error, for aria-describedby. */
  describedBy: ComputedRef<string | undefined>
  /** Whether the field currently shows an error. */
  invalid: ComputedRef<boolean>
  /** Whether the control has to be filled in before the form can submit. */
  required: ComputedRef<boolean>
  /** Whether the field disables the control it wraps. */
  disabled: ComputedRef<boolean>
}

export const fieldKey: InjectionKey<FieldContext> = Symbol('wk-field')
