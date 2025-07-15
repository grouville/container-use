// environment/environment.go
package environment

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
	// "github.com/mattn/go-isatty"
)

type EnvironmentInfo struct {
	State *State `json:"state,omitempty"`
	ID    string `json:"id,omitempty"`
}

type Environment struct {
	*EnvironmentInfo

	containerID      string
	proxyManager     net.Conn
	snapshotCallback SnapshotCallback

	Notes Notes

	mu sync.RWMutex
}

func New(ctx context.Context, _ interface{}, id, title, worktree string, _ interface{}) (*Environment, error) {
	config := DefaultConfig()
	if err := config.Load(worktree); err != nil {
		return nil, err
	}

	env := &Environment{
		EnvironmentInfo: &EnvironmentInfo{
			ID: id,
			State: &State{
				Config:    config,
				Title:     title,
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
		},
	}

	// Ensure proxy image exists
	if err := EnsureProxyImage("container-use-claude"); err != nil {
		return nil, fmt.Errorf("failed to ensure proxy image: %w", err)
	}

	slog.Info("Creating environment", "id", env.ID, "workdir", env.State.Config.Workdir)

	// Container will be created on first use
	env.State.UpdatedAt = time.Now()

	return env, nil
}

func Load(ctx context.Context, _ interface{}, id string, state []byte, worktree string) (*Environment, error) {
	envInfo, err := LoadInfo(ctx, id, state, worktree)
	if err != nil {
		return nil, err
	}

	env := &Environment{
		EnvironmentInfo: envInfo,
	}

	// Restore container ID from state
	env.containerID = env.State.Container

	return env, nil
}

func LoadInfo(ctx context.Context, id string, state []byte, worktree string) (*EnvironmentInfo, error) {
	envInfo := &EnvironmentInfo{
		ID:    id,
		State: &State{},
	}

	if err := envInfo.State.Unmarshal(state); err != nil {
		return nil, err
	}

	// Backward compatibility: if there's no config in the state, load it from the worktree
	if envInfo.State.Config == nil {
		config := DefaultConfig()
		if err := config.Load(worktree); err != nil {
			return nil, err
		}
		envInfo.State.Config = config
	}

	return envInfo, nil
}

func (env *Environment) Workdir() string {
	return env.State.Config.Workdir
}

func (env *Environment) Run(ctx context.Context, command, shell string, useEntrypoint bool) (string, error) {
	// Ensure container exists
	if env.containerID == "" {
		return "", fmt.Errorf("no container running - use 'start' command first")
	}

	args := []string{"exec", env.containerID}
	if command != "" {
		args = append(args, shell, "-c", command)
	} else {
		args = append(args, shell)
	}

	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	stdoutStr := stdout.String()
	stderrStr := stderr.String()

	exitCode := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
		err = nil // Non-zero exit is not an error
	}

	// Log the command execution
	env.Notes.AddCommand(command, exitCode, stdoutStr, stderrStr)

	// Combine output
	combinedOutput := stdoutStr
	if stderrStr != "" {
		if stdoutStr != "" {
			combinedOutput += "\n"
		}
		combinedOutput += "stderr: " + stderrStr
	}

	return combinedOutput, err
}

func (env *Environment) Checkpoint(ctx context.Context, target string) (string, error) {
	if env.containerID == "" {
		return "", fmt.Errorf("no container to checkpoint")
	}

	// Commit current state
	imageID, err := env.commitContainer(ctx, "Checkpoint for publish")
	if err != nil {
		return "", err
	}

	// Tag and push
	if err := exec.CommandContext(ctx, "docker", "tag", imageID, target).Run(); err != nil {
		return "", fmt.Errorf("failed to tag image: %w", err)
	}

	if err := exec.CommandContext(ctx, "docker", "push", target).Run(); err != nil {
		return "", fmt.Errorf("failed to push image: %w", err)
	}

	return target, nil
}

func (env *Environment) ensureContainer(ctx context.Context, worktree string) error {
	if env.containerID != "" {
		// Check if container still exists and is running
		cmd := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Running}}", env.containerID)
		if output, err := cmd.Output(); err == nil {
			running := strings.TrimSpace(string(output)) == "true"
			if !running {
				// Try to start it
				if err := exec.CommandContext(ctx, "docker", "start", env.containerID).Run(); err == nil {
					return nil
				}
			} else {
				return nil // Container is already running
			}
		}
		// Container doesn't exist or can't be started, create new one
	}

	// Check if we have snapshots to restore from
	var fromImage string
	if len(env.State.Snapshots) > 0 {
		lastSnapshot := env.State.Snapshots[len(env.State.Snapshots)-1]
		fromImage = lastSnapshot.ID
		slog.Info("Restoring from snapshot", "image", fromImage, "message", lastSnapshot.Message)
	} else {
		fromImage = "container-use-claude"
	}

	// Create container
	return env.createContainer(ctx, worktree, fromImage)
}

