// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/** A catalogue: nested objects, with a string at every leaf. */
export interface MessageTree {
  [key: string]: string | MessageTree
}
