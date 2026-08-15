package tools

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/coolcake/cvkeharness/securitypolicy"
)

// ShellEffect is a deterministic fact extracted from a shell action. Policy
// resolution happens after classification so profiles can make different
// decisions about the same effect.
type ShellEffect struct {
	Setting string
	Detail  string
	Target  string
}

type ShellAssessment struct {
	Effects  []ShellEffect
	Decision securitypolicy.Decision
	Reason   string
}

func AssessShellCommand(command string, policy securitypolicy.EffectivePolicy) (ShellAssessment, error) {
	if len([]byte(command)) > policy.Int(securitypolicy.SettingMaxCommandBytes) {
		return ShellAssessment{Decision: securitypolicy.DecisionDeny}, fmt.Errorf("command exceeds %d-byte security limit", policy.Int(securitypolicy.SettingMaxCommandBytes))
	}
	parsed, err := ParseShellCommand(command)
	if err != nil {
		return ShellAssessment{Decision: securitypolicy.DecisionDeny}, err
	}
	if len(parsed.Segments) > policy.Int(securitypolicy.SettingMaxSegments) {
		return ShellAssessment{Decision: securitypolicy.DecisionDeny}, fmt.Errorf("command has %d segments; security limit is %d", len(parsed.Segments), policy.Int(securitypolicy.SettingMaxSegments))
	}

	assessment := ShellAssessment{Decision: securitypolicy.DecisionAllow}
	for _, segment := range parsed.Segments {
		assessment.Effects = append(assessment.Effects, classifyShellEffects(segment, 0)...)
	}
	assessment.Effects = uniqueEffects(assessment.Effects)
	if len(assessment.Effects) == 0 {
		assessment.Effects = []ShellEffect{{Setting: securitypolicy.SettingUnknownCommands, Detail: "no classifiable effect"}}
	}

	var reasons []string
	for _, effect := range assessment.Effects {
		decision := policy.Decision(effect.Setting)
		if decision == "" {
			decision = policy.Decision(securitypolicy.SettingUnknownCommands)
		}
		if policy.Bool(securitypolicy.SettingProtectCritical) &&
			(effect.Setting == securitypolicy.SettingFileDelete || effect.Setting == securitypolicy.SettingFileOverwrite) &&
			isCriticalPath(effect.Target) {
			decision = strictestSecurityDecision(decision, securitypolicy.DecisionAsk)
			reasons = append(reasons, "critical path "+printableTarget(effect.Target)+" requires approval")
		}
		if policy.Bool(securitypolicy.SettingProtectCredentials) && isCredentialTarget(effect.Target+" "+effect.Detail) {
			credentialDecision := policy.Decision(securitypolicy.SettingCredentialAccess)
			decision = strictestSecurityDecision(decision, credentialDecision)
			reasons = append(reasons, "credential-bearing path requires "+string(credentialDecision))
		}
		assessment.Decision = strictestSecurityDecision(assessment.Decision, decision)
		if effect.Detail != "" {
			reasons = append(reasons, effect.Detail+" is "+string(decision))
		}
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "all classified effects are allowed")
	}
	assessment.Reason = strings.Join(uniqueStrings(reasons), "; ")
	return assessment, nil
}

