# Container-Use Setup Guide

## Prerequisites

1. **Docker** installed and running
2. **Go** 1.21+ (for building from source)
3. **Git** repository to track your work
4. **Claude CLI** credentials (optional but recommended)

## Initial Setup

### 1. Build Container-Use

```bash
# Clone and build
git clone <repository-url>
cd container-use
go build -o container-use ./cmd/container-use
```

### 2. Set up Claude Authentication (Optional)

Container-Use can work without Claude credentials, but you'll need them for Claude to function properly.

#### Option A: If you already have Claude CLI installed
```bash
# Your credentials are already in place:
# ~/.claude.json
# ~/.claude/.credentials.json
```

#### Option B: First-time Claude setup
```bash
# Install Claude CLI first
npm install -g @anthropic-ai/claude-code

# Login to Claude (this will open a browser)
claude login

# This creates:
# ~/.claude.json - Claude configuration
# ~/.claude/.credentials.json - Authentication token
```

#### Option C: Manual setup
Create `~/.claude.json`:
```json
{
  "canonicalWorkspace": "/workspace"
}
```

For credentials, you'll need to get them from Claude's web interface or existing installation.

### 3. Initialize Your Project

```bash
# In your git repository
cd my-project
git init  # if not already a git repo

# Add container-use to your PATH or use full path
export PATH="$PATH:/path/to/container-use"
```

## Usage

### Start a New Session

```bash
# Default environment
container-use start

# Named environment
container-use start myproject

# With Claude arguments
container-use start myproject -- --print "Hello"
```

### First Run

On first run, container-use will:
1. Build the Docker image (this takes a few minutes)
2. Set up the proxy and container
3. Start Claude in the container

The image build is cached, so subsequent runs are fast.

### List Environments and Snapshots

```bash
# List all environments
container-use list

# List snapshots for an environment
container-use list myproject
```

### Resume from Snapshot

```bash
# Resume from the latest snapshot (automatic)
container-use start myproject

# Resume from a specific snapshot
container-use start myproject --from-snapshot abc123
```

## How It Works

1. **Automatic Snapshots**: When Claude uses tools (reads files, writes code, etc.), container-use automatically creates snapshots
2. **Git Integration**: Each snapshot includes both Docker state and git commits
3. **Isolation**: Each environment runs in its own container with its own worktree

## Troubleshooting

### Reset State

```bash
# Remove container-use data
rm -rf ~/.config/container-use

# Remove all containers
docker ps -a | grep "cu-" | awk '{print $1}' | xargs docker rm -f

# Remove all images
docker images | grep "container-use-claude" | awk '{print $3}' | xargs docker rmi
```

### Missing Credentials

If Claude can't authenticate:
1. Check `~/.claude.json` exists
2. Check `~/.claude/.credentials.json` exists
3. Try `claude login` to refresh credentials

### Build Errors

If the Docker image fails to build:
```bash
# Manually build to see errors
cd container-use
docker build -t container-use-claude -f environment/resources/Dockerfile.claude .
```

## Testing

### Basic Test
```bash
# Create a test environment
container-use start test-env -- --print "list files" --no-interactive

# Check snapshots were created
container-use list test-env

# Check git commits
git log --oneline
```

### Interactive Test
```bash
# Start interactive session
container-use start test-env

# In Claude, try:
# > Read README.md
# > Write a test file
# > Exit

# Check snapshots
container-use list test-env
```

## Architecture

- **Host Process**: Manages git, Docker containers, and orchestration
- **Container**: Runs Claude with a proxy that intercepts API calls
- **Proxy**: Tracks tool usage and signals for snapshots
- **Manager Protocol**: Host listens, container connects (supports multiple containers)

## Next Steps

1. Start with `container-use start myproject`
2. Use Claude normally - snapshots are automatic
3. Resume work anytime with `container-use start myproject`
4. Review history with `container-use list myproject`