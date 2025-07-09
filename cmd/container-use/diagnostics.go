package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/dagger/container-use/repository"
)

// VersionInfo contains version information.
type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
}

// Snapshot captures the complete diagnostic state.
type Snapshot struct {
	Version         VersionInfo                `json:"version"`
	Git             GitInfo                    `json:"git"`
	Docker          DockerInfo                 `json:"docker"`
	Filesystem      FilesystemInfo             `json:"filesystem"`
	Environments    map[string]EnvironmentInfo `json:"environments"`
	Inconsistencies []string                   `json:"inconsistencies,omitempty"`
	RecentErrors    []string                   `json:"recent_errors,omitempty"`
}

// GitInfo holds Git repository information.
type GitInfo struct {
	InRepo            bool              `json:"in_repo"`
	Branch            string            `json:"branch,omitempty"`
	Remotes           map[string]string `json:"remotes,omitempty"`
	WorktreeCount     int               `json:"worktree_count"`
	CURemoteExists    bool              `json:"cu_remote_exists"`
	CURemoteReachable bool              `json:"cu_remote_reachable"`
	CUBranchCount     int               `json:"cu_branch_count"`
	CUBranches        []string          `json:"cu_branches,omitempty"`
}

// DockerInfo holds Docker and Dagger information.
type DockerInfo struct {
	Available        bool              `json:"available"`
	DaggerSDKVersion string            `json:"dagger_sdk_version,omitempty"`
	DaggerEngines    []DaggerEngine    `json:"dagger_engines,omitempty"`
	DaggerEnvVars    map[string]string `json:"dagger_env_vars,omitempty"`
}

// DaggerEngine represents a running Dagger engine.
type DaggerEngine struct {
	Name    string `json:"name"`
	Image   string `json:"image"`
	Version string `json:"version"`
}

// FilesystemInfo holds filesystem state.
type FilesystemInfo struct {
	ConfigDirExists   bool     `json:"config_dir_exists"`
	WorktreeDirExists bool     `json:"worktree_dir_exists"`
	WorktreeNames     []string `json:"worktree_names,omitempty"`
}

// EnvironmentInfo represents a single environment's state.
type EnvironmentInfo struct {
	ID           string `json:"id"`
	HasWorktree  bool   `json:"has_worktree"`
	HasCUDir     bool   `json:"has_cu_dir"`
	HasEnvJSON   bool   `json:"has_environment_json"`
	HasAgentMD   bool   `json:"has_agent_md"`
	HasGitBranch bool   `json:"has_git_branch"`
	HasGitNotes  bool   `json:"has_git_notes"`
}

// Collector gathers diagnostic information.
type Collector struct {
	ctx    context.Context
	errors []error
}

// NewCollector creates a new diagnostic collector.
func NewCollector(ctx context.Context) *Collector {
	return &Collector{
		ctx:    ctx,
		errors: make([]error, 0),
	}
}

// Collect gathers all diagnostic information.
func (c *Collector) Collect() Snapshot {
	snapshot := Snapshot{
		Version:      c.version(),
		Git:          c.git(),
		Docker:       c.docker(),
		Filesystem:   c.filesystem(),
		Environments: make(map[string]EnvironmentInfo),
	}
	
	c.environments(&snapshot)
	snapshot.Inconsistencies = c.inconsistencies(&snapshot)
	snapshot.RecentErrors = c.recentErrors()
	
	return snapshot
}

func (c *Collector) version() VersionInfo {
	return VersionInfo{
		Version:   version,
		Commit:    commit,
		BuildDate: date,
		GoVersion: runtime.Version(),
		OS:        runtime.GOOS + "/" + runtime.GOARCH,
	}
}

func (c *Collector) git() GitInfo {
	info := GitInfo{
		Remotes: make(map[string]string),
	}
	
	if _, err := c.gitCmd("rev-parse", "--git-dir"); err != nil {
		return info
	}
	
	info.InRepo = true
	
	if branch, err := c.gitCmd("branch", "--show-current"); err == nil {
		info.Branch = strings.TrimSpace(branch)
	}
	
	c.gitRemotes(&info)
	c.gitWorktrees(&info)
	c.gitCUBranches(&info)
	
	return info
}

