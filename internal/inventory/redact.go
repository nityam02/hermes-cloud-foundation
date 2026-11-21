package inventory

import (
	"regexp"
	"strings"
)

// Redacted replaces account identifiers when --redact is used.
const Redacted = "REDACTED"

var (
	arnAccountRe = regexp.MustCompile(`(arn:aws[a-zA-Z-]*:[^:\s]*:[^:\s]*:)(\d{12})(:)`)
	bareAccount  = regexp.MustCompile(`^\d{12}$`)
	// SensitiveKeyRe matches property names whose values must never be stored, whatever
	// the source (Cloud Control models, AWS Config configuration items).
	SensitiveKeyRe = regexp.MustCompile(`(?i)password|secret|token|privatekey|private_key|credential|passphrase`)
	// ValueBearingKeyRe matches property names that hold configuration VALUES which are often
	// secrets although the key name says nothing about it (F-06): environment variables
	// (ECS ContainerDefinitions[].Environment, Lambda Environment.Variables, CodeBuild
	// EnvironmentVariables) and EC2/launch-template user data. Exact names, case-insensitive.
	ValueBearingKeyRe = regexp.MustCompile(`(?i)^(environment|variables|environmentvariables|userdata)$`)
)

// Redact replaces the account id and every 12-digit account number found in ARNs (and
// bare account ids in lists such as trust principals) with "REDACTED". Names, tags and
// network ranges are kept because they are needed for planning; review them before sharing.
func Redact(inv *Inventory) {
	acct := inv.Metadata.AccountID
	rs := func(s string) string { return redactString(s, acct) }
	inv.Metadata.AccountID = Redacted
	inv.Metadata.Redacted = true
	if inv.Metadata.Identity != nil {
		inv.Metadata.Identity.PrincipalARN = rs(inv.Metadata.Identity.PrincipalARN)
		inv.Metadata.Identity.RoleARN = rs(inv.Metadata.Identity.RoleARN)
	}
	for i := range inv.Resources {
		r := &inv.Resources[i]
		r.ID = rs(r.ID)
		r.Name = rs(r.Name)
		r.ManagedByEvidence = rs(r.ManagedByEvidence)
		for k, v := range r.Tags {
			r.Tags[k] = rs(v)
		}
		for k, v := range r.Attributes {
			r.Attributes[k] = redactValue(v, acct)
		}
	}
	for i := range inv.Relationships {
		inv.Relationships[i].From = rs(inv.Relationships[i].From)
		inv.Relationships[i].To = rs(inv.Relationships[i].To)
	}
	for i := range inv.Gaps {
		inv.Gaps[i].Message = rs(inv.Gaps[i].Message)
		inv.Gaps[i].ResourceID = rs(inv.Gaps[i].ResourceID)
	}
}

func redactString(s, acct string) string {
	if s == "" {
		return s
	}
	if bareAccount.MatchString(s) {
		return Redacted
	}
	s = arnAccountRe.ReplaceAllString(s, "${1}"+Redacted+"${3}")
	if acct != "" && acct != Redacted {
		s = strings.ReplaceAll(s, acct, Redacted)
	}
	return s
}

func redactValue(v any, acct string) any {
	switch t := v.(type) {
	case string:
		return redactString(t, acct)
	case []string:
		out := make([]string, len(t))
		for i, s := range t {
			out[i] = redactString(s, acct)
		}
		return out
	case []any:
		for i := range t {
			t[i] = redactValue(t[i], acct)
		}
		return t
	case map[string]any:
		for k := range t {
			t[k] = redactValue(t[k], acct)
		}
		return t
	case []map[string]any:
		for _, m := range t {
			for k := range m {
				m[k] = redactValue(m[k], acct)
			}
		}
		return t
	}
	return v
}

// SanitizeProperties returns a deep copy of props without any key that matches
// SensitiveKeyRe or ValueBearingKeyRe. It is applied to generic property bags (AWS Config
// configuration items) whose shape Hermes does not control. The Cloud Control fallback does
// not store properties at all.
func SanitizeProperties(props map[string]any) map[string]any {
	out := make(map[string]any, len(props))
	for k, v := range props {
		if SensitiveKeyRe.MatchString(k) || ValueBearingKeyRe.MatchString(k) {
			continue
		}
		out[k] = sanitizeValue(v)
	}
	return out
}

func sanitizeValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		return SanitizeProperties(t)
	case []any:
		out := make([]any, len(t))
		for i := range t {
			out[i] = sanitizeValue(t[i])
		}
		return out
	}
	return v
}
