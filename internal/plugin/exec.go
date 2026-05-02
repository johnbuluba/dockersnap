package plugin

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"

	"github.com/johnbuluba/dockersnap/pkg/pluginsdk"
)

// Run invokes a JSON-in/JSON-out plugin command. The input is marshaled to
// JSON and written to the plugin's stdin; stdout is parsed into the type
// pointed to by out. Returns an error for non-zero exits, JSON parse errors,
// or context cancellation.
//
// For streaming commands (deploy, teardown), use RunStream instead.
func (m *Manager) Run(ctx context.Context, name, command string, in *pluginsdk.PluginInput, out interface{}) error {
	p, err := m.Get(name)
	if err != nil {
		return err
	}

	stdinBytes, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("marshaling PluginInput: %w", err)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, m.timeouts.timeoutFor(command))
	defer cancel()

	stdoutBytes, err := m.runRaw(cmdCtx, p.Path, command, stdinBytes)
	if err != nil {
		return err
	}

	if out == nil {
		return nil
	}
	if err := json.Unmarshal(stdoutBytes, out); err != nil {
		return fmt.Errorf("parsing %s response: %w (stdout: %s)",
			command, err, truncate(stdoutBytes, 200))
	}
	return nil
}

// RunStream invokes a streaming command. Each NDJSON line written to stdout
// is delivered to the events callback in order. The function returns when
// the plugin exits.
//
// On non-zero exit, the error includes the captured stderr.
func (m *Manager) RunStream(ctx context.Context, name, command string, in *pluginsdk.PluginInput, events func(pluginsdk.ProgressEvent)) error {
	p, err := m.Get(name)
	if err != nil {
		return err
	}

	stdinBytes, err := json.Marshal(in)
	if err != nil {
		return fmt.Errorf("marshaling PluginInput: %w", err)
	}

	cmdCtx, cancel := context.WithTimeout(ctx, m.timeouts.timeoutFor(command))
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, p.Path, command)
	cmd.Stdin = bytes.NewReader(stdinBytes)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	tee := newStderrTee(m.logger, name, command)
	cmd.Stderr = tee

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting plugin %s %s: %w", name, command, err)
	}

	// Drain stdout line-by-line, emitting each event.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		// Allow large lines (kubeconfigs can be a few KB).
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Bytes()
			if len(line) == 0 {
				continue
			}
			var ev pluginsdk.ProgressEvent
			if err := json.Unmarshal(line, &ev); err != nil {
				m.logger.Debug("plugin emitted non-JSON line",
					"plugin", name, "command", command, "line", string(line))
				continue
			}
			if events != nil {
				events(ev)
			}
		}
	}()

	wg.Wait()
	waitErr := cmd.Wait()
	tee.flush()
	if waitErr != nil {
		return fmt.Errorf("plugin %s %s: %w (stderr: %s)",
			name, command, waitErr, tee.String())
	}
	return nil
}

// bytesReader is a tiny wrapper to avoid pulling in bytes via runRaw's
// signature in places that don't otherwise need it.
func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

// truncate returns the first n bytes of b, with a "..." suffix if truncated.
func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "..."
}
