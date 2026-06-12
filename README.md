# hopper

A small TUI that shows live Claude Code sessions grouped by git repo, with status
(idle/working/blocked), vim navigation, and one-key jump to the terminal pane a session
runs in (kitty). When the terminal supports it, sessions show in a sidebar with a
live preview of the selected session filling the main area; narrow terminals fall
back to the list with the preview stacked below. Toggle the preview with `p`.

## Build & run

    go build -o hopper .
    ./hopper

## Keys

    j/k move · h/l fold · Enter focus · p preview · / filter · r refresh · q quit

## Flags

    --terminal auto|kitty|none   terminal backend (default auto)
