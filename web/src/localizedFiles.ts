// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

/**
 * localizedFiles turns a route table into the files of a language site: one
 * page per public route and language, and the sitemap. It is the `files`
 * export a site's SSR bundle hands to `govue-prerender --out-dir`.
 *
 * The language logic stays here, in TypeScript compiled into the site's
 * bundle, so the plain JavaScript CLI only writes what it gets. The render
 * entry stays generic: the site's render callback installs the language.
 */

import type { LocaleCode } from './i18n/catalogues'
import {
  hreflangLinks,
  localizeRoutes,
  sitemapXml,
  validateOrigin,
  type LocalizedRoute,
  type RouteDef,
} from './i18n/routes'
import { DEFAULT_PLACEHOLDER, injectIntoTemplate } from './template'

/** The marker in the template head where the canonical and hreflang links go. */
export const HEAD_PLACEHOLDER = '<!--app-head-->'

/** One file of the build, its path relative to the output directory. */
export interface PrerenderFile {
  /** Relative, with `/` as separator, such as `de/impressum/index.html`. */
  path: string
  content: string
}

export interface LocalizedFilesOptions {
  /** The site origin, `https://host[:port]`, for canonical, hreflang and sitemap. */
  origin: string
  /**
   * The built `index.html`. It holds `<!--app-head-->` in the head,
   * `<!--app-html-->` in the mount element and one `<html>` start tag.
   */
  template: string
  /**
   * Returns the markup of one variant, usually through `render` with
   * `createGovueI18n({ locale: variant.locale })` installed in `setup`.
   * It is called once per variant, one after another.
   */
  render: (variant: LocalizedRoute) => string | Promise<string>
}

/**
 * localizedFiles renders every public route once per language and returns
 * the pages and `sitemap.xml`. `/de/` is written as `de/index.html` and
 * `/de/impressum` as `de/impressum/index.html`. Each page gets its `lang`,
 * a self referential canonical link and the hreflang alternates with
 * x-default. The template, the routes and the origin are checked before the
 * first render, and any error rejects.
 */
export async function localizedFiles(
  routes: readonly RouteDef[],
  options: LocalizedFilesOptions,
): Promise<PrerenderFile[]> {
  const { origin, template, render } = options
  validateOrigin(origin)
  const variants = localizeRoutes(routes)
  const sitemap = sitemapXml(routes, origin)
  // A dry run over the template, so a broken one fails before any render.
  fillTemplate(template, 'de', '', '')

  const files: PrerenderFile[] = []
  for (const variant of variants) {
    const html = await render(variant)
    if (typeof html !== 'string') {
      throw new Error(`govue prerender: the render of "${variant.path}" must resolve to a string`)
    }
    files.push({
      path: filePath(variant.path),
      content: fillTemplate(template, variant.locale, hreflangLinks(variant, origin), html),
    })
  }
  files.push({ path: 'sitemap.xml', content: sitemap })
  return files
}

/**
 * The page markup goes in last, so a placeholder or an `<html>` tag inside
 * it is never taken for the template's own.
 */
function fillTemplate(template: string, lang: LocaleCode, head: string, html: string): string {
  const page = injectIntoTemplate(setHtmlLang(template, lang), head, HEAD_PLACEHOLDER)
  return injectIntoTemplate(page, html, DEFAULT_PLACEHOLDER)
}

/** `/de/` is `de/index.html`, `/de/impressum` is `de/impressum/index.html`. */
function filePath(path: string): string {
  const relative = path.slice(1)
  return relative.endsWith('/') ? `${relative}index.html` : `${relative}/index.html`
}

const SPACE = /[\s/]*/y
const ATTRIBUTE = /([^\s"'>/=]+)(?:\s*=\s*(?:"[^"]*"|'[^']*'|[^\s"'=<>`]+))?/y

/** The offsets of every `<html` start tag outside a comment. */
function htmlStartTags(template: string): number[] {
  const found: number[] = []
  let i = 0
  while (i < template.length) {
    if (template.startsWith('<!--', i)) {
      const end = template.indexOf('-->', i + 4)
      if (end === -1) break
      i = end + 3
      continue
    }
    if (
      template.slice(i, i + 5).toLowerCase() === '<html' &&
      (i + 5 === template.length || /[\s/>]/.test(template.charAt(i + 5)))
    ) {
      found.push(i)
    }
    i++
  }
  return found
}

/**
 * setHtmlLang rewrites the `<html>` start tag of a template with
 * `lang="<lang>"` first and every other attribute kept as written. It
 * throws when the template has no such tag, has two or leaves it open.
 */
export function setHtmlLang(template: string, lang: LocaleCode): string {
  const starts = htmlStartTags(template)
  const start = starts[0]
  if (start === undefined) {
    throw new Error('govue prerender: the template has no <html> start tag to carry the language')
  }
  if (starts.length > 1) {
    throw new Error('govue prerender: the template has the <html> start tag more than once')
  }
  const kept: string[] = []
  let pos = start + 5
  for (;;) {
    SPACE.lastIndex = pos
    SPACE.exec(template)
    pos = SPACE.lastIndex
    if (template.charAt(pos) === '>') break
    ATTRIBUTE.lastIndex = pos
    const match = ATTRIBUTE.exec(template)
    if (!match) {
      throw new Error('govue prerender: the <html> start tag of the template is not closed')
    }
    if (match[1]?.toLowerCase() !== 'lang') kept.push(match[0])
    pos = ATTRIBUTE.lastIndex
  }
  const tag = [`<html lang="${lang}"`, ...kept].join(' ') + '>'
  return template.slice(0, start) + tag + template.slice(pos + 1)
}