func (c *Collector) gitRemotes(info *GitInfo) {
	out, err := c.gitCmd("remote", "-v")
	if err != nil {
		return
	}
	
	for line := range strings.SplitSeq(out, "\n") {
		parts := strings.Fields(line)
		if len(parts) >= 2 && strings.HasSuffix(line, "(fetch)") {
			info.Remotes[parts[0]] = parts[1]
		}
	}
	
	if _, ok := info.Remotes["container-use"]; ok {
		info.CURemoteExists = true
		if _, err := c.gitCmd("ls-remote", "--exit-code", "container-use", "HEAD"); err == nil {
			info.CURemoteReachable = true
		}
	}
}

func (c *Collector) gitWorktrees(info *GitInfo) {
	out, err := c.gitCmd("worktree", "list", "--porcelain")
	if err != nil {
		return
	}
	info.WorktreeCount = strings.Count(out, "worktree ")
}

func (c *Collector) gitCUBranches(info *GitInfo) {
	if !info.CURemoteExists {
		return
	}
	
	out, err := c.gitCmd("branch", "-r", "--list", "container-use/*")
	if err != nil {
		return
	}
	
	const prefix = "container-use/"
	const prefixLen = len(prefix)
	
	for line := range strings.SplitSeq(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		if idx := strings.Index(line, prefix); idx != -1 {
			branch := line[idx+prefixLen:]
			info.CUBranches = append(info.CUBranches, branch)
		}
	}
	info.CUBranchCount = len(info.CUBranches)
}

func (c *Collector) docker() DockerInfo {
	info := DockerInfo{
		DaggerEnvVars: make(map[string]string),
	}
	
	ctx, cancel := context.WithTimeout(c.ctx, 2*time.Second)
	defer cancel()
	
	cmd := exec.CommandContext(ctx, "docker", "version", "--format", "json")
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		c.recordError(fmt.Errorf("docker version: %w", err))
		return info
	}
	
	info.Available = true
	c.daggerSDKVersion(&info)
	c.daggerEngines(&info)
	c.daggerEnvVars(&info)
	
	return info
}

func (c *Collector) daggerSDKVersion(info *DockerInfo) {
	gomodContent, err := os.ReadFile("go.mod")
	if err != nil {
		c.recordError(fmt.Errorf("read go.mod: %w", err))
		return
	}
	
	for line := range strings.SplitSeq(string(gomodContent), "\n") {
		if strings.Contains(line, "dagger.io/dagger") && strings.Contains(line, "v") {
			if parts := strings.Fields(line); len(parts) >= 2 {
				info.DaggerSDKVersion = parts[1]
				return
			}
		}
	}
}

func (c *Collector) daggerEngines(info *DockerInfo) {
	ctx, cancel := context.WithTimeout(c.ctx, 2*time.Second)
	defer cancel()
	
	cmd := exec.CommandContext(ctx, "docker", "ps", "--filter", "name=dagger-engine", "--format", "{{.Names}}\t{{.Image}}")
	out, err := cmd.Output()
	if err != nil {
		c.recordError(fmt.Errorf("docker ps: %w", err))
		return
	}
	
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		
		parts := strings.Split(line, "\t")
		if len(parts) != 2 {
			continue
		}
		
		engine := DaggerEngine{
			Name:  parts[0],
			Image: parts[1],
		}
		
		if imageParts := strings.Split(parts[1], ":"); len(imageParts) > 1 {
			engine.Version = imageParts[1]
		}
		
		info.DaggerEngines = append(info.DaggerEngines, engine)
	}
}

var daggerEnvironmentVars = []string{
	"_EXPERIMENTAL_DAGGER_RUNNER_HOST",
	"_EXPERIMENTAL_DAGGER_CLI_BIN",
}

