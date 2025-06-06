package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"dagger.io/dagger"

	"github.com/google/uuid"
)

const (
	defaultImage     = "ubuntu:24.04"
	alpineImage      = "alpine:3.21.3@sha256:a8560b36e8b8210634f77d9f7f9efd7ffa463e380b75e2e74aff4511df3ef88c"
	configDir        = ".container-use"
	instructionsFile = "AGENT.md"
	environmentFile  = "environment.json"
	lockFile         = "lock"
)

type Version int

type Revision struct {
	Version     Version   `json:"version"`
	Name        string    `json:"name"`
	Explanation string    `json:"explanation"`
	Output      string    `json:"output,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	State       string    `json:"state"`

	container *dagger.Container `json:"-"`
}

type History []*Revision

func (h History) Latest() *Revision {
	if len(h) == 0 {
		return nil
	}
	return h[len(h)-1]
}

func (h History) LatestVersion() Version {
	latest := h.Latest()
	if latest == nil {
		return 0
	}
	return latest.Version
}

func (h History) Get(version Version) *Revision {
	for _, revision := range h {
		if revision.Version == version {
			return revision
		}
	}
	return nil
}

type Environment struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Source   string `json:"-"`
	Worktree string `json:"-"`

	Instructions  string   `json:"-"`
	Workdir       string   `json:"workdir"`
	BaseImage     string   `json:"base_image"`
	SetupCommands []string `json:"setup_commands"`

	History History `json:"-"`

	mu        sync.Mutex
	container *dagger.Container
}

func (env *Environment) save(baseDir string) error {
	cfg := path.Join(baseDir, configDir)
	if err := os.MkdirAll(cfg, 0755); err != nil {
		return err
	}

	if err := os.WriteFile(path.Join(cfg, instructionsFile), []byte(env.Instructions), 0644); err != nil {
		return err
	}

	envState, err := json.MarshalIndent(env, "", "  ")
	if err != nil {
		return err
	}

	if err := os.WriteFile(path.Join(cfg, environmentFile), envState, 0644); err != nil {
		return err
	}

	return nil
}

func (env *Environment) load(baseDir string) error {
	cfg := path.Join(baseDir, configDir)

	instructions, err := os.ReadFile(path.Join(cfg, instructionsFile))
	if err != nil {
		return err
	}
	env.Instructions = string(instructions)

	envState, err := os.ReadFile(path.Join(cfg, environmentFile))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(envState, env); err != nil {
		return err
	}

	return nil
}

func (env *Environment) isLocked(baseDir string) bool {
	if _, err := os.Stat(path.Join(baseDir, configDir, lockFile)); err == nil {
		return true
	}
	return false
}

func (e *Environment) apply(ctx context.Context, name, explanation, output string, newState *dagger.Container) error {
	if _, err := newState.Sync(ctx); err != nil {
		return err
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	revision := &Revision{
		Version:     e.History.LatestVersion() + 1,
		Name:        name,
		Explanation: explanation,
		Output:      output,
		CreatedAt:   time.Now(),
		container:   newState,
	}
	containerID, err := revision.container.ID(ctx)
	if err != nil {
		return err
	}
	revision.State = string(containerID)
	e.container = revision.container
	e.History = append(e.History, revision)

	return nil
}

var environments = map[string]*Environment{}

func CreateEnvironment(ctx context.Context, explanation, source, name string) (*Environment, error) {
	env := &Environment{
		ID:           uuid.New().String(),
		Name:         name,
		Source:       source,
		BaseImage:    defaultImage,
		Instructions: "No instructions found. Please look around the filesystem and update me",
		Workdir:      "/workdir",
	}

	worktreePath, err := env.InitializeWorktree(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("failed intializing worktree: %w", err)
	}
	env.Worktree = worktreePath

	container, err := env.buildBase(ctx)
	if err != nil {
		return nil, err
	}

	slog.Info("Creating environment", "id", env.ID, "name", env.Name, "workdir", env.Workdir)

	if err := env.apply(ctx, "Create environment", "Create the environment", "", container); err != nil {
		return nil, err
	}
	environments[env.ID] = env

	if err := env.propagateToWorktree(ctx, "Init env "+name, explanation); err != nil {
		return nil, fmt.Errorf("failed to propagate to worktree: %w", err)
	}

	return env, nil
}

func OpenEnvironment(ctx context.Context, explanation, source, name string) (*Environment, error) {
	env := &Environment{}
	if err := env.load(source); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return CreateEnvironment(ctx, explanation, source, name)
		}
		return nil, err
	}

	env.Name = name
	env.Source = source
	worktreePath, err := env.InitializeWorktree(ctx, source)
	if err != nil {
		return nil, fmt.Errorf("failed intializing worktree: %w", err)
	}
	env.Worktree = worktreePath

	if err := env.loadStateFromNotes(ctx, worktreePath); err != nil {
		return nil, fmt.Errorf("failed to load state from notes: %w", err)
	}

	for _, revision := range env.History {
		revision.container = dag.LoadContainerFromID(dagger.ContainerID(revision.State))
	}
	if latest := env.History.Latest(); latest != nil {
		env.container = latest.container
	}

	environments[env.ID] = env
	return env, nil
}

func (env *Environment) buildBase(ctx context.Context) (*dagger.Container, error) {
	sourceDir := dag.Host().Directory(env.Worktree)

	container := dag.
		Container().
		From(env.BaseImage).
		WithWorkdir(env.Workdir)

	for _, command := range env.SetupCommands {
		container = container.WithExec([]string{"sh", "-c", command})
	}

	container = container.WithDirectory(".", sourceDir)

	container, err := container.Sync(ctx)
	if err != nil {
		var exitErr *dagger.ExecError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("build failed with exit code %d.\nstdout: %s\nstderr: %s", exitErr.ExitCode, exitErr.Stdout, exitErr.Stderr)
		}
		return nil, err
	}

	return container, nil
}

func (env *Environment) Update(ctx context.Context, explanation, instructions, baseImage string, setupCommands []string) error {
	if env.isLocked(env.Source) {
		return fmt.Errorf("Environment is locked, no updates allowed. Try to make do with the current environment or ask a human to remove the lock file (%s)", path.Join(env.Source, configDir, lockFile))
	}

	env.Instructions = instructions
	env.BaseImage = baseImage
	env.SetupCommands = setupCommands

	container, err := env.buildBase(ctx)
	if err != nil {
		return err
	}

	if err := env.apply(ctx, "Update environment", explanation, "", container); err != nil {
		return err
	}

	return env.propagateToWorktree(ctx, "Update environment "+env.Name, explanation)
}

func GetEnvironment(idOrName string) *Environment {
	if environment, ok := environments[idOrName]; ok {
		return environment
	}
	for _, environment := range environments {
		if environment.Name == idOrName {
			return environment
		}
	}
	return nil
}

func ListEnvironments() []*Environment {
	env := make([]*Environment, 0, len(environments))
	for _, environment := range environments {
		env = append(env, environment)
	}
	return env
}

func (env *Environment) Run(ctx context.Context, explanation, command, shell string, useEntrypoint bool) (string, error) {
	args := []string{}
	if command != "" {
		args = []string{shell, "-c", command}
	}
	newState := env.container.WithExec(args, dagger.ContainerWithExecOpts{
		UseEntrypoint: useEntrypoint,
	})
	stdout, err := newState.Stdout(ctx)
	if err != nil {
		var exitErr *dagger.ExecError
		if errors.As(err, &exitErr) {
			return fmt.Sprintf("command failed with exit code %d.\nstdout: %s\nstderr: %s", exitErr.ExitCode, exitErr.Stdout, exitErr.Stderr), nil
		}
		return "", err
	}
	if err := env.apply(ctx, "Run "+command, explanation, stdout, newState); err != nil {
		return "", err
	}

	if err := env.propagateToWorktree(ctx, "Run "+command, explanation); err != nil {
		return "", fmt.Errorf("failed to propagate to worktree: %w", err)
	}

	return stdout, nil
}

type EndpointMapping struct {
	Internal string `json:"internal"`
	External string `json:"external"`
}

type EndpointMappings map[int]*EndpointMapping

func (env *Environment) RunBackground(ctx context.Context, explanation, command, shell string, ports []int, useEntrypoint bool) (EndpointMappings, error) {
	args := []string{}
	if command != "" {
		args = []string{shell, "-c", command}
	}
	serviceState := env.container

	// Expose ports
	for _, port := range ports {
		serviceState = serviceState.WithExposedPort(port, dagger.ContainerWithExposedPortOpts{
			Protocol:    dagger.NetworkProtocolTcp,
			Description: fmt.Sprintf("Port %d", port),
		})
	}

	// Start the service
	svc, err := serviceState.AsService(dagger.ContainerAsServiceOpts{
		Args:          args,
		UseEntrypoint: useEntrypoint,
	}).Start(ctx)
	if err != nil {
		var exitErr *dagger.ExecError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("command failed with exit code %d.\nstdout: %s\nstderr: %s", exitErr.ExitCode, exitErr.Stdout, exitErr.Stderr)
		}
		return nil, err
	}

	endpoints := EndpointMappings{}
	hostForwards := []dagger.PortForward{}

	for _, port := range ports {
		endpoints[port] = &EndpointMapping{}
		hostForwards = append(hostForwards, dagger.PortForward{
			Backend:  port,
			Frontend: rand.Intn(1000) + 5000,
			Protocol: dagger.NetworkProtocolTcp,
		})
	}

	// Expose ports on the host
	tunnel, err := dag.Host().Tunnel(svc, dagger.HostTunnelOpts{Ports: hostForwards}).Start(ctx)
	if err != nil {
		return nil, err
	}

	// Retrieve endpoints
	for _, forward := range hostForwards {
		externalEndpoint, err := tunnel.Endpoint(ctx, dagger.ServiceEndpointOpts{
			Port: forward.Frontend,
		})
		if err != nil {
			return nil, err
		}

		endpoints[forward.Backend].External = externalEndpoint
	}
	for port, endpoint := range endpoints {
		internalEndpoint, err := svc.Endpoint(ctx, dagger.ServiceEndpointOpts{
			Port: port,
		})
		if err != nil {
			return nil, err
		}
		endpoint.Internal = internalEndpoint
	}

	return endpoints, nil
}

func (env *Environment) SetEnv(ctx context.Context, explanation string, envs []string) error {
	state := env.container
	for _, env := range envs {
		parts := strings.SplitN(env, "=", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid environment variable: %s", env)
		}
		state = state.WithEnvVariable(parts[0], parts[1])
	}
	return env.apply(ctx, "Set env "+strings.Join(envs, ", "), explanation, "", state)
}

func (env *Environment) Revert(ctx context.Context, explanation string, version Version) error {
	revision := env.History.Get(version)
	if revision == nil {
		return errors.New("no revisions found")
	}
	if err := env.apply(ctx, "Revert to "+revision.Name, explanation, "", revision.container); err != nil {
		return err
	}
	return env.propagateToWorktree(ctx, "Revert to "+revision.Name, explanation)
}

func (env *Environment) Fork(ctx context.Context, explanation, name string, version *Version) (*Environment, error) {
	revision := env.History.Latest()
	if version != nil {
		revision = env.History.Get(*version)
	}
	if revision == nil {
		return nil, errors.New("version not found")
	}

	forkedEnvironment := &Environment{
		ID:   uuid.New().String(),
		Name: name,
	}
	if err := forkedEnvironment.apply(ctx, "Fork from "+env.Name, explanation, "", revision.container); err != nil {
		return nil, err
	}
	environments[forkedEnvironment.ID] = forkedEnvironment
	return forkedEnvironment, nil
}
