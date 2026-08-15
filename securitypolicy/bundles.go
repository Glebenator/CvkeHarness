package securitypolicy

func bundleValues() map[Profile]map[string]string {
	return map[Profile]map[string]string{
		ProfileExtraStrict: decisions(
			DecisionAllow, DecisionDeny, DecisionDeny,
			DecisionAsk, DecisionDeny, DecisionAsk, DecisionDeny,
			DecisionDeny, DecisionAsk, DecisionDeny, DecisionDeny,
			DecisionAsk, DecisionDeny, DecisionDeny, DecisionDeny, DecisionDeny,
			DecisionDeny, DecisionDeny,
			false, true, true, 4096, 4, 15, 4096,
		),
		ProfileReasonable: decisions(
			DecisionAllow, DecisionAsk, DecisionAsk,
			DecisionAllow, DecisionAsk, DecisionAllow, DecisionAsk,
			DecisionAsk, DecisionAsk, DecisionAsk, DecisionDeny,
			DecisionAsk, DecisionAsk, DecisionAsk, DecisionAsk, DecisionAsk,
			DecisionAsk, DecisionDeny,
			false, true, true, 8192, 16, 30, 8192,
		),
		ProfileLessStrict: decisions(
			DecisionAllow, DecisionLLMReview, DecisionAllow,
			DecisionAllow, DecisionAllow, DecisionAllow, DecisionAsk,
			DecisionAsk, DecisionAsk, DecisionAllow, DecisionDeny,
			DecisionAllow, DecisionAsk, DecisionAsk, DecisionAllow, DecisionAsk,
			DecisionAllow, DecisionAsk,
			true, true, true, 16384, 24, 60, 16384,
		),
		ProfileMinimal: decisions(
			DecisionAllow, DecisionAllow, DecisionAllow,
			DecisionAllow, DecisionAllow, DecisionAllow, DecisionAllow,
			DecisionAllow, DecisionAllow, DecisionAllow, DecisionAsk,
			DecisionAllow, DecisionAllow, DecisionAllow, DecisionAllow, DecisionAllow,
			DecisionAllow, DecisionAsk,
			true, true, true, 32768, 32, 120, 32768,
		),
		ProfileYOLO: decisions(
			DecisionAllow, DecisionAllow, DecisionAllow,
			DecisionAllow, DecisionAllow, DecisionAllow, DecisionAllow,
			DecisionAllow, DecisionAllow, DecisionAllow, DecisionAllow,
			DecisionAllow, DecisionAllow, DecisionAllow, DecisionAllow, DecisionAllow,
			DecisionAllow, DecisionAllow,
			false, false, false, 65536, 64, 3600, 1048576,
		),
	}
}

func decisions(
	read, unknown, scripts Decision,
	create, overwrite, appendPolicy, deletePolicy Decision,
	privilege, service, packages, rawDevices Decision,
	network, remote, cloud, containers, database Decision,
	scheduled, credentials Decision,
	remember, protectCritical, protectCredentials bool,
	commandBytes, segments, timeoutSeconds, outputBytes int,
) map[string]string {
	return map[string]string{
		SettingReadCommands:        string(read),
		SettingUnknownCommands:     string(unknown),
		SettingScriptExecution:     string(scripts),
		SettingFileCreate:          string(create),
		SettingFileOverwrite:       string(overwrite),
		SettingFileAppend:          string(appendPolicy),
		SettingFileDelete:          string(deletePolicy),
		SettingPrivilegeEscalation: string(privilege),
		SettingServiceChanges:      string(service),
		SettingPackageChanges:      string(packages),
		SettingRawDeviceAccess:     string(rawDevices),
		SettingNetworkAccess:       string(network),
		SettingRemoteMutation:      string(remote),
		SettingCloudChanges:        string(cloud),
		SettingContainerChanges:    string(containers),
		SettingDatabaseDestructive: string(database),
		SettingScheduledChanges:    string(scheduled),
		SettingCredentialAccess:    string(credentials),
		SettingRememberApprovals:   boolString(remember),
		SettingProtectCritical:     boolString(protectCritical),
		SettingProtectCredentials:  boolString(protectCredentials),
		SettingMaxCommandBytes:     intString(commandBytes),
		SettingMaxSegments:         intString(segments),
		SettingTimeoutSeconds:      intString(timeoutSeconds),
		SettingMaxOutputBytes:      intString(outputBytes),
	}
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func intString(value int) string {
	result := ""
	for value > 0 {
		result = string(rune('0'+value%10)) + result
		value /= 10
	}
	if result == "" {
		return "0"
	}
	return result
}
