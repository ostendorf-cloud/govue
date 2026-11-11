import { existsSync, readFileSync, readdirSync } from 'node:fs'
import { basename, resolve } from 'node:path'

import { describe, expect, it } from 'vitest'

import { themeNames } from '../composables/useTheme'

const stylesDir = resolve(import.meta.dirname, '.')
const themesDir = resolve(stylesDir, 'themes')
const componentsDir = resolve(stylesDir, '..', 'components')

/*
 * Two things are checked here.
 *
 * Every theme fills the whole contract: a token declared in base.css and
 * forgotten in a theme leaves that theme on the neutral default for one role,
 * which looks like a bug nobody can place.
 *
 * Every utility a component uses resolves to a token this package declares.
 * Tailwind drops a class it cannot resolve without a word, so a typo like
 * bg-surfce simply renders unstyled, and nobody notices until a page looks
 * wrong. This catches it in CI instead. No component exists yet, so the check
 * runs over an empty set until the first one lands.
 */

/** The utility prefixes whose value has to come from the theme. */
const CHECKED_PREFIXES = [
  'bg',
  'text',
  'border',
  'outline',
  'ring',
  'fill',
  'stroke',
  'divide',
  'shadow',
  'rounded',
  'font',
  'ease',
  'max-w',
  'min-w',
  'w',
  'h',
  'min-h',
  'size',
  'gap',
  'gap-x',
  'gap-y',
  'space-x',
  'space-y',
  'p',
  'px',
  'py',
  'pt',
  'pr',
  'pb',
  'pl',
  'm',
  'mx',
  'my',
  'mt',
  'mr',
  'mb',
  'ml',
]

/**
 * The built in values Tailwind ships, as far as the components use them. A new
 * entry here is a deliberate statement that the value is Tailwind's and not
 * ours.
 */
const BUILT_IN = new Set([
  // Colours and keywords every colour utility accepts.
  'transparent',
  'current',
  'inherit',
  'white',
  'black',
  'none',
  // Sizes that are Tailwind's own scale, not the theme's.
  'auto',
  'full',
  'screen',
  'min',
  'max',
  'fit',
  'px',
  'dvh',
  'svh',
  // Weights.
  'thin',
  'light',
  'normal',
  'medium',
  'semibold',
  'bold',
  // Easings.
  'in',
  'out',
  'in-out',
  'linear',
  'initial',
  // Text layout, which shares the text- prefix with colours and sizes.
  'left',
  'center',
  'right',
  'justify',
  'start',
  'end',
  'balance',
  'pretty',
  'nowrap',
  'wrap',
  'ellipsis',
  'clip',
  // One side or one axis of a border, which is a width and not a colour.
  't',
  'r',
  'b',
  'l',
  'x',
  'y',
  // Borders and rings without a colour.
  'solid',
  'dashed',
  'dotted',
  'hidden',
  'offset',
  // Fonts.
  'sans',
  'mono',
  'serif',
])

/** declaredTokens reads every token name out of the theme block. */
function declaredTokens(): Set<string> {
  const css = readFileSync(resolve(stylesDir, 'base.css'), 'utf8')
  const names = new Set<string>()
  for (const match of css.matchAll(
    /--(?:color|text|shadow|radius|container|spacing|font|ease)-([\w-]+):/g,
  )) {
    names.add(match[1])
  }
  return names
}

/** colourTokens reads the --color-* names one stylesheet declares. */
function colourTokens(file: string): Set<string> {
  const css = readFileSync(file, 'utf8')
  const names = new Set<string>()
  for (const match of css.matchAll(/--color-([\w-]+):/g)) {
    names.add(match[1])
  }
  return names
}

function themeFiles(): string[] {
  return readdirSync(themesDir)
    .filter((name) => name.endsWith('.css'))
    .sort()
}

/** componentFiles is every single file component of the package. */
function componentFiles(): string[] {
  if (!existsSync(componentsDir)) {
    return []
  }
  return readdirSync(componentsDir)
    .filter((name) => name.endsWith('.vue'))
    .map((name) => resolve(componentsDir, name))
}

