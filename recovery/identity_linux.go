package recovery

import "fmt"

func platformMachineID() ([]byte, error) {
	return nil, fmt.Errorf("Linux recovery requires a provisioned /etc/machine-id; hostname alone is insufficient")
}