func (c *Collector) daggerEnvVars(info *DockerInfo) {
	for _, envVar := range daggerEnvironmentVars {
		if value := os.Getenv(envVar); value != "" {
			info.DaggerEnvVars[envVar] = value
		}
	}
}

func (c *Collector) filesystem() FilesystemInfo {
	info := FilesystemInfo{}
	
	homeDir, _ := os.UserHomeDir()
	configDir := filepath.Join(homeDir, ".config", "container-use")
	
	if _, err := os.Stat(configDir); err != nil {
		return info
	}
	
	info.ConfigDirExists = true
	
	worktreesDir := filepath.Join(configDir, "worktrees")
	entries, err := os.ReadDir(worktreesDir)
	if err != nil {
		return info
	}
	
	info.WorktreeDirExists = true
	for _, entry := range entries {
		if entry.IsDir() {
			info.WorktreeNames = append(info.WorktreeNames, entry.Name())
		}
	}
	
	return info
}

func (c *Collector) environments(snapshot *Snapshot) {
	c.environmentsFromFilesystem(snapshot)
	c.environmentsFromGit(snapshot)
	c.environmentsFromGitNotes(snapshot)
}

func (c *Collector) environmentsFromFilesystem(snapshot *Snapshot) {
	homeDir, _ := os.UserHomeDir()
	
	for _, name := range snapshot.Filesystem.WorktreeNames {
		env := EnvironmentInfo{
			ID:          name,
			HasWorktree: true,
		}
		
		worktreePath := filepath.Join(homeDir, ".config", "container-use", "worktrees", name)
		cuDir := filepath.Join(worktreePath, ".container-use")
		
		if _, err := os.Stat(cuDir); err == nil {
			env.HasCUDir = true
			
			if _, err := os.Stat(filepath.Join(cuDir, "environment.json")); err == nil {
				env.HasEnvJSON = true
			}
			
			if _, err := os.Stat(filepath.Join(cuDir, "AGENT.md")); err == nil {
				env.HasAgentMD = true
			}
		}
		
		snapshot.Environments[name] = env
	}
}

func (c *Collector) environmentsFromGit(snapshot *Snapshot) {
	for _, branch := range snapshot.Git.CUBranches {
		if env, exists := snapshot.Environments[branch]; exists {
			env.HasGitBranch = true
			snapshot.Environments[branch] = env
		} else {
			snapshot.Environments[branch] = EnvironmentInfo{
				ID:           branch,
				HasGitBranch: true,
			}
		}
	}
}

func (c *Collector) environmentsFromGitNotes(snapshot *Snapshot) {
	if !snapshot.Git.CURemoteExists {
		return
	}
	
	homeDir, _ := os.UserHomeDir()
	repoPath := filepath.Join(homeDir, ".config", "container-use", "repos")
	
	entries, err := os.ReadDir(repoPath)
	if err != nil || len(entries) == 0 {
		return
	}
	
	for id, env := range snapshot.Environments {
		cmd := fmt.Sprintf("cd %s && git notes --ref container-use-state show %s 2>/dev/null",
			filepath.Join(repoPath, entries[0].Name()), id)
		
		ctx, cancel := context.WithTimeout(c.ctx, 1*time.Second)
		defer cancel()
		
		if out, err := exec.CommandContext(ctx, "sh", "-c", cmd).Output(); err == nil && len(out) > 0 {
			env.HasGitNotes = true
			snapshot.Environments[id] = env
		}
	}
}

func (c *Collector) inconsistencies(snapshot *Snapshot) []string {
	var results []string
	
	c.checkEnvironmentCounts(snapshot, &results)
	c.checkEnvironmentStates(snapshot, &results)
	c.checkGitRemote(snapshot, &results)
	c.checkDaggerVersions(snapshot, &results)
	
	return results
}

func (c *Collector) checkEnvironmentCounts(snapshot *Snapshot, results *[]string) {
	worktreeCount := len(snapshot.Filesystem.WorktreeNames)
	branchCount := snapshot.Git.CUBranchCount
	
	if worktreeCount != branchCount {
		*results = append(*results,
			fmt.Sprintf("Worktree count (%d) != Git branch count (%d)", worktreeCount, branchCount))
	}
}

