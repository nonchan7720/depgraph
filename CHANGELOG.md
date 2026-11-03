# Changelog

## [0.1.2](https://github.com/nonchan7720/depgraph/compare/v0.1.1...v0.1.2) (2026-11-03)


### Features

* **config:** JSON Schema of .depgraph.yml ([#30](https://github.com/nonchan7720/depgraph/issues/30)) ([06ebcdc](https://github.com/nonchan7720/depgraph/commit/06ebcdca22c46bd918b28c8665a0fa32ffb9f920))
* **hook:** plain-text output and env session for claude-hook-gate's session_start ([#28](https://github.com/nonchan7720/depgraph/issues/28)) ([4faeae1](https://github.com/nonchan7720/depgraph/commit/4faeae11b1d7a7401db678bd12b7442f572b01ce))

## [0.1.1](https://github.com/nonchan7720/depgraph/compare/v0.1.0...v0.1.1) (2026-11-03)


### Features

* **gate:** fail a claude-hook-gate check on debt the session added ([#27](https://github.com/nonchan7720/depgraph/issues/27)) ([ac90bca](https://github.com/nonchan7720/depgraph/commit/ac90bca3ff3382b2e7d45c23b4a2b292a31b6476))


### Documentation

* state the Apache-2.0 license as final in the READMEs ([#25](https://github.com/nonchan7720/depgraph/issues/25)) ([f0ddc92](https://github.com/nonchan7720/depgraph/commit/f0ddc92060a56e4c9cc27daf8492a75b4a7bb221))

## 0.1.0 (2026-11-03)


### Features

* replace cgo go-tree-sitter with pure-Go gotreesitter; parallel full scan ([c84e315](https://github.com/nonchan7720/depgraph/commit/c84e315105e3bf3918a2b8c8a2da07775ebc1015))
* **rules:** configure architecture rules with .depgraph.yml ([#13](https://github.com/nonchan7720/depgraph/issues/13)) ([1d9c6a1](https://github.com/nonchan7720/depgraph/commit/1d9c6a168cbc564fceeac76c1a1559fa5dd386ec))
* **rules:** report duplicated code whatever the names ([#15](https://github.com/nonchan7720/depgraph/issues/15)) ([82d67dd](https://github.com/nonchan7720/depgraph/commit/82d67ddae97ad080da1799e7c5acb185ba216162))
* **rules:** report exports and files nothing in the project uses ([#17](https://github.com/nonchan7720/depgraph/issues/17)) ([ffa022b](https://github.com/nonchan7720/depgraph/commit/ffa022b6d3505a994d42d81339b232e97b602068))
* **store:** start and clean up Neo4j automatically (container provider) ([#18](https://github.com/nonchan7720/depgraph/issues/18)) ([c1de1a2](https://github.com/nonchan7720/depgraph/commit/c1de1a2f0bdd7f04ae6a10888d2adc2b87cca750))


### Bug Fixes

* **deps:** update module github.com/neo4j/neo4j-go-driver/v5 to v6 ([1f6fdcf](https://github.com/nonchan7720/depgraph/commit/1f6fdcfde6cdffdea452df51b87d3445ece69a9b))
* **hook:** keep session state in Neo4j, not in the repository ([#16](https://github.com/nonchan7720/depgraph/issues/16)) ([e0e3954](https://github.com/nonchan7720/depgraph/commit/e0e39548396716865a93c61ad9f5fb7ad37eb4bf))
* **resolver:** read config and Go files without bufio.Scanner's line limit ([#24](https://github.com/nonchan7720/depgraph/issues/24)) ([e25c24b](https://github.com/nonchan7720/depgraph/commit/e25c24be043b610a4678e287458adc918e6d5944))
* **resolver:** resolve imports with each ecosystem's own rules ([#12](https://github.com/nonchan7720/depgraph/issues/12)) ([1c8726b](https://github.com/nonchan7720/depgraph/commit/1c8726bcd60e60413da9a5fcc8de139db8d411ed))
* **rules:** a Go package counts once in duplicate-symbol ([#22](https://github.com/nonchan7720/depgraph/issues/22)) ([b6e6dbd](https://github.com/nonchan7720/depgraph/commit/b6e6dbd4edf4ed3c9f465980fe9ed4ef80ea18b9))
* **rules:** find import cycles in polynomial time ([#20](https://github.com/nonchan7720/depgraph/issues/20)) ([9ae2135](https://github.com/nonchan7720/depgraph/commit/9ae213500877da5daeda45868a028c74aa76a6eb))
* **rules:** measure duplicate-code similarity over all fingerprints ([#19](https://github.com/nonchan7720/depgraph/issues/19)) ([f859e55](https://github.com/nonchan7720/depgraph/commit/f859e557441e8b9dde3b56ed4f8d0575b3ba635a))
* **treesitter:** extract Go type aliases ([#10](https://github.com/nonchan7720/depgraph/issues/10)) ([e545a96](https://github.com/nonchan7720/depgraph/commit/e545a9649e75df72405d44ea4fac368902096d3e))


### Code Refactoring

* fix what depgraph reports on its own repository ([#21](https://github.com/nonchan7720/depgraph/issues/21)) ([38dcde5](https://github.com/nonchan7720/depgraph/commit/38dcde5cd3a1d2ca46c0efc093c22581f4cdab07))


### Documentation

* add CLAUDE.md with maintainer role and quality bar ([#4](https://github.com/nonchan7720/depgraph/issues/4)) ([0e149e9](https://github.com/nonchan7720/depgraph/commit/0e149e9922be94c8d4730803fb2420f0136e289d))
* state the product focus in CLAUDE.md ([#11](https://github.com/nonchan7720/depgraph/issues/11)) ([435c77c](https://github.com/nonchan7720/depgraph/commit/435c77c224ead07aa6aef3ac297df8dca85ba951))