func (env *Environment) createContainer(ctx context.Context, worktree, fromImage string) error {
	// Remove any existing container with same name
	exec.CommandContext(ctx, "docker", "rm", "-f", fmt.Sprintf("cu-%s", env.ID)).Run()

	// Start manager server before creating container
	managerAddr, err := env.startManagerServer(ctx)
	if err != nil {
		return fmt.Errorf("failed to start manager server: %w", err)
	}

	args := []string{
		"run", "-d", "-it",
		"--name", fmt.Sprintf("cu-%s", env.ID),
		"-h", fmt.Sprintf("cu-%s", env.ID),
		"-w", env.State.Config.Workdir,
		"-v", fmt.Sprintf("%s:%s", worktree, env.State.Config.Workdir),
		"-e", "CU_ENVIRONMENT_ID=" + env.ID,
		"-e", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1",
		"-e", "MANAGER_ADDR=" + managerAddr, // Tell proxy where to connect
	}

	// Add environment variables
	for _, envVar := range env.State.Config.Env {
		args = append(args, "-e", envVar)
	}

	// Add secrets as environment variables
	for _, name := range env.State.Config.Secrets.Keys() {
		args = append(args, "-e", fmt.Sprintf("%s=%s", name, env.State.Config.Secrets.Get(name)))
	}

	// Handle Claude auth - only mount .claude directory, not .claude.json
	args = setupClaudeAuth(args)

	// Use the specified image
	args = append(args, fromImage)

	output, err := exec.CommandContext(ctx, "docker", args...).Output()
	if err != nil {
		return fmt.Errorf("failed to create container: %w\nOutput: %s", err, output)
	}

	env.containerID = strings.TrimSpace(string(output))
	env.State.Container = env.containerID

	// Copy .claude.json file after container creation to avoid bind mount issues
	if err := env.copyClaudeConfig(ctx); err != nil {
		slog.Warn("Failed to copy Claude config", "error", err)
	}

	// Wait for proxy to connect to manager
	if err := env.waitForProxyConnection(ctx); err != nil {
		return fmt.Errorf("proxy failed to connect: %w", err)
	}

	// // Run setup commands if this is a fresh container (not from snapshot)
	// if fromImage == "container-use-claude" && len(env.State.Config.SetupCommands) > 0 {
	// 	slog.Info("Running setup commands", "count", len(env.State.Config.SetupCommands))
	// 	for i, command := range env.State.Config.SetupCommands {
	// 		slog.Info("Running setup command", "index", i, "command", command)
	// 		stdout, stderr, exitCode, err := env.execInContainer(ctx, []string{"sh", "-c", command})
	// 		if err != nil {
	// 			return fmt.Errorf("failed to run setup command: %w", err)
	// 		}
	// 		env.Notes.AddCommand(command, exitCode, stdout, stderr)
	// 		if exitCode != 0 {
	// 			slog.Error("Setup command failed", "command", command, "exitCode", exitCode, "stdout", stdout, "stderr", stderr)
	// 			return fmt.Errorf("setup command failed with exit code %d", exitCode)
	// 		}
	// 	}
	// }

	return nil
}

func (env *Environment) execInContainer(ctx context.Context, args []string) (stdout, stderr string, exitCode int, err error) {
	cmdArgs := append([]string{"exec", env.containerID}, args...)
	cmd := exec.CommandContext(ctx, "docker", cmdArgs...)

	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err = cmd.Run()
	stdout = stdoutBuf.String()
	stderr = stderrBuf.String()

	if exitErr, ok := err.(*exec.ExitError); ok {
		exitCode = exitErr.ExitCode()
		err = nil // Non-zero exit is not an error
	}

	return
}

func (env *Environment) StartSession(ctx context.Context, worktree string, claudeArgs []string) error {
	// Ensure we have a container
	if err := env.ensureContainer(ctx, worktree); err != nil {
		return fmt.Errorf("failed to ensure container: %w", err)
	}

	return env.attachToContainer(ctx, claudeArgs)
}

