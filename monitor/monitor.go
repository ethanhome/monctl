// Package monitor provides cross-platform DDC/CI access to physical monitors.
package monitor

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Info describes one physical monitor.
type Info struct {
	Index       int     `json:"index"`
	Description string  `json:"description"`
	Primary     bool    `json:"primary"`
	Handle      uintptr `json:"-"`
}

// Controller is the platform seam (Windows implemented; macOS phase 2).
type Controller interface {
	List() ([]Info, error)
	GetVCP(info Info, code byte) (current, max int, err error)
	SetVCP(info Info, code byte, value byte) error
}

// ErrNotImplemented is returned by platforms without a backend yet.
var ErrNotImplemented = errors.New("monitor: no backend for this platform")

// New returns the platform Controller.
func New() Controller { return newPlatform() }

// ResolveTargets resolves "-m N" / "@all" style selection against List().
func ResolveTargets(ctrl Controller, sel string) ([]Info, error) {
	all, err := ctrl.List()
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, errors.New("no DDC/CI-capable monitor found")
	}
	sel = strings.TrimSpace(sel)
	if sel == "" || sel == "@all" || sel == "all" {
		return all, nil
	}
	var idx int
	if _, err := fmt.Sscanf(sel, "%d", &idx); err != nil {
		return nil, fmt.Errorf("invalid monitor selector %q (use index or @all)", sel)
	}
	for _, m := range all {
		if m.Index == idx {
			return []Info{m}, nil
		}
	}
	return nil, fmt.Errorf("no monitor with index %d", idx)
}

// SortInfos keeps monitor ordering stable by index.
func SortInfos(infos []Info) {
	sort.Slice(infos, func(i, j int) bool { return infos[i].Index < infos[j].Index })
}
