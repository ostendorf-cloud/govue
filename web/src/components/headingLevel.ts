// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/** The level of a component title. Level 1 is the page's own heading. */
export type HeadingLevel = 2 | 3 | 4 | 5 | 6

const headingLevels: readonly number[] = [2, 3, 4, 5, 6]

/** The tag for a level. A value the types would have refused still never
 *  becomes an invalid tag: it renders h2. */
export function headingTag(level: number): string {
  return headingLevels.includes(level) ? `h${level}` : 'h2'
}
