#!/usr/bin/env node

import {
  cp,
  lstat,
  mkdir,
  readFile,
  readdir,
  realpath,
  rm,
} from 'node:fs/promises'
import { dirname, isAbsolute, join, relative, resolve, sep } from 'node:path'

function fail(message) {
  throw new Error(`materialize-dsh-runtime: ${message}`)
}

const [
  stagingArgument,
  sourceNodeModulesArgument,
  closureManifestArgument,
  closureNodeModulesArgument,
  builtNodeModulesArgument,
] = process.argv.slice(2)
if (stagingArgument === undefined || sourceNodeModulesArgument === undefined) {
  fail('usage: materialize-dsh-runtime.mjs STAGING SOURCE_NODE_MODULES [CLOSURE_MANIFEST CLOSURE_NODE_MODULES [BUILT_NODE_MODULES]]')
}
if ((closureManifestArgument === undefined) !== (closureNodeModulesArgument === undefined)) {
  fail('closure manifest and closure node_modules must be provided together')
}
for (const path of [stagingArgument, sourceNodeModulesArgument, closureManifestArgument, closureNodeModulesArgument, builtNodeModulesArgument]) {
  if (path !== undefined && !isAbsolute(path)) fail('all paths must be absolute')
}

const staging = resolve(stagingArgument)
const nodeModules = join(staging, 'node_modules')
const sourceNodeModules = resolve(sourceNodeModulesArgument)
if (staging === sep || nodeModules === sep || sourceNodeModules === sep) {
  fail('refusing a filesystem-root path')
}

async function exists(path) {
  try {
    await lstat(path)
    return true
  } catch (error) {
    if (error !== null && typeof error === 'object' && error.code === 'ENOENT') return false
    throw error
  }
}

async function copyPackage(source, destination) {
  const nestedNodeModules = join(source, 'node_modules')
  await mkdir(dirname(destination), { recursive: true, mode: 0o700 })
  await cp(source, destination, {
    recursive: true,
    dereference: true,
    preserveTimestamps: true,
    filter: path => path !== nestedNodeModules && !path.startsWith(nestedNodeModules + sep),
  })
}

async function restoreDependencies(manifestPath, dependencySource) {
  const manifest = JSON.parse(await readFile(manifestPath, 'utf8'))
  const dependencies = Object.keys(manifest.dependencies ?? {}).sort()
  for (const dependency of dependencies) {
    const destination = join(nodeModules, dependency)
    if (await exists(destination)) continue
    const source = join(dependencySource, dependency)
    if (!(await exists(source))) fail(`deployed dependency is missing: ${dependency}`)
    await copyPackage(await realpath(source), destination)
  }
}

async function firstSymlink(directory) {
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name)
    const metadata = await lstat(path)
    if (metadata.isSymbolicLink()) return path
    if (metadata.isDirectory()) {
      const nested = await firstSymlink(path)
      if (nested !== undefined) return nested
    }
  }
  return undefined
}

async function materializeLinks() {
  let link = await firstSymlink(nodeModules)
  while (link !== undefined) {
    const segments = relative(nodeModules, link).split(sep)
    const binIndex = segments.indexOf('.bin')
    if (binIndex >= 0) {
      await rm(join(nodeModules, ...segments.slice(0, binIndex + 1)), {
        recursive: true,
        force: true,
      })
      link = await firstSymlink(nodeModules)
      continue
    }

    const source = await realpath(link)
    await rm(link, { recursive: true, force: true })
    await copyPackage(source, link)
    link = await firstSymlink(nodeModules)
  }
}

async function restoreReviewedBuildOutputs() {
  if (builtNodeModulesArgument === undefined) return
  const builtNodeModules = resolve(builtNodeModulesArgument)
  const reviewed = [
    '@deepseek-ai/dsh-subprocess-local',
    'esbuild',
    'koffi',
    'node-pty',
  ]
  for (const dependency of reviewed) {
    const destination = join(nodeModules, dependency)
    if (!(await exists(destination))) continue
    const source = join(builtNodeModules, dependency)
    if (!(await exists(source))) fail(`reviewed build output is missing: ${dependency}`)
    await rm(destination, { recursive: true, force: true })
    await copyPackage(await realpath(source), destination)
  }
}

async function topLevelPackages() {
  const packages = []
  for (const entry of await readdir(nodeModules, { withFileTypes: true })) {
    if (!entry.isDirectory() || entry.name.startsWith('.')) continue
    const path = join(nodeModules, entry.name)
    if (!entry.name.startsWith('@')) {
      packages.push(path)
      continue
    }
    for (const scoped of await readdir(path, { withFileTypes: true })) {
      if (scoped.isDirectory()) packages.push(join(path, scoped.name))
    }
  }
  return packages
}

async function findDependencySource(dependency, sources, parentSource) {
  const roots = parentSource === undefined
    ? sources
    : [join(parentSource, 'node_modules'), ...sources]
  for (const sourceRoot of roots) {
    const candidate = join(sourceRoot, dependency)
    if (await exists(candidate)) return realpath(candidate)
  }
  return undefined
}

// pnpm's dedicated deploy lock includes ordinary production dependencies, but
// the DSH plugin graph intentionally declares its runtime capabilities as
// workspace peers. Reconstruct only the required declared closure from the
// already installed, pinned source tree; never copy arbitrary workspace files.
async function restoreRequiredPeerClosure() {
  if (builtNodeModulesArgument === undefined) return
  const sources = [
    resolve(builtNodeModulesArgument),
    sourceNodeModules,
    ...(closureNodeModulesArgument === undefined ? [] : [resolve(closureNodeModulesArgument)]),
  ]
  const pending = await topLevelPackages()
  const visited = new Set()
  const sourceByDestination = new Map()
  while (pending.length > 0) {
    const packageRoot = pending.shift()
    const manifestPath = join(packageRoot, 'package.json')
    if (visited.has(manifestPath) || !(await exists(manifestPath))) continue
    visited.add(manifestPath)
    const manifest = JSON.parse(await readFile(manifestPath, 'utf8'))
    let packageSource = sourceByDestination.get(packageRoot)
    if (packageSource === undefined && typeof manifest.name === 'string') {
      packageSource = await findDependencySource(manifest.name, sources)
    }
    const required = new Set(Object.keys(manifest.dependencies ?? {}))
    for (const dependency of Object.keys(manifest.peerDependencies ?? {})) {
      if (manifest.peerDependenciesMeta?.[dependency]?.optional !== true) required.add(dependency)
    }
    for (const dependency of [...required].sort()) {
      const destination = join(nodeModules, dependency)
      if (!(await exists(destination))) {
        const source = await findDependencySource(dependency, sources, packageSource)
        if (source === undefined) {
          fail(`required runtime dependency is missing: ${dependency} for ${manifest.name ?? 'unknown package'}`)
        }
        await copyPackage(source, destination)
        sourceByDestination.set(destination, source)
      }
      pending.push(destination)
    }
  }
}

await restoreDependencies(join(staging, 'package.json'), sourceNodeModules)
if (closureManifestArgument !== undefined && closureNodeModulesArgument !== undefined) {
  await restoreDependencies(resolve(closureManifestArgument), resolve(closureNodeModulesArgument))
}
await materializeLinks()
await restoreReviewedBuildOutputs()
await restoreRequiredPeerClosure()

const remaining = await firstSymlink(nodeModules)
if (remaining !== undefined) fail(`unmaterialized link remains: ${remaining}`)
