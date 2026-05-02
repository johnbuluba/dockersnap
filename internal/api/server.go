package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/johnbuluba/dockersnap/internal/config"
	"github.com/johnbuluba/dockersnap/internal/instance"
	"github.com/johnbuluba/dockersnap/internal/plugin"
	"github.com/johnbuluba/dockersnap/internal/state"
	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
	"github.com/johnbuluba/dockersnap/pkg/version"
)

// SetPluginAdmin wires a plugin administration interface (typically
// *plugin.Manager). Pass nil to leave the /plugins endpoints disabled.
func (s *Server) SetPluginAdmin(p PluginAdmin) { s.pluginAdmin = p }

// SetHealthReader wires a workload-health cache (typically
// *instance.HealthCache via the poller). Pass nil to disable cached
// /health responses (the daemon will fall back to fresh polls).
func (s *Server) SetHealthReader(h HealthReader) { s.healthReader = h }

// PluginInfo is the public-facing description of a discovered plugin.
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

// PluginAdmin is the subset of plugin.Manager the API needs.
type PluginAdmin interface {
	List() []*plugin.Plugin
	Get(name string) (*plugin.Plugin, error)
	Reload(ctx context.Context) error
}

// HealthReader is the subset of instance.HealthCache the API reads.
type HealthReader interface {
	Get(name string) *instance.HealthEntry
}

// Server is the dockersnap REST API server.
type Server struct {
	cfg          *config.Config
	mgr          *instance.Manager
	pluginAdmin  PluginAdmin  // optional; nil disables /plugins endpoints
	healthReader HealthReader // optional; nil disables cached /health reads
	logger       *slog.Logger
	srv          *http.Server
	startedAt    time.Time
}

// NewServer creates a new API server.
func NewServer(cfg *config.Config, mgr *instance.Manager, logger *slog.Logger) *Server {
	s := &Server{
		cfg:       cfg,
		mgr:       mgr,
		logger:    logger,
		startedAt: time.Now(),
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(5 * time.Minute))
	r.Use(s.requestLogger)

	// Public routes: health check + dashboard. The dashboard's API calls
	// still hit the authenticated /api/v1 group, so this is just static
	// assets (which contain no secrets).
	r.Group(func(r chi.Router) {
		r.Get("/api/v1/health", s.healthCheck)
		s.mountDashboard(r)
	})

	// Authenticated routes: token auth applied as middleware on this group only.
	r.Group(func(r chi.Router) {
		if cfg.API.Token != "" {
			r.Use(s.tokenAuth)
		}
		r.Route("/api/v1", func(r chi.Router) {
			r.Get("/instances", s.listInstances)
			r.Post("/instances", s.createInstance)
			r.Get("/instances/{name}", s.getInstance)
			r.Delete("/instances/{name}", s.deleteInstance)
			r.Post("/instances/{name}/start", s.startInstance)
			r.Post("/instances/{name}/stop", s.stopInstance)
			r.Get("/instances/{name}/ports", s.listPorts)
			r.Post("/instances/{name}/ports/refresh", s.refreshPorts)
			r.Post("/instances/{name}/snapshot", s.createSnapshot)
			r.Post("/instances/{name}/revert", s.revertInstance)
			r.Post("/instances/{name}/clone", s.cloneInstance)

			// Plugin / workload endpoints.
			r.Get("/instances/{name}/access", s.getAccess)
			r.Get("/instances/{name}/workload", s.getWorkload)
			r.Get("/instances/{name}/workload/health", s.getWorkloadHealth)
			r.Get("/plugins", s.listPlugins)
			r.Get("/plugins/{name}", s.getPlugin)
			r.Post("/plugins/reload", s.reloadPlugins)
		})
	})

	s.srv = &http.Server{Handler: r}
	return s
}

// Handler returns the underlying http.Handler for in-process testing.
func (s *Server) Handler() http.Handler {
	return s.srv.Handler
}

// Run starts the server and blocks until context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	ln, err := net.Listen("tcp", s.cfg.API.Listen)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", s.cfg.API.Listen, err)
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.srv.Shutdown(shutdownCtx); err != nil {
			s.logger.Warn("HTTP shutdown error", "error", err)
		}
	}()

	s.logger.Info("API server listening", "address", s.cfg.API.Listen)
	if err := s.srv.Serve(ln); err != http.ErrServerClosed {
		return fmt.Errorf("server error: %w", err)
	}
	return nil
}

