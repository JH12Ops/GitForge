# Code signing

How GitForge release bundles are signed.

## macOS (current CI)

`release.yml` builds the Go binary into the tracked `GitForge.app` bundle, then re-seals it with an ad-hoc signature: `codesign --force --deep --sign -`.

| CI secrets | Result |
|------------|--------|
| None | Ad-hoc signed `.app.zip` (first launch may need **Right-click → Open**) |
| Apple certificate only | Developer ID signed |
| Certificate + notarization secrets | Signed + notarized (opens normally) |

Secrets (`APPLE_CERTIFICATE`, `APPLE_CERTIFICATE_PASSWORD`, `APPLE_SIGNING_IDENTITY`,
`APPLE_ID`, `APPLE_PASSWORD`, `APPLE_TEAM_ID`) live in GitHub repo settings,
never in the tree. Wire them into the release workflow's build step (see
`DEVELOPER.md` “macOS code signing”).

## Release checklist

Before cutting a release:

1. CI already runs the typecheck/test/vet gates in `release.yml` before packaging.
2. macOS: extend the build step with `codesign --force --options runtime --sign "$APPLE_SIGNING_IDENTITY"`, then `xcrun notarytool submit` + `xcrun stapler staple` when notarizing.
3. Keep git spawn validation (`ValidateRepo`) intact — every added gateway capability widens the attack surface.

## Hardening notes

- Renderer has no shell access — git runs only in Go via argv arrays (no interpolation of renderer input).
- Gateway commands are a fixed allowlist registered in `backend/gateway.go`.
- Never commit certificates, `.p12` files, or Apple IDs. Rotate `APPLE_PASSWORD` if it ever touches the tree or logs.
