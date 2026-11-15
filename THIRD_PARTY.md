# Third-party notices

Arandu is MIT licensed (see `LICENSE.md`). This file covers the third-party
work that is **embedded** by `github.com/arandu-io/hesape/view` and therefore
redistributed inside every binary built with this framework — including
binaries built by people who never downloaded any of it.

That is what makes this file necessary rather than polite. `go:embed` puts these
bytes in the executable, so every user of Arandu becomes a redistributor, and a
redistributor of MIT-licensed code owes the copyright notice. There is no CDN
and no `node_modules` to point at instead (RULE 13): the notice has to travel
with the repository.

**No copy of these files lives in this repository.** They are `view/assets/` in
the `hesape` release recorded in `go.mod`, and that module carries the same
notice in `view/THIRD_PARTY.md`, beside the bytes. The versions below describe
the files that release embeds; `view/third_party_test.go` there is what checks
each version against the file it was read from, which is a question only the
module holding the file can answer.

What is checked here is what this repository can answer for on its own:
`TestTheLicenseTextsAreComplete` in `tests/Unit/view/third_party_test.go` fails
when a required licence text goes missing from this file, and
`TestNoAssetFileLivesInThisModule` in `tests/Unit/view/no_assets_test.go` fails
if an asset file reappears under `view/assets/` here — where nothing would embed
it, and where a reader would take it for the file a browser receives.

---

## YPY — `ypy.min.js` and `ypy-csrf.min.js`

| | |
|---|---|
| Version | 0.1.0-dev |
| Author | HYZIS - SERVICOS DIGITAIS LTDA - EPP |
| Home | none published yet; the public address is recorded here when the first release is tagged |
| License | MIT |

The core of YPY, the hypermedia framework the pages this framework draws are
written for, and its `ypy-csrf` extension, which sends the token of the
`ypy-csrf` meta tag on every request that changes state. HYZIS wrote it and it
is not a third party in the sense of the rest of this list; it is recorded here
because it is a separate work with its own licence, embedded by hesape and
therefore redistributed in every binary.

```
The MIT License (MIT)

Copyright (c) HYZIS - SERVICOS DIGITAIS LTDA - EPP

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in
all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
THE SOFTWARE.
```

---

## Tailwind CSS — `app.css`

| | |
|---|---|
| Version | 4.3.3 |
| Author | Tailwind Labs, Inc. |
| Home | https://tailwindcss.com |
| License | MIT |

`app.css` is compiled output, not a copy of the distribution: the Arandu source
in `app.src.css` goes through the standalone `tailwindcss` binary, and what
comes out contains Tailwind's own preflight and utility declarations.
The compiler preserves its banner at the top of the file, which is the version
recorded above:

```
/*! tailwindcss v4.3.3 | MIT License | https://tailwindcss.com */
```

```
MIT License

Copyright (c) Tailwind Labs, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

---

## Basecoat — `basecoat.bundle.js`

| | |
|---|---|
| Version | 1.0.2 |
| Author | Ronan Berder |
| Home | https://basecoatui.com |
| License | MIT |

The bundle is the concatenation of seven of the component scripts upstream
ships: the registry the others register into, plus the dropdown menu, the
popover, the select, the sidebar, the tabs and the toast. Each is a plain IIFE
with no import and no export, so concatenating them is the whole build.

Its stylesheet is **not** here. That ships with the project rather than with the
framework, vendored under `resources/css/basecoat/`, with this same licence
beside it -- a project owns its design system, and changing how a button looks
should be editing a file you can see rather than upgrading a dependency.

```
MIT License

Copyright (c) 2025 Ronan Berder

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

---

## Arandu's own files

Listed so that every embedded file is accounted for, and so that adding one is
a decision somebody wrote down rather than an omission.

- `theme.js` — Arandu, MIT, covered by `LICENSE.md`. It reads the theme somebody
  chose out of localStorage and applies it before the first paint. It contains
  no third-party code.
- `ui.js` — Arandu, MIT, covered by `LICENSE.md`. It is the delegated client
  behaviour: copy buttons, the theme toggle, the combobox, the command palette
  and the range slider, all dispatched from `data-*` attributes read as data. It
  contains no third-party code, and it is why no directive framework is embedded
  here — compiling an attribute into a function needs `unsafe-eval`, and the
  Content-Security-Policy this framework sets is `script-src 'self'`.
- `app.src.css` — Arandu, MIT, covered by `LICENSE.md`. It is the Tailwind input
  this project wrote; it contains no third-party code, only
  `@import "tailwindcss"`, which is a build instruction. It is the one file here
  that is not itself served: `app.css` is what it compiles to.
