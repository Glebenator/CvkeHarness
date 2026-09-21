package recovery

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"unicode"
)

type nginxDirective struct {
	Words    []string
	Children []nginxDirective
	Block    bool
}

// A deliberately small grammar, not a pretend general NGINX interpreter.
// Unsupported syntax/directives fail closed before running NGINX. Keep all
// resource references, structure and the worker attestation header invariant.
func validateNGINXChange(s NGINXService, before, after []byte) error {
	a, err := nginxShape(s, string(before))
	if err != nil {
		return fmt.Errorf("unsupported original NGINX config: %w", err)
	}
	b, err := nginxShape(s, string(after))
	if err != nil {
		return fmt.Errorf("unsupported candidate NGINX config: %w", err)
	}
	if string(a) != string(b) {
		return fmt.Errorf("NGINX change alters protected structure, listener, PID, identity or health attestation")
	}
	return nil
}

func nginxShape(s NGINXService, input string) ([]byte, error) {
	return nginxShapeWithResources(s, input, nil)
}

func nginxShapeWithResources(s NGINXService, input string, resources *NGINXResources) ([]byte, error) {
	if resources != nil {
		*resources = NGINXResources{Workers: 1, ConnectionsPerWorker: 512}
	}
	tokens, err := nginxTokens(input)
	if err != nil {
		return nil, err
	}
	i := 0
	var parse func(bool, int) ([]nginxDirective, error)
	parse = func(nested bool, depth int) ([]nginxDirective, error) {
		if depth > 4 {
			return nil, fmt.Errorf("configuration nesting limit")
		}
		var out []nginxDirective
		for i < len(tokens) {
			if tokens[i] == "}" {
				i++
				if !nested {
					return nil, fmt.Errorf("unexpected closing brace")
				}
				return out, nil
			}
			var d nginxDirective
			for i < len(tokens) && tokens[i] != ";" && tokens[i] != "{" && tokens[i] != "}" {
				d.Words = append(d.Words, tokens[i])
				i++
			}
			if len(d.Words) == 0 || i == len(tokens) || tokens[i] == "}" {
				return nil, fmt.Errorf("missing directive terminator")
			}
			d.Block = tokens[i] == "{"
			i++
			if d.Block {
				d.Children, err = parse(true, depth+1)
				if err != nil {
					return nil, err
				}
			}
			out = append(out, d)
		}
		if nested {
			return nil, fmt.Errorf("unclosed block")
		}
		return out, nil
	}
	directives, err := parse(false, 0)
	if err != nil {
		return nil, err
	}
	u, _ := url.Parse(s.HealthURL)
	pidCount, logCount, healthHeader := 0, 0, 0
	var validate func([]nginxDirective, string, bool) error
	validate = func(ds []nginxDirective, location string, health bool) error {
		seen := map[string]bool{}
		for index := range ds {
			d := &ds[index]
			name, args := d.Words[0], d.Words[1:]
			if name != "server" && name != "location" && seen[name] {
				return fmt.Errorf("duplicate directive %s", name)
			}
			seen[name] = true
			if d.Block {
				switch {
				case location == "main" && (name == "events" || name == "http") && len(args) == 0:
					if err := validate(d.Children, name, false); err != nil {
						return err
					}
				case location == "http" && name == "server" && len(args) == 0:
					if err := validate(d.Children, "server", false); err != nil {
						return err
					}
				case location == "server" && name == "location" && (len(args) == 1 || len(args) == 2 && args[0] == "="):
					path := args[len(args)-1]
					if !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "$\n\r\\") {
						return fmt.Errorf("unsupported location")
					}
					isHealth := len(args) == 2 && path == u.Path
					if err := validate(d.Children, "location", isHealth); err != nil {
						return err
					}
				default:
					return fmt.Errorf("unsupported block %s in %s", name, location)
				}
				continue
			}
			mutable := false
			switch {
			case location == "main" && name == "pid" && len(args) == 1 && args[0] == s.PIDFile:
				pidCount++
			case location == "main" && name == "error_log" && len(args) == 2 && args[0] == "stderr" && args[1] == "notice":
				logCount++
			case location == "main" && name == "user" && len(args) == 1 && args[0] == "root" && os.Geteuid() == 0:
			case location == "main" && (name == "daemon" || name == "master_process") && len(args) == 1 && args[0] == "on":
			case location == "main" && name == "worker_processes":
				if !nginxInt(args, 1, 16) {
					return fmt.Errorf("worker_processes must be an integer in 1..16")
				}
				mutable = true
				if resources != nil {
					resources.Workers, _ = strconv.Atoi(args[0])
				}
			case location == "events" && name == "worker_connections":
				if !nginxInt(args, 1, 65536) {
					return fmt.Errorf("worker_connections must be an integer in 1..65536")
				}
				mutable = true
				if resources != nil {
					resources.ConnectionsPerWorker, _ = strconv.Atoi(args[0])
				}
			case location == "http" && name == "access_log" && len(args) == 1 && args[0] == "off":
			case location == "http" && name == "default_type" && len(args) == 1 && args[0] == "text/plain":
			case location == "http" && (name == "client_body_temp_path" || name == "proxy_temp_path" || name == "fastcgi_temp_path" || name == "uwsgi_temp_path" || name == "scgi_temp_path") && len(args) == 1 && args[0] == s.Prefix+"/"+name:
			case location == "http" && (name == "sendfile" || name == "server_tokens") && len(args) == 1 && (args[0] == "on" || args[0] == "off"):
				mutable = true
			case (location == "http" || location == "server") && name == "keepalive_timeout":
				if !nginxInt(args, 0, 300) {
					return fmt.Errorf("keepalive_timeout must be integer seconds in 0..300")
				}
				mutable = true
			case location == "server" && name == "listen" && len(args) == 1 && args[0] == u.Host:
			case location == "location" && name == "return" && len(args) >= 1 && len(args) <= 2:
				if !nginxInt(args[:1], 200, 599) || len(args) == 2 && (len(args[1]) > 8192 || strings.Contains(args[1], "$")) {
					return fmt.Errorf("unsupported return status/body")
				}
				mutable = true
			case location == "location" && health && name == "add_header" && len(args) == 3 && args[0] == "X-Cvke-Worker" && args[1] == "$pid" && args[2] == "always":
				healthHeader++
			default:
				return fmt.Errorf("unsupported directive %s in %s", name, location)
			}
			if mutable {
				d.Words = []string{name, "<reviewed-value>"}
			}
		}
		if location == "http" && !seen["access_log"] {
			return fmt.Errorf("explicit access_log off is required")
		}
		if location == "http" {
			for _, name := range []string{"client_body_temp_path", "proxy_temp_path", "fastcgi_temp_path", "uwsgi_temp_path", "scgi_temp_path"} {
				if !seen[name] {
					return fmt.Errorf("explicit service-prefix temp path %s is required", name)
				}
			}
		}
		if location == "server" && !seen["listen"] {
			return fmt.Errorf("explicit loopback listener is required")
		}
		return nil
	}
	if err := validate(directives, "main", false); err != nil {
		return nil, err
	}
	if pidCount != 1 || logCount != 1 || healthHeader != 1 {
		return nil, fmt.Errorf("exact PID, stderr notice logging and one attested health location are required")
	}
	return json.Marshal(directives)
}

