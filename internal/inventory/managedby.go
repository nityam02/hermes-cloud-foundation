package inventory

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// CloudFormationStackTag is the tag AWS adds to resources created by a CloudFormation stack.
const CloudFormationStackTag = "aws:cloudformation:stack-name"

// AnnotateFromTags marks resources carrying the CloudFormation stack tag as managed by
// CloudFormation. It never removes resources and never changes anything else.
func AnnotateFromTags(inv *Inventory) {
	for i := range inv.Resources {
		r := &inv.Resources[i]
		if stack, ok := r.Tags[CloudFormationStackTag]; ok && r.ManagedBy != ManagedTerraform {
			r.ManagedBy = ManagedCloudFormation
			r.ManagedByEvidence = fmt.Sprintf("tag %s=%s", CloudFormationStackTag, stack)
		}
	}
}

// StackResource is a physical resource that belongs to a CloudFormation stack.
type StackResource struct {
	Stack      string
	PhysicalID string
	Type       string
}

// AnnotateFromStacks marks resources whose id, name or native id matches a physical id of
// a CloudFormation stack resource.
func AnnotateFromStacks(inv *Inventory, stackResources []StackResource) int {
	idx := nativeIndex(inv)
	n := 0
	for _, sr := range stackResources {
		for _, i := range idx[sr.PhysicalID] {
			r := &inv.Resources[i]
			if sr.Type != "" && sr.Type != r.Type {
				continue
			}
			if r.ManagedBy == ManagedTerraform {
				continue
			}
			r.ManagedBy = ManagedCloudFormation
			r.ManagedByEvidence = fmt.Sprintf("physical resource of CloudFormation stack %s", sr.Stack)
			n++
		}
	}
	return n
}

// nativeIndex maps every identifier a resource is known by (id/ARN, name and the native
// identifiers collectors record) to its index.
func nativeIndex(inv *Inventory) map[string][]int {
	idx := map[string][]int{}
	add := func(k string, i int) {
		if k == "" {
			return
		}
		for _, j := range idx[k] {
			if j == i {
				return
			}
		}
		idx[k] = append(idx[k], i)
	}
	for i, r := range inv.Resources {
		add(r.ID, i)
		for _, k := range []string{"resource_id", "queue_url", "identifier", "key_id", "zone_id", "arn"} {
			add(r.AttrString(k), i)
		}
	}
	return idx
}

// tfState is the subset of the Terraform/OpenTofu state v4 format Hermes reads.
type tfState struct {
	Version   int `json:"version"`
	Resources []struct {
		Mode      string `json:"mode"`
		Type      string `json:"type"`
		Name      string `json:"name"`
		Module    string `json:"module"`
		Instances []struct {
			IndexKey   any            `json:"index_key"`
			Attributes map[string]any `json:"attributes"`
		} `json:"instances"`
	} `json:"resources"`
}

// AnnotateFromTerraformState marks resources found in the given state v4 JSON files
// (matching on ARN or id) as managed by Terraform. The files are only read; the inventory
// never depends on them. It returns the number of annotated resources.
func AnnotateFromTerraformState(inv *Inventory, files []string) (int, error) {
	idx := nativeIndex(inv)
	n := 0
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return n, fmt.Errorf("terraform state: %w", err)
		}
		var st tfState
		if err := json.Unmarshal(data, &st); err != nil {
			return n, fmt.Errorf("terraform state %s: %w", file, err)
		}
		if st.Version != 4 {
			return n, fmt.Errorf("terraform state %s: unsupported state version %d (want 4)", file, st.Version)
		}
		for _, res := range st.Resources {
			if res.Mode != "managed" {
				continue
			}
			addr := res.Type + "." + res.Name
			if res.Module != "" {
				addr = res.Module + "." + addr
			}
			for _, inst := range res.Instances {
				for _, key := range []string{"arn", "id"} {
					v, _ := inst.Attributes[key].(string)
					for _, i := range idx[v] {
						r := &inv.Resources[i]
						if r.ManagedBy == ManagedTerraform {
							continue
						}
						r.ManagedBy = ManagedTerraform
						r.ManagedByEvidence = fmt.Sprintf("found in terraform state %s as %s", filepath.Base(file), addr)
						n++
					}
				}
			}
		}
	}
	return n, nil
}
