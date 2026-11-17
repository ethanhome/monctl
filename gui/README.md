# monctl GUI

Wails v2 shell around the same DDC backend as the CLI
(`internal/service`, `internal/settings`, `monitor`). Vanilla JS frontend,
no framework.

## Develop

Requires the Wails CLI (`go install github.com/wailsapp/wails/v2/cmd/wails@latest`),
Node, and Xcode CLT on macOS.

    cd gui
    wails dev          # hot-reload window
    wails build        # -> build/bin/monctl.app (macOS) / monctl.exe (Windows)

`go vet`/`go test` only work once `frontend/dist` exists (it is embedded by
`main.go`), i.e. after a `wails build` or `npm run build` in `frontend/`.

Bindings in `frontend/wailsjs/` are generated; regenerate with
`wails generate module` after changing exported `App` methods.
