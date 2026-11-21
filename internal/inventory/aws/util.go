package awsinv

import (
	"encoding/json"
	"net/url"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
)

// attrs is a convenience map builder that skips empty values, so inventories only carry
// what was actually observed.
type attrs map[string]any

func (a attrs) str(k string, v *string) attrs {
	if s := aws.ToString(v); s != "" {
		a[k] = s
	}
	return a
}

func (a attrs) s(k, v string) attrs {
	if v != "" {
		a[k] = v
	}
	return a
}

func (a attrs) b(k string, v *bool) attrs {
	if v != nil {
		a[k] = *v
	}
	return a
}

func (a attrs) i32(k string, v *int32) attrs {
	if v != nil {
		a[k] = int64(*v)
	}
	return a
}

func (a attrs) i64(k string, v *int64) attrs {
	if v != nil {
		a[k] = *v
	}
	return a
}

func (a attrs) list(k string, v []string) attrs {
	if len(v) > 0 {
		cp := append([]string(nil), v...)
		sort.Strings(cp)
		a[k] = cp
	}
	return a
}

func (a attrs) any(k string, v any) attrs {
	a[k] = v
	return a
}

func (a attrs) m() map[string]any { return map[string]any(a) }

// tagPairs converts any SDK tag slice via accessor functions.
func tagMap[T any](tags []T, kv func(T) (*string, *string)) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for _, t := range tags {
		k, v := kv(t)
		if aws.ToString(k) != "" {
			out[aws.ToString(k)] = aws.ToString(v)
		}
	}
	return out
}

func nameTag(tags map[string]string, fallback string) string {
	if n := tags["Name"]; n != "" {
		return n
	}
	return fallback
}

// kmsRef normalizes a KMS key reference (key id, key ARN, alias name or alias ARN) to an ARN.
func (s *scope) kmsRef(v string) string {
	switch {
	case v == "":
		return ""
	case strings.HasPrefix(v, "arn:"):
		return v
	case strings.HasPrefix(v, "alias/"):
		return s.arn("kms", v)
	default:
		return s.arn("kms", "key/"+v)
	}
}

// secretRef normalizes an ECS/Lambda secret reference to a Secrets Manager or SSM ARN.
func (s *scope) secretRef(v string) string {
	switch {
	case strings.HasPrefix(v, "arn:") && strings.Contains(v, ":secretsmanager:"):
		// arn:aws:secretsmanager:region:acct:secret:name-AbCdEf[:json-key:version-stage:version-id]
		parts := strings.SplitN(v, ":", 8)
		if len(parts) >= 7 {
			return strings.Join(parts[:7], ":")
		}
		return v
	case strings.HasPrefix(v, "arn:"):
		return v
	case strings.HasPrefix(v, "/"):
		return s.arn("ssm", "parameter"+v)
	default:
		return s.arn("ssm", "parameter/"+v)
	}
}

func (s *scope) ssmParameterARN(name string) string {
	if strings.HasPrefix(name, "/") {
		return s.arn("ssm", "parameter"+name)
	}
	return s.arn("ssm", "parameter/"+name)
}

// workloadHints derives configuration-management and container hints from tags.
func workloadHints(tags map[string]string) []string {
	set := map[string]bool{}
	for k, v := range tags {
		t := strings.ToLower(k + "=" + v)
		switch {
		case strings.Contains(t, "docker"), strings.Contains(t, "compose"), strings.Contains(t, "container"):
			set["docker"] = true
		}
		for _, tool := range []string{"ansible", "chef", "puppet", "saltstack", "salt"} {
			if strings.Contains(t, tool) {
				if tool == "saltstack" {
					tool = "salt"
				}
				set["config-management:"+tool] = true
				break
			}
		}
		for _, v := range []string{"vpn", "wireguard", "openvpn", "strongswan", "ipsec", "relay"} {
			if strings.Contains(t, v) {
				set["vpn"] = true
			}
		}
		if strings.Contains(t, "kubernetes") || strings.Contains(t, "k8s") || strings.Contains(t, "k3s") {
			set["kubernetes"] = true
		}
	}
	var out []string
	for h := range set {
		out = append(out, h)
	}
	sort.Strings(out)
	return out
}

