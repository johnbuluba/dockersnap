package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/johnbuluba/dockersnap/internal/instance"
	"github.com/johnbuluba/dockersnap/internal/state"
	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// ProgressCallback is called for each progress event during streaming operations.
type ProgressCallback func(event instance.ProgressEvent)

// Client is a remote API client for dockersnap.
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

// New creates a new remote client.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute,
		},
	}
}

// HealthStatus represents the daemon health response.
type HealthStatus struct {
	Status           string `json:"status"`
	Version          string `json:"version,omitempty"`
	Uptime           string `json:"uptime"`
	StartedAt        string `json:"started_at"`
	Instances        int    `json:"instances"`
	Running          int    `json:"running"`
	RunningHealthy   int    `json:"running_healthy,omitempty"`
	RunningUnhealthy int    `json:"running_unhealthy,omitempty"`
}

// Health checks if the daemon is healthy and returns status info.
func (c *Client) Health(ctx context.Context) (*HealthStatus, error) {
	var status HealthStatus
	if err := c.get(ctx, "/api/v1/health", &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// List returns all instances from the remote daemon.
func (c *Client) List(ctx context.Context) ([]*state.Instance, error) {
	var instances []*state.Instance
	if err := c.get(ctx, "/api/v1/instances", &instances); err != nil {
		return nil, err
	}
	return instances, nil
}

// Create creates a new instance on the remote daemon. Pass nil for inline
// to create a plain-Docker (no-workload) instance.
func (c *Client) Create(ctx context.Context, name string, inline *WorkloadInline, cb ProgressCallback) (*state.Instance, error) {
	body := map[string]interface{}{"name": name}
	if inline != nil {
		body["workload_inline"] = inline
	}

	if cb != nil {
		// Streaming form — caller wants progress events.
		if err := c.postStream(ctx, "/api/v1/instances", body, cb); err != nil {
			return nil, err
		}
		// After streaming, fetch the created instance.
		return c.Get(ctx, name)
	}

	var inst state.Instance
	if err := c.post(ctx, "/api/v1/instances", body, &inst); err != nil {
		return nil, err
	}
	return &inst, nil
}

// WorkloadInline is the shape POSTed to /api/v1/instances under
// "workload_inline" to bind a plugin without using a preset.
type WorkloadInline struct {
	Plugin string                 `json:"plugin"`
	Config map[string]interface{} `json:"config,omitempty"`
}

// Get returns a single instance by name from the remote daemon.
func (c *Client) Get(ctx context.Context, name string) (*state.Instance, error) {
	var inst state.Instance
	if err := c.get(ctx, fmt.Sprintf("/api/v1/instances/%s", name), &inst); err != nil {
		return nil, err
	}
	return &inst, nil
}

// Start starts a stopped instance on the remote daemon.
func (c *Client) Start(ctx context.Context, name string) error {
	return c.post(ctx, fmt.Sprintf("/api/v1/instances/%s/start", name), nil, nil)
}

// Stop stops a running instance on the remote daemon.
func (c *Client) Stop(ctx context.Context, name string) error {
	return c.post(ctx, fmt.Sprintf("/api/v1/instances/%s/stop", name), nil, nil)
}

// Delete deletes an instance on the remote daemon.
func (c *Client) Delete(ctx context.Context, name string) error {
	return c.del(ctx, fmt.Sprintf("/api/v1/instances/%s", name))
}

// Snapshot creates a snapshot on the remote daemon.
func (c *Client) Snapshot(ctx context.Context, name, label string, tags map[string]string) error {
	body := map[string]interface{}{"label": label}
	if len(tags) > 0 {
		body["tags"] = tags
	}
	return c.post(ctx, fmt.Sprintf("/api/v1/instances/%s/snapshot", name), body, nil)
}

// Revert reverts an instance to a snapshot on the remote daemon.
func (c *Client) Revert(ctx context.Context, name, label string, force bool) error {
	body := map[string]interface{}{"label": label, "force": force}
	return c.post(ctx, fmt.Sprintf("/api/v1/instances/%s/revert", name), body, nil)
}

// Clone clones an instance on the remote daemon.
func (c *Client) Clone(ctx context.Context, name, label, newName string) (*state.Instance, error) {
	body := map[string]string{"label": label, "new_name": newName}
	var inst state.Instance
	if err := c.post(ctx, fmt.Sprintf("/api/v1/instances/%s/clone", name), body, &inst); err != nil {
		return nil, err
	}
	return &inst, nil
}

// PortMapping describes an active port forwarding.
type PortMapping struct {
	HostPort      int    `json:"host_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
	Description   string `json:"description"`
}

// InstancePorts holds port forwarding state for an instance.
type InstancePorts struct {
	Instance string        `json:"instance"`
	TargetIP string        `json:"target_ip"`
	Ports    []PortMapping `json:"ports"`
}

// Ports returns active port forwardings for an instance.
func (c *Client) Ports(ctx context.Context, name string) (*InstancePorts, error) {
	var ports InstancePorts
	if err := c.get(ctx, fmt.Sprintf("/api/v1/instances/%s/ports", name), &ports); err != nil {
		return nil, err
	}
	return &ports, nil
}

// RefreshPorts rescans and refreshes port forwardings for an instance.
func (c *Client) RefreshPorts(ctx context.Context, name string) (*InstancePorts, error) {
	var ports InstancePorts
	if err := c.post(ctx, fmt.Sprintf("/api/v1/instances/%s/ports/refresh", name), nil, &ports); err != nil {
		return nil, err
	}
	return &ports, nil
}

// Access returns the workload's connection bundle (kubeconfig, env, endpoints)
// with template tokens already resolved by the daemon.
func (c *Client) Access(ctx context.Context, name string) (*pluginsdk.AccessResponse, error) {
	var resp pluginsdk.AccessResponse
	if err := c.get(ctx, fmt.Sprintf("/api/v1/instances/%s/access", name), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// Workload returns the plugin's `describe` response.
func (c *Client) Workload(ctx context.Context, name string) (map[string]interface{}, error) {
	var resp map[string]interface{}
	if err := c.get(ctx, fmt.Sprintf("/api/v1/instances/%s/workload", name), &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// WorkloadHealth returns the cached health entry, or — when fresh is true —
// triggers a synchronous re-check on the daemon.
func (c *Client) WorkloadHealth(ctx context.Context, name string, fresh bool) (map[string]interface{}, error) {
	path := fmt.Sprintf("/api/v1/instances/%s/workload/health", name)
	if fresh {
		path += "?fresh=true"
	}
	var resp map[string]interface{}
	if err := c.get(ctx, path, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// PluginInfo mirrors api.PluginInfo for client consumers.
type PluginInfo struct {
	Name                      string                   `json:"name"`
	Status                    string                   `json:"status"`
	Error                     string                   `json:"error,omitempty"`
	Version                   string                   `json:"version,omitempty"`
	Description               string                   `json:"description,omitempty"`
	SupportedContractVersions []string                 `json:"supported_contract_versions,omitempty"`
	ConfigOptions             []pluginsdk.ConfigOption `json:"config_options,omitempty"`
	SchemaDigest              string                   `json:"schema_digest,omitempty"`
	BinaryDigest              string                   `json:"binary_digest,omitempty"`
}

// Plugins returns metadata for every discovered plugin.
func (c *Client) Plugins(ctx context.Context) ([]PluginInfo, error) {
	var resp []PluginInfo
	if err := c.get(ctx, "/api/v1/plugins", &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// Plugin returns a single plugin's metadata, including its full config schema.
func (c *Client) Plugin(ctx context.Context, name string) (*PluginInfo, error) {
	var resp PluginInfo
	if err := c.get(ctx, fmt.Sprintf("/api/v1/plugins/%s", name), &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// ReloadPlugins re-runs plugin discovery on the daemon.
func (c *Client) ReloadPlugins(ctx context.Context) ([]PluginInfo, error) {
	var resp []PluginInfo
	if err := c.post(ctx, "/api/v1/plugins/reload", nil, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) get(ctx context.Context, path string, result interface{}) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	return c.do(req, result)
}

func (c *Client) post(ctx context.Context, path string, body interface{}, result interface{}) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshaling body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.do(req, result)
}

func (c *Client) del(ctx context.Context, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	return c.do(req, nil)
}

func (c *Client) do(req *http.Request, result interface{}) error {
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return decodeAPIError(resp)
	}

	if result != nil && resp.StatusCode != http.StatusNoContent {
		if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
	}

	return nil
}

// decodeAPIError extracts a meaningful error from a 4xx/5xx response. If the
// body is JSON with an "error" field, that's used; otherwise, the first 200
// bytes of the body are included so the caller sees what actually came back.
func decodeAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))

	var errResp struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
		return fmt.Errorf("API error (%d): %s", resp.StatusCode, errResp.Error)
	}

	excerpt := strings.TrimSpace(string(body))
	if len(excerpt) > 200 {
		excerpt = excerpt[:200] + "..."
	}
	if excerpt != "" {
		return fmt.Errorf("API error (HTTP %d): %s", resp.StatusCode, excerpt)
	}
	return fmt.Errorf("API error: HTTP %d", resp.StatusCode)
}

// postStream sends a POST request with Accept: application/x-ndjson and streams
// progress events to the callback. Returns an error if the operation failed
// (detected by a progress event with status "error").
func (c *Client) postStream(ctx context.Context, path string, body interface{}, cb ProgressCallback) error {
	return c.streamRequest(ctx, http.MethodPost, path, body, cb)
}

// streamRequest is the shared NDJSON-stream consumer for any HTTP method.
// Method-specific helpers (postStream, delStream) wrap it so callers don't
// have to remember which verbs the daemon streams on.
func (c *Client) streamRequest(ctx context.Context, method, path string, body interface{}, cb ProgressCallback) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshaling body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/x-ndjson")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return decodeAPIError(resp)
	}

	// If server doesn't support streaming, fall back gracefully
	if resp.Header.Get("Content-Type") != "application/x-ndjson" {
		return nil
	}

	var lastError string
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var event instance.ProgressEvent
		if err := json.Unmarshal(line, &event); err != nil {
			continue
		}
		if cb != nil {
			cb(event)
		}
		if event.Status == "error" {
			lastError = event.Message
		}
	}

	if lastError != "" {
		return fmt.Errorf("%s", lastError)
	}
	return scanner.Err()
}

// StartStream starts an instance with progress streaming.
func (c *Client) StartStream(ctx context.Context, name string, cb ProgressCallback) error {
	return c.postStream(ctx, fmt.Sprintf("/api/v1/instances/%s/start", name), nil, cb)
}

// StopStream stops an instance with progress streaming.
func (c *Client) StopStream(ctx context.Context, name string, cb ProgressCallback) error {
	return c.postStream(ctx, fmt.Sprintf("/api/v1/instances/%s/stop", name), nil, cb)
}

// SnapshotStream creates a snapshot with progress streaming.
func (c *Client) SnapshotStream(ctx context.Context, name, label string, tags map[string]string, cb ProgressCallback) error {
	body := map[string]interface{}{"label": label}
	if len(tags) > 0 {
		body["tags"] = tags
	}
	return c.postStream(ctx, fmt.Sprintf("/api/v1/instances/%s/snapshot", name), body, cb)
}

// RevertStream reverts an instance with progress streaming.
func (c *Client) RevertStream(ctx context.Context, name, label string, force bool, cb ProgressCallback) error {
	body := map[string]interface{}{"label": label, "force": force}
	return c.postStream(ctx, fmt.Sprintf("/api/v1/instances/%s/revert", name), body, cb)
}

// CloneStream clones an instance with progress streaming.
func (c *Client) CloneStream(ctx context.Context, name, label, newName string, cb ProgressCallback) error {
	body := map[string]string{"label": label, "new_name": newName}
	return c.postStream(ctx, fmt.Sprintf("/api/v1/instances/%s/clone", name), body, cb)
}

// DeleteStream deletes an instance with progress streaming. The daemon
// emits stopping_dockerd / destroying_dataset / complete events plus any
// plugin teardown progress when a workload is bound.
func (c *Client) DeleteStream(ctx context.Context, name string, cb ProgressCallback) error {
	return c.streamRequest(ctx, http.MethodDelete,
		fmt.Sprintf("/api/v1/instances/%s", name), nil, cb)
}
