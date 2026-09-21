package recovery

import (
	"fmt"
	"os/exec"
	"regexp"
)

func platformMachineID() ([]byte, error) {
	b, err := exec.Command("/usr/sbin/ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
	if err != nil {
		return nil, err
	}
	m := regexp.MustCompile(`"IOPlatformUUID"\s*=\s*"([A-Fa-f0-9-]+)"`).FindSubmatch(b)
	if len(m) != 2 {
		return nil, fmt.Errorf("cannot establish local machine identity")
	}
	return m[1], nil
}
