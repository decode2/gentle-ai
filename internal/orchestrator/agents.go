package orchestrator

import (
	"context"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// KnownAgent is a coding agent CLI the launcher knows how to run. Detection
// scans this list against the machine's PATH.
type KnownAgent struct {
	// Command is the executable name to look for.
	Command string
	// Agent is the id used in POST /sessions (agent field).
	Agent string
}

// knownAgents is the detection roster: the CLIs the fleet can drive today
// (headless forms in agentArgTemplates, extended by the adapters phase).
var knownAgents = []KnownAgent{
	{Command: "pi", Agent: "pi"},
	{Command: "claude", Agent: "claude"},
	{Command: "codex", Agent: "codex"},
	{Command: "gemini", Agent: "gemini"},
	{Command: "opencode", Agent: "opencode"},
	{Command: "aider", Agent: "aider"},
	{Command: "cursor-agent", Agent: "cursor"},
	{Command: "qwen", Agent: "qwen"},
	{Command: "kimi", Agent: "kimi"},
	{Command: "crush", Agent: "crush"},
	{Command: "amp", Agent: "amp"},
	{Command: "goose", Agent: "goose"},
}

// DetectedAgent is one roster entry resolved against this machine.
type DetectedAgent struct {
	Agent   string `json:"agent"`
	Command string `json:"command"`
	Path    string `json:"path,omitempty"`
	Version string `json:"version,omitempty"`
	Found   bool   `json:"found"`
}

// Prober resolves one command name against the machine: path and version.
// Tests replace it; the default uses PATH and a short --version call.
type Prober func(command string) (path, version string)

// DefaultProber looks the command up on PATH and asks --version with a short
// timeout. Any failure yields an empty path and version.
func DefaultProber(command string) (string, string) {
	path, err := exec.LookPath(command)
	if err != nil {
		return "", ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, command, "--version").Output()
	if err != nil {
		return path, ""
	}
	return path, strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
}

// AgentDetector resolves the known roster against the machine and caches the
// result briefly: the board asks on every form open without re-probing.
type AgentDetector struct {
	prober Prober
	ttl    time.Duration

	mu         sync.Mutex
	last       []DetectedAgent
	cachedAt   time.Time
	rescanning bool
}

// NewAgentDetector returns a detector using prober (nil = default) with the
// given cache TTL (<= 0 falls back to 30 seconds).
func NewAgentDetector(prober Prober, ttl time.Duration) *AgentDetector {
	if prober == nil {
		prober = DefaultProber
	}
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &AgentDetector{prober: prober, ttl: ttl}
}

// Detect returns the roster resolved against this machine, from cache when
// it is fresh. force bypasses the cache.
func (d *AgentDetector) Detect(force bool) []DetectedAgent {
	d.mu.Lock()
	if !force && d.last != nil && time.Since(d.cachedAt) < d.ttl {
		out := d.last
		d.mu.Unlock()
		return out
	}
	d.mu.Unlock()

	agents := make([]DetectedAgent, 0, len(knownAgents))
	for _, known := range knownAgents {
		path, version := d.prober(known.Command)
		agents = append(agents, DetectedAgent{
			Agent:   known.Agent,
			Command: known.Command,
			Path:    path,
			Version: version,
			Found:   path != "",
		})
	}

	d.mu.Lock()
	d.last = agents
	d.cachedAt = time.Now()
	d.mu.Unlock()
	return agents
}