func classifyShellEffects(segment ShellSegment, depth int) []ShellEffect {
	if depth > 4 {
		return []ShellEffect{{Setting: securitypolicy.SettingUnknownCommands, Detail: "nested command depth exceeded"}}
	}
	header := segment.Command
	if segment.Heredoc {
		if newline := strings.IndexAny(header, "\r\n"); newline >= 0 {
			header = header[:newline]
		}
	}
	argv, redirects, err := tokenizePolicySegment(header)
	if err != nil || len(argv) == 0 {
		return []ShellEffect{{Setting: securitypolicy.SettingUnknownCommands, Detail: "unclassifiable command"}}
	}
	base := filepath.Base(argv[0])
	args := argv[1:]
	var effects []ShellEffect
	add := func(setting, detail, target string) {
		effects = append(effects, ShellEffect{Setting: setting, Detail: detail, Target: target})
	}

	if segment.Heredoc {
		add(securitypolicy.SettingScriptExecution, "quoted heredoc script or data", "")
	}
	for _, redirect := range redirects {
		if redirect.DescriptorOnly || redirect.Target == "/dev/null" {
			continue
		}
		target := expandPolicyPath(redirect.Target)
		switch redirect.Mode {
		case "read":
			add(securitypolicy.SettingReadCommands, "input redirection", target)
			if isCredentialTarget(target) {
				add(securitypolicy.SettingCredentialAccess, "credential-bearing input redirection", target)
			}
		case "append":
			add(securitypolicy.SettingFileAppend, "file append", target)
		case "write":
			if pathExists(target) {
				add(securitypolicy.SettingFileOverwrite, "existing-file truncation", target)
			} else {
				add(securitypolicy.SettingFileCreate, "new-file redirection", target)
			}
		}
	}

	if isCredentialTarget(strings.Join(argv, " ")) {
		add(securitypolicy.SettingCredentialAccess, "credential-bearing command arguments", firstPathArg(args))
	}

	switch base {
	case "set", "df", "free", "uptime", "ps", "netstat", "ss", "du", "ls", "stat", "head", "tail", "grep", "rg", "cat", "sort", "wc", "pwd", "whoami", "id", "uname", "date", "echo", "printf", "true", "false", "which", "whereis", "file", "readlink", "realpath":
		add(securitypolicy.SettingReadCommands, "known read-only command "+base, "")
	case "tmutil":
		if containsArg(args, "listlocalsnapshots", "listbackups", "destinationinfo", "status") {
			add(securitypolicy.SettingReadCommands, "Time Machine inspection", "")
		} else {
			add(securitypolicy.SettingFileDelete, "Time Machine mutation", strings.Join(args, " "))
		}
	case "journalctl":
		if hasPrefixArg(args, "--vacuum") || containsArg(args, "--rotate", "--sync", "--flush", "--relinquish-var") {
			add(securitypolicy.SettingServiceChanges, "journal mutation", "")
			add(securitypolicy.SettingFileDelete, "journal retention deletion", "")
		} else {
			add(securitypolicy.SettingReadCommands, "journal inspection", "")
		}
	case "systemctl":
		if systemctlReadOnly(args) {
			add(securitypolicy.SettingReadCommands, "service inspection", firstNonFlag(args))
		} else {
			add(securitypolicy.SettingServiceChanges, "service state change", firstNonFlag(args))
		}
	case "service", "launchctl":
		if containsArg(args, "status", "list", "print") {
			add(securitypolicy.SettingReadCommands, "service inspection", firstNonFlag(args))
		} else {
			add(securitypolicy.SettingServiceChanges, "service state change", firstNonFlag(args))
		}
	case "rm", "unlink", "rmdir", "shred":
		for _, target := range nonFlagArgs(args) {
			add(securitypolicy.SettingFileDelete, base+" deletion", expandPolicyPath(target))
		}
	case "find":
		if containsArg(args, "-delete") || containsNestedDelete(args) {
			add(securitypolicy.SettingFileDelete, "find deletion", firstNonFlag(args))
		} else {
			add(securitypolicy.SettingReadCommands, "filesystem search", firstNonFlag(args))
		}
	case "cp", "mv", "install", "ln", "tee", "touch", "mkdir", "truncate":
		target := lastNonFlag(args)
		setting := securitypolicy.SettingFileCreate
		if base == "truncate" || pathExists(expandPolicyPath(target)) || containsArg(args, "-f", "--force") {
			setting = securitypolicy.SettingFileOverwrite
		}
		add(setting, base+" filesystem mutation", expandPolicyPath(target))
		if base == "mv" && len(nonFlagArgs(args)) > 1 {
			add(securitypolicy.SettingFileDelete, "move removes source path", expandPolicyPath(nonFlagArgs(args)[0]))
		}
	case "sed":
		if containsArg(args, "-i", "--in-place") || hasPrefixArg(args, "-i") {
			add(securitypolicy.SettingFileOverwrite, "in-place sed edit", expandPolicyPath(lastNonFlag(args)))
		} else {
			add(securitypolicy.SettingReadCommands, "stream transformation", "")
		}
	case "dd":
		target := argValuePrefix(args, "of=")
		if strings.HasPrefix(target, "/dev/") {
			add(securitypolicy.SettingRawDeviceAccess, "raw-device write", target)
		} else {
			add(securitypolicy.SettingFileOverwrite, "dd output write", expandPolicyPath(target))
		}
	case "mkfs", "wipefs", "fdisk", "parted", "mount", "umount":
		add(securitypolicy.SettingRawDeviceAccess, "raw-device or mount mutation", firstNonFlag(args))
	case "sudo", "doas", "su":
		add(securitypolicy.SettingPrivilegeEscalation, "privilege escalation", "")
		nested := nestedAfterWrapper(base, args)
		if len(nested) > 0 {
			effects = append(effects, classifySyntheticSegment(nested, depth+1)...)
		}
	case "chmod", "chown", "chgrp", "setfacl", "dscl", "useradd", "userdel", "usermod":
		add(securitypolicy.SettingPrivilegeEscalation, "permission or identity change", lastNonFlag(args))
	case "apt", "apt-get", "yum", "dnf", "apk", "brew", "pip", "pip3", "npm", "pnpm", "yarn", "gem", "cargo":
		if packageReadOnlyPolicy(base, args) {
			add(securitypolicy.SettingNetworkAccess, "package metadata access", "")
		} else {
			add(securitypolicy.SettingPackageChanges, "package lifecycle mutation", strings.Join(args, " "))
			add(securitypolicy.SettingNetworkAccess, "package registry access", "")
		}
	case "curl", "wget":
		add(securitypolicy.SettingNetworkAccess, "outbound HTTP access", lastNonFlag(args))
		if httpMutates(base, args) {
			add(securitypolicy.SettingRemoteMutation, "remote HTTP mutation", lastNonFlag(args))
		}
	case "ping", "traceroute", "dig", "nslookup", "nc", "ncat", "telnet":
		add(securitypolicy.SettingNetworkAccess, "network probe", lastNonFlag(args))
	case "ssh", "scp", "sftp", "rsync":
		add(securitypolicy.SettingNetworkAccess, "remote transport", firstNonFlag(args))
		if base != "ssh" || len(nonFlagArgs(args)) > 1 || containsArg(args, "--delete") {
			add(securitypolicy.SettingRemoteMutation, "remote filesystem or command action", strings.Join(args, " "))
		}
		if base == "rsync" && containsArg(args, "--delete", "--delete-before", "--delete-after", "--delete-excluded") {
			add(securitypolicy.SettingFileDelete, "rsync deletion", lastNonFlag(args))
		}
	case "aws", "az", "gcloud", "terraform", "tofu", "kubectl", "helm":
		add(securitypolicy.SettingNetworkAccess, "remote control-plane access", "")
		if cloudReadOnly(base, args) {
			add(securitypolicy.SettingReadCommands, "cloud resource inspection", "")
		} else {
			add(securitypolicy.SettingCloudChanges, "cloud resource mutation", strings.Join(args, " "))
			add(securitypolicy.SettingRemoteMutation, "remote control-plane mutation", "")
		}
		if base == "kubectl" || base == "helm" {
			if !cloudReadOnly(base, args) {
				add(securitypolicy.SettingContainerChanges, "cluster workload mutation", strings.Join(args, " "))
			}
		}
	case "docker", "podman", "nerdctl":
		if containerReadOnly(args) {
			add(securitypolicy.SettingReadCommands, "container inspection", "")
		} else {
			add(securitypolicy.SettingContainerChanges, "container or image mutation", strings.Join(args, " "))
			if containsArg(args, "prune", "rm", "rmi", "volume") {
				add(securitypolicy.SettingFileDelete, "container storage deletion", strings.Join(args, " "))
			}
		}
	case "psql", "mysql", "sqlite3":
		query := strings.ToLower(strings.Join(args, " "))
		if destructiveSQL(query) {
			add(securitypolicy.SettingDatabaseDestructive, "destructive database statement", "")
			add(securitypolicy.SettingRemoteMutation, "database mutation", "")
		} else if strings.Contains(query, "select ") || strings.Contains(query, "pragma ") || strings.Contains(query, ".schema") {
			add(securitypolicy.SettingReadCommands, "database inspection", "")
		} else {
			add(securitypolicy.SettingUnknownCommands, "database command with unclassified effects", "")
		}
	case "crontab", "at", "atq", "atrm", "systemd-run":
		if (base == "crontab" && containsArg(args, "-l")) || base == "atq" {
			add(securitypolicy.SettingReadCommands, "schedule inspection", "")
		} else {
			add(securitypolicy.SettingScheduledChanges, "scheduled-work mutation", strings.Join(args, " "))
		}
	case "git":
		classifyGitEffects(args, add)
	case "python", "python3", "node", "ruby", "perl", "sh", "bash", "zsh", "fish", "pwsh", "powershell":
		add(securitypolicy.SettingScriptExecution, "interpreter execution", firstNonFlag(args))
		script := strings.ToLower(strings.Join(args, " "))
		if containsAnySubstring(script, "os.remove", "os.unlink", "shutil.rmtree", "fs.rmsync", "fs.unlink", "file.delete", "unlink(") {
			add(securitypolicy.SettingFileDelete, "script contains deletion primitive", "")
		}
		if nested := commandStringArg(args); nested != "" {
			effects = append(effects, classifySyntheticSegment([]string{nested}, depth+1)...)
		}
	case "env", "command", "exec", "nohup", "xargs", "busybox", "toybox":
		nested := nestedAfterWrapper(base, args)
		if len(nested) == 0 {
			add(securitypolicy.SettingUnknownCommands, "opaque command wrapper", "")
		} else {
			effects = append(effects, classifySyntheticSegment(nested, depth+1)...)
		}
	default:
		add(securitypolicy.SettingUnknownCommands, "unclassified command "+base, "")
	}
	return effects
}

