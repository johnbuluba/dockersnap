// Package sdktest provides helpers for in-process unit testing of plugin
// handlers without spawning a binary.
//
// Typical usage:
//
//	func TestDeploy(t *testing.T) {
//	    in := sdktest.NewContext(t).
//	        WithInstanceName("test").
//	        WithConfig(map[string]any{
//	            "cluster_name": "test", "wait_ready": true,
//	        }, []pluginsdk.ConfigOption{
//	            {Name: "cluster_name", Type: pluginsdk.ConfigTypeString},
//	            {Name: "wait_ready",   Type: pluginsdk.ConfigTypeBool},
//	        }).
//	        Build()
//	    progress := sdktest.NewProgress()
//
//	    err := deployHandler(t.Context(), in, progress.Progress())
//
//	    require.NoError(t, err)
//	    assert.Contains(t, progress.Steps(), "creating_cluster")
//	}
package sdktest
