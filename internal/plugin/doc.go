// Package plugin is the daemon-side runner for workload plugins.
//
// The Manager discovers plugin binaries in a directory, runs each plugin's
// `schema` and `init` commands at startup, caches the result, and exposes
// methods other daemon code uses to invoke plugin commands. Plugin authors
// don't import this package — they import pkg/pluginsdk.
package plugin