// trustPolicy summarizes an IAM role trust policy without storing the document.
type trustSummary struct {
	Services       []string
	AWS            []string
	Federated      []string
	Wildcard       bool
	ExternalIDCond bool
}

func parseTrustPolicy(doc string) trustSummary {
	var ts trustSummary
	if dec, err := url.QueryUnescape(doc); err == nil {
		doc = dec
	}
	var p struct {
		Statement json.RawMessage `json:"Statement"`
	}
	if json.Unmarshal([]byte(doc), &p) != nil {
		return ts
	}
	var stmts []map[string]any
	if json.Unmarshal(p.Statement, &stmts) != nil {
		var one map[string]any
		if json.Unmarshal(p.Statement, &one) != nil {
			return ts
		}
		stmts = []map[string]any{one}
	}
	seen := map[string]bool{}
	add := func(dst *[]string, v any) {
		for _, s := range anyStrings(v) {
			if !seen[s] {
				seen[s] = true
				*dst = append(*dst, s)
			}
		}
	}
	for _, st := range stmts {
		if eff, _ := st["Effect"].(string); eff != "Allow" {
			continue
		}
		switch pr := st["Principal"].(type) {
		case string:
			if pr == "*" {
				ts.Wildcard = true
			}
		case map[string]any:
			add(&ts.Services, pr["Service"])
			add(&ts.AWS, pr["AWS"])
			add(&ts.Federated, pr["Federated"])
			for _, a := range anyStrings(pr["AWS"]) {
				if a == "*" {
					ts.Wildcard = true
				}
			}
		}
		if cond, ok := st["Condition"].(map[string]any); ok {
			for _, block := range cond {
				if m, ok := block.(map[string]any); ok {
					for k := range m {
						if strings.EqualFold(k, "sts:ExternalId") {
							ts.ExternalIDCond = true
						}
					}
				}
			}
		}
	}
	sort.Strings(ts.Services)
	sort.Strings(ts.AWS)
	sort.Strings(ts.Federated)
	return ts
}

func anyStrings(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		var out []string
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// bucketPolicyFlags summarizes a bucket policy without storing it.
func bucketPolicyFlags(doc, account string) (wildcardPrincipal, deniesInsecureTransport bool, crossAccount int) {
	var p struct {
		Statement []map[string]any `json:"Statement"`
	}
	if json.Unmarshal([]byte(doc), &p) != nil {
		return
	}
	others := map[string]bool{}
	for _, st := range p.Statement {
		eff, _ := st["Effect"].(string)
		var principals []string
		switch pr := st["Principal"].(type) {
		case string:
			principals = []string{pr}
		case map[string]any:
			principals = anyStrings(pr["AWS"])
		}
		for _, pr := range principals {
			if pr == "*" && eff == "Allow" {
				wildcardPrincipal = true
			}
			if eff == "Allow" && strings.HasPrefix(pr, "arn:") {
				parts := strings.Split(pr, ":")
				if len(parts) > 4 && parts[4] != "" && parts[4] != account {
					others[parts[4]] = true
				}
			}
		}
		if eff == "Deny" {
			if cond, ok := st["Condition"].(map[string]any); ok {
				for _, block := range cond {
					if m, ok := block.(map[string]any); ok {
						for k := range m {
							if strings.EqualFold(k, "aws:SecureTransport") {
								deniesInsecureTransport = true
							}
						}
					}
				}
			}
		}
	}
	return wildcardPrincipal, deniesInsecureTransport, len(others)
}

// arnsIn returns the distinct AWS ARNs that appear as string values in a JSON document
// (used to infer Step Functions task targets without storing the definition).
func arnsIn(doc string) []string {
	var v any
	if json.Unmarshal([]byte(doc), &v) != nil {
		return nil
	}
	set := map[string]bool{}
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			if strings.HasPrefix(t, "arn:aws") && !strings.HasPrefix(t, "arn:aws:states:::") {
				set[t] = true
			}
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	out := make([]string, 0, len(set))
	for a := range set {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}

func chunk(xs []string, n int) [][]string {
	var out [][]string
	for len(xs) > n {
		out = append(out, xs[:n])
		xs = xs[n:]
	}
	if len(xs) > 0 {
		out = append(out, xs)
	}
	return out
}
