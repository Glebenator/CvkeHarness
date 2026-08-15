package tools

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/coolcake/cvkeharness/internal/secrets"
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
	for _, segment := range parsed.Segments {
		if err := validateNestedShellSyntax(segment, 0); err != nil {
			return ShellAssessment{Decision: securitypolicy.DecisionDeny}, err
		}
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
			isPersistentMutationEffect(effect.Setting) &&
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
		target, stableTarget := resolvePolicyTarget(redirect.Target)
		switch redirect.Mode {
		case "read":
			add(securitypolicy.SettingReadCommands, "input redirection", target)
			if isCredentialTarget(target) {
				add(securitypolicy.SettingCredentialAccess, "credential-bearing input redirection", target)
			}
		case "append":
			add(securitypolicy.SettingFileAppend, "file append", target)
		case "write":
			if !stableTarget {
				add(securitypolicy.SettingUnknownCommands, "dynamic or unresolved write target", target)
			}
			if stableTarget && pathExists(target) {
				add(securitypolicy.SettingFileOverwrite, "existing-file truncation", target)
			} else {
				add(securitypolicy.SettingFileCreate, "new-file redirection", target)
			}
		}
		if redirect.Mode != "read" && isRawDeviceTarget(target) {
			add(securitypolicy.SettingRawDeviceAccess, "raw-device redirection", target)
		}
	}

	if isCredentialTarget(strings.Join(argv, " ")) {
		add(securitypolicy.SettingCredentialAccess, "credential-bearing command arguments", firstPathArg(args))
	}
	if secrets.Contains(strings.Join(argv, " ")) {
		add(securitypolicy.SettingCredentialAccess, "literal secret-like value in command arguments", "")
	}
	if executableNeedsReview(argv[0]) {
		add(securitypolicy.SettingUnknownCommands, "untrusted or path-qualified executable "+argv[0], argv[0])
		return effects
	}

	switch base {
	case "set", "df", "free", "uptime", "ps", "netstat", "ss", "du", "ls", "stat", "head", "tail", "grep", "rg", "cat", "wc", "pwd", "whoami", "id", "uname", "date", "echo", "printf", "true", "false", "which", "whereis", "file", "readlink", "realpath":
		add(securitypolicy.SettingReadCommands, "known read-only command "+base, "")
	case "sort":
		if target := optionValue(args, "-o", "--output"); target != "" {
			target, stable := resolvePolicyTarget(target)
			if !stable {
				add(securitypolicy.SettingUnknownCommands, "dynamic or unresolved sort output", target)
			}
			add(securitypolicy.SettingFileOverwrite, "sort output file mutation", target)
		} else {
			add(securitypolicy.SettingReadCommands, "known read-only command sort", "")
		}
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
		} else if containsArg(args, "-exec", "-execdir", "-ok", "-okdir") {
			add(securitypolicy.SettingUnknownCommands, "find executes a nested command", firstNonFlag(args))
		} else if target := optionValue(args, "-fprint", "-fprint0", "-fls", "-fprintf"); target != "" {
			add(securitypolicy.SettingFileOverwrite, "find writes a result file", expandPolicyPath(target))
		} else {
			add(securitypolicy.SettingReadCommands, "filesystem search", firstNonFlag(args))
		}
	case "cp", "mv", "install", "ln":
		targetArg := lastNonFlag(args)
		if targetDirectory := optionValue(args, "-t", "--target-directory"); targetDirectory != "" {
			targetArg = targetDirectory
		}
		target, stableTarget := resolvePolicyTarget(targetArg)
		setting := securitypolicy.SettingFileCreate
		if (stableTarget && pathExists(target)) || containsArg(args, "-f", "--force") {
			setting = securitypolicy.SettingFileOverwrite
		}
		if !stableTarget {
			add(securitypolicy.SettingUnknownCommands, "dynamic or unresolved filesystem target", target)
		}
		add(setting, base+" filesystem mutation", target)
		if isRawDeviceTarget(target) {
			add(securitypolicy.SettingRawDeviceAccess, "raw-device filesystem mutation", target)
		}
		if base == "mv" && len(nonFlagArgs(args)) > 1 {
			for _, source := range nonFlagArgs(args) {
				if source != targetArg {
					add(securitypolicy.SettingFileDelete, "move removes source path", expandPolicyPath(source))
				}
			}
		}
	case "tee", "touch", "mkdir", "truncate":
		targets := nonFlagArgs(args)
		if len(targets) == 0 {
			add(securitypolicy.SettingUnknownCommands, base+" has no stable filesystem target", "")
		}
		for _, targetArg := range targets {
			target, stableTarget := resolvePolicyTarget(targetArg)
			setting := securitypolicy.SettingFileCreate
			if base == "tee" && containsArg(args, "-a", "--append") {
				setting = securitypolicy.SettingFileAppend
			} else if base == "truncate" || (stableTarget && pathExists(target)) {
				setting = securitypolicy.SettingFileOverwrite
			}
			if !stableTarget {
				add(securitypolicy.SettingUnknownCommands, "dynamic or unresolved filesystem target", target)
			}
			add(setting, base+" filesystem mutation", target)
			if isRawDeviceTarget(target) {
				add(securitypolicy.SettingRawDeviceAccess, "raw-device filesystem mutation", target)
			}
		}
	case "sed":
		if containsArg(args, "-i", "--in-place") || hasPrefixArg(args, "-i") || hasPrefixArg(args, "--in-place=") {
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
		if nested := commandStringArg(args); nested != "" && isShellInterpreter(base) {
			effects = append(effects, classifySyntheticSegment([]string{nested}, depth+1)...)
		} else {
			add(securitypolicy.SettingUnknownCommands, "opaque interpreter body requires review", firstNonFlag(args))
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
			target, end, scanErr := scanShellWord(command, start)
			if scanErr != nil {
				return nil, nil, scanErr
			}
			if target == "" {
				return nil, nil, fmt.Errorf("redirection missing target")
			}
			i = end
			redirects = append(redirects, policyRedirect{Mode: mode, Target: target})
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

func scanShellWord(command string, start int) (string, int, error) {
	var word strings.Builder
	inSingle, inDouble, escaped := false, false, false
	end := start - 1
	for index := start; index < len(command); index++ {
		ch := command[index]
		end = index
		if escaped {
			word.WriteByte(ch)
			escaped = false
			continue
		}
		if inSingle {
			if ch == '\'' {
				inSingle = false
			} else {
				word.WriteByte(ch)
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
				word.WriteByte(ch)
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
		default:
			if unicode.IsSpace(rune(ch)) || strings.ContainsRune("|;&<>", rune(ch)) {
				return word.String(), index - 1, nil
			}
			word.WriteByte(ch)
		}
	}
	if inSingle || inDouble || escaped {
		return "", end, fmt.Errorf("unterminated quoted redirection target")
	}
	return word.String(), end, nil
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
		return []ShellEffect{{Setting: securitypolicy.SettingUnknownCommands, Detail: "nested command uses blocked or malformed shell syntax"}}
	}
	return classifyShellEffects(ShellSegment{Command: strings.Join(argv, " "), Normalized: strings.Join(argv, " ")}, depth)
}

func validateNestedShellSyntax(segment ShellSegment, depth int) error {
	if depth > 4 {
		return fmt.Errorf("nested command depth exceeded")
	}
	header := segment.Command
	if segment.Heredoc {
		if newline := strings.IndexAny(header, "\r\n"); newline >= 0 {
			header = header[:newline]
		}
	}
	argv, _, err := tokenizePolicySegment(header)
	if err != nil || len(argv) == 0 || !isShellInterpreter(filepath.Base(argv[0])) {
		return nil
	}
	nested := commandStringArg(argv[1:])
	if nested == "" {
		return nil
	}
	parsed, err := ParseShellCommand(nested)
	if err != nil {
		return fmt.Errorf("blocked nested shell action: %w", err)
	}
	for _, nestedSegment := range parsed.Segments {
		if err := validateNestedShellSyntax(nestedSegment, depth+1); err != nil {
			return err
		}
	}
	return nil
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
	verb, verbArgs := gitVerbAndArgs(args)
	switch verb {
	case "status", "diff", "log", "show", "rev-parse", "ls-files":
		add(securitypolicy.SettingReadCommands, "git inspection", "")
	case "branch":
		mutatingFlag := containsArg(verbArgs, "-d", "-D", "-f", "-m", "-M", "-c", "-C", "--delete", "--force", "--move", "--copy", "--edit-description", "--set-upstream-to", "--unset-upstream")
		readMode := containsArg(verbArgs, "-a", "--all", "-r", "--remotes", "-l", "--list", "--show-current", "--contains", "--no-contains", "--merged", "--no-merged", "--points-at")
		createsNamedBranch := len(nonFlagArgs(verbArgs)) > 0 && !readMode
		if mutatingFlag || createsNamedBranch {
			if containsArg(verbArgs, "-d", "-D", "--delete") {
				add(securitypolicy.SettingFileDelete, "git branch deletion", lastNonFlag(verbArgs))
			} else {
				add(securitypolicy.SettingFileOverwrite, "git branch mutation", lastNonFlag(verbArgs))
			}
		} else {
			add(securitypolicy.SettingReadCommands, "git branch inspection", "")
		}
	case "remote":
		remoteVerb := firstNonFlag(verbArgs)
		switch remoteVerb {
		case "", "show", "get-url":
			add(securitypolicy.SettingReadCommands, "git remote inspection", "")
		case "remove", "rm":
			add(securitypolicy.SettingFileDelete, "git remote deletion", lastNonFlag(verbArgs))
		default:
			add(securitypolicy.SettingFileOverwrite, "git remote configuration mutation", strings.Join(verbArgs, " "))
		}
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

func gitVerbAndArgs(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-C" || arg == "-c" || arg == "--git-dir" || arg == "--work-tree" || arg == "--namespace" {
			i++
			continue
		}
		if strings.HasPrefix(arg, "--git-dir=") || strings.HasPrefix(arg, "--work-tree=") || strings.HasPrefix(arg, "--namespace=") || strings.HasPrefix(arg, "-c=") {
			continue
		}
		if strings.HasPrefix(arg, "-") {
			continue
		}
		return arg, args[i+1:]
	}
	return "", nil
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
	if base == "wget" {
		joined := strings.ToLower(strings.Join(args, " "))
		return containsAnySubstring(joined, "--post-data", "--post-file", "--method=")
	}
	for index, arg := range args {
		lower := strings.ToLower(arg)
		if lower == "-x" || lower == "--request" {
			if index+1 < len(args) {
				method := strings.ToLower(args[index+1])
				if method != "get" && method != "head" {
					return true
				}
			}
		}
		if strings.HasPrefix(lower, "-x") && len(lower) > 2 {
			method := strings.TrimPrefix(lower, "-x")
			if method != "get" && method != "head" {
				return true
			}
		}
		if strings.HasPrefix(lower, "--request=") {
			method := strings.TrimPrefix(lower, "--request=")
			if method != "get" && method != "head" {
				return true
			}
		}
		if lower == "-d" || strings.HasPrefix(lower, "--data") || lower == "-f" || strings.HasPrefix(lower, "--form") || lower == "-t" || strings.HasPrefix(lower, "--upload-file") || strings.HasPrefix(lower, "--json") {
			return true
		}
	}
	return false
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
	for index := 0; index < len(args); index++ {
		arg := args[index]
		if strings.HasPrefix(arg, "-") || (base == "env" && strings.Contains(arg, "=")) {
			if wrapperFlagConsumesValue(base, arg) && index+1 < len(args) {
				index++
			}
			continue
		}
		if base == "xargs" && index == len(args)-1 {
			return args[index:]
		}
		return args[index:]
	}
	return nil
}

func wrapperFlagConsumesValue(base, flag string) bool {
	switch base {
	case "sudo", "doas":
		return containsArg([]string{flag}, "-u", "--user", "-g", "--group", "-h", "--host", "-p", "--prompt", "-C", "--close-from", "-R", "--chroot", "-D", "--chdir")
	case "env":
		return containsArg([]string{flag}, "-u", "--unset", "-C", "--chdir")
	case "xargs":
		return containsArg([]string{flag}, "-n", "--max-args", "-L", "--max-lines", "-P", "--max-procs", "-s", "--max-chars", "-d", "--delimiter", "-a", "--arg-file", "-I", "--replace")
	default:
		return false
	}
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
	path, stable := resolvePolicyTarget(raw)
	if !stable {
		return true
	}
	home, _ := os.UserHomeDir()
	exact := []string{"/", "/Users"}
	descendants := []string{"/System", "/Library", "/Applications", "/bin", "/sbin", "/usr", "/etc", "/var", "/boot", "/dev", "/Volumes"}
	if home != "" {
		exact = append(exact, filepath.Clean(home))
		descendants = append(descendants, filepath.Join(home, ".Trash"), filepath.Join(home, ".ssh"), filepath.Join(home, ".aws"), filepath.Join(home, ".azure"), filepath.Join(home, ".kube"))
	}
	clean := filepath.Clean(path)
	parts := strings.Split(filepath.ToSlash(clean), "/")
	for _, part := range parts {
		if part == ".git" || strings.Contains(strings.ToLower(part), "backup") || strings.Contains(strings.ToLower(part), "snapshot") {
			return true
		}
	}
	if temp, err := filepath.EvalSymlinks(os.TempDir()); err == nil {
		temp = filepath.Clean(temp)
		if clean == temp || strings.HasPrefix(clean, temp+string(filepath.Separator)) {
			return false
		}
	}
	for _, protected := range exact {
		if clean == filepath.Clean(protected) {
			return true
		}
	}
	for _, protected := range descendants {
		protected = filepath.Clean(protected)
		if resolved, err := filepath.EvalSymlinks(protected); err == nil {
			protected = filepath.Clean(resolved)
		}
		if clean == protected || strings.HasPrefix(clean, protected+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func isPersistentMutationEffect(setting string) bool {
	switch setting {
	case securitypolicy.SettingFileCreate, securitypolicy.SettingFileAppend, securitypolicy.SettingFileOverwrite, securitypolicy.SettingFileDelete, securitypolicy.SettingPrivilegeEscalation:
		return true
	default:
		return false
	}
}

func resolvePolicyTarget(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	if strings.ContainsAny(raw, "*?[{}") || strings.Contains(raw, "$") || strings.Contains(raw, "`") {
		return expandPolicyPath(raw), false
	}
	path := expandPolicyPath(raw)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved), true
	}
	parent := filepath.Dir(path)
	if resolvedParent, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(resolvedParent, filepath.Base(path)), true
	}
	return path, false
}

func isRawDeviceTarget(raw string) bool {
	path, stable := resolvePolicyTarget(raw)
	if !stable || path == "" {
		return false
	}
	clean := filepath.Clean(path)
	if clean == "/dev" || strings.HasPrefix(clean, "/dev/") {
		return true
	}
	info, err := os.Stat(clean)
	return err == nil && info.Mode()&(os.ModeDevice|os.ModeCharDevice) != 0
}

func executableNeedsReview(command string) bool {
	if strings.TrimSpace(command) == "" || isTrustedShellBuiltin(command) {
		return false
	}
	if strings.ContainsRune(command, filepath.Separator) {
		return !isTrustedExecutablePath(command)
	}
	resolved, err := exec.LookPath(command)
	if err != nil {
		// A missing executable cannot run. Preserve name-based classification so
		// policies remain portable across hosts where the system tool exists.
		return false
	}
	return !isTrustedExecutablePath(resolved)
}

func isTrustedExecutablePath(path string) bool {
	resolved, err := filepath.EvalSymlinks(expandPolicyPath(path))
	if err != nil {
		return false
	}
	dir := filepath.Dir(filepath.Clean(resolved))
	for _, trusted := range []string{"/bin", "/sbin", "/usr/bin", "/usr/sbin"} {
		if dir == trusted {
			return true
		}
	}
	return false
}

func isTrustedShellBuiltin(command string) bool {
	if strings.ContainsRune(command, filepath.Separator) {
		return false
	}
	switch command {
	case "set", "pwd", "echo", "printf", "true", "false", "command", "exec":
		return true
	default:
		return false
	}
}

func isShellInterpreter(base string) bool {
	switch base {
	case "sh", "bash", "zsh", "fish", "pwsh", "powershell":
		return true
	default:
		return false
	}
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

func optionValue(args []string, names ...string) string {
	for index, arg := range args {
		for _, name := range names {
			if arg == name && index+1 < len(args) {
				return args[index+1]
			}
			if strings.HasPrefix(arg, name+"=") {
				return strings.TrimPrefix(arg, name+"=")
			}
			if len(name) == 2 && strings.HasPrefix(arg, name) && len(arg) > len(name) {
				return strings.TrimPrefix(arg, name)
			}
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
