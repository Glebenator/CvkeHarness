//go:build !linux || (!amd64 && !arm64)

package recovery

import (
	"context"
	"fmt"
)

func sshUnsupported() error {
	return fmt.Errorf("managed SSH adapter requires Linux amd64/arm64 procfs, boot clock and pidfd support")
}
func sshClock() (string, int64, error)                                    { return "", 0, sshUnsupported() }
func sshTicksNow() (uint64, error)                                        { return 0, sshUnsupported() }
func verifySSHProcess(ServiceProcess, string) error                       { return sshUnsupported() }
func verifySSHGuard(SSHGuardIdentity) error                               { return sshUnsupported() }
func sshReload(ServiceProcess, string) error                              { return sshUnsupported() }
func validateSSHConfig(context.Context, SSHService, string) error         { return sshUnsupported() }
func sshPortReady(context.Context, ServiceProcess, SSHService, int) error { return sshUnsupported() }
func newSSHConnectionProof(SSHPlan, uint64) ([]ServiceProcess, error)     { return nil, sshUnsupported() }
func (e *Engine) SuperviseSSH(context.Context, SSHService) error          { return sshUnsupported() }