func (c *Collector) checkEnvironmentStates(snapshot *Snapshot, results *[]string) {
	for id, env := range snapshot.Environments {
		if env.HasWorktree && !env.HasGitBranch {
			*results = append(*results,
				fmt.Sprintf("'%s': has worktree but no git branch", id))
		}
		
		if env.HasGitBranch && !env.HasWorktree {
			*results = append(*results,
				fmt.Sprintf("'%s': has git branch but no worktree", id))
		}
		
		if env.HasWorktree && !env.HasGitNotes {
			*results = append(*results,
				fmt.Sprintf("'%s': has worktree but no git notes (invisible to 'cu list')", id))
		}
		
		if env.HasGitNotes && !env.HasWorktree {
			*results = append(*results,
				fmt.Sprintf("'%s': has git notes but no worktree", id))
		}
		
		if env.HasWorktree && !env.HasCUDir {
			*results = append(*results,
				fmt.Sprintf("'%s': worktree missing .container-use directory", id))
		}
		
		if env.HasCUDir && !env.HasEnvJSON {
			*results = append(*results,
				fmt.Sprintf("'%s': .container-use directory missing environment.json", id))
		}
	}
}

func (c *Collector) checkGitRemote(snapshot *Snapshot, results *[]string) {
	if !snapshot.Git.InRepo {
		*results = append(*results, "Not in a git repository")
		return
	}
	
	envCount := len(snapshot.Environments)
	if !snapshot.Git.CURemoteExists && envCount > 0 {
		*results = append(*results, "Git remote 'container-use' missing but environments exist")
	} else if snapshot.Git.CURemoteExists && !snapshot.Git.CURemoteReachable {
		*results = append(*results, "Git remote 'container-use' unreachable")
	}
}

func (c *Collector) checkDaggerVersions(snapshot *Snapshot, results *[]string) {
	if snapshot.Docker.DaggerSDKVersion == "" || len(snapshot.Docker.DaggerEngines) == 0 {
		return
	}
	
	sdkVersion := snapshot.Docker.DaggerSDKVersion
	hasMatchingEngine := slices.ContainsFunc(snapshot.Docker.DaggerEngines, func(e DaggerEngine) bool {
		return e.Version == sdkVersion
	})
	
	if !hasMatchingEngine {
		engineVersions := make([]string, 0, len(snapshot.Docker.DaggerEngines))
		for _, engine := range snapshot.Docker.DaggerEngines {
			engineVersions = append(engineVersions, engine.Version)
		}
		*results = append(*results,
			fmt.Sprintf("Dagger SDK %s expects matching engine, but found: %s",
				sdkVersion, strings.Join(engineVersions, ", ")))
	}
	
	if len(snapshot.Docker.DaggerEngines) > 1 {
		*results = append(*results,
			fmt.Sprintf("Multiple Dagger engines running (%d), may cause connection issues",
				len(snapshot.Docker.DaggerEngines)))
	}
}

func (c *Collector) recentErrors() []string {
	logFile := c.errorLogPath()
	content, err := os.ReadFile(logFile)
	if err != nil {
		return nil
	}
	
	const maxBytes = 1024
	if len(content) > maxBytes {
		content = content[len(content)-maxBytes:]
	}
	
	errors := make([]string, 0, 3)
	for line := range strings.SplitSeq(string(content), "\n") {
		if strings.Contains(line, "ERROR") || strings.Contains(line, "exit code 128") {
			errors = append(errors, strings.TrimSpace(line))
			if len(errors) >= 3 {
				break
			}
		}
	}
	
	return errors
}

func (c *Collector) errorLogPath() string {
	if logFile := os.Getenv("CU_STDERR_FILE"); logFile != "" {
		return logFile
	}
	
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), "cu.debug.stderr.log")
	}
	return "/tmp/cu.debug.stderr.log"
}

