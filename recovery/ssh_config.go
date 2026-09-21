package recovery

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// SSHService describes a dedicated OpenSSH instance owned by the target-local
// supervisor. The first workflow changes only its listening port. Authentication,
// host keys, accounts, addresses and command hooks cannot change in this workflow.
type SSHService struct {
	Name                string `json:"name" yaml:"name"`
	Binary              string `json:"binary" yaml:"binary"`
	SessionBinary       string `json:"session_binary" yaml:"session_binary"`
	ConfigPath          string `json:"config_path" yaml:"config_path"`
	PIDFile             string `json:"pid_file" yaml:"pid_file"`
	HostKeyPath         string `json:"host_key_path" yaml:"host_key_path"`
	AuthorizedKeysPath  string `json:"authorized_keys_path" yaml:"authorized_keys_path"`
	TimeoutSeconds      int    `json:"timeout_seconds" yaml:"timeout_seconds"`
	ConfirmationSeconds int    `json:"confirmation_seconds" yaml:"confirmation_seconds"`
}

func (s SSHService) validate() error {
	if s.Name == "" || len(s.Name) > 64 || strings.Trim(s.Name, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789-_") != "" {
		return fmt.Errorf("invalid managed SSH service name")
	}
	seen := map[string]bool{}
	for _, p := range []string{s.Binary, s.SessionBinary, s.ConfigPath, s.PIDFile, s.HostKeyPath, s.AuthorizedKeysPath} {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p || p == "/" || strings.ContainsAny(p, " \t\r\n\x00;$%\\\"'") || seen[p] {
			return fmt.Errorf("managed SSH paths must be distinct, clean absolute paths without expansion")
		}
		seen[p] = true
	}
	if s.TimeoutSeconds < 1 || s.TimeoutSeconds > 5 || s.ConfirmationSeconds < 15 || s.ConfirmationSeconds > 300 {
		return fmt.Errorf("managed SSH needs a 1..5 second command timeout and 15..300 second confirmation window")
	}
	return nil
}

// The daemon receives this entire standalone configuration with no includes or
// command-line overrides. Refuse unknown/duplicate directives rather than
// assuming sshd's first-value and Match/include semantics are interchangeable.
func parseManagedSSHConfig(s SSHService, data []byte) (int, error) {
	if err := s.validate(); err != nil {
		return 0, err
	}
	if len(data) > 64<<10 || strings.ContainsRune(string(data), 0) {
		return 0, fmt.Errorf("managed SSH configuration exceeds its syntax/size boundary")
	}
	required := map[string]string{
		"listenaddress": "0.0.0.0", "hostkey": s.HostKeyPath, "pidfile": s.PIDFile,
		"authorizedkeysfile": s.AuthorizedKeysPath, "permitrootlogin": "prohibit-password",
		"passwordauthentication": "no", "kbdinteractiveauthentication": "no",
		"permitemptypasswords": "no", "allowusers": "root", "allowtcpforwarding": "no",
		"x11forwarding": "no", "permittunnel": "no", "usedns": "no", "subsystem": "sftp internal-sftp",
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return 0, fmt.Errorf("invalid managed SSH directive")
		}
		key, value := strings.ToLower(parts[0]), strings.Join(parts[1:], " ")
		if _, exists := values[key]; exists {
			return 0, fmt.Errorf("duplicate managed SSH directive: %s", key)
		}
		if key != "port" && required[key] != value {
			return 0, fmt.Errorf("unsupported managed SSH directive/value: %s", key)
		}
		values[key] = value
	}
	for key, value := range required {
		if values[key] != value {
			return 0, fmt.Errorf("managed SSH requires its fixed %s directive", key)
		}
	}
	portText := values["port"]
	if portText == "" || strings.Trim(portText, "0123456789") != "" {
		return 0, fmt.Errorf("managed SSH requires one decimal port")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("managed SSH port is out of range")
	}
	return port, nil
}

func validateManagedSSHChange(s SSHService, before, after []byte) (int, int, error) {
	oldPort, err := parseManagedSSHConfig(s, before)
	if err != nil {
		return 0, 0, err
	}
	newPort, err := parseManagedSSHConfig(s, after)
	if err != nil {
		return 0, 0, err
	}
	if newPort < 1024 || newPort == oldPort {
		return 0, 0, fmt.Errorf("managed SSH candidate must select a different unprivileged port")
	}
	return oldPort, newPort, nil
}
