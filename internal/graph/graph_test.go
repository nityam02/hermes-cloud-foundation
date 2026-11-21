package graph

import (
	"strings"
	"testing"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
)

func inv(edges ...[2]string) *inventory.Inventory {
	i := inventory.New("aws", "fixture")
	seen := map[string]bool{}
	for _, e := range edges {
		for _, id := range e {
			if !seen[id] && !strings.HasPrefix(id, "ext:") {
				seen[id] = true
				i.Resources = append(i.Resources, inventory.Resource{ID: id, Type: "T", Name: id})
			}
		}
		i.Relationships = append(i.Relationships, inventory.Relationship{From: e[0], To: e[1], Type: "depends-on"})
	}
	return i
}

func TestWavesCollapseCycles(t *testing.T) {
	// app -> db -> subnet -> vpc; sg-a <-> sg-b (cycle) -> vpc; app -> sg-a; app -> ext:key
	g := Build(inv([2]string{"app", "db"}, [2]string{"db", "subnet"}, [2]string{"subnet", "vpc"},
		[2]string{"sg-a", "sg-b"}, [2]string{"sg-b", "sg-a"}, [2]string{"sg-a", "vpc"}, [2]string{"app", "sg-a"}, [2]string{"app", "ext:key"}))
	waves := g.Waves()
	w := WaveOf(waves)
	if w["vpc"] != 0 || w["subnet"] != 1 || w["db"] != 2 || w["app"] != 3 {
		t.Fatalf("waves: %+v", waves)
	}
	if w["sg-a"] != w["sg-b"] || w["sg-a"] != 1 {
		t.Fatalf("cycle members must share a wave after their dependency: %+v", w)
	}
	found := false
	for _, wv := range waves {
		if len(wv.Cycles) == 1 && strings.Join(wv.Cycles[0], ",") == "sg-a,sg-b" {
			found = true
		}
	}
	if !found {
		t.Errorf("cycle not reported: %+v", waves)
	}
	if _, ok := w["ext:key"]; ok {
		t.Error("external nodes are not migrated and must not be in a wave")
	}
}

func TestBlastRadiusAndRendering(t *testing.T) {
	g := Build(inv([2]string{"app", "db"}, [2]string{"db", "subnet"}, [2]string{"report", "db"}))
	r, err := g.BlastRadius("db")
	if err != nil {
		t.Fatal(err)
	}
	if r.Dependents["app"] != 1 || r.Dependents["report"] != 1 || r.Dependencies["subnet"] != 1 || len(r.Dependents) != 2 {
		t.Fatalf("radius: %+v", r)
	}
	if _, err := g.BlastRadius("missing"); err == nil {
		t.Error("unknown id must error")
	}
	sub, _, _ := g.Focus("subnet")
	if !strings.Contains(sub.Mermaid(), "-- depends-on -->") || !strings.Contains(sub.DOT(), "digraph") {
		t.Error("rendering")
	}
	if b, err := g.JSON(r); err != nil || !strings.Contains(string(b), `"blast_radius"`) {
		t.Errorf("json: %v", err)
	}
}

func TestDeepChainIsIterative(t *testing.T) {
	var edges [][2]string
	for i := 0; i < 20000; i++ {
		edges = append(edges, [2]string{idOf(i), idOf(i + 1)})
	}
	g := Build(inv(edges...))
	if n := len(g.Waves()); n != 20001 {
		t.Fatalf("waves: %d", n)
	}
}

func idOf(i int) string { return "r" + strings.Repeat("0", 6-len(itoa(i))) + itoa(i) }

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