func (env *Environment) StartSessionFromSnapshot(ctx context.Context, worktree string, snapshotID string, claudeArgs []string) error {
	// Stop any existing container
	if env.containerID != "" {
		exec.CommandContext(ctx, "docker", "stop", env.containerID).Run()
		exec.CommandContext(ctx, "docker", "rm", env.containerID).Run()
		env.containerID = ""
	}

	// Create container from specific snapshot
	if err := env.createContainer(ctx, worktree, snapshotID); err != nil {
		return fmt.Errorf("failed to create container from snapshot: %w", err)
	}

	// Add -c flag to continue conversation
	if len(claudeArgs) == 0 || claudeArgs[0] != "-c" {
		claudeArgs = append([]string{"-c"}, claudeArgs...)
	}

	return env.attachToContainer(ctx, claudeArgs)
}

func (env *Environment) attachToContainer(ctx context.Context, claudeArgs []string) error {
	// Attach to container for interactive session
	args := []string{"attach"}
	args = append(args, env.containerID)

	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Pass claude args via environment
	if len(claudeArgs) > 0 {
		cmd.Env = append(os.Environ(), fmt.Sprintf("CLAUDE_ARGS=%s", strings.Join(claudeArgs, " ")))
	}

	return cmd.Run()
}

// startManagerServer starts a TCP server that the proxy will connect to
func (env *Environment) startManagerServer(ctx context.Context) (string, error) {
	// Listen on all interfaces so Docker can connect
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		return "", fmt.Errorf("failed to create listener: %w", err)
	}

	addr := listener.Addr().String()
	slog.Info("Manager server listening", "addr", addr)

	// On macOS, Docker containers need to use host.docker.internal
	if runtime.GOOS == "darwin" {
		_, port, _ := net.SplitHostPort(addr)
		addr = fmt.Sprintf("host.docker.internal:%s", port)
		slog.Info("Using Docker host address for macOS", "addr", addr)
	}

	// Accept connection in background
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			slog.Error("Failed to accept proxy connection", "error", err)
			return
		}
		slog.Info("Proxy connected", "remote", conn.RemoteAddr())

		env.mu.Lock()
		env.proxyManager = conn
		env.mu.Unlock()

		// Handle messages from proxy
		slog.Info("Starting to handle proxy messages")
		env.handleProxyMessages(ctx, "")
	}()

	return addr, nil
}

// waitForProxyConnection waits for the proxy to connect to the manager
func (env *Environment) waitForProxyConnection(ctx context.Context) error {
	for i := 0; i < 30; i++ {
		env.mu.RLock()
		connected := env.proxyManager != nil
		env.mu.RUnlock()

		if connected {
			slog.Info("Proxy connected successfully")
			return nil
		}

		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("proxy failed to connect after 15 seconds")
}

// SnapshotCallback is called when a snapshot is created
type SnapshotCallback func(*Environment, string) error

func (env *Environment) SetSnapshotCallback(callback SnapshotCallback) {
	env.mu.Lock()
	defer env.mu.Unlock()
	env.snapshotCallback = callback
}

func (env *Environment) handleProxyMessages(ctx context.Context, worktree string) {
	dec := json.NewDecoder(env.proxyManager)

	for {
		var msg struct {
			Action string          `json:"Action"`
			Data   json.RawMessage `json:"Data"`
		}

		if err := dec.Decode(&msg); err != nil {
			slog.Error("Failed to decode proxy message", "error", err)
			return
		}

		slog.Info("Received proxy message", "action", msg.Action)

		switch msg.Action {
		case "commit":
			// Proxy is signaling that tools completed
			var message string
			json.Unmarshal(msg.Data, &message)
			slog.Info("Processing commit", "message", message)

			// Create Docker snapshot
			imageID, err := env.commitContainer(ctx, message)
			if err != nil {
				slog.Error("Failed to commit container", "error", err)
				continue
			}

			// Add to notes
			env.Notes.Add("Snapshot created: %s (image: %s)", message, imageID[:12])

			// Trigger callback to commit worktree changes
			env.mu.RLock()
			callback := env.snapshotCallback
			env.mu.RUnlock()

			if callback != nil {
				if err := callback(env, message); err != nil {
					slog.Error("Failed to handle snapshot callback", "error", err)
				}
			}

		default:
			slog.Warn("Unknown proxy action", "action", msg.Action)
		}
	}
}

func (env *Environment) commitContainer(ctx context.Context, message string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "commit", "-m", message, env.containerID)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}

	imageID := strings.TrimSpace(string(output))

	// Track in state
	env.State.Snapshots = append(env.State.Snapshots, Snapshot{
		ID:        imageID,
		Message:   message,
		Timestamp: time.Now(),
		GitCommit: "", // Will be filled by repository when it commits
	})
	env.State.UpdatedAt = time.Now()

	return imageID, nil
}

