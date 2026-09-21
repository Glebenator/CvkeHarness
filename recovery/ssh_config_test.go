package recovery

import (
	"fmt"
	"strings"
	"testing"
)

func sshConfigFixture() (SSHService, string) {
	s := SSHService{Name: "lab", Binary: "/usr/sbin/sshd", SessionBinary: "/usr/lib/ssh/sshd-session", ConfigPath: "/srv/ssh/sshd_config", PIDFile: "/run/cvke-sshd.pid", HostKeyPath: "/srv/ssh/host_key", AuthorizedKeysPath: "/srv/ssh/authorized_keys", TimeoutSeconds: 3, ConfirmationSeconds: 30}
	data := fmt.Sprintf("Port 22\nListenAddress 0.0.0.0\nHostKey %s\nPidFile %s\nAuthorizedKeysFile %s\nPermitRootLogin prohibit-password\nPasswordAuthentication no\nKbdInteractiveAuthentication no\nPermitEmptyPasswords no\nAllowUsers root\nAllowTcpForwarding no\nX11Forwarding no\nPermitTunnel no\nUseDNS no\nSubsystem sftp internal-sftp\n", s.HostKeyPath, s.PIDFile, s.AuthorizedKeysPath)
	return s, data
}

func TestManagedSSHOnlyChangesThePort(t *testing.T) {
	s, before := sshConfigFixture()
	after := strings.Replace(before, "Port 22", "Port 2223", 1)
	oldPort, newPort, err := validateManagedSSHChange(s, []byte(before), []byte(after))
	if err != nil || oldPort != 22 || newPort != 2223 {
		t.Fatalf("valid port change: %d %d %v", oldPort, newPort, err)
	}
	for name, candidate := range map[string]string{
		"password":   strings.Replace(after, "PasswordAuthentication no", "PasswordAuthentication yes", 1),
		"key":        strings.Replace(after, s.AuthorizedKeysPath, "/tmp/injected", 1),
		"address":    strings.Replace(after, "0.0.0.0", "127.0.0.1", 1),
		"include":    after + "Include /tmp/extra\n",
		"match":      after + "Match all\n",
		"command":    after + "ForceCommand /bin/sh\n",
		"duplicate":  after + "Port 2224\n",
		"empty":      "",
		"unchanged":  before,
		"privileged": strings.Replace(before, "Port 22", "Port 23", 1),
		"overflow":   strings.Replace(before, "Port 22", "Port 99999999999999999999", 1),
		"negative":   strings.Replace(before, "Port 22", "Port -1", 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := validateManagedSSHChange(s, []byte(before), []byte(candidate)); err == nil {
				t.Fatal("unsafe/unsupported SSH candidate accepted")
			}
		})
	}
}
