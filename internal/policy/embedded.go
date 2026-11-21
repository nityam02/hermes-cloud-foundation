package policy

import (
	"io/fs"

	"github.com/nityam02/hermes-cloud-foundation/policies"
)

// Embedded is the plan-policy bundle compiled into the binary: policies/plan/*.rego
// (package main), maintained by the security workstream. The policies read
// data.hermes.protected_environments, which Check supplies through --data.
var Embedded fs.FS = mustSub(policies.FS, policies.Dir)

func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(err)
	}
	return sub
}
