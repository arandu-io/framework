# Release Notes

## [v0.68.0](https://github.com/arandu-io/framework/compare/v0.67.0...v0.68.0) - 2026-11-14

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.67.0...v0.68.0

## [v0.68.0](https://github.com/arandu-io/framework/compare/v0.67.0...v0.68.0) - 2026-11-14

- **bootstrap: `Configuration.Brand`** is the application's name as a visitor's browser is given it, from `APP_NAME` in slug (`Peráta` is `perata`). An unwritten `APP_NAME` is the default brand, `arandu`, so a deployment that never named itself keeps its cookies. An application assigns the field to name itself otherwise.
- **foundation: the cookies are named after the brand.** The session store, the CSRF issuer and the Application's flash are built over the same `session.WithCookieNames`, so `perata_session`, `perata_csrf_guest`, `perata_flash` and `perata_intended` are written and read by the same value.
- **foundation: the assets are served under the brand's prefix.** `view.UseBrand` runs when the Application is built, the view module registers `/_perata/assets/{hash}/{name}` beside the old `/_arandu/assets/`, and the brand's namespace is reserved like the framework's own: the route is answered outside the application's pipeline, and a module or an application route cannot answer under it. The token refresh answers at `/_perata/csrf` too (`CSRFRefreshUnder`).
- **security: `NewSessionStore`, `NewCSRF` and `NewFlash`** take a trailing `...session.CookieOption`.
- **Requires hesape v0.106.0**, which issues the CSRF token when a view asks for it (an API response sets no cookie) and marks a page that carries one `no-store, private`. See `UPGRADE.md`: the first deploy of an application that writes `APP_NAME` signs everybody out once.

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.67.0...v0.68.0

## [v0.67.0](https://github.com/arandu-io/framework/compare/v0.66.0...v0.67.0) - 2026-10-25

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.66.0...v0.67.0

## [v0.66.0](https://github.com/arandu-io/framework/compare/v0.65.0...v0.66.0) - 2026-10-25

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.65.0...v0.66.0

## [v0.65.0](https://github.com/arandu-io/framework/compare/v0.64.0...v0.65.0) - 2026-10-23

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.64.0...v0.65.0

## [v0.64.0](https://github.com/arandu-io/framework/compare/v0.63.0...v0.64.0) - 2026-10-23

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.63.0...v0.64.0

## [v0.63.0](https://github.com/arandu-io/framework/compare/v0.62.1...v0.63.0) - 2026-10-23

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.62.1...v0.63.0

## [v0.62.1](https://github.com/arandu-io/framework/compare/v0.62.0...v0.62.1) - 2026-10-22

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.62.0...v0.62.1

## [v0.62.0](https://github.com/arandu-io/framework/compare/v0.61.0...v0.62.0) - 2026-10-22

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.61.0...v0.62.0

## [v0.61.0](https://github.com/arandu-io/framework/compare/v0.60.0...v0.61.0) - 2026-10-21

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.60.0...v0.61.0

## [v0.60.0](https://github.com/arandu-io/framework/compare/v0.59.0...v0.60.0) - 2026-10-21

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.59.0...v0.60.0

## [v0.59.0](https://github.com/arandu-io/framework/compare/v0.58.0...v0.59.0) - 2026-10-21

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.58.0...v0.59.0

## [v0.58.0](https://github.com/arandu-io/framework/compare/v0.57.1...v0.58.0) - 2026-10-19

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.57.1...v0.58.0

## [v0.57.1](https://github.com/arandu-io/framework/compare/v0.57.0...v0.57.1) - 2026-10-15

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.57.0...v0.57.1

## [v0.57.0](https://github.com/arandu-io/framework/compare/v0.56.1...v0.57.0) - 2026-10-15

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.56.1...v0.57.0

## [v0.56.1](https://github.com/arandu-io/framework/compare/v0.56.0...v0.56.1) - 2026-10-15

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.56.0...v0.56.1

## [v0.56.0](https://github.com/arandu-io/framework/compare/v0.55.1...v0.56.0) - 2026-10-10

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.55.1...v0.56.0

## [v0.55.1](https://github.com/arandu-io/framework/compare/v0.55.0...v0.55.1) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.55.0...v0.55.1

## [v0.55.0](https://github.com/arandu-io/framework/compare/v0.54.0...v0.55.0) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.54.0...v0.55.0

## [v0.54.0](https://github.com/arandu-io/framework/compare/v0.53.0...v0.54.0) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.53.0...v0.54.0

## [v0.53.0](https://github.com/arandu-io/framework/compare/v0.52.0...v0.53.0) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.52.0...v0.53.0

## [v0.52.0](https://github.com/arandu-io/framework/compare/v0.51.0...v0.52.0) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.51.0...v0.52.0