type policyRedirect struct {
	Mode           string
	Target         string
	DescriptorOnly bool
}

func tokenizePolicySegment(command string) ([]string, []policyRedirect, error) {
	var argv []string
	var redirects []policyRedirect
	var current strings.Builder
	inSingle, inDouble, escaped := false, false, false
	flush := func() {
		if current.Len() > 0 {
			argv = append(argv, current.String())
			current.Reset()
		}
	}
	for i := 0; i < len(command); i++ {
		ch := command[i]
		if escaped {
			current.WriteByte(ch)
			escaped = false
			continue
		}
		if inSingle {
			if ch == '\'' {
				inSingle = false
			} else {
				current.WriteByte(ch)
			}
			continue
		}
		if inDouble {
			switch ch {
			case '"':
				inDouble = false
			case '\\':
				escaped = true
			default:
				current.WriteByte(ch)
			}
			continue
		}
		switch ch {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '\\':
			escaped = true
		case ' ', '\t':
			flush()
		case '>', '<':
			fdPrefix := current.String()
			if fdPrefix == "1" || fdPrefix == "2" {
				current.Reset()
			} else {
				flush()
			}
			mode := "write"
			if ch == '<' {
				mode = "read"
			} else if i+1 < len(command) && command[i+1] == '>' {
				mode = "append"
				i++
			}
			for i+1 < len(command) && unicode.IsSpace(rune(command[i+1])) {
				i++
			}
			if i+1 < len(command) && command[i+1] == '&' {
				i++
				start := i + 1
				for i+1 < len(command) && (unicode.IsDigit(rune(command[i+1])) || command[i+1] == '-') {
					i++
				}
				redirects = append(redirects, policyRedirect{Mode: mode, Target: command[start : i+1], DescriptorOnly: true})
				continue
			}
			start := i + 1
			for i+1 < len(command) && !unicode.IsSpace(rune(command[i+1])) && !strings.ContainsRune("|;&<>", rune(command[i+1])) {
				i++
			}
			if start > i || start >= len(command) {
				return nil, nil, fmt.Errorf("redirection missing target")
			}
			redirects = append(redirects, policyRedirect{Mode: mode, Target: strings.Trim(command[start:i+1], "\"'")})
		default:
			current.WriteByte(ch)
		}
	}
	if inSingle || inDouble || escaped {
		return nil, nil, fmt.Errorf("unterminated quoted argument")
	}
	flush()
	return argv, redirects, nil
}

