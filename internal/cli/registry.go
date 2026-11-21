package cli

import (
	"sync"

	"github.com/spf13/cobra"
)

// CommandFactory builds a top-level command bound to the shared App.
type CommandFactory func(app *App) *cobra.Command

var (
	registryMu sync.Mutex
	factories  []CommandFactory
)

// Register adds a top-level command. Each command group calls Register from an init()
// function in its own file, so independent command groups never edit a shared file.
func Register(f CommandFactory) {
	registryMu.Lock()
	defer registryMu.Unlock()
	factories = append(factories, f)
}

func registeredFactories() []CommandFactory {
	registryMu.Lock()
	defer registryMu.Unlock()
	out := make([]CommandFactory, len(factories))
	copy(out, factories)
	return out
}