// UpdateConfig recreates the container with new configuration
func (env *Environment) UpdateConfig(ctx context.Context, newConfig *EnvironmentConfig) error {
	_ = env.State.Config // oldConfig not used yet
	env.State.Config = newConfig

	// Stop existing container
	if env.containerID != "" {
		exec.CommandContext(ctx, "docker", "stop", env.containerID).Run()
		exec.CommandContext(ctx, "docker", "rm", env.containerID).Run()
		env.containerID = ""
	}

	// Container will be recreated with new config on next use
	return nil
}

// Delete removes the container and all snapshots
func (env *Environment) Delete(ctx context.Context) error {
	// Stop and remove container
	if env.containerID != "" {
		exec.CommandContext(ctx, "docker", "stop", env.containerID).Run()
		exec.CommandContext(ctx, "docker", "rm", env.containerID).Run()
	}

	// Remove all snapshot images
	for _, snapshot := range env.State.Snapshots {
		exec.CommandContext(ctx, "docker", "rmi", snapshot.ID).Run()
	}

	return nil
}

// setupClaudeAuth handles Claude authentication setup
func setupClaudeAuth(args []string) []string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		slog.Warn("Failed to get home directory", "error", err)
		return args
	}

	// Check for existing Claude config files
	claudeJSON := filepath.Join(homeDir, ".claude.json")
	claudeDir := filepath.Join(homeDir, ".claude")
	credentialsFile := filepath.Join(claudeDir, ".credentials.json")

	// Check if user has Claude credentials
	hasClaudeJSON := false
	hasCredentials := false

	if _, err := os.Stat(claudeJSON); err == nil {
		hasClaudeJSON = true
	}
	if _, err := os.Stat(credentialsFile); err == nil {
		hasCredentials = true
	}

	if !hasClaudeJSON && !hasCredentials {
		slog.Warn("Claude credentials not found. Claude may not work properly.\n" +
			"To set up Claude:\n" +
			"  1. Install: npm install -g @anthropic-ai/claude-code\n" +
			"  2. Login: claude login\n" +
			"Or see SETUP.md for manual configuration.")
	}

	// If we have credentials, mount only the .claude directory (not .claude.json to avoid macOS bind mount issues)
	if hasCredentials {
		args = append(args, "-v", fmt.Sprintf("%s:/home/cosmos/.claude", claudeDir))
		return args
	}

	// Try platform-specific credential retrieval
	configDir, err := os.UserConfigDir()
	if err != nil {
		return args
	}

	cuAuthDir := filepath.Join(configDir, "container-use", "claude-auth")
	os.MkdirAll(filepath.Join(cuAuthDir, ".claude"), 0755)

	// Copy .claude.json if it exists
	if _, err := os.Stat(claudeJSON); err == nil {
		if data, err := os.ReadFile(claudeJSON); err == nil {
			os.WriteFile(filepath.Join(cuAuthDir, ".claude.json"), data, 0644)
		}
	}

	// Try to get credentials from keychain (macOS only)
	if runtime.GOOS == "darwin" {
		keychainCmd := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w")
		if output, err := keychainCmd.Output(); err == nil {
			credPath := filepath.Join(cuAuthDir, ".claude", ".credentials.json")
			os.WriteFile(credPath, output, 0600)
		}
	}

	// Mount only credentials, not .claude.json
	credentialsInCU := filepath.Join(cuAuthDir, ".claude", ".credentials.json")

	if _, err := os.Stat(credentialsInCU); err == nil {
		args = append(args, "-v", fmt.Sprintf("%s:/home/cosmos/.claude/.credentials.json", credentialsInCU))
	}

	return args
}

// copyClaudeConfig copies the .claude.json file from host to container
func (env *Environment) copyClaudeConfig(ctx context.Context) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	claudeJSON := filepath.Join(homeDir, ".claude.json")

	// Check if the file exists
	if _, err := os.Stat(claudeJSON); err != nil {
		// Try from the container-use auth directory
		configDir, _ := os.UserConfigDir()
		claudeJSON = filepath.Join(configDir, "container-use", "claude-auth", ".claude.json")
		if _, err := os.Stat(claudeJSON); err != nil {
			return nil // No claude.json to copy
		}
	}

	// Copy the file using docker cp
	cmd := exec.CommandContext(ctx, "docker", "cp", claudeJSON, fmt.Sprintf("%s:/home/cosmos/.claude.json", env.containerID))
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to copy claude.json: %w\nOutput: %s", err, output)
	}

	return nil
}
