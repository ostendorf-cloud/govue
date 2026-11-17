// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package legal_test

import (
	"errors"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/ostendorf-cloud/govue/legal"
)

// filler is visible text of a plausible length, different per language.
func filler(lang string) string {
	return strings.Repeat("Example Provider, 1 Example Street, "+lang+". ", 4)
}

// page renders a prerendered page the way the Vue components mark it.
func page(kind, lang, body string) string {
	return `<!doctype html><html lang="` + lang + `"><head><title>t</title></head><body>` +
		`<main><div data-govue-legal="` + kind + `" lang="` + lang + `" class="x">` + body + `</div></main>` +
		`</body></html>`
}

func site() (fstest.MapFS, legal.Pages) {
	fsys := fstest.MapFS{}
	pages := legal.Pages{}
	for _, lang := range []string{"de", "en"} {
		imprint := lang + "/imprint.html"
		privacy := lang + "/privacy.html"
		fsys[imprint] = &fstest.MapFile{Data: []byte(page("imprint", lang, "<p>"+filler(lang)+"</p>"))}
		fsys[privacy] = &fstest.MapFile{Data: []byte(page("privacy", lang, "<p>"+filler(lang)+"</p>"))}
		pages[lang] = legal.Page{Imprint: imprint, Privacy: privacy}
	}
	return fsys, pages
}

func TestCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(fstest.MapFS, legal.Pages)
		want   error
	}{
		{name: "a site with both pages in both languages passes"},
		{
			name:   "no English pages listed",
			change: func(_ fstest.MapFS, p legal.Pages) { delete(p, "en") },
			want:   legal.ErrNoLanguage,
		},
		{
			name:   "no German pages listed",
			change: func(_ fstest.MapFS, p legal.Pages) { delete(p, "de") },
			want:   legal.ErrNoLanguage,
		},
		{
			name: "no imprint listed",
			change: func(_ fstest.MapFS, p legal.Pages) {
				p["de"] = legal.Page{Privacy: p["de"].Privacy}
			},
			want: legal.ErrNoPage,
		},
		{
			name: "no privacy page listed",
			change: func(_ fstest.MapFS, p legal.Pages) {
				p["en"] = legal.Page{Imprint: p["en"].Imprint}
			},
			want: legal.ErrNoPage,
		},
		{
			name:   "an imprint file that was never rendered",
			change: func(f fstest.MapFS, _ legal.Pages) { delete(f, "de/imprint.html") },
			want:   legal.ErrNoPage,
		},
		{
			name: "a page without the mark",
			change: func(f fstest.MapFS, _ legal.Pages) {
				f["en/privacy.html"] = &fstest.MapFile{Data: []byte("<main><p>" + filler("en") + "</p></main>")}
			},
			want: legal.ErrNoMarker,
		},
		{
			name: "the mark of the other page kind",
			change: func(f fstest.MapFS, _ legal.Pages) {
				f["en/privacy.html"] = &fstest.MapFile{Data: []byte(page("imprint", "en", filler("en")))}
			},
			want: legal.ErrNoMarker,
		},
		{
			name: "a mark inside a comment does not count",
			change: func(f fstest.MapFS, _ legal.Pages) {
				f["en/imprint.html"] = &fstest.MapFile{
					Data: []byte("<!-- " + page("imprint", "en", filler("en")) + " --><main></main>"),
				}
			},
			want: legal.ErrNoMarker,
		},
		{
			name: "English text served under the German language",
			change: func(f fstest.MapFS, _ legal.Pages) {
				f["de/privacy.html"] = &fstest.MapFile{Data: []byte(page("privacy", "en", filler("de")))}
			},
			want: legal.ErrWrongLanguage,
		},
		{
			name: "a page with no lang on the mark",
			change: func(f fstest.MapFS, _ legal.Pages) {
				f["de/imprint.html"] = &fstest.MapFile{
					Data: []byte(`<div data-govue-legal="imprint">` + filler("de") + `</div>`),
				}
			},
			want: legal.ErrWrongLanguage,
		},
		{
			name: "an empty shell",
			change: func(f fstest.MapFS, _ legal.Pages) {
				f["en/imprint.html"] = &fstest.MapFile{Data: []byte(page("imprint", "en", "<p>Imprint</p>"))}
			},
			want: legal.ErrNoText,
		},
		{
			name: "text hidden in a script does not count",
			change: func(f fstest.MapFS, _ legal.Pages) {
				f["en/imprint.html"] = &fstest.MapFile{
					Data: []byte(page("imprint", "en", "<script>"+filler("en")+"</script>")),
				}
			},
			want: legal.ErrNoText,
		},
		{
			name: "the same text in both languages",
			change: func(f fstest.MapFS, _ legal.Pages) {
				f["en/privacy.html"] = &fstest.MapFile{Data: []byte(page("privacy", "en", filler("de")))}
			},
			want: legal.ErrSameText,
		},
		{
			name: "nested elements of the same kind do not end the mark early",
			change: func(f fstest.MapFS, _ legal.Pages) {
				body := "<div><div>" + filler("en") + "</div></div>"
				f["en/imprint.html"] = &fstest.MapFile{Data: []byte(page("imprint", "en", body))}
			},
		},
		{
			name: "text after a nested element still counts",
			change: func(f fstest.MapFS, _ legal.Pages) {
				body := "<div>x</div>" + filler("en")
				f["en/imprint.html"] = &fstest.MapFile{Data: []byte(page("imprint", "en", body))}
			},
		},
		{
			name: "text outside the mark does not count",
			change: func(f fstest.MapFS, _ legal.Pages) {
				f["en/imprint.html"] = &fstest.MapFile{
					Data: []byte(page("imprint", "en", "<p>short</p>") + "<p>" + filler("en") + "</p>"),
				}
			},
			want: legal.ErrNoText,
		},
		{
			name: "a third language is held to the same rules",
			change: func(_ fstest.MapFS, p legal.Pages) {
				p["fr"] = legal.Page{Imprint: "fr/imprint.html", Privacy: "fr/privacy.html"}
			},
			want: legal.ErrNoPage,
		},
		{
			name: "attribute values are entity decoded and quotes may be single",
			change: func(f fstest.MapFS, _ legal.Pages) {
				f["en/imprint.html"] = &fstest.MapFile{
					Data: []byte(`<div class='a' data-govue-legal='imprint' lang='EN'>` + filler("en") + `</div>`),
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fsys, pages := site()
			if tt.change != nil {
				tt.change(fsys, pages)
			}

			err := legal.Check(fsys, pages)

			if tt.want == nil {
				if err != nil {
					t.Fatalf("Check() = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("Check() = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCheckReportsEveryProblem(t *testing.T) {
	t.Parallel()
	fsys, pages := site()
	delete(fsys, "de/imprint.html")
	delete(pages, "en")

	err := legal.Check(fsys, pages)

	if !errors.Is(err, legal.ErrNoPage) || !errors.Is(err, legal.ErrNoLanguage) {
		t.Fatalf("Check() = %v, want both ErrNoPage and ErrNoLanguage", err)
	}
}

func TestCheckNamesTheFile(t *testing.T) {
	t.Parallel()
	fsys, pages := site()
	delete(fsys, "de/imprint.html")

	err := legal.Check(fsys, pages)

	if err == nil || !strings.Contains(err.Error(), "imprint de") ||
		!strings.Contains(err.Error(), "de/imprint.html") {
		t.Fatalf("Check() = %v, want the page kind, language and file named", err)
	}
}

func TestCheckFailsForNoSiteAtAll(t *testing.T) {
	t.Parallel()

	err := legal.Check(fstest.MapFS{}, nil)

	if !errors.Is(err, legal.ErrNoLanguage) {
		t.Fatalf("Check() = %v, want ErrNoLanguage", err)
	}
}