func classifySyntheticSegment(argv []string, depth int) []ShellEffect {
	if len(argv) == 1 {
		parsed, err := ParseShellCommand(argv[0])
		if err == nil {
			var effects []ShellEffect
			for _, segment := range parsed.Segments {
				effects = append(effects, classifyShellEffects(segment, depth)...)
			}
			return effects
		}
	}
	return classifyShellEffects(ShellSegment{Command: strings.Join(argv, " "), Normalized: strings.Join(argv, " ")}, depth)
}

func strictestSecurityDecision(a, b securitypolicy.Decision) securitypolicy.Decision {
	rank := func(value securitypolicy.Decision) int {
		switch value {
		case securitypolicy.DecisionDeny:
			return 4
		case securitypolicy.DecisionAsk:
			return 3
		case securitypolicy.DecisionLLMReview:
			return 2
		default:
			return 1
		}
	}
	if rank(b) > rank(a) {
		return b
	}
	return a
}

func classifyGitEffects(args []string, add func(string, string, string)) {
	verb := firstNonFlag(args)
	switch verb {
	case "status", "diff", "log", "show", "branch", "rev-parse", "ls-files", "remote":
		add(securitypolicy.SettingReadCommands, "git inspection", "")
	case "clean":
		add(securitypolicy.SettingFileDelete, "git clean deletion", "")
	case "reset", "checkout", "restore":
		if containsArg(args, "--hard", "-f", "--force") || verb == "restore" {
			add(securitypolicy.SettingFileOverwrite, "git working-tree overwrite", "")
		} else {
			add(securitypolicy.SettingUnknownCommands, "git state change", "")
		}
	case "push", "fetch", "pull", "clone":
		add(securitypolicy.SettingNetworkAccess, "git remote access", "")
		if verb == "push" {
			add(securitypolicy.SettingRemoteMutation, "git remote mutation", "")
		} else if verb == "clone" {
			add(securitypolicy.SettingFileCreate, "repository creation", lastNonFlag(args))
		}
	default:
		add(securitypolicy.SettingFileOverwrite, "git repository mutation", "")
	}
}