/** classesOf pulls the class names out of the class attributes of a template. */
function classesOf(source: string): string[] {
  const classes: string[] = []
  // Both forms: class="a b" and :class="['a b', condition ? 'c' : '']", plus
  // the class lists a script block builds as plain strings.
  for (const match of source.matchAll(/'([^'\n]*)'|"([^"\n]*)"/g)) {
    const value = match[1] ?? match[2] ?? ''
    if (!/[a-z]-/.test(value)) {
      continue
    }
    for (const candidate of value.split(/\s+/)) {
      // A nested class list leaves quotes, commas and brackets on the ends.
      const cleaned = candidate.replace(/^[[\]'"`,]+|[[\]'"`,]+$/g, '')
      // Anything else is not a class name, so it is not this test's business.
      if (cleaned !== '' && /^[-A-Za-z0-9:_./%!]+$/.test(cleaned)) {
        classes.push(cleaned)
      }
    }
  }
  return classes
}

/** unresolved reports the classes whose value is neither a token nor built in. */
function unresolved(classes: string[], tokens: Set<string>): string[] {
  const problems: string[] = []
  for (const raw of classes) {
    // Variants and the important marker are not part of the value.
    const utility = raw.split(':').at(-1)?.replace(/^!/, '') ?? ''
    // An arbitrary value is the caller's own and cannot be checked here.
    if (utility.includes('[') || utility === '') {
      continue
    }
    const prefix = CHECKED_PREFIXES.filter((candidate) => utility.startsWith(`${candidate}-`)).sort(
      (a, b) => b.length - a.length,
    )[0]
    if (!prefix) {
      continue
    }
    const value = utility.slice(prefix.length + 1)
    if (
      tokens.has(value) ||
      BUILT_IN.has(value) ||
      // Tailwind's numeric scales: spacing, widths, fractions, opacities.
      /^\d+(\.\d+)?(\/\d+)?$/.test(value)
    ) {
      continue
    }
    problems.push(utility)
  }
  return [...new Set(problems)]
}

describe('the token contract', () => {
  const tokens = declaredTokens()

  it('declares the roles a theme overrides', () => {
    for (const token of ['bg', 'surface', 'text-muted', 'accent-strong', 'danger-on', 'focus']) {
      expect(tokens, `--color-${token}`).toContain(token)
    }
  })

  it.each(themeFiles())('%s fills every role base.css declares', (name) => {
    const base = colourTokens(resolve(stylesDir, 'base.css'))
    const theme = colourTokens(resolve(themesDir, name))
    expect([...base].filter((token) => !theme.has(token))).toEqual([])
  })

  // themeNames cannot discover the directory itself: it ships in the browser
  // bundle, where there is no filesystem. This case is what keeps the one list
  // a consumer reads in step with the files on disk. Its order is the order an
  // app would offer the themes in rather than the directory order, so the two
  // are compared as sets.
  it('lists every theme on disk in themeNames', () => {
    expect([...themeNames].sort()).toEqual(
      themeFiles()
        .map((name) => name.replace(/\.css$/, ''))
        .sort(),
    )
  })

  it.each(themeFiles())('%s invents no role of its own', (name) => {
    const base = colourTokens(resolve(stylesDir, 'base.css'))
    const theme = colourTokens(resolve(themesDir, name))
    // A token only a theme knows is one no component can use, so it is dead
    // weight at best and a role that belongs in the contract at worst.
    expect([...theme].filter((token) => !base.has(token))).toEqual([])
  })
})

describe('the components only use declared tokens', () => {
  const tokens = declaredTokens()

  // One case over every component rather than one per file: the set is empty
  // until the first component lands, and an empty it.each is an error.
  it('resolves every utility of every component', () => {
    const problems = componentFiles().flatMap((file) =>
      unresolved(classesOf(readFileSync(file, 'utf8')), tokens).map(
        (utility) => `${basename(file)}: ${utility}`,
      ),
    )
    expect(problems).toEqual([])
  })

  it('catches a typo', () => {
    // The check itself has to fail on something; this is that proof.
    expect(unresolved(['bg-surfce', 'text-text-mutted'], tokens)).toEqual([
      'bg-surfce',
      'text-text-mutted',
    ])
    expect(unresolved(['bg-surface', 'text-text-muted', 'hover:bg-surface-hover'], tokens)).toEqual(
      [],
    )
  })
})
