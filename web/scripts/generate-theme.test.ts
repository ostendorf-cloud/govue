// parseArgs is the one piece of logic this thin CLI wrapper owns itself
// (see generate-theme.ts's header comment), so it is the one piece that
// gets its own test here rather than leaning on generate.test.ts.

import { describe, expect, it } from 'vitest'

import { parseArgs } from './generate-theme.ts'

describe('parseArgs', () => {
  it('reads --config and --out', () => {
    expect(parseArgs(['--config', 'seeds.json', '--out', 'theme.css'])).toEqual({
      help: false,
      config: 'seeds.json',
      out: 'theme.css',
    })
  })

  it('reads --help', () => {
    expect(parseArgs(['--help'])).toEqual({ help: true })
    expect(parseArgs(['-h'])).toEqual({ help: true })
  })

  it('tolerates a leading "--" separator, as pnpm and yarn forward it into argv', () => {
    expect(parseArgs(['--', '--config', 'seeds.json', '--out', 'theme.css'])).toEqual({
      help: false,
      config: 'seeds.json',
      out: 'theme.css',
    })
  })

  it('tolerates "--" anywhere, not only at the front', () => {
    expect(parseArgs(['--config', 'seeds.json', '--', '--out', 'theme.css'])).toEqual({
      help: false,
      config: 'seeds.json',
      out: 'theme.css',
    })
  })

  it('still rejects a genuinely unknown argument', () => {
    expect(() => parseArgs(['--bogus'])).toThrow('unknown argument: --bogus')
  })
})
