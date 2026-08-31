// Package clientplatform isolates operating-system policy used by the Remote
// Core entrypoint from the platform-neutral controller and Tunnel state.
package clientplatform

import "context"

// Runtime is the narrow set of process-level OS behavior required by the
// Remote Core. Concrete implementations are selected by Go build tags.
type Runtime interface {
	Name() string
	DefaultDataDirectory() (string, error)
	ValidateDataDirectory(string) error
	ValidatePrivilege(development, allowPrivilegedDevelopment bool) error
	NotifyShutdown(context.Context) (context.Context, context.CancelFunc)
}

// Current returns the implementation for the build target.
func Current() Runtime {
	return currentRuntime()
}