// tokenAuth performs a constant-time comparison against the configured token.
func (s *Server) tokenAuth(next http.Handler) http.Handler {
	expected := []byte("Bearer " + s.cfg.API.Token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, expected) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) listInstances(w http.ResponseWriter, r *http.Request) {
	instances, err := s.mgr.List(r.Context())
	if err != nil {
		s.errorResponse(w, http.StatusInternalServerError, err)
		return
	}
	s.jsonResponse(w, http.StatusOK, instances)
}

func (s *Server) createInstance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name           string          `json:"name"`
		WorkloadInline *workloadInline `json:"workload_inline,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.errorResponse(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if err := instance.ValidateName(req.Name); err != nil {
		s.errorResponse(w, http.StatusBadRequest, err)
		return
	}

	var opts instance.WorkloadOpts
	if req.WorkloadInline != nil {
		opts.Plugin = req.WorkloadInline.Plugin
		opts.Config = req.WorkloadInline.Config
	}

	if wantsStream(r) {
		ch := make(chan instance.ProgressEvent, 16)
		progress := instance.NewProgressReporter(ch)
		go func() {
			defer close(ch)
			if _, err := s.mgr.Create(r.Context(), req.Name, opts, progress); err != nil {
				progress.Emit("error", "error", err.Error())
			}
		}()
		streamProgress(w, ch)
		return
	}

	inst, err := s.mgr.Create(r.Context(), req.Name, opts, nil)
	if err != nil {
		s.errorResponse(w, http.StatusInternalServerError, err)
		return
	}
	s.jsonResponse(w, http.StatusCreated, inst)
}

type workloadInline struct {
	Plugin string                 `json:"plugin"`
	Config map[string]interface{} `json:"config"`
}

func (s *Server) getInstance(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	inst, err := s.mgr.Get(r.Context(), name)
	if err != nil {
		s.errorResponse(w, http.StatusNotFound, err)
		return
	}
	s.jsonResponse(w, http.StatusOK, inst)
}

func (s *Server) deleteInstance(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	if wantsStream(r) {
		ch := make(chan instance.ProgressEvent, 16)
		progress := instance.NewProgressReporter(ch)
		go func() {
			defer close(ch)
			if err := s.mgr.Delete(r.Context(), name, progress); err != nil {
				progress.Emit("error", "error", err.Error())
			}
		}()
		streamProgress(w, ch)
		return
	}

	if err := s.mgr.Delete(r.Context(), name, nil); err != nil {
		s.errorResponse(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listPorts(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	ports := s.mgr.Proxy().ListPorts(name)
	s.jsonResponse(w, http.StatusOK, ports)
}

func (s *Server) refreshPorts(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")

	inst, err := s.mgr.Get(r.Context(), name)
	if err != nil {
		s.errorResponse(w, http.StatusNotFound, err)
		return
	}
	if inst.Status != state.StatusRunning {
		s.errorResponse(w, http.StatusBadRequest, fmt.Errorf("instance %q is not running", name))
		return
	}

	s.mgr.Proxy().StopInstance(name)
	s.mgr.SetupPortForwarding(r.Context(), inst)

	ports := s.mgr.Proxy().ListPorts(name)
	s.jsonResponse(w, http.StatusOK, ports)
}

func (s *Server) startInstance(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	s.runOrStream(w, r, func(progress *instance.ProgressReporter) error {
		return s.mgr.Start(r.Context(), name, progress)
	}, http.StatusOK)
}

func (s *Server) stopInstance(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	s.runOrStream(w, r, func(progress *instance.ProgressReporter) error {
		return s.mgr.Stop(r.Context(), name, progress)
	}, http.StatusOK)
}

func (s *Server) createSnapshot(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var req struct {
		Label string            `json:"label"`
		Tags  map[string]string `json:"tags,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.errorResponse(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if req.Label == "" {
		s.errorResponse(w, http.StatusBadRequest, fmt.Errorf("label is required"))
		return
	}

	s.runOrStream(w, r, func(progress *instance.ProgressReporter) error {
		return s.mgr.Snapshot(r.Context(), name, req.Label, req.Tags, progress)
	}, http.StatusCreated)
}

