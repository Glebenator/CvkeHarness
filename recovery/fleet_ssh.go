package recovery

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

func fleetAsset(path string, executable bool) (Fingerprint, error) {
	p, err := parentFor(path)
	if err != nil {
		return Fingerprint{}, err
	}
	defer p.Close()
	limit := int64(1 << 20)
	if executable {
		limit = 64 << 20
	}
	f, _, err := readRegular(p, filepath.Base(path), limit)
	if err != nil {
		return f, err
	}
	if !f.Exists || f.Mode&0022 != 0 {
		return f, fmt.Errorf("fleet transport asset is missing or writable by another principal")
	}
	if executable {
		if f.Mode&0111 == 0 {
			return f, fmt.Errorf("fleet SSH executable is not executable")
		}
	} else if f.Mode&0077 != 0 {
		return f, fmt.Errorf("fleet credentials/known-hosts must be private (0600 or stricter)")
	}
	return f, nil
}
func captureFleetHost(h FleetHost) (FleetHostPlan, error) {
	p := FleetHostPlan{Config: h}
	var err error
	p.SSHBinary, err = fleetAsset(h.SSHBinary, true)
	if err != nil {
		return p, err
	}
	p.Identity, err = fleetAsset(h.IdentityFile, false)
	if err != nil {
		return p, err
	}
	p.KnownHosts, err = fleetAsset(h.KnownHostsFile, false)
	return p, err
}

// Every session ignores ambient SSH configuration/agents, refuses unknown or
// changed keys, and uses one operator-owned key. No local command, proxy,
// forwarding, multiplexing, TOFU or model-supplied shell text is admitted.
// HostKeyAlias is the enrolled expected executor identity, independent of IP/port.
func fleetSSHArguments(h FleetHost, action, id, digest string) ([]string, error) {
	if !hexID(id, 32) || !hexID(digest, 64) || !hexID(h.ExpectedTarget, 64) {
		return nil, fmt.Errorf("invalid remote operation reference")
	}
	if action != "inspect" && action != "apply" && action != "recover" {
		return nil, fmt.Errorf("unsupported remote recovery action")
	}
	if !fleetPath(h.Executor) || !fleetPath(h.StatePath) {
		return nil, fmt.Errorf("invalid remote executor paths")
	}
	remote := h.Executor + " recovery --state " + h.StatePath + " --expect-target " + h.ExpectedTarget + " " + action + " " + id
	if action != "inspect" {
		remote += " --confirm " + digest
	}
	args := []string{"-F", "none", "-T", "-a", "-x", "-o", "BatchMode=yes", "-o", "StrictHostKeyChecking=yes", "-o", "UpdateHostKeys=no", "-o", "VerifyHostKeyDNS=no", "-o", "GlobalKnownHostsFile=/dev/null", "-o", "UserKnownHostsFile=" + h.KnownHostsFile, "-o", "HostKeyAlias=" + h.ExpectedTarget, "-o", "IdentityAgent=none", "-o", "IdentitiesOnly=yes", "-o", "CertificateFile=none", "-o", "PreferredAuthentications=publickey", "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no", "-o", "ControlMaster=no", "-o", "ControlPath=none", "-o", "ClearAllForwardings=yes", "-o", "PermitLocalCommand=no", "-o", "ProxyCommand=none", "-o", "ProxyJump=none", "-o", "ConnectionAttempts=1", "-o", "ConnectTimeout=5", "-o", "ServerAliveInterval=5", "-o", "ServerAliveCountMax=2", "-i", h.IdentityFile, "-p", strconv.Itoa(h.Port), "-l", h.User, "--", h.Address, remote}
	return args, nil
}

type fleetOutput struct {
	bytes.Buffer
	limit int
}

func (w *fleetOutput) Write(p []byte) (int, error) {
	if len(p) > w.limit-w.Len() {
		return 0, fmt.Errorf("remote recovery output limit exceeded")
	}
	return w.Buffer.Write(p)
}
func callFleetSSH(ctx context.Context, h FleetHostPlan, action, id, digest string) (Operation, error) {
	current, err := captureFleetHost(h.Config)
	if err != nil {
		return Operation{}, err
	}
	if current != h {
		return Operation{}, fmt.Errorf("fleet transport executable, key or known-hosts changed")
	}
	args, err := fleetSSHArguments(h.Config, action, id, digest)
	if err != nil {
		return Operation{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(h.Config.TimeoutSeconds)*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, h.Config.SSHBinary, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "SSH_ASKPASS_REQUIRE=never"}
	cmd.WaitDelay = time.Second
	stdout := &fleetOutput{limit: 16 << 20}
	cmd.Stdout = stdout
	// Do not reflect arbitrary remote login banners, secrets or SSH diagnostics
	// into model-visible records. Operators can diagnose the fixed transport.
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil {
		return Operation{}, fmt.Errorf("fixed SSH recovery %s failed or disconnected: %w", action, err)
	}
	var op Operation
	d := json.NewDecoder(bytes.NewReader(stdout.Bytes()))
	if err = d.Decode(&op); err != nil {
		return op, fmt.Errorf("invalid target executor response: %w", err)
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return op, fmt.Errorf("unexpected trailing target response")
	}
	return op, nil
}
