package tfplan

import "testing"

func TestClassify(t *testing.T) {
	cases := map[string]struct {
		in   []string
		want Action
	}{
		"create": {[]string{"create"}, ActionCreate}, "update": {[]string{"update"}, ActionUpdate},
		"delete": {[]string{"delete"}, ActionDelete}, "read": {[]string{"read"}, ActionRead},
		"noop": {[]string{"no-op"}, ActionNoOp}, "replace delete-first": {[]string{"delete", "create"}, ActionReplace},
		"replace create-first": {[]string{"create", "delete"}, ActionReplace},
		"unknown is dangerous": {[]string{"forget"}, ActionReplace}, "empty is dangerous": {nil, ActionReplace},
	}
	for name, c := range cases {
		if got := Classify(c.in); got != c.want {
			t.Errorf("%s: Classify(%v) = %s, want %s", name, c.in, got, c.want)
		}
	}
}

const planDoc = `{"format_version":"1.2","resource_changes":[
 {"address":"aws_s3_bucket.a","mode":"managed","type":"aws_s3_bucket","change":{"actions":["create"]}},
 {"address":"aws_db_instance.db","mode":"managed","type":"aws_db_instance","change":{"actions":["delete","create"]},"action_reason":"replace_because_cannot_update"},
 {"address":"aws_sqs_queue.q","mode":"managed","type":"aws_sqs_queue","change":{"actions":["no-op"],"importing":{"id":"https://sqs.example.com/q"}}},
 {"address":"aws_iam_role.r","mode":"managed","type":"aws_iam_role","change":{"actions":["update"],"importing":{"id":"r"}}},
 {"address":"data.aws_caller_identity.me","mode":"data","type":"aws_caller_identity","change":{"actions":["read"]}},
 {"address":"aws_kms_key.k","mode":"managed","type":"aws_kms_key","change":{"actions":["delete"]}}
]}`

func TestSummarize(t *testing.T) {
	p, err := Parse([]byte(planDoc))
	if err != nil {
		t.Fatal(err)
	}
	s := Summarize(p)
	if s.Create != 1 || s.Replace != 1 || s.Delete != 1 || s.Update != 1 || s.NoOp != 1 || s.Import != 2 || s.Read != 0 {
		t.Errorf("summary: %+v", s)
	}
	if len(s.Items) != 5 || s.Items[0].Address != "aws_db_instance.db" {
		t.Errorf("items must be sorted and exclude data sources: %+v", s.Items)
	}
	if !s.HasChanges() {
		t.Error("HasChanges")
	}
	d := s.Destructive()
	if len(d) != 2 || d[0].Action != ActionReplace || d[0].Reason != "replace_because_cannot_update" {
		t.Errorf("destructive: %+v", d)
	}
	// Import + no-op is the only adoption-safe change; import + update is not.
	nic := s.NonImportChanges()
	for _, it := range nic {
		if it.Address == "aws_sqs_queue.q" {
			t.Error("pure import must not be reported")
		}
	}
	if len(nic) != 4 {
		t.Errorf("NonImportChanges: %+v", nic)
	}
}

func TestParseErrors(t *testing.T) {
	if _, err := Parse([]byte("{")); err == nil {
		t.Error("invalid JSON must fail")
	}
	if _, err := Parse([]byte(`{"resource_changes":[]}`)); err == nil {
		t.Error("missing format_version must fail")
	}
	p, err := Parse([]byte(`{"format_version":"1.2"}`))
	if err != nil || Summarize(p).HasChanges() {
		t.Errorf("empty plan: %v", err)
	}
}
