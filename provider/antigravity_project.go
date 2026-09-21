package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var agProjectEndpoints = []string{"https://cloudcode-pa.googleapis.com", antigravityEndpoint}
var agOperationName = regexp.MustCompile(`^operations/[A-Za-z0-9_-]+$`)

type agTier struct {
	ID                 string `json:"id"`
	IsDefault          bool   `json:"isDefault"`
	UserDefinedProject bool   `json:"userDefinedCloudaicompanionProject"`
}
type agIneligibleTier struct {
	TierID     string `json:"tierId"`
	ReasonCode string `json:"reasonCode"`
}

type agProjectResponse struct {
	Project         json.RawMessage    `json:"cloudaicompanionProject"`
	CurrentTier     *agTier            `json:"currentTier"`
	AllowedTiers    []agTier           `json:"allowedTiers"`
	IneligibleTiers []agIneligibleTier `json:"ineligibleTiers"`
}
type agOnboardOperation struct {
	Name     string            `json:"name"`
	Done     bool              `json:"done"`
	Error    json.RawMessage   `json:"error"`
	Response agProjectResponse `json:"response"`
}

func agProjectID(raw json.RawMessage) string {
	var id string
	if json.Unmarshal(raw, &id) == nil {
		return strings.TrimSpace(id)
	}
	var object struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(raw, &object) == nil {
		return strings.TrimSpace(object.ID)
	}
	return ""
}

// Discover on both known Google endpoints before onboarding. Only the default
// tier offered by Google is used, and tiers requiring a caller-owned project
// are not enrolled automatically. No guessed/shared project IDs or paid tier
// selection are used. The operation runs only as part of explicit login.
func discoverAntigravityProject(ctx context.Context, client *http.Client, token string, endpoints []string, interval time.Duration, out io.Writer) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	metadata := map[string]string{"ideType": "IDE_UNSPECIFIED", "platform": "PLATFORM_UNSPECIFIED", "pluginType": "GEMINI"}
	type result struct {
		endpoint string
		data     agProjectResponse
	}
	var loaded []result
	for _, endpoint := range endpoints {
		var data agProjectResponse
		if err := agProjectAPI(ctx, client, token, http.MethodPost, endpoint+"/v1internal:loadCodeAssist", map[string]any{"metadata": metadata}, &data); err != nil {
			return "", fmt.Errorf("Antigravity project lookup failed: %w", err)
		}
		if id := agProjectID(data.Project); id != "" {
			return id, nil
		}
		loaded = append(loaded, result{endpoint, data})
	}
	hasCurrentTier := false
	for _, entry := range loaded {
		hasCurrentTier = hasCurrentTier || entry.data.CurrentTier != nil
	}
	for _, entry := range loaded {
		// Match Google's setup flow: an existing tier is not automatically changed.
		if hasCurrentTier {
			continue
		}
		var tier *agTier
		for i := range entry.data.AllowedTiers {
			candidate := &entry.data.AllowedTiers[i]
			if candidate.IsDefault && candidate.ID != "" && !candidate.UserDefinedProject {
				tier = candidate
				break
			}
		}
		if tier == nil {
			continue
		}
		fmt.Fprintln(out, "Google authorization succeeded. Completing the account's default Code Assist onboarding...")
		var operation agOnboardOperation
		if err := agProjectAPI(ctx, client, token, http.MethodPost, entry.endpoint+"/v1internal:onboardUser", map[string]any{"tierId": tier.ID, "metadata": metadata}, &operation); err != nil {
			return "", fmt.Errorf("Antigravity onboarding failed: %w", err)
		}
		name := operation.Name
		for polls := 0; !operation.Done && polls < 8; polls++ {
			if len(operation.Error) > 0 && string(operation.Error) != "null" {
				return "", fmt.Errorf("Google reported an Antigravity onboarding error")
			}
			// Never follow an arbitrary URL supplied in a backend response with OAuth.
			if !agOperationName.MatchString(name) {
				return "", fmt.Errorf("Google returned an unsupported Antigravity onboarding operation name")
			}
			timer := time.NewTimer(interval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return "", ctx.Err()
			case <-timer.C:
			}
			operation = agOnboardOperation{}
			if err := agProjectAPI(ctx, client, token, http.MethodGet, entry.endpoint+"/v1internal/"+name, nil, &operation); err != nil {
				return "", fmt.Errorf("Antigravity onboarding status failed: %w", err)
			}
		}
		if len(operation.Error) > 0 && string(operation.Error) != "null" {
			return "", fmt.Errorf("Google reported an Antigravity onboarding error")
		}
		if !operation.Done {
			return "", fmt.Errorf("Antigravity onboarding is still pending; retry login later")
		}
		if id := agProjectID(operation.Response.Project); id != "" {
			return id, nil
		}
		// Some accounts receive a project only on the lookup after completion.
		var data agProjectResponse
		if err := agProjectAPI(ctx, client, token, http.MethodPost, entry.endpoint+"/v1internal:loadCodeAssist", map[string]any{"metadata": metadata}, &data); err != nil {
			return "", err
		}
		if id := agProjectID(data.Project); id != "" {
			return id, nil
		}
		return "", fmt.Errorf("Google completed Antigravity onboarding without returning a project ID; credentials were not saved (this does not establish an account or subscription problem)")
	}
	current, allowed, ineligible := false, 0, 0
	var details []string
	for _, entry := range loaded {
		current = current || entry.data.CurrentTier != nil
		allowed += len(entry.data.AllowedTiers)
		ineligible += len(entry.data.IneligibleTiers)
		details = append(details, fmt.Sprintf("endpoint %d: %s", len(details)+1, agEligibilitySummary(entry.data)))
	}
	return "", fmt.Errorf("Google authorized sign-in but returned no project ID or eligible default onboarding path across %d endpoints (current tier present: %t; allowed tier entries: %d; ineligible tier entries: %d). This may be a discovery compatibility issue; it does not establish that your account needs setup. Credentials were not saved. Discovery details: %s", len(loaded), current, allowed, ineligible, strings.Join(details, "; "))
}

