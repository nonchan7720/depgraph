package imports

import (
	"strings"

	"github.com/nonchan7720/depgraph/pkg/domain/graph"
)

// IsStdlib reports whether target, the Target of an External import of a
// file in lang ("ext:<name>"), is the language's standard library. Layers
// tell frameworks from the standard library with it: ext:* never matches
// fmt, node:fs, os.path or json.
func IsStdlib(lang graph.Language, target string) bool {
	name, ok := strings.CutPrefix(target, ExternalPrefix)
	if !ok || name == "" {
		return false
	}
	switch lang {
	case graph.Go:
		// the go command's rule: a path whose first element has no dot is
		// the standard library ("C" is cgo's)
		first, _, _ := strings.Cut(name, "/")
		return !strings.Contains(first, ".")
	case graph.TypeScript, graph.JavaScript:
		if strings.HasPrefix(name, "node:") {
			return true
		}
		first, _, _ := strings.Cut(name, "/")
		return NodeBuiltins[first]
	case graph.Python:
		first, _, _ := strings.Cut(name, ".")
		return pyStdlib[first]
	case graph.Ruby:
		first, _, _ := strings.Cut(name, "/")
		return rbStdlib[first]
	}
	return false
}

// NodeBuiltins are Node.js's built-in modules, required without "node:".
var NodeBuiltins = words(`assert async_hooks buffer child_process cluster console constants
	crypto dgram diagnostics_channel dns domain events fs http http2 https inspector module net os
	path perf_hooks process punycode querystring readline repl stream string_decoder sys timers tls
	trace_events tty url util v8 vm wasi worker_threads zlib test sqlite`)

// rbStdlib are the libraries that come with Ruby (default and bundled gems
// included), by the first element of their require path.
var rbStdlib = words(`abbrev base64 benchmark bigdecimal cgi coverage csv date delegate did_you_mean
	digest drb English erb etc expect fcntl fiddle fileutils find forwardable getoptlong io ipaddr irb
	json logger matrix minitest monitor mutex_m net nkf objspace observer open3 open-uri openssl
	optparse ostruct pathname pp prettyprint prime pstore psych racc rdoc readline reline resolv
	resolv-replace rinda ripper rss securerandom set shellwords singleton socket stringio strscan
	syslog tempfile time timeout tmpdir tsort un unicode_normalize uri weakref yaml zlib`)

func words(s string) map[string]bool {
	m := map[string]bool{}
	for w := range strings.FieldsSeq(s) {
		m[w] = true
	}
	return m
}
