package recovery

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNGINXResourceEnvelope(t *testing.T) {
	l := NGINXResourceLimits{}.effective()
	r := NGINXResources{2, 1024}
	if !evaluateServiceResources(r, l, 2, 2, 2048).Passed {
		t.Fatal("supported demand refused")
	}
	for _, v := range []ServiceResourceEvidence{
		evaluateServiceResources(r, l, 1, 2, 2048),
		evaluateServiceResources(r, l, 2, 7, 2048),
		evaluateServiceResources(r, l, 2, 2, 1024),
		evaluateServiceResources(NGINXResources{4, 4096}, l, 4, 2, 65536),
		evaluateServiceResources(NGINXResources{1 << 30, 1 << 30}, l, 4, 1, 65536),
	} {
		if v.Passed {
			t.Fatalf("unsafe configured impact accepted: %+v", v)
		}
	}
	if err := (NGINXResourceLimits{MaxWorkers: -1}).effective().validate(); err == nil {
		t.Fatal("invalid operator limits defaulted away")
	}
}

func TestNGINXResourceParsingUsesGrammarAndPreservesOldDigests(t *testing.T) {
	var r NGINXResources
	cfg := strings.Replace(testNGINXConfig(), `return 200 "original"`, `return 200 "worker_processes 16; worker_connections 65536;"`, 1)
	if _, err := nginxShapeWithResources(testNGINXService(), cfg, &r); err != nil || r.Workers != 1 || r.ConnectionsPerWorker != 128 {
		t.Fatalf("quoted response altered demand: %+v %v", r, err)
	}
	cfg = strings.Replace(cfg, "worker_processes 1;", "", 1)
	cfg = strings.Replace(cfg, "worker_connections 128;", "", 1)
	if _, err := nginxShapeWithResources(testNGINXService(), cfg, &r); err != nil || r.Workers != 1 || r.ConnectionsPerWorker != 512 {
		t.Fatalf("NGINX defaults: %+v %v", r, err)
	}
	b, err := json.Marshal(testNGINXService())
	if err != nil || strings.Contains(string(b), "resource_limits") {
		t.Fatal("zero limits changed existing manifest serialization")
	}
}
