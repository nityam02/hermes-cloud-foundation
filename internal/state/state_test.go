package state

import (
	"strings"
	"testing"

	"github.com/nityam02/hermes-cloud-foundation/internal/config"
)

func platform(provider, backend string) *config.Platform {
	p := &config.Platform{Environment: "dev"}
	p.Project.Name = "demo-app"
	p.Cloud.Provider, p.Cloud.Region = provider, "r1"
	p.State = config.State{Backend: backend, Bucket: "b-state", Prefix: "hermes", Region: "r1"}
	p.Extensions = config.Extensions{}
	return p
}

func TestKeyAndLocation(t *testing.T) {
	p := platform("aws", "s3")
	if Key(p) != "hermes/demo-app/dev/aws/terraform.tfstate" {
		t.Errorf("Key = %s", Key(p))
	}
	cases := map[string]string{
		"s3":  "s3://b-state/hermes/demo-app/dev/aws/terraform.tfstate",
		"gcs": "gs://b-state/hermes/demo-app/dev/aws/default.tfstate",
		"oss": "oss://b-state/hermes/demo-app/dev/aws/terraform.tfstate",
	}
	for backend, want := range cases {
		p.State.Backend = backend
		be, err := ForPlatform(p, "x", false)
		if err != nil || be.Location() != want {
			t.Errorf("%s: %v %s", backend, err, be.Location())
		}
	}
}

func TestLocalRules(t *testing.T) {
	p := platform("aws", "local")
	if be, err := ForPlatform(p, "../../state/x", false); err != nil || be.Config["path"] != "../../state/x" {
		t.Fatalf("local: %v %v", err, be)
	}
	p.Protected = true
	if _, err := ForPlatform(p, "x", false); err == nil {
		t.Fatal("local backend must be rejected in protected environments")
	}
	if _, err := ForPlatform(p, "x", true); err != nil {
		t.Fatalf("forced local (offline validation) must be allowed: %v", err)
	}
	p = platform("aws", "s3")
	p.State.Bucket = ""
	if _, err := ForPlatform(p, "x", false); err == nil || !strings.Contains(err.Error(), "bootstrap") {
		t.Fatalf("missing bucket: %v", err)
	}
}

func TestEncryption(t *testing.T) {
	p := platform("gcp", "gcs")
	if enc, _ := Encryption(p, true); enc != nil {
		t.Fatal("no block unless client_side")
	}
	p.State.Encryption = config.StateEncryption{ClientSide: true, KMSKey: "projects/x/locations/r1/keyRings/k/cryptoKeys/c"}
	if enc, _ := Encryption(p, false); enc != nil {
		t.Fatal("no block for Terraform")
	}
	enc, err := Encryption(p, true)
	if err != nil || enc["key_provider"].(map[string]any)["gcp_kms"] == nil || enc["state"].(map[string]any)["enforced"] != false {
		t.Fatalf("gcp block: %v %v", err, enc)
	}
	p.Cloud.Provider = "alibaba"
	if _, err := Encryption(p, true); err == nil {
		t.Fatal("alibaba has no OpenTofu key provider")
	}
}
