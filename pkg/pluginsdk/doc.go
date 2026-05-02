// Package pluginsdk is the public Go SDK for writing dockersnap workload plugins.
//
// A plugin is an executable that the dockersnap daemon invokes with a single
// subcommand and a JSON PluginInput on stdin. The plugin returns a typed JSON
// response (or an NDJSON ProgressEvent stream for long-running commands) on
// stdout. Plugins always run on the daemon host. See docs/PLUGIN-DESIGN.md for
// the full contract.
//
// Typical usage:
//
//	func main() {
//	    p := pluginsdk.New(pluginsdk.Plugin{
//	        Name:                      "kind",
//	        Version:                   "1.0.0",
//	        Description:               "Deploy and manage kind clusters",
//	        SupportedContractVersions: []string{"1"},
//	        ConfigOptions: []pluginsdk.ConfigOption{
//	            {Name: "cluster_name", Type: pluginsdk.ConfigTypeString,
//	             Default: "{{ instance_name }}"},
//	        },
//	    })
//
//	    p.OnDeploy(deployHandler)
//	    p.OnAccess(accessHandler)
//	    p.OnHealth(healthHandler)
//	    p.Run()
//	}
//
// The SDK handles JSON wire encoding, contract negotiation, command dispatch,
// and progress streaming. Plugin authors implement typed handlers and use the
// Progress helper for emitting status events.
package pluginsdk

// ContractVersion is the wire-format version this SDK implements.
// SDK v1.x.y maps to plugin contract version "1".
const ContractVersion = "1"
