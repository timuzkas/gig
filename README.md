# gig

A minimalist GTK4 Git client for keyboard-centric workflows.

- Fast split and unified diff viewing
- Full keyboard shortcut support (customizable)
- Multi-repository sidebar
- Stash management and conflict resolution
- TOML-based configuration (~/.config/gig/config.toml)

## Keybindings

| Shortcut | Action |
| :--- | :--- |
| `Ctrl + O` | Open directory / Add repositories |
| `Ctrl + R` | Refresh state |
| `Ctrl + F` | Fetch all remotes |
| `Ctrl + L` | Pull current branch |
| `Ctrl + P` | Push current branch |
| `Ctrl + Enter`| Commit staged changes |
| `Ctrl + B` | Switch to Branches view |
| `Ctrl + T` | Toggle Split/Unified diff |
| `Ctrl + [ / ]` | Navigate commit history |
| `/` | Focus search |
| `Ctrl + 1 / 2` | Focus Changes / History panels |
| `Esc` | Close popup overlays |

## Build

Requirements: Go 1.25+, GTK 4.x, pkg-config.

```bash
go build -o gig .
```

## License
MIT
