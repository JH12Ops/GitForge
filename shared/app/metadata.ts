import packageJson from '../../package.json'

export const APP_NAME = 'GitForge'
export const APP_VERSION = packageJson.version

// Short commit hash injected by vite.config.ts (define) for local,
// non-CI builds; empty for release builds. The `typeof` guard keeps the
// reference safe under vitest/node, which run without the define.
declare const __LOCAL_BUILD_HASH__: string
export const APP_LOCAL_BUILD_HASH =
  typeof __LOCAL_BUILD_HASH__ === 'string' ? __LOCAL_BUILD_HASH__ : ''
export const APP_BUILD_LABEL = APP_LOCAL_BUILD_HASH
  ? `Local Build: ${APP_LOCAL_BUILD_HASH}`
  : ''
export const APP_DESCRIPTION = packageJson.description
export const APP_AUTHOR = packageJson.author
export const APP_LICENSE = packageJson.license
export const APP_HOMEPAGE = 'https://github.com/JH12Ops/GitForge'
export const APP_REPOSITORY = 'https://github.com/JH12Ops/GitForge'
export const APP_AUTHOR_URL = ''
export const APP_MAKER = 'gitforge'
export const APP_SPONSOR_URL = ''
