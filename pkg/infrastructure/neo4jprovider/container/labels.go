package container

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"strings"
)

// Labels on every container and volume this package creates. Only objects
// carrying LabelManaged=1 are ever stopped or removed.
const (
	LabelManaged  = "depgraph.managed"   // "1"
	LabelRoot     = "depgraph.root"      // RootHash(root)
	LabelRootPath = "depgraph.root-path" // the root itself (display only, never filtered on)
	LabelProject  = "depgraph.project"   // project key
	LabelPersist  = "depgraph.persist"   // "1" persistent, "0" ephemeral
	LabelOwnerPID = "depgraph.owner-pid" // pid of the process that started it
	LabelHost     = "depgraph.host"      // HostID() of the machine that started it
	LabelVolume   = "depgraph.volume"    // the data volume (persistent only)
)

// boltPort is Neo4j's bolt port inside the container.
const boltPort = "7687/tcp"

func shortHash(s string, n int) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:n]
}

// RootHash identifies a root in labels (a path can hold characters label
// filters cannot).
func RootHash(root string) string { return shortHash("depgraph-root:"+root, 16) }

// HostID identifies this machine, so a daemon shared by several machines
// (DOCKER_HOST=tcp://..., a remote podman) never sees one machine remove
// another's containers because their owner PID looks dead here.
func HostID() string {
	h, _ := os.Hostname()
	return shortHash("depgraph-host:"+h, 12)
}

// slug keeps [a-z0-9_.-], maps everything else to '-' and bounds the length,
// so a project key becomes part of a container / volume name.
func slug(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '.', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-._")
	if len(out) > 40 {
		out = strings.Trim(out[:40], "-._")
	}
	if out == "" {
		out = "project"
	}
	return out
}

// ContainerName is the one container name of a root. Being unique, it also
// keeps two processes from starting two stores for the same root.
func ContainerName(project, root string) string {
	return "depgraph-" + slug(project) + "-" + RootHash(root)[:8]
}

// VolumeName is the data volume of a persistent project. The hash keeps keys
// that slug the same (a@b, a-b) apart.
func VolumeName(project string) string {
	return "depgraph-" + slug(project) + "-" + shortHash("depgraph-project:"+project, 8)
}