func nginxInt(args []string, min, max int) bool {
	if len(args) != 1 || args[0] == "" || strings.Trim(args[0], "0123456789") != "" {
		return false
	}
	n, err := strconv.Atoi(args[0])
	return err == nil && n >= min && n <= max
}

func nginxTokens(input string) ([]string, error) {
	var out []string
	runes := []rune(input)
	for i := 0; i < len(runes); {
		ch := runes[i]
		if unicode.IsSpace(ch) {
			i++
			continue
		}
		if ch == '#' {
			for i < len(runes) && runes[i] != '\n' {
				i++
			}
			continue
		}
		if strings.ContainsRune(";{}", ch) {
			out = append(out, string(ch))
			i++
			continue
		}
		var b strings.Builder
		quote := rune(0)
		if ch == '\'' || ch == '"' {
			quote = ch
			i++
		}
		closed := quote == 0
		for i < len(runes) {
			ch = runes[i]
			if ch == 0 || ch == '\\' {
				return nil, fmt.Errorf("escapes and NUL are unsupported")
			}
			if quote != 0 {
				if ch == quote {
					i++
					closed = true
					break
				}
			} else if unicode.IsSpace(ch) || strings.ContainsRune(";{}#", ch) {
				break
			} else if ch == '\'' || ch == '"' {
				return nil, fmt.Errorf("mixed quoting is unsupported")
			}
			b.WriteRune(ch)
			i++
		}
		if !closed || quote != 0 && i < len(runes) && !unicode.IsSpace(runes[i]) && !strings.ContainsRune(";{}#", runes[i]) {
			return nil, fmt.Errorf("invalid quoted token")
		}
		// Quoted grammar delimiters are valid NGINX strings but excluded here
		// so the structural parser cannot confuse a value with punctuation.
		if b.String() == ";" || b.String() == "{" || b.String() == "}" {
			return nil, fmt.Errorf("quoted delimiters are unsupported")
		}
		out = append(out, b.String())
		if len(out) > 4096 {
			return nil, fmt.Errorf("configuration token limit exceeded")
		}
	}
	return out, nil
}
