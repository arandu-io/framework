# Skills

Procedures an assistant follows when changing this module.

They live in `.agents/skills/<name>/SKILL.md`, which is the path the coding
assistants read from — Cursor, Codex, Cline, Copilot, Gemini CLI, Amp, OpenCode,
Warp, Zed and the rest all look there. It is one directory rather than a file
per vendor, so a skill written once is read by whatever this project is being
written with.

Each file opens with frontmatter carrying a `name` and a `description`. The
description is what a tool reads to decide whether the skill is relevant, so it
names the situation you are in rather than the topic it covers.

| skill | when it fires |
| --- | --- |
| `framework-bridge` | the file you are about to change is one line long, or the package is one of the fifteen that hold no implementation |
| `framework-module` | a module, the boot sequence, the pipeline, or a setting read at start |
| `framework-grant` | anything that reaches stored data, or a signature that will not compile without a Grant |
| `framework-view` | the runtime a compiled view calls, the registries, or `view.Page` |

## Why these exist

This is a library, and every change compiles into every project that imports it.
Three of the four skills exist because the three ways to get that wrong here are
specific and none of them is obvious from the file you are looking at.

The first is writing the fix in the wrong repository. Fifteen of the twenty
packages here hold no implementation — they are old names pointing at
`github.com/arandu-io/hesape`, with a death date in their doc comment — so a
plausible-looking edit to `security/session.go` or `view/render.go` is very
likely an edit to the wrong module, and the compiler will not say so.

The second is breaking the module contract. `foundation.Module` is the public
interface of the whole ecosystem, and nine optional interfaces hang off it, each
asked for exactly once by a type assertion in `foundation/application.go`. A
module that implements one with the wrong signature is simply never asked, and
nothing reports it.

The third is the one the framework exists to prevent: a path to stored data
that nobody authorized. `security.Grant` cannot be built outside the package
that issues it, and the two fixtures under `data/testdata/` are compiled by a
test that requires the compiler to refuse them.

The fourth is narrower and it is a registry problem. The view runtime keeps
three package-level tables, and a copy of one of them renders as "no view named
x" over a file that is on disk.

## Adding your own

A skill in this directory travels with the repository. Keep it a procedure
rather than a description: a file that says "read the documentation" never
changes what anybody does. Every command in one has to be a command that runs,
and every number in one has to be a number somebody measured.
