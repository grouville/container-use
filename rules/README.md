# Container-Use Rules for AI Coding Assistants

This directory contains rule files and setup guides for various AI coding assistants to ensure they work correctly with the container-use system.

## Quick Reference

| AI Assistant | Rule File | Where to Place It | Notes |
|--------------|-----------|-------------------|-------|
| Claude Code | [claude.md](./claude.md) | `CLAUDE.md` in project root | Copy file content directly |
| GitHub Copilot | [copilot.md](./copilot.md) | `.github/copilot-instructions.md` | Works in VSCode, Visual Studio, JetBrains, etc. |
| Goose | [goose.md](./goose.md) | `.goosehints` in project root | Copy file content directly |
| Cursor | [cursor.md](./cursor.md) | See setup guide | Requires MDC format configuration |

## Available Rules

### 1. Rule Content Files
These files contain the actual rules that should be copied to your project:
- **claude.md** - Content for `CLAUDE.md`
- **copilot.md** - Content for `.github/copilot-instructions.md`
- **goose.md** - Content for `.goosehints`

### 2. Setup Guides
These files provide instructions for more complex configurations:
- **cursor.md** - Instructions for setting up Cursor with its MDC format

## Core Rules

All AI assistants using container-use follow these essential rules:

1. **Always use container environments** for all file, code, or shell operations
2. **Git operations are restricted** within containers - changes automatically propagate to the container-use git remote
3. **Never install git CLI** within containers
4. **Never run `rm .git`** as it will compromise system integrity
5. **Inform users** about the correct git checkout command to view their changes

## Setup Instructions

### For Claude, GitHub Copilot, and Goose:
1. Copy the content from the appropriate `.md` file
2. Create the target file in your project (see table above)
3. No modifications needed - use as-is

### For Cursor:
1. Follow the instructions in [cursor.md](./cursor.md)

## Note on VSCode

VSCode uses GitHub Copilot for AI assistance, so use the `copilot.md` rules by placing them in `.github/copilot-instructions.md`.

## Contributing

When adding support for new AI assistants:

1. Create a rule file with the essential container-use rules
2. Use a clear filename that matches the assistant name
3. Add a header explaining where the file should be placed
4. Update this README with the new entry
5. If complex setup is required, create a setup guide like `cursor.md`

## Learn More

For more information about container-use and its capabilities, see the [main README](../README.md) and check out the [examples directory](../examples/).