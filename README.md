# container-use

Containerized environments for coding agents

## Usage

### From Source

```sh
go run .
```

### Claude Code

```sh
npx @anthropic-ai/claude-code mcp add container-use -e CU_STDERR_FILE=/tmp/cu.debug.stderr.log -- container-use
```

See [rules/claude.md](rules/claude.md) for Claude-specific configuration.

### Goose

In `~/.config/goose/config.yaml`

```yaml
extensions:
  container-use:
    name: container-use
    type: stdio
    enabled: true
    args:
    - run
    - <path to checked out repo>
    cmd: go
    envs:
      CU_STDERR_FILE: /tmp/cu.debug.stderr.log
```

See [rules/goose.md](rules/goose.md) for Goose-specific configuration.

### Other AI Assistants

See the [rules directory](rules/) for configuration instructions for other AI coding assistants.

## Configuration

AI coding assistants need rule files with instructions for working with container-use. See the [rules directory](rules/) for setup guides for your specific assistant.

## Examples

| Example | Description |
|---------|-------------|
| [hello_world.md](examples/hello_world.md) | Creates a simple app and runs it, accessible via localhost HTTP URL |
| [history.md](examples/history.md) | Demonstrates container snapshotting by making changes to an app and showing history/diffs of the modifications |
| [parallel.md](examples/parallel.md) | Creates and serves two variations of a hello world app (Flask and FastAPI) on different URLs |
| [multibuild.md](examples/multibuild.md) | Builds the current project using the 3 most recent Go versions |
| [security.md](examples/security.md) | Security scanning example that checks for updates/vulnerabilities in the repository, applies updates, verifies builds still work, and generates patch file |

Run with goose:

```console
goose run -i ./examples/security.md -s
```