func (s *Server) revertInstance(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var req struct {
		Label string `json:"label"`
		Force bool   `json:"force"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.errorResponse(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if req.Label == "" {
		s.errorResponse(w, http.StatusBadRequest, fmt.Errorf("label is required"))
		return
	}

	opts := instance.RevertOpts{Force: req.Force}
	s.runOrStream(w, r, func(progress *instance.ProgressReporter) error {
		return s.mgr.Revert(r.Context(), name, req.Label, opts, progress)
	}, http.StatusOK)
}

func (s *Server) cloneInstance(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	var req struct {
		Label   string `json:"label"`
		NewName string `json:"new_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.errorResponse(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}
	if req.Label == "" {
		s.errorResponse(w, http.StatusBadRequest, fmt.Errorf("label is required"))
		return
	}
	if err := instance.ValidateName(req.NewName); err != nil {
		s.errorResponse(w, http.StatusBadRequest, err)
		return
	}

	if wantsStream(r) {
		ch := make(chan instance.ProgressEvent, 16)
		progress := instance.NewProgressReporter(ch)
		go func() {
			defer close(ch)
			if _, err := s.mgr.Clone(r.Context(), name, req.Label, req.NewName, progress); err != nil {
				progress.Emit("error", "error", err.Error())
			}
		}()
		streamProgress(w, ch)
		return
	}

	inst, err := s.mgr.Clone(r.Context(), name, req.Label, req.NewName, nil)
	if err != nil {
		s.errorResponse(w, http.StatusInternalServerError, err)
		return
	}
	s.jsonResponse(w, http.StatusCreated, inst)
}

// runOrStream handles the streaming-vs-sync split for operations that don't
// produce a body (start/stop/snapshot/revert).
func (s *Server) runOrStream(w http.ResponseWriter, r *http.Request, fn func(*instance.ProgressReporter) error, successStatus int) {
	if wantsStream(r) {
		ch := make(chan instance.ProgressEvent, 16)
		progress := instance.NewProgressReporter(ch)
		go func() {
			defer close(ch)
			if err := fn(progress); err != nil {
				progress.Emit("error", "error", err.Error())
			}
		}()
		streamProgress(w, ch)
		return
	}

	if err := fn(nil); err != nil {
		s.errorResponse(w, http.StatusInternalServerError, err)
		return
	}
	s.jsonResponse(w, successStatus, map[string]string{"status": "ok"})
}

func (s *Server) jsonResponse(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		s.logger.Warn("failed to encode JSON response", "error", err)
	}
}

func (s *Server) errorResponse(w http.ResponseWriter, status int, err error) {
	// "Instance not found" surfaces from anywhere in the manager — promote
	// the caller's chosen status to 404 so we don't return 500 for a
	// client-side issue.
	if errors.Is(err, instance.ErrNotFound) && status >= 500 {
		status = http.StatusNotFound
	}
	// 4xx is the client's problem (bad request, not found, conflict, …).
	// Logging every "instance X not found" at ERROR level pollutes the
	// journal during normal use — tests and CLI users routinely hit
	// these expected error paths. Only 5xx is the daemon's problem.
	if status >= 500 {
		s.logger.Error("api error", "status", status, "error", err)
	} else {
		s.logger.Debug("api client error", "status", status, "error", err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if encErr := json.NewEncoder(w).Encode(map[string]string{"error": err.Error()}); encErr != nil {
		s.logger.Warn("failed to encode error response", "error", encErr)
	}
}

func (s *Server) requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		s.logger.Debug("api request",
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
		)
		next.ServeHTTP(w, r)
		s.logger.Debug("api response",
			"method", r.Method,
			"path", r.URL.Path,
			"duration", time.Since(start).String(),
		)
	})
}

func (s *Server) healthCheck(w http.ResponseWriter, r *http.Request) {
	instances, _ := s.mgr.List(r.Context())
	running := 0
	runningHealthy := 0
	runningUnhealthy := 0
	for _, inst := range instances {
		if inst.Status != state.StatusRunning {
			continue
		}
		running++
		if s.healthReader != nil && inst.WorkloadPlugin != "" {
			if e := s.healthReader.Get(inst.Name); e != nil {
				if e.Healthy {
					runningHealthy++
				} else {
					runningUnhealthy++
				}
			}
		}
	}
	s.jsonResponse(w, http.StatusOK, DaemonHealth{
		Status:           "ok",
		Version:          version.String(),
		Uptime:           time.Since(s.startedAt).Truncate(time.Second).String(),
		StartedAt:        s.startedAt.Format(time.RFC3339),
		Instances:        len(instances),
		Running:          running,
		RunningHealthy:   runningHealthy,
		RunningUnhealthy: runningUnhealthy,
	})
}

