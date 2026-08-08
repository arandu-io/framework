# Third-party notices

Arandu is MIT licensed (see `LICENSE.md`). This file covers the third-party
work that is **embedded** in `view/assets/` and therefore redistributed inside
every binary built with this framework — including binaries built by people who
never downloaded any of it.

That is what makes this file necessary rather than polite. `go:embed` puts these
bytes in the executable, so every user of Arandu becomes a redistributor, and a
redistributor of MIT-licensed code owes the copyright notice. There is no CDN
and no `node_modules` to point at instead (RULE 13): the notice has to travel
with the repository.

Keeping it current is checked, not remembered: `TestEveryEmbeddedAssetIsCredited`
in `view/third_party_test.go` fails when a file appears in `view/assets/`
without an entry here, and when the version recorded here stops matching the
version inside the file.

---

## htmx — `view/assets/htmx.min.js`

| | |
|---|---|
| Version | 2.0.4 |
| Author | Big Sky Software |
| Home | https://htmx.org |
| License | Zero-Clause BSD (0BSD) |

0BSD asks for no notice at all — it is the one license here that does not
require this section. It is recorded anyway, because a list that documents only
what is compulsory does not tell a reader what is in the binary.

```
Zero-Clause BSD
=============

Permission to use, copy, modify, and/or distribute this software for
any purpose with or without fee is hereby granted.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL
WARRANTIES WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES
OF MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE
FOR ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY
DAMAGES WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN
AN ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT
OF OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.
```

---

## Alpine.js — `view/assets/alpine.min.js`

| | |
|---|---|
| Version | 3.14.8 |
| Author | Caleb Porzio and contributors |
| Home | https://alpinejs.dev |
| License | MIT |

```
MIT License

Copyright © 2019-2021 Caleb Porzio and contributors

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

## Tailwind CSS — `view/assets/app.css`

| | |
|---|---|
| Version | 4.3.3 |
| Author | Tailwind Labs, Inc. |
| Home | https://tailwindcss.com |
| License | MIT |

`app.css` is compiled output, not a copy of the distribution: the Arandu source
in `view/assets/app.src.css` goes through the standalone `tailwindcss` binary,
and what comes out contains Tailwind's own preflight and utility declarations.
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

## Basecoat — `view/assets/basecoat.bundle.js`

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

Listed so that every file in `view/assets/` is accounted for, and so that
adding one is a decision somebody wrote down rather than an omission.

- `view/assets/theme.js` — Arandu, MIT, covered by `LICENSE.md`. It reads the
  theme somebody chose out of localStorage and applies it before the first
  paint. It contains no third-party code.
- `view/assets/app.src.css` — Arandu, MIT, covered by `LICENSE.md`. It is the
  Tailwind input this project wrote; it contains no third-party code, only
  `@import "tailwindcss"`, which is a build instruction.