func systemctlReadOnly(args []string) bool {
	verb := firstNonFlag(args)
	switch verb {
	case "status", "show", "list-units", "list-unit-files", "is-active", "is-enabled", "is-failed", "cat", "help", "--version":
		return true
	default:
		return false
	}
}

func packageReadOnlyPolicy(base string, args []string) bool {
	verb := firstNonFlag(args)
	switch base {
	case "apt", "apt-get":
		return containsArg([]string{verb}, "list", "show", "search", "policy", "check")
	case "yum", "dnf":
		return containsArg([]string{verb}, "list", "info", "search", "check", "check-update", "repolist", "history")
	case "apk":
		return containsArg([]string{verb}, "info", "search", "list", "version", "policy")
	case "brew":
		return containsArg([]string{verb}, "list", "info", "search", "outdated", "config", "doctor")
	case "pip", "pip3":
		return containsArg([]string{verb}, "list", "show", "check", "freeze", "index")
	case "npm", "pnpm", "yarn":
		return containsArg([]string{verb}, "list", "ls", "view", "info", "search", "outdated", "audit")
	default:
		return false
	}
}

func cloudReadOnly(base string, args []string) bool {
	verb := strings.ToLower(strings.Join(args, " "))
	if containsAnySubstring(verb, " delete", " remove", " destroy", " apply", " create", " update", " set ", " patch", " replace", " scale", " deploy", " import", " taint", " untaint") {
		return false
	}
	switch base {
	case "terraform", "tofu":
		return containsArg(args, "plan", "show", "state", "validate", "version", "providers", "output")
	case "kubectl":
		return containsArg(args, "get", "describe", "logs", "explain", "api-resources", "api-versions", "version", "diff")
	case "helm":
		return containsArg(args, "list", "status", "get", "history", "show", "search", "version", "template")
	default:
		return containsArg(args, "list", "show", "get", "describe", "read", "status", "version", "help") || hasPrefixArg(args, "describe-") || hasPrefixArg(args, "list-") || hasPrefixArg(args, "get-")
	}
}

func containerReadOnly(args []string) bool {
	return containsArg([]string{firstNonFlag(args)}, "ps", "images", "inspect", "logs", "stats", "version", "info", "top", "port")
}

func httpMutates(base string, args []string) bool {
	joined := strings.ToLower(strings.Join(args, " "))
	if base == "wget" {
		return containsAnySubstring(joined, "--post-data", "--post-file", "--method=")
	}
	return containsAnySubstring(joined, " -x post", " -x put", " -x patch", " -x delete", "--request=post", "--request=put", "--request=patch", "--request=delete", " --data", " -d ", " --form", " -f ", " --upload-file", " -t ")
}

func destructiveSQL(query string) bool {
	query = strings.ToLower(query)
	if strings.Contains(query, "drop ") || strings.Contains(query, "truncate ") {
		return true
	}
	if index := strings.Index(query, "delete from"); index >= 0 {
		return !strings.Contains(query[index:], " where ")
	}
	return false
}