// getAccess returns the workload's connection bundle (kubeconfig, env,
// endpoints) with template tokens resolved against the live forwarded-port
// state. ${ACCESS_DIR} is left for the CLI to resolve locally.
func (s *Server) getAccess(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	inst, err := s.mgr.Get(r.Context(), name)
	if err != nil {
		s.errorResponse(w, http.StatusNotFound, err)
		return
	}

	host := s.resolveHost(r)

	if inst.WorkloadPlugin == "" {
		// No-workload instance — synthesize an empty bundle and let the
		// daemon inject the always-present env vars below.
		resp := &pluginsdk.AccessResponse{ContractVersion: pluginsdk.ContractVersion}
		injectDaemonEnv(resp, inst)
		s.jsonResponse(w, http.StatusOK, resp)
		return
	}

	resp, err := s.mgr.CallAccess(r.Context(), inst, host)
	if err != nil {
		s.errorResponse(w, http.StatusInternalServerError, err)
		return
	}
	if resp == nil {
		resp = &pluginsdk.AccessResponse{ContractVersion: pluginsdk.ContractVersion}
	}
	injectDaemonEnv(resp, inst)
	s.jsonResponse(w, http.StatusOK, resp)
}

// injectDaemonEnv sets the env vars the daemon owns regardless of whether
// the bound plugin emitted them. Both DOCKER_HOST and DOCKERSNAP_INSTANCE
// are derivable from instance state — making the daemon authoritative
// means plugin authors never have to remember to emit them, and a buggy
// plugin can't surface a wrong value (e.g. a stale DOCKER_HOST after a
// socket path change). Plugin-emitted values for these two keys are
// overwritten silently. Anything else from the plugin's Env is preserved
// untouched.
func injectDaemonEnv(resp *pluginsdk.AccessResponse, inst *state.Instance) {
	if resp.Env == nil {
		resp.Env = map[string]string{}
	}
	resp.Env["DOCKER_HOST"] = "unix://" + inst.Socket
	resp.Env["DOCKERSNAP_INSTANCE"] = inst.Name
}

// resolveHost returns the host string to use when resolving ${HOST} in
// AccessResponse content. Mirrors patchKubeconfig's logic: prefer an explicit
// proxy_bind, fall back to the request Host header, fall back to "127.0.0.1".
func (s *Server) resolveHost(r *http.Request) string {
	if s.cfg.API.ProxyBind != "" && s.cfg.API.ProxyBind != "0.0.0.0" {
		return s.cfg.API.ProxyBind
	}
	host, _, _ := net.SplitHostPort(r.Host)
	if host == "" {
		host = r.Host
	}
	if host == "" {
		host = "127.0.0.1"
	}
	return host
}

// getWorkload calls the plugin's `describe` and returns its response.
func (s *Server) getWorkload(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	inst, err := s.mgr.Get(r.Context(), name)
	if err != nil {
		s.errorResponse(w, http.StatusNotFound, err)
		return
	}
	if inst.WorkloadPlugin == "" {
		s.jsonResponse(w, http.StatusOK, WorkloadDescribeResponse{
			WorkloadPlugin: "",
			WorkloadType:   "none",
		})
		return
	}
	resp, err := s.mgr.CallDescribe(r.Context(), inst)
	if err != nil {
		s.errorResponse(w, http.StatusInternalServerError, err)
		return
	}
	// Flatten plugin response into the named API type so the wire shape
	// is one-level deep (matches what the dashboard's TS expects).
	out := WorkloadDescribeResponse{
		WorkloadPlugin: inst.WorkloadPlugin,
	}
	if resp != nil {
		out.WorkloadType = resp.WorkloadType
		out.ContractVersion = resp.ContractVersion
		out.Status = resp.Status
		out.Ports = resp.Ports
		out.Config = resp.Config
		out.Details = resp.Details
	}
	s.jsonResponse(w, http.StatusOK, out)
}

