package config

// The types in this file are the *normalized* Platform spec (data-contracts §1): every field
// is populated after Resolve, tri-state values (null => environment-dependent default) are
// resolved, and environment overlays are applied. yaml/json tags are the spec field names,
// so `hermes render --print` shows a document that is itself a valid spec.

// Defaults used during normalization.
const (
	APIVersion            = "hermes/v1alpha1"
	KindPlatform          = "Platform"
	DefaultStatePrefix    = "hermes"
	DefaultNetworkCIDR    = "10.20.0.0/16"
	DefaultProtectedEnv   = "production"
	DefaultBucketName     = "data"
	DefaultTopicName      = "events"
	DefaultSubscription   = "events-default"
	ProviderAWS           = "aws"
	ProviderGCP           = "gcp"
	ProviderAlibaba       = "alibaba"
	BackendS3             = "s3"
	BackendGCS            = "gcs"
	BackendOSS            = "oss"
	BackendLocal          = "local"
	ComputeContainerized  = "containerized"
	ComputeNone           = "none"
	ComputeKubernetes     = "kubernetes"
	EnginePostgreSQL      = "postgresql"
	AccessRead            = "read"
	AccessReadWrite       = "read-write"
	reservedEnvPrefixHerm = "HERMES_"
	reservedEnvPrefixOtel = "OTEL_"
)

// Providers lists the supported cloud providers.
var Providers = []string{ProviderAWS, ProviderGCP, ProviderAlibaba}

// DefaultBackendFor returns the remote state backend used by state.backend: auto.
func DefaultBackendFor(provider string) string {
	switch provider {
	case ProviderAWS:
		return BackendS3
	case ProviderGCP:
		return BackendGCS
	case ProviderAlibaba:
		return BackendOSS
	}
	return ""
}

// Platform is the normalized spec for one environment.
type Platform struct {
	APIVersion string `yaml:"apiVersion" json:"apiVersion"`
	Kind       string `yaml:"kind" json:"kind"`

	// Environment is the resolved environment (the --env flag or project.environment).
	Environment string `yaml:"environment" json:"environment"`
	// Protected is true when Environment is listed in project.protected_environments.
	Protected bool `yaml:"protected" json:"protected"`

	Project    Project    `yaml:"project" json:"project"`
	Cloud      Cloud      `yaml:"cloud" json:"cloud"`
	Engine     Engine     `yaml:"engine" json:"engine"`
	State      State      `yaml:"state" json:"state"`
	Network    Network    `yaml:"network" json:"network"`
	Security   Security   `yaml:"security" json:"security"`
	Workloads  Workloads  `yaml:"workloads" json:"workloads"`
	Extensions Extensions `yaml:"extensions" json:"extensions"`

	// Source is the spec file the platform was loaded from (not part of the spec).
	Source string `yaml:"-" json:"-"`
}

// Project identifies the platform.
type Project struct {
	Name                  string            `yaml:"name" json:"name"`
	Environment           string            `yaml:"environment" json:"environment"` // default environment
	Owner                 string            `yaml:"owner" json:"owner"`
	Labels                map[string]string `yaml:"labels" json:"labels"`
	ProtectedEnvironments []string          `yaml:"protected_environments" json:"protected_environments"`
}

// Cloud selects the provider.
type Cloud struct {
	Provider string `yaml:"provider" json:"provider"`
	Region   string `yaml:"region" json:"region"`
	Account  string `yaml:"account" json:"account"`
}

// Engine selects the IaC engine binary.
type Engine struct {
	Binary string `yaml:"binary" json:"binary"` // auto | tofu | terraform
}

// State configures the state backend.
type State struct {
	Backend    string          `yaml:"backend" json:"backend"` // s3 | gcs | oss | local (auto is resolved)
	Bucket     string          `yaml:"bucket" json:"bucket"`
	Prefix     string          `yaml:"prefix" json:"prefix"`
	Region     string          `yaml:"region" json:"region"`
	LockTable  string          `yaml:"lock_table" json:"lock_table"`
	Encryption StateEncryption `yaml:"encryption" json:"encryption"`
}

// StateEncryption configures OpenTofu client-side state encryption.
type StateEncryption struct {
	ClientSide bool   `yaml:"client_side" json:"client_side"`
	KMSKey     string `yaml:"kms_key" json:"kms_key"`
}

// Network is the private network layout.
type Network struct {
	CIDR  string `yaml:"cidr" json:"cidr"`
	Zones int    `yaml:"zones" json:"zones"`
	NAT   bool   `yaml:"nat" json:"nat"`
}

// Security holds cross-cutting security settings.
type Security struct {
	CustomerManagedKeys bool `yaml:"customer_managed_keys" json:"customer_managed_keys"`
}

// Workloads groups the capabilities.
type Workloads struct {
	Compute       Compute       `yaml:"compute" json:"compute"`
	Database      Database      `yaml:"database" json:"database"`
	Storage       Storage       `yaml:"storage" json:"storage"`
	Messaging     Messaging     `yaml:"messaging" json:"messaging"`
	Secrets       Secrets       `yaml:"secrets" json:"secrets"`
	Observability Observability `yaml:"observability" json:"observability"`
	DNS           DNS           `yaml:"dns" json:"dns"`
}

