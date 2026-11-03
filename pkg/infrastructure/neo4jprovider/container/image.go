package container

// Image is the Neo4j image the container provider runs: the same release and
// digest as docker-compose.yml and the CI service container (a test keeps
// them equal; Renovate updates all three). Fully qualified so podman never
// asks which registry a short name means.
const Image = "docker.io/library/neo4j:5.26.32@sha256:249a1ec6e3c52d82101a5ed33d6ed2b5ed5b0017b21a28c0bfebc335e87fb177"

// Memory settings of the managed Neo4j: small, because the graph of one
// repository is small. The transaction logs are not preallocated (Neo4j
// otherwise writes two 256 MiB files on an empty store, which on the
// ephemeral tmpfs is memory).
var neo4jEnv = []string{
	"NEO4J_server_memory_heap_initial__size=128m",
	"NEO4J_server_memory_heap_max__size=512m",
	"NEO4J_server_memory_pagecache_size=64m",
	"NEO4J_db_tx__log_preallocate=false",
	"NEO4J_db_tx__log_rotation_size=16M",
	"NEO4J_dbms_usage__report_enabled=false",
}