func agProjectAPI(ctx context.Context, client *http.Client, token, method, endpoint string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "CvkeHarness")
	req.Header.Set("X-Goog-Api-Client", "google-cloud-sdk vscode_cloudshelleditor/0.1")
	req.Header.Set("Client-Metadata", `{"ideType":"IDE_UNSPECIFIED","platform":"PLATFORM_UNSPECIFIED","pluginType":"GEMINI"}`)
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Google project request failed: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return antigravityStatusError(res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1024*1024+1))
	if err != nil {
		return fmt.Errorf("Google project response could not be read")
	}
	if len(raw) > 1024*1024 || json.Unmarshal(raw, target) != nil || string(bytes.TrimSpace(raw)) == "null" {
		return fmt.Errorf("invalid Google project response")
	}
	return nil
}

// Keep diagnostics limited to recognized protocol identifiers and booleans.
// Google's free-text messages and validation URLs can contain account data.
func agDiagnosticTier(id string) string {
	switch id {
	case "free-tier", "legacy-tier", "standard-tier":
		return id
	case "":
		return "missing"
	default:
		return "unrecognized"
	}
}

// Preserve new protocol enum values instead of hiding the diagnostic needed
// to understand eligibility. Reject free text, URLs, and terminal controls.
var agReasonIdentifier = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,95}$`)

func agDiagnosticReason(code string) string {
	if code == "" {
		return "missing"
	}
	if agReasonIdentifier.MatchString(code) {
		return code
	}
	return "unrecognized"
}

func agEligibilitySummary(data agProjectResponse) string {
	var allowed, ineligible []string
	for _, tier := range data.AllowedTiers {
		if len(allowed) == 8 {
			allowed = append(allowed, "additional entries omitted")
			break
		}
		allowed = append(allowed, fmt.Sprintf("%s(default=%t,requires_project=%t)", agDiagnosticTier(tier.ID), tier.IsDefault, tier.UserDefinedProject))
	}
	for _, tier := range data.IneligibleTiers {
		if len(ineligible) == 8 {
			ineligible = append(ineligible, "additional entries omitted")
			break
		}
		ineligible = append(ineligible, agDiagnosticTier(tier.TierID)+"="+agDiagnosticReason(tier.ReasonCode))
	}
	return "allowed=[" + strings.Join(allowed, ",") + "]; ineligible=[" + strings.Join(ineligible, ",") + "]"
}