func (c *Collector) gitCmd(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(c.ctx, 2*time.Second)
	defer cancel()
	
	out, err := repository.RunGitCommand(ctx, ".", args...)
	if err != nil {
		c.recordError(fmt.Errorf("git %s: %w", strings.Join(args, " "), err))
	}
	return out, err
}

func (c *Collector) recordError(err error) {
	if err != nil && !errors.Is(err, context.DeadlineExceeded) {
		c.errors = append(c.errors, err)
	}
}

// printDiagnostics prints a clean state snapshot.
func printDiagnostics(snapshot Snapshot) {
	fmt.Printf("VERSION: %s (%s)\n", snapshot.Version.Version, snapshot.Version.OS)
	fmt.Printf("BUILD: %s\n\n", snapshot.Version.Commit)
	
	printGitInfo(snapshot.Git)
	printDockerInfo(snapshot.Docker)
	printFilesystemInfo(snapshot.Filesystem)
	printEnvironments(snapshot.Environments)
	printInconsistencies(snapshot.Inconsistencies)
	printRecentErrors(snapshot.RecentErrors)
}

func printGitInfo(git GitInfo) {
	fmt.Println("GIT:")
	fmt.Printf("  in_repo: %v\n", git.InRepo)
	if !git.InRepo {
		return
	}
	
	fmt.Printf("  branch: %s\n", git.Branch)
	fmt.Printf("  remotes: %d\n", len(git.Remotes))
	fmt.Printf("  cu_remote: %v (reachable: %v)\n", git.CURemoteExists, git.CURemoteReachable)
	fmt.Printf("  cu_branches: %d\n", git.CUBranchCount)
	fmt.Printf("  worktrees: %d\n", git.WorktreeCount)
}

func printDockerInfo(docker DockerInfo) {
	fmt.Println("\nDOCKER:")
	fmt.Printf("  available: %v\n", docker.Available)
	
	if docker.DaggerSDKVersion != "" {
		fmt.Printf("  dagger_sdk: %s\n", docker.DaggerSDKVersion)
	}
	
	if len(docker.DaggerEngines) > 0 {
		fmt.Printf("  dagger_engines: %d running\n", len(docker.DaggerEngines))
		for _, engine := range docker.DaggerEngines {
			fmt.Printf("    - %s (%s)\n", engine.Name, engine.Version)
		}
	} else {
		fmt.Printf("  dagger_engines: none running\n")
	}
	
	if len(docker.DaggerEnvVars) > 0 {
		fmt.Printf("  dagger_env_vars:\n")
		for k, v := range docker.DaggerEnvVars {
			fmt.Printf("    %s: %s\n", k, v)
		}
	}
}

func printFilesystemInfo(fs FilesystemInfo) {
	fmt.Println("\nFILESYSTEM:")
	fmt.Printf("  config_dir: %v\n", fs.ConfigDirExists)
	fmt.Printf("  worktree_count: %d\n", len(fs.WorktreeNames))
}

func printEnvironments(environments map[string]EnvironmentInfo) {
	if len(environments) == 0 {
		return
	}
	
	fmt.Println("\nENVIRONMENTS:")
	for id, env := range environments {
		fmt.Printf("  %s:\n", id)
		fmt.Printf("    worktree: %v, cu_dir: %v, env.json: %v, agent.md: %v\n",
			env.HasWorktree, env.HasCUDir, env.HasEnvJSON, env.HasAgentMD)
		fmt.Printf("    git_branch: %v, git_notes: %v\n",
			env.HasGitBranch, env.HasGitNotes)
	}
}

func printInconsistencies(inconsistencies []string) {
	if len(inconsistencies) == 0 {
		return
	}
	
	fmt.Println("\nINCONSISTENCIES:")
	for _, inc := range inconsistencies {
		fmt.Printf("  - %s\n", inc)
	}
}

func printRecentErrors(errors []string) {
	if len(errors) == 0 {
		return
	}
	
	fmt.Println("\nRECENT ERRORS:")
	for _, err := range errors {
		fmt.Printf("  %s\n", err)
	}
}