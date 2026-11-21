package inventory

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// AWS Config advanced query import (`aws configservice select-resource-config` output).
//
// Each query result is a JSON object (usually delivered as a JSON-encoded string) with the
// selected fields, for example:
//
//	SELECT resourceId, resourceType, resourceName, awsRegion, arn, accountId, tags,
//	       configuration, relationships
//
// AWS Config only returns resource types it records, `SELECT *` returns only top-level
// scalars, and the configuration item shape varies per type. The importer therefore marks
// every resource `partial` (or `identifier-only` without `configuration`) and records an
// explicit gap for the unrecorded scope.

type configItem struct {
	ResourceID    string          `json:"resourceId"`
	ResourceType  string          `json:"resourceType"`
	ResourceName  string          `json:"resourceName"`
	AWSRegion     string          `json:"awsRegion"`
	ARN           string          `json:"arn"`
	AccountID     string          `json:"accountId"`
	Tags          json.RawMessage `json:"tags"`
	Configuration json.RawMessage `json:"configuration"`
	Relationships []struct {
		ResourceID       string `json:"resourceId"`
		ResourceType     string `json:"resourceType"`
		ResourceName     string `json:"resourceName"`
		RelationshipName string `json:"relationshipName"`
	} `json:"relationships"`
}

// configRelationship maps an AWS Config relationshipName to a contract relationship type.
func configRelationship(name string) (string, bool) {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "contained in vpc"):
		return RelInVPC, true
	case strings.Contains(n, "contained in subnet"):
		return RelInSubnet, true
	case strings.Contains(n, "associated with securitygroup"), strings.Contains(n, "associated with security group"):
		return RelUsesSecurityGroup, true
	case strings.Contains(n, "associated with role"), strings.Contains(n, "attached to role"):
		return RelAssumesRole, true
	case strings.Contains(n, "associated with kms"), strings.Contains(n, "encrypted"):
		return RelEncryptedBy, true
	case strings.HasPrefix(n, "is attached to"):
		return RelAttachedTo, true
	case strings.HasPrefix(n, "contains"), strings.HasPrefix(n, "is associated with"):
		// "Contains X" points from the container to the contained resource, which is the
		// reverse of a dependency; "associated with" is too vague to direct reliably.
		return "", false
	}
	return RelDependsOn, true
}

// ImportAWSConfig converts one or more select-resource-config outputs into an inventory.
// Each input may be the CLI output object ({"Results": [...]}) or a JSON array of results;
// each result may be an object or a JSON-encoded string.
func ImportAWSConfig(inputs ...[]byte) (*Inventory, error) {
	inv := New("aws", CollectorAWSConfig)
	var items []configItem
	for n, data := range inputs {
		raw, err := configResults(data)
		if err != nil {
			return nil, fmt.Errorf("aws-config input %d: %w", n+1, err)
		}
		for i, r := range raw {
			var it configItem
			body := []byte(r)
			var s string
			if json.Unmarshal(r, &s) == nil {
				body = []byte(s)
			}
			if err := json.Unmarshal(body, &it); err != nil {
				return nil, fmt.Errorf("aws-config input %d result %d: %w", n+1, i+1, err)
			}
			if it.ResourceType == "" || (it.ResourceID == "" && it.ARN == "") {
				return nil, fmt.Errorf("aws-config input %d result %d: resourceType and resourceId or arn must be selected", n+1, i+1)
			}
			items = append(items, it)
		}
	}

	byNativeID := map[string]string{}
	regions := map[string]bool{}
	types := map[string]bool{}
	for _, it := range items {
		id := it.ARN
		if id == "" {
			id = it.ResourceID
		}
		byNativeID[it.ResourceID] = id
		byNativeID[it.ARN] = id
	}
	for _, it := range items {
		id := byNativeID[it.ResourceID]
		if it.ARN != "" {
			id = it.ARN
		}
		r := Resource{
			ID:           id,
			Type:         it.ResourceType,
			Name:         firstNonEmpty(it.ResourceName, it.ResourceID, id),
			Region:       it.AWSRegion,
			Tags:         configTags(it.Tags),
			Completeness: CompletenessIdentifierOnly,
			Attributes:   map[string]any{"resource_id": it.ResourceID},
		}
		if len(it.Configuration) > 0 && string(it.Configuration) != "null" {
			var cfg map[string]any
			if err := json.Unmarshal(it.Configuration, &cfg); err == nil {
				for k, v := range SanitizeProperties(cfg) {
					r.Attributes[toSnake(k)] = v
				}
				normalizeConfigAttributes(&r)
				r.Completeness = CompletenessPartial
			}
		}
		if it.AccountID != "" && inv.Metadata.AccountID == "" {
			inv.Metadata.AccountID = it.AccountID
		}
		if it.AWSRegion != "" {
			regions[it.AWSRegion] = true
		}
		types[it.ResourceType] = true
		inv.Resources = append(inv.Resources, r)
		for _, rel := range it.Relationships {
			typ, ok := configRelationship(rel.RelationshipName)
			if !ok {
				continue
			}
			to := byNativeID[rel.ResourceID]
			if to == "" {
				to = rel.ResourceID
			}
			inv.Relationships = append(inv.Relationships, Relationship{From: id, To: to, Type: typ, Evidence: EvidenceAPI})
		}
	}
	for r := range regions {
		inv.Metadata.Regions = append(inv.Metadata.Regions, r)
	}
	sort.Strings(inv.Metadata.Regions)
	inv.Gaps = append(inv.Gaps, Gap{
		Service:   "config",
		Region:    "*",
		Operation: "SelectResourceConfig",
		Reason:    GapNotEnabled,
		Message:   "AWS Config returns only resource types the configuration recorder records; unrecorded types and deleted resources are absent",
		Impact:    "Resources of unrecorded types are not inventoried; run `hermes inventory aws` for API-based discovery",
	})
	inv.Metadata.Notes = append(inv.Metadata.Notes,
		fmt.Sprintf("Imported from AWS Config advanced query results (%d resources, %d types); completeness is at most partial.", len(items), len(types)))
	AnnotateFromTags(inv)
	inv.Finalize()
	return inv, nil
}

// normalizeConfigAttributes copies the AWS Config configuration fields that the catalog
// matches on into the normalized attribute names used by the aws-api collector.
func normalizeConfigAttributes(r *Resource) {
	// engine (RDS, ElastiCache) and source_dest_check (EC2) already arrive under the same
	// names after snake-casing; only acronym-bearing keys need renaming.
	a := r.Attributes
	for from, to := range map[string]string{"multi_a_z": "multi_az", "d_b_instance_class": "instance_class"} {
		if v, ok := a[from]; ok {
			a[to] = v
			delete(a, from)
		}
	}
}

func configResults(data []byte) ([]json.RawMessage, error) {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		var arr []json.RawMessage
		if err := json.Unmarshal(data, &arr); err != nil {
			return nil, err
		}
		return arr, nil
	}
	var obj struct {
		Results []json.RawMessage `json:"Results"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, err
	}
	if obj.Results == nil {
		return nil, fmt.Errorf("no Results array; expected the output of `aws configservice select-resource-config`")
	}
	return obj.Results, nil
}

func configTags(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	out := map[string]string{}
	var list []struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &list) == nil {
		for _, t := range list {
			out[t.Key] = t.Value
		}
	} else {
		var m map[string]string
		if json.Unmarshal(raw, &m) == nil {
			out = m
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// toSnake converts camelCase/PascalCase keys to snake_case.
func toSnake(s string) string {
	var b strings.Builder
	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