## [v0.51.0](https://github.com/arandu-io/framework/compare/v0.50.2...v0.51.0) - 2026-10-09

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.50.2...v0.51.0

## [v0.50.2](https://github.com/arandu-io/framework/compare/v0.50.1...v0.50.2) - 2026-10-03

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.50.1...v0.50.2

## [v0.50.1](https://github.com/arandu-io/framework/compare/v0.50.0...v0.50.1) - 2026-10-01

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.50.0...v0.50.1

## [v0.50.0](https://github.com/arandu-io/framework/compare/v0.49.0...v0.50.0) - 2026-10-01

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.49.0...v0.50.0

## [v0.49.0](https://github.com/arandu-io/framework/compare/v0.48.0...v0.49.0) - 2026-09-28

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.48.0...v0.49.0

## [v0.48.0](https://github.com/arandu-io/framework/compare/v0.47.1...v0.48.0) - 2026-09-17

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.47.1...v0.48.0

## [v0.47.1](https://github.com/arandu-io/framework/compare/v0.47.0...v0.47.1) - 2026-09-17

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.47.0...v0.47.1

## [v0.47.0](https://github.com/arandu-io/framework/compare/v0.46.5...v0.47.0) - 2026-09-14

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.46.5...v0.47.0

## [v0.46.5](https://github.com/arandu-io/framework/compare/v0.46.4...v0.46.5) - 2026-09-14

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.46.4...v0.46.5

## [v0.46.4](https://github.com/arandu-io/framework/compare/v0.46.3...v0.46.4) - 2026-09-09

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.46.3...v0.46.4

## [v0.46.3](https://github.com/arandu-io/framework/compare/v0.46.0...v0.46.3) - 2026-09-09

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.46.0...v0.46.3

## [v0.46.2](https://github.com/arandu-io/framework/compare/v0.46.1...v0.46.2) - 2026-09-07

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.46.1...v0.46.2

## [v0.46.1](https://github.com/arandu-io/framework/compare/v0.46.0...v0.46.1) - 2026-09-06

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.46.0...v0.46.1

## [v0.46.0](https://github.com/arandu-io/framework/compare/v0.45.0...v0.46.0) - 2026-09-05

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.45.0...v0.46.0

## [v0.45.0](https://github.com/arandu-io/framework/compare/v0.44.0...v0.45.0) - 2026-09-05

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.44.0...v0.45.0

## [v0.43.0](https://github.com/arandu-io/framework/compare/v0.42.1...v0.43.0) - 2026-09-03

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.42.1...v0.43.0

## [v0.43.0](https://github.com/arandu-io/framework/compare/v0.42.1...v0.43.0) - 2026-09-03

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.42.1...v0.43.0

## [v0.42.1](https://github.com/arandu-io/framework/compare/v0.42.0...v0.42.1) - 2026-09-02

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.42.0...v0.42.1

## [v0.42.0](https://github.com/arandu-io/framework/compare/v0.41.0...v0.42.0) - 2026-09-01

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.41.0...v0.42.0

## [v0.41.0](https://github.com/arandu-io/framework/compare/v0.40.0...v0.41.0) - 2026-08-29

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.40.0...v0.41.0

## [v0.40.0](https://github.com/arandu-io/framework/compare/v0.39.0...v0.40.0) - 2026-08-27

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.39.0...v0.40.0

## [v0.39.0](https://github.com/arandu-io/framework/compare/v0.38.1...v0.39.0) - 2026-08-27

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.38.1...v0.39.0

## [v0.38.1](https://github.com/arandu-io/framework/compare/v0.38.0...v0.38.1) - 2026-08-27

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.38.0...v0.38.1

## [v0.38.0](https://github.com/arandu-io/framework/compare/v0.37.0...v0.38.0) - 2026-08-26

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.37.0...v0.38.0

## [v0.37.0](https://github.com/arandu-io/framework/compare/v0.35.0...v0.37.0) - 2026-08-23

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.35.0...v0.37.0

## [v0.36.0](https://github.com/arandu-io/framework/compare/v0.35.0...v0.36.0) - 2026-08-23

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.35.0...v0.36.0

## [v0.35.0](https://github.com/arandu-io/framework/compare/v0.34.0...v0.35.0) - 2026-08-21

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.34.0...v0.35.0

## [v0.34.0](https://github.com/arandu-io/framework/compare/v0.33.0...v0.34.0) - 2026-08-21

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.33.0...v0.34.0

## [v0.33.0](https://github.com/arandu-io/framework/compare/v0.32.0...v0.33.0) - 2026-08-21

**Full Changelog**: https://github.com/arandu-io/framework/compare/v0.32.0...v0.33.0

Written by the release, not by hand. `.github/workflows/release.yml` publishes
the release a tag names and prepends what it said, so the tag, the release and
this file cannot disagree.

Tags cut before that workflow existed have no release and are not listed here.
`git tag` is where they are.
