package agent

import "fmt"

// executionTarget derives the complete execution profile from trusted Device
// metadata. Windows is intentionally amd64-only until another native client
// architecture passes the platform contract.
func executionTarget(platform, arch string) (ExecutionTarget, error) {
	if arch == "" {
		return ExecutionTarget{}, fmt.Errorf("%w: device architecture is unavailable", ErrInvalidState)
	}
	switch platform {
	case "linux":
		return ExecutionTarget{
			Platform: platform, Arch: arch,
			Shell: ExecutionShellPOSIXUser, PathFlavor: PathFlavorPOSIX,
			DefaultCWDPolicy: DefaultCWDInherit,
		}, nil
	case "windows":
		if arch != "amd64" {
			return ExecutionTarget{}, fmt.Errorf("%w: Windows device architecture is unsupported", ErrInvalidState)
		}
		return ExecutionTarget{
			Platform: platform, Arch: arch,
			Shell: ExecutionShellWindowsPowerShell, PathFlavor: PathFlavorWindows,
			DefaultCWDPolicy: DefaultCWDUserProfile,
		}, nil
	default:
		return ExecutionTarget{}, fmt.Errorf("%w: device platform is unsupported", ErrInvalidState)
	}
}
