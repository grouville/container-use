# Container-Use Rules

Concise rule files and instructions for AI coding assistants using container-use.

## Quick Reference

| File                       | Description                       | Placement                         |
| -------------------------- | --------------------------------- | --------------------------------- |
| [agent.md](./agent.md)     | Generic rules for most assistants | Assistant-specific placement      |
| [cursor.mdc](./cursor.mdc) | Cursor-specific MDC rules         | `.cursor/rules/container-use.mdc` |

## Essential Container-Use Rules

1. **Always use container environments** for file, code, or shell operations.
2. **Git commands are unsupported** inside containers; changes propagate automatically.
3. **Never install git CLI** or execute `rm .git` inside containers.

## Contributing

When adding support for new AI assistants:

1. Create a concise rule file matching the assistant name.
2. Clearly state file placement instructions.
3. Update this README with an entry.

For complete setup details, see the [main README](../README.md).