// getWorkloadHealth returns the cached health entry for an instance, or
// triggers a synchronous re-check when ?fresh=true is passed.
func (s *Server) getWorkloadHealth(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	inst, err := s.mgr.Get(r.Context(), name)
	if err != nil {
		s.errorResponse(w, http.StatusNotFound, err)
		return
	}
	if inst.WorkloadPlugin == "" {
		s.jsonResponse(w, http.StatusOK, WorkloadHealthResponse{
			WorkloadPlugin: "",
			Healthy:        true,
			Checks: []pluginsdk.HealthCheck{
				{Name: "workload", OK: true, Message: "no workload bound"},
			},
		})
		return
	}

	fresh := r.URL.Query().Get("fresh") == "true"
	if !fresh && s.healthReader != nil {
		if e := s.healthReader.Get(name); e != nil {
			body := WorkloadHealthResponse{
				WorkloadPlugin:   inst.WorkloadPlugin,
				Healthy:          e.Healthy,
				CheckedAt:        e.CheckedAt.Format(time.RFC3339),
				ConsecutiveFails: e.ConsecutiveFails,
			}
			if e.Response != nil {
				body.ContractVersion = e.Response.ContractVersion
				body.Checks = e.Response.Checks
			}
			s.jsonResponse(w, http.StatusOK, body)
			return
		}
	}

	// No cached entry, or fresh=true — synchronous poll. The plugin's
	// HealthResponse maps cleanly onto WorkloadHealthResponse minus the
	// cache fields.
	resp, err := s.mgr.CallHealth(r.Context(), inst)
	if err != nil {
		s.errorResponse(w, http.StatusInternalServerError, err)
		return
	}
	s.jsonResponse(w, http.StatusOK, WorkloadHealthResponse{
		WorkloadPlugin:  inst.WorkloadPlugin,
		Healthy:         resp.Healthy,
		Checks:          resp.Checks,
		ContractVersion: resp.ContractVersion,
	})
}

// listPlugins returns metadata for every discovered plugin (ready or disabled).
func (s *Server) listPlugins(w http.ResponseWriter, r *http.Request) {
	if s.pluginAdmin == nil {
		s.jsonResponse(w, http.StatusOK, []PluginInfo{})
		return
	}
	all := s.pluginAdmin.List()
	out := make([]PluginInfo, 0, len(all))
	for _, p := range all {
		out = append(out, pluginInfoFrom(p))
	}
	s.jsonResponse(w, http.StatusOK, out)
}

// getPlugin returns a single plugin's metadata, including its full schema.
func (s *Server) getPlugin(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	if s.pluginAdmin == nil {
		s.errorResponse(w, http.StatusNotFound, fmt.Errorf("plugin admin not enabled"))
		return
	}
	p, err := s.pluginAdmin.Get(name)
	if err != nil {
		s.errorResponse(w, http.StatusNotFound, err)
		return
	}
	s.jsonResponse(w, http.StatusOK, pluginInfoFrom(p))
}

// reloadPlugins re-runs plugin discovery (re-scans the dir, re-runs schema + init).
func (s *Server) reloadPlugins(w http.ResponseWriter, r *http.Request) {
	if s.pluginAdmin == nil {
		s.errorResponse(w, http.StatusServiceUnavailable, fmt.Errorf("plugin admin not enabled"))
		return
	}
	if err := s.pluginAdmin.Reload(r.Context()); err != nil {
		s.errorResponse(w, http.StatusInternalServerError, err)
		return
	}
	all := s.pluginAdmin.List()
	out := make([]PluginInfo, 0, len(all))
	for _, p := range all {
		out = append(out, pluginInfoFrom(p))
	}
	s.jsonResponse(w, http.StatusOK, out)
}

func pluginInfoFrom(p *plugin.Plugin) PluginInfo {
	return PluginInfo{
		Name:                      p.Name,
		Status:                    string(p.Status),
		Error:                     p.Error,
		Version:                   p.Schema.PluginVersion,
		Description:               p.Schema.Description,
		SupportedContractVersions: p.Schema.SupportedContractVersions,
		ConfigOptions:             p.Schema.ConfigOptions,
		SchemaDigest:              p.SchemaDigest,
		BinaryDigest:              p.BinaryDigest,
	}
}
