// jest-dom's matchers, so an assertion reads as what a user would see:
// toBeVisible, toHaveAccessibleName, toBeDisabled.
import '@testing-library/jest-dom/vitest'

import { cleanup } from '@testing-library/vue'
import { afterEach } from 'vitest'

// Testing Library only registers this itself when Vitest runs with globals, and
// this package does not: without it one test finds the elements of the
// previous one.
afterEach(cleanup)
