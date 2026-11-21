// Package tfplan models the subset of the OpenTofu/Terraform JSON plan format
// (`tofu show -json <planfile>`) that Hermes uses for safety gates, and classifies
// changes. It is shared by the execution engine (apply/destroy gates) and the
// migration engine (import-only adoption gate).
//
// Format reference: https://opentofu.org/docs/internals/json-format/ and
// https://developer.hashicorp.com/terraform/internals/json-format
package tfplan

import (
	"encoding/json"
	"fmt"
	"sort"
)

// Plan is the subset of the JSON plan representation that Hermes reads.
type Plan struct {
	FormatVersion    string           `json:"format_version"`
	TerraformVersion string           `json:"terraform_version"`
	ResourceChanges  []ResourceChange `json:"resource_changes"`
	OutputChanges    map[string]any   `json:"output_changes,omitempty"`
	Errored          bool             `json:"errored"`
}

// ResourceChange describes the planned change for one resource instance.
type ResourceChange struct {
	Address      string `json:"address"`
	Mode         string `json:"mode"` // managed | data
	Type         string `json:"type"`
	Name         string `json:"name"`
	ProviderName string `json:"provider_name"`
	Change       Change `json:"change"`
	ActionReason string `json:"action_reason,omitempty"`
}

// Change holds the planned actions.
type Change struct {
	Actions   []string   `json:"actions"` // no-op | create | read | update | delete (combinations for replace)
	Importing *Importing `json:"importing,omitempty"`
}

// Importing is present when the change imports an existing object.
type Importing struct {
	ID string `json:"id"`
}

// Action is the classified action for a resource change.
type Action string

const (
	ActionNoOp    Action = "no-op"
	ActionCreate  Action = "create"
	ActionRead    Action = "read"
	ActionUpdate  Action = "update"
	ActionDelete  Action = "delete"
	ActionReplace Action = "replace"
)

// Parse decodes a JSON plan.
func Parse(data []byte) (*Plan, error) {
	var p Plan
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("tfplan: decode plan JSON: %w", err)
	}
	if p.FormatVersion == "" {
		return nil, fmt.Errorf("tfplan: missing format_version; is this the output of 'show -json <planfile>'?")
	}
	return &p, nil
}

// Classify maps a raw action list to a single Action.
func Classify(actions []string) Action {
	switch {
	case len(actions) == 2 && contains(actions, "delete") && contains(actions, "create"):
		return ActionReplace
	case len(actions) == 1:
		switch actions[0] {
		case "create":
			return ActionCreate
		case "read":
			return ActionRead
		case "update":
			return ActionUpdate
		case "delete":
			return ActionDelete
		case "no-op":
			return ActionNoOp
		}
	}
	// Unknown combinations are treated as the most dangerous interpretation.
	return ActionReplace
}

// Item is one classified resource change.
type Item struct {
	Address   string `json:"address"`
	Type      string `json:"type"`
	Action    Action `json:"action"`
	Importing bool   `json:"importing,omitempty"`
	ImportID  string `json:"import_id,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// Summary is a classified view of a plan.
type Summary struct {
	Create  int    `json:"create"`
	Update  int    `json:"update"`
	Delete  int    `json:"delete"`
	Replace int    `json:"replace"`
	Import  int    `json:"import"`
	Read    int    `json:"read"`
	NoOp    int    `json:"no_op"`
	Items   []Item `json:"items"`
}

// Summarize classifies every managed resource change in the plan.
func Summarize(p *Plan) Summary {
	var s Summary
	for _, rc := range p.ResourceChanges {
		if rc.Mode == "data" {
			continue
		}
		it := Item{Address: rc.Address, Type: rc.Type, Action: Classify(rc.Change.Actions), Reason: rc.ActionReason}
		if rc.Change.Importing != nil {
			it.Importing = true
			it.ImportID = rc.Change.Importing.ID
			s.Import++
		}
		switch it.Action {
		case ActionCreate:
			s.Create++
		case ActionUpdate:
			s.Update++
		case ActionDelete:
			s.Delete++
		case ActionReplace:
			s.Replace++
		case ActionRead:
			s.Read++
		case ActionNoOp:
			s.NoOp++
		}
		s.Items = append(s.Items, it)
	}
	sort.Slice(s.Items, func(i, j int) bool { return s.Items[i].Address < s.Items[j].Address })
	return s
}

// HasChanges reports whether applying the plan would change any infrastructure or state.
func (s Summary) HasChanges() bool {
	return s.Create+s.Update+s.Delete+s.Replace+s.Import > 0
}

// Destructive returns the changes that delete or replace existing objects.
func (s Summary) Destructive() []Item {
	var out []Item
	for _, it := range s.Items {
		if it.Action == ActionDelete || it.Action == ActionReplace {
			out = append(out, it)
		}
	}
	return out
}

// NonImportChanges returns every change that is not a pure import (import + no-op).
// An adoption plan is safe for the source environment only when this is empty: importing
// writes state but must never create, modify, replace or delete the adopted objects.
func (s Summary) NonImportChanges() []Item {
	var out []Item
	for _, it := range s.Items {
		if it.Importing && it.Action == ActionNoOp {
			continue
		}
		if !it.Importing && it.Action == ActionNoOp {
			continue
		}
		out = append(out, it)
	}
	return out
}

func contains(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
