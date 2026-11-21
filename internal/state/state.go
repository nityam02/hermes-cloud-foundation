// Package state computes the state backend configuration and the OpenTofu client-side
// state encryption block for rendered root modules.
//
// Layout: every environment and provider has its own state object,
// <prefix>/<project>/<environment>/<provider>/ (object terraform.tfstate for s3/oss; the gcs backend has no key
// setting and stores <prefix>/default.tfstate), so the same spec can be
// deployed to several clouds (one environment per cloud) without sharing state.
package state

import (
	"fmt"
	"path"

	"github.com/nityam02/hermes-cloud-foundation/internal/config"
)

// StateFile is the state object name.
const StateFile = "terraform.tfstate"

// Dir returns <prefix>/<project>/<environment>/<provider>.
func Dir(p *config.Platform) string {
	return path.Join(p.State.Prefix, p.Project.Name, p.Environment, p.Cloud.Provider)
}

// Key returns <prefix>/<project>/<environment>/<provider>/terraform.tfstate (used by s3 and oss; gcs uses the
// directory part as its prefix and stores default.tfstate under it).
func Key(p *config.Platform) string { return path.Join(Dir(p), StateFile) }

// Backend is a rendered backend block: {"<Type>": Config}.
type Backend struct {
	Type   string         `json:"type"`
	Config map[string]any `json:"config"`
}

// Location describes where state lives, for humans (`hermes status`, docs).
func (b Backend) Location() string {
	switch b.Type {
	case config.BackendS3:
		return fmt.Sprintf("s3://%v/%v", b.Config["bucket"], b.Config["key"])
	case config.BackendGCS:
		return fmt.Sprintf("gs://%v/%v/default.tfstate", b.Config["bucket"], b.Config["prefix"])
	case config.BackendOSS:
		return fmt.Sprintf("oss://%v/%v/%v", b.Config["bucket"], b.Config["prefix"], b.Config["key"])
	case config.BackendLocal:
		return fmt.Sprintf("local file %v", b.Config["path"])
	}
	return b.Type
}

// ForPlatform returns the backend for p. localPath is the path of the local state file
// relative to the root module directory; it is used when the spec selects the local
// backend or when forceLocal is set (offline rendering and tests). The local backend is
// rejected for protected environments unless forceLocal is set.
func ForPlatform(p *config.Platform, localPath string, forceLocal bool) (Backend, error) {
	st := p.State
	if forceLocal || st.Backend == config.BackendLocal {
		if !forceLocal && p.Protected {
			return Backend{}, fmt.Errorf("state: the local backend is not allowed in protected environment %q", p.Environment)
		}
		return Backend{Type: config.BackendLocal, Config: map[string]any{"path": localPath}}, nil
	}
	if st.Bucket == "" {
		return Backend{}, fmt.Errorf("state: state.bucket is required for the %s backend (create one with `hermes state bootstrap`)", st.Backend)
	}
	switch st.Backend {
	case config.BackendS3:
		cfg := map[string]any{
			"bucket":  st.Bucket,
			"key":     Key(p),
			"region":  st.Region,
			"encrypt": true,
		}
		if st.LockTable != "" {
			cfg["dynamodb_table"] = st.LockTable
		} else {
			cfg["use_lockfile"] = true
		}
		return Backend{Type: config.BackendS3, Config: cfg}, nil
	case config.BackendGCS:
		// The gcs backend stores <prefix>/<workspace>.tfstate, i.e. <dir>/default.tfstate.
		return Backend{Type: config.BackendGCS, Config: map[string]any{"bucket": st.Bucket, "prefix": Dir(p)}}, nil
	case config.BackendOSS:
		cfg := map[string]any{
			"bucket":  st.Bucket,
			"prefix":  Dir(p),
			"key":     StateFile,
			"region":  st.Region,
			"encrypt": true,
		}
		if ep := p.Extensions.String(config.ProviderAlibaba, "state_tablestore_endpoint"); ep != "" {
			cfg["tablestore_endpoint"] = ep
		}
		if tbl := p.Extensions.String(config.ProviderAlibaba, "state_tablestore_table"); tbl != "" {
			cfg["tablestore_table"] = tbl
		}
		return Backend{Type: config.BackendOSS, Config: cfg}, nil
	}
	return Backend{}, fmt.Errorf("state: unsupported backend %q", st.Backend)
}

// Encryption returns the OpenTofu `terraform.encryption` block for client-side state and
// plan encryption, or nil when it does not apply (not requested, or engine not OpenTofu).
// Key providers: aws_kms (aws) and gcp_kms (gcp); OpenTofu has none for Alibaba Cloud.
// enforced = true in protected environments, so unencrypted state is never written.
//
// In JSON syntax the method and key references must be bare traversals
// ("method.aes_gcm.hermes"), not "${...}" templates (verified with OpenTofu 1.13).
func Encryption(p *config.Platform, engineIsTofu bool) (map[string]any, error) {
	enc := p.State.Encryption
	if !enc.ClientSide || !engineIsTofu {
		return nil, nil
	}
	if enc.KMSKey == "" {
		return nil, fmt.Errorf("state: state.encryption.kms_key is required for client-side encryption")
	}
	var keyProvider map[string]any
	var ref string
	switch p.Cloud.Provider {
	case config.ProviderAWS:
		keyProvider = map[string]any{"aws_kms": map[string]any{"hermes": map[string]any{
			"kms_key_id": enc.KMSKey, "region": p.State.Region, "key_spec": "AES_256",
		}}}
		ref = "key_provider.aws_kms.hermes"
	case config.ProviderGCP:
		keyProvider = map[string]any{"gcp_kms": map[string]any{"hermes": map[string]any{
			"kms_encryption_key": enc.KMSKey, "key_length": 32,
		}}}
		ref = "key_provider.gcp_kms.hermes"
	default:
		return nil, fmt.Errorf("state: client-side state encryption is not supported on %s (no OpenTofu key provider)", p.Cloud.Provider)
	}
	target := map[string]any{"method": "method.aes_gcm.hermes", "enforced": p.Protected}
	return map[string]any{
		"key_provider": keyProvider,
		"method":       map[string]any{"aes_gcm": map[string]any{"hermes": map[string]any{"keys": ref}}},
		"state":        target,
		"plan":         target,
	}, nil
}
