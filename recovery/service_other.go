//go:build !linux

package recovery

import "fmt"

func currentServiceMaster(NGINXService) (ServiceProcess, error) {
	return ServiceProcess{}, fmt.Errorf("NGINX recovery adapter currently requires Linux procfs and pidfd support")
}
func serviceWorkers(ServiceProcess) (map[int]string, error) {
	return nil, fmt.Errorf("NGINX recovery adapter requires Linux")
}
func signalService(ServicePlan) error { return fmt.Errorf("NGINX recovery adapter requires Linux") }
func checkServiceCapability(ServicePlan) error {
	return fmt.Errorf("NGINX recovery adapter requires Linux")
}

func measureServiceResources(ServicePlan) (int, int, uint64, error) {
	return 0, 0, 0, fmt.Errorf("service resource measurements require Linux")
}