func nestedAfterWrapper(base string, args []string) []string {
	if base == "su" {
		if nested := commandStringArg(args); nested != "" {
			return []string{nested}
		}
	}
	for index, arg := range args {
		if strings.HasPrefix(arg, "-") || (base == "env" && strings.Contains(arg, "=")) {
			continue
		}
		if base == "xargs" && index == len(args)-1 {
			return args[index:]
		}
		return args[index:]
	}
	return nil
}

func commandStringArg(args []string) string {
	for index, arg := range args {
		if (arg == "-c" || arg == "-Command" || arg == "--command") && index+1 < len(args) {
			return args[index+1]
		}
	}
	return ""
}

func containsNestedDelete(args []string) bool {
	joined := strings.ToLower(strings.Join(args, " "))
	return containsAnySubstring(joined, "-exec rm", "-execdir rm", "-ok rm", "-okdir rm")
}

func isCriticalPath(raw string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	path := expandPolicyPath(raw)
	home, _ := os.UserHomeDir()
	critical := []string{"/", "/System", "/Library", "/Applications", "/Users", "/bin", "/sbin", "/usr", "/etc", "/var", "/boot", "/dev", "/Volumes"}
	if home != "" {
		critical = append(critical, filepath.Clean(home), filepath.Join(home, ".Trash"), filepath.Join(home, ".ssh"), filepath.Join(home, ".aws"), filepath.Join(home, ".azure"), filepath.Join(home, ".kube"))
	}
	clean := filepath.Clean(path)
	for _, protected := range critical {
		if clean == filepath.Clean(protected) {
			return true
		}
	}
	parts := strings.Split(filepath.ToSlash(clean), "/")
	for _, part := range parts {
		if part == ".git" || strings.Contains(strings.ToLower(part), "backup") || strings.Contains(strings.ToLower(part), "snapshot") {
			return true
		}
	}
	return false
}

func isCredentialTarget(value string) bool {
	lower := strings.ToLower(value)
	markers := []string{".ssh", ".aws", ".azure", ".kube", ".gnupg", ".env", "credentials", "secret", "token", "api_key", "apikey", "password", "private_key", "/etc/shadow", ".bash_history", ".zsh_history", "kubeconfig", ".pem", ".key"}
	for _, marker := range markers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func expandPolicyPath(raw string) string {
	raw = os.ExpandEnv(strings.TrimSpace(raw))
	if strings.HasPrefix(raw, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			raw = filepath.Join(home, strings.TrimPrefix(raw, "~/"))
		}
	}
	if raw == "" {
		return ""
	}
	if absolute, err := filepath.Abs(raw); err == nil {
		return filepath.Clean(absolute)
	}
	return filepath.Clean(raw)
}

func pathExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Lstat(path)
	return err == nil
}

func nonFlagArgs(args []string) []string {
	var out []string
	for _, arg := range args {
		if !strings.HasPrefix(arg, "-") {
			out = append(out, arg)
		}
	}
	return out
}

func firstNonFlag(args []string) string {
	items := nonFlagArgs(args)
	if len(items) == 0 {
		return ""
	}
	return items[0]
}

func lastNonFlag(args []string) string {
	items := nonFlagArgs(args)
	if len(items) == 0 {
		return ""
	}
	return items[len(items)-1]
}

func firstPathArg(args []string) string { return expandPolicyPath(firstNonFlag(args)) }

func argValuePrefix(args []string, prefix string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return strings.TrimPrefix(arg, prefix)
		}
	}
	return ""
}

func hasPrefixArg(args []string, prefix string) bool {
	for _, arg := range args {
		if strings.HasPrefix(arg, prefix) {
			return true
		}
	}
	return false
}

func containsArg(args []string, values ...string) bool {
	for _, arg := range args {
		for _, value := range values {
			if arg == value {
				return true
			}
		}
	}
	return false
}

func containsAnySubstring(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func uniqueEffects(effects []ShellEffect) []ShellEffect {
	seen := map[string]bool{}
	var out []ShellEffect
	for _, effect := range effects {
		key := effect.Setting + "\x00" + effect.Detail + "\x00" + effect.Target
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, effect)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Setting < out[j].Setting })
	return out
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func printableTarget(value string) string {
	if value == "" {
		return "(unresolved)"
	}
	return value
}