// Compute describes containerized services.
type Compute struct {
	Type     string    `yaml:"type" json:"type"`
	Services []Service `yaml:"services" json:"services"`
}

// Service is one containerized service.
type Service struct {
	Name            string            `yaml:"name" json:"name"`
	Image           string            `yaml:"image" json:"image"`
	Port            int               `yaml:"port" json:"port"`
	CPU             float64           `yaml:"cpu" json:"cpu"`
	MemoryMB        int               `yaml:"memory_mb" json:"memory_mb"`
	MinInstances    int               `yaml:"min_instances" json:"min_instances"`
	MaxInstances    int               `yaml:"max_instances" json:"max_instances"`
	Public          bool              `yaml:"public" json:"public"`
	HealthCheckPath string            `yaml:"health_check_path" json:"health_check_path"`
	Command         []string          `yaml:"command" json:"command"`
	Env             map[string]string `yaml:"env" json:"env"`
	SecretEnv       map[string]string `yaml:"secret_env" json:"secret_env"`
	Access          Access            `yaml:"access" json:"access"`
}

// Access lists least-privilege grants for a service.
type Access struct {
	Buckets  map[string]string `yaml:"buckets" json:"buckets"` // bucket -> read | read-write
	Publish  []string          `yaml:"publish" json:"publish"`
	Consume  []string          `yaml:"consume" json:"consume"`
	Database bool              `yaml:"database" json:"database"`
}

// Database is the managed relational database.
type Database struct {
	Enabled             bool     `yaml:"enabled" json:"enabled"`
	Engine              string   `yaml:"engine" json:"engine"`
	Version             string   `yaml:"version" json:"version"`
	Size                string   `yaml:"size" json:"size"`
	StorageGB           int      `yaml:"storage_gb" json:"storage_gb"`
	MaxStorageGB        int      `yaml:"max_storage_gb" json:"max_storage_gb"`
	HighAvailability    bool     `yaml:"high_availability" json:"high_availability"`
	BackupRetentionDays int      `yaml:"backup_retention_days" json:"backup_retention_days"`
	PointInTimeRecovery bool     `yaml:"point_in_time_recovery" json:"point_in_time_recovery"`
	DeletionProtection  bool     `yaml:"deletion_protection" json:"deletion_protection"`
	Extensions          []string `yaml:"extensions" json:"extensions"`
}

// Storage is object storage.
type Storage struct {
	Enabled bool     `yaml:"enabled" json:"enabled"`
	Buckets []Bucket `yaml:"buckets" json:"buckets"`
}

// Bucket is one object-storage bucket.
type Bucket struct {
	Name          string `yaml:"name" json:"name"`
	Versioning    bool   `yaml:"versioning" json:"versioning"`
	RetentionDays int    `yaml:"retention_days" json:"retention_days"`
}

// Messaging is publish/subscribe messaging.
type Messaging struct {
	Enabled bool    `yaml:"enabled" json:"enabled"`
	Topics  []Topic `yaml:"topics" json:"topics"`
}

// Topic is one topic with its subscriptions.
type Topic struct {
	Name           string         `yaml:"name" json:"name"`
	RetentionHours int            `yaml:"retention_hours" json:"retention_hours"`
	Subscriptions  []Subscription `yaml:"subscriptions" json:"subscriptions"`
}

// Subscription is a durable queue attached to a topic.
type Subscription struct {
	Name                string `yaml:"name" json:"name"`
	AckDeadlineSeconds  int    `yaml:"ack_deadline_seconds" json:"ack_deadline_seconds"`
	MaxDeliveryAttempts int    `yaml:"max_delivery_attempts" json:"max_delivery_attempts"`
}

// Secrets lists secret containers (values are set out-of-band).
type Secrets struct {
	Enabled bool     `yaml:"enabled" json:"enabled"`
	Names   []string `yaml:"names" json:"names"`
}

// Observability configures logs, metrics, tracing and alerts.
type Observability struct {
	Enabled          bool   `yaml:"enabled" json:"enabled"`
	LogRetentionDays int    `yaml:"log_retention_days" json:"log_retention_days"`
	Metrics          bool   `yaml:"metrics" json:"metrics"`
	Tracing          bool   `yaml:"tracing" json:"tracing"`
	AlertEmail       string `yaml:"alert_email" json:"alert_email"`
}

// DNS is reserved in v1alpha1.
type DNS struct {
	Enabled bool `yaml:"enabled" json:"enabled"`
}

// Extensions holds provider-specific settings keyed by provider name.
type Extensions map[string]map[string]any

// For returns the extension map for provider (never nil).
func (e Extensions) For(provider string) map[string]any {
	if m, ok := e[provider]; ok && m != nil {
		return m
	}
	return map[string]any{}
}

// String returns a string extension value ("" when absent or not a string).
func (e Extensions) String(provider, key string) string {
	s, _ := e.For(provider)[key].(string)
	return s
}

// HasPublicService reports whether any service is publicly reachable.
func (p *Platform) HasPublicService() bool {
	for _, s := range p.Workloads.Compute.Services {
		if s.Public {
			return true
		}
	}
	return false
}

// SubscriptionNames returns every subscription name across topics.
func (p *Platform) SubscriptionNames() []string {
	var out []string
	for _, t := range p.Workloads.Messaging.Topics {
		for _, s := range t.Subscriptions {
			out = append(out, s.Name)
		}
	}
	return out
}
