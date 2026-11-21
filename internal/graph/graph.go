// Package graph builds the dependency graph of an Inventory: relationships point from the
// dependent resource to its dependency. It provides cycle-safe topological migration waves
// (strongly connected components are collapsed), blast-radius queries and Mermaid/DOT/JSON
// renderings.
package graph

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/nityam02/hermes-cloud-foundation/internal/inventory"
)

// Node is a resource (or an external reference that is not in the inventory).
type Node struct {
	ID       string `json:"id"`
	Type     string `json:"type,omitempty"`
	Name     string `json:"name,omitempty"`
	External bool   `json:"external,omitempty"`
}

// Edge is a dependency: From depends on To.
type Edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Type string `json:"type"`
}

// Graph is an immutable dependency graph.
type Graph struct {
	nodes []Node
	index map[string]int
	edges []Edge
	out   [][]int // node -> dependency node indices
	in    [][]int // node -> dependent node indices
}

// Build creates the graph from an inventory.
func Build(inv *inventory.Inventory) *Graph {
	g := &Graph{index: map[string]int{}}
	for _, r := range inv.Resources {
		g.addNode(Node{ID: r.ID, Type: r.Type, Name: r.Name})
	}
	for _, rel := range inv.Relationships {
		if _, ok := g.index[rel.From]; !ok {
			continue
		}
		if _, ok := g.index[rel.To]; !ok {
			g.addNode(Node{ID: rel.To, External: true})
		}
		g.edges = append(g.edges, Edge{From: rel.From, To: rel.To, Type: rel.Type})
	}
	g.out = make([][]int, len(g.nodes))
	g.in = make([][]int, len(g.nodes))
	for _, e := range g.edges {
		f, t := g.index[e.From], g.index[e.To]
		g.out[f] = appendUnique(g.out[f], t)
		g.in[t] = appendUnique(g.in[t], f)
	}
	return g
}

func (g *Graph) addNode(n Node) {
	if _, ok := g.index[n.ID]; ok {
		return
	}
	g.index[n.ID] = len(g.nodes)
	g.nodes = append(g.nodes, n)
}

func appendUnique(xs []int, v int) []int {
	for _, x := range xs {
		if x == v {
			return xs
		}
	}
	return append(xs, v)
}

// Nodes returns all nodes in insertion order.
func (g *Graph) Nodes() []Node { return append([]Node(nil), g.nodes...) }

// Edges returns all edges.
func (g *Graph) Edges() []Edge { return append([]Edge(nil), g.edges...) }

// Has reports whether id is a node.
func (g *Graph) Has(id string) bool { _, ok := g.index[id]; return ok }

// SCCs returns the strongly connected components (Tarjan, iterative), each sorted.
func (g *Graph) SCCs() [][]string {
	comp := g.components()
	groups := map[int][]string{}
	for i, c := range comp {
		groups[c] = append(groups[c], g.nodes[i].ID)
	}
	var out [][]string
	for _, ids := range groups {
		sort.Strings(ids)
		out = append(out, ids)
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0] < out[j][0] })
	return out
}

// components returns the SCC index of every node.
func (g *Graph) components() []int {
	n := len(g.nodes)
	index := make([]int, n)
	low := make([]int, n)
	onStack := make([]bool, n)
	comp := make([]int, n)
	for i := range index {
		index[i] = -1
	}
	var stack []int
	next, ncomp := 0, 0
	type frame struct{ v, child int }
	for s := 0; s < n; s++ {
		if index[s] != -1 {
			continue
		}
		call := []frame{{v: s}}
		index[s], low[s] = next, next
		next++
		stack = append(stack, s)
		onStack[s] = true
		for len(call) > 0 {
			f := &call[len(call)-1]
			if f.child < len(g.out[f.v]) {
				w := g.out[f.v][f.child]
				f.child++
				if index[w] == -1 {
					index[w], low[w] = next, next
					next++
					stack = append(stack, w)
					onStack[w] = true
					call = append(call, frame{v: w})
				} else if onStack[w] && index[w] < low[f.v] {
					low[f.v] = index[w]
				}
				continue
			}
			v := f.v
			if low[v] == index[v] {
				for {
					w := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					onStack[w] = false
					comp[w] = ncomp
					if w == v {
						break
					}
				}
				ncomp++
			}
			call = call[:len(call)-1]
			if len(call) > 0 {
				p := call[len(call)-1].v
				if low[v] < low[p] {
					low[p] = low[v]
				}
			}
		}
	}
	return comp
}

// Wave is a set of resources that can migrate together once earlier waves are done.
type Wave struct {
	Index     int        `json:"index"`
	Resources []string   `json:"resources"`
	Cycles    [][]string `json:"cycles,omitempty"` // dependency cycles collapsed into this wave
}

// Waves returns topological migration waves over the inventory resources: wave 0 has no
// dependencies inside the inventory; a resource's wave is one more than the highest wave of
// its dependencies. Members of a cycle share a wave. External nodes are ignored.
func (g *Graph) Waves() []Wave {
	comp := g.components()
	ncomp := 0
	for _, c := range comp {
		if c+1 > ncomp {
			ncomp = c + 1
		}
	}
	members := make([][]int, ncomp)
	for v, c := range comp {
		if !g.nodes[v].External {
			members[c] = append(members[c], v)
		}
	}
	// Tarjan emits components in reverse topological order of the condensation (a
	// component is emitted after every component it depends on), so increasing component
	// index is a valid dependency-first order.
	level := make([]int, ncomp)
	for c := 0; c < ncomp; c++ {
		for _, v := range members[c] {
			for _, w := range g.out[v] {
				if g.nodes[w].External {
					continue
				}
				cw := comp[w]
				if cw != c && level[cw]+1 > level[c] {
					level[c] = level[cw] + 1
				}
			}
		}
	}
	byLevel := map[int]*Wave{}
	maxLevel := -1
	for c := 0; c < ncomp; c++ {
		if len(members[c]) == 0 {
			continue
		}
		w := byLevel[level[c]]
		if w == nil {
			w = &Wave{Index: level[c]}
			byLevel[level[c]] = w
		}
		var ids []string
		for _, v := range members[c] {
			ids = append(ids, g.nodes[v].ID)
		}
		sort.Strings(ids)
		w.Resources = append(w.Resources, ids...)
		if len(ids) > 1 {
			w.Cycles = append(w.Cycles, ids)
		}
		if level[c] > maxLevel {
			maxLevel = level[c]
		}
	}
	var out []Wave
	for l := 0; l <= maxLevel; l++ {
		if w := byLevel[l]; w != nil {
			sort.Strings(w.Resources)
			sort.Slice(w.Cycles, func(i, j int) bool { return w.Cycles[i][0] < w.Cycles[j][0] })
			out = append(out, *w)
		}
	}
	return out
}

// WaveOf returns resource id -> wave index.
func WaveOf(waves []Wave) map[string]int {
	m := map[string]int{}
	for _, w := range waves {
		for _, id := range w.Resources {
			m[id] = w.Index
		}
	}
	return m
}

// Radius is the blast radius of a resource.
type Radius struct {
	Focus        string         `json:"focus"`
	Dependents   map[string]int `json:"dependents"`   // resources that (transitively) depend on the focus -> distance
	Dependencies map[string]int `json:"dependencies"` // resources the focus (transitively) depends on -> distance
}

// BlastRadius returns everything that depends on id (affected if it changes or moves) and
// everything it depends on (must exist on the target first).
func (g *Graph) BlastRadius(id string) (*Radius, error) {
	start, ok := g.index[id]
	if !ok {
		return nil, fmt.Errorf("graph: no resource with id %q", id)
	}
	return &Radius{Focus: id, Dependents: g.bfs(start, g.in), Dependencies: g.bfs(start, g.out)}, nil
}

func (g *Graph) bfs(start int, adj [][]int) map[string]int {
	dist := map[int]int{start: 0}
	queue := []int{start}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		for _, w := range adj[v] {
			if _, seen := dist[w]; !seen {
				dist[w] = dist[v] + 1
				queue = append(queue, w)
			}
		}
	}
	out := map[string]int{}
	for v, d := range dist {
		if v != start {
			out[g.nodes[v].ID] = d
		}
	}
	return out
}

// Subgraph returns the graph restricted to the given ids.
func (g *Graph) Subgraph(ids []string) *Graph {
	keep := map[string]bool{}
	for _, id := range ids {
		keep[id] = true
	}
	sub := &Graph{index: map[string]int{}}
	for _, n := range g.nodes {
		if keep[n.ID] {
			sub.addNode(n)
		}
	}
	for _, e := range g.edges {
		if keep[e.From] && keep[e.To] {
			sub.edges = append(sub.edges, e)
		}
	}
	sub.out = make([][]int, len(sub.nodes))
	sub.in = make([][]int, len(sub.nodes))
	for _, e := range sub.edges {
		f, t := sub.index[e.From], sub.index[e.To]
		sub.out[f] = appendUnique(sub.out[f], t)
		sub.in[t] = appendUnique(sub.in[t], f)
	}
	return sub
}

// Focus returns the subgraph of the blast radius of id (dependents and dependencies).
func (g *Graph) Focus(id string) (*Graph, *Radius, error) {
	r, err := g.BlastRadius(id)
	if err != nil {
		return nil, nil, err
	}
	ids := []string{id}
	for k := range r.Dependents {
		ids = append(ids, k)
	}
	for k := range r.Dependencies {
		ids = append(ids, k)
	}
	return g.Subgraph(ids), r, nil
}

func (g *Graph) label(n Node) string {
	if n.External {
		return "external: " + shorten(n.ID)
	}
	return n.Type + "\\n" + n.Name
}

func shorten(id string) string {
	if len(id) > 60 {
		return id[:28] + "..." + id[len(id)-28:]
	}
	return id
}

func escapeQuotes(s string) string { return strings.ReplaceAll(s, `"`, `'`) }

// Mermaid renders a flowchart (dependent --> dependency).
func (g *Graph) Mermaid() string {
	var b strings.Builder
	b.WriteString("flowchart LR\n")
	for i, n := range g.nodes {
		lbl := strings.ReplaceAll(escapeQuotes(g.label(n)), "\\n", "<br/>")
		if n.External {
			fmt.Fprintf(&b, "  n%d([\"%s\"])\n", i, lbl)
		} else {
			fmt.Fprintf(&b, "  n%d[\"%s\"]\n", i, lbl)
		}
	}
	for _, e := range g.sortedEdges() {
		fmt.Fprintf(&b, "  n%d -- %s --> n%d\n", g.index[e.From], e.Type, g.index[e.To])
	}
	return b.String()
}

// DOT renders a Graphviz digraph.
func (g *Graph) DOT() string {
	var b strings.Builder
	b.WriteString("digraph inventory {\n  rankdir=LR;\n  node [shape=box, fontsize=10];\n")
	for i, n := range g.nodes {
		style := ""
		if n.External {
			style = ", style=dashed"
		}
		fmt.Fprintf(&b, "  n%d [label=\"%s\"%s];\n", i, escapeQuotes(g.label(n)), style)
	}
	for _, e := range g.sortedEdges() {
		fmt.Fprintf(&b, "  n%d -> n%d [label=\"%s\"];\n", g.index[e.From], g.index[e.To], e.Type)
	}
	b.WriteString("}\n")
	return b.String()
}

func (g *Graph) sortedEdges() []Edge {
	es := g.Edges()
	sort.Slice(es, func(i, j int) bool {
		if es[i].From != es[j].From {
			return es[i].From < es[j].From
		}
		if es[i].To != es[j].To {
			return es[i].To < es[j].To
		}
		return es[i].Type < es[j].Type
	})
	return es
}

// Document is the JSON rendering.
type Document struct {
	Nodes  []Node  `json:"nodes"`
	Edges  []Edge  `json:"edges"`
	Waves  []Wave  `json:"waves"`
	Radius *Radius `json:"blast_radius,omitempty"`
}

// JSON renders nodes, edges and waves.
func (g *Graph) JSON(r *Radius) ([]byte, error) {
	doc := Document{Nodes: g.Nodes(), Edges: g.sortedEdges(), Waves: g.Waves(), Radius: r}
	if doc.Nodes == nil {
		doc.Nodes = []Node{}
	}
	if doc.Edges == nil {
		doc.Edges = []Edge{}
	}
	return json.MarshalIndent(doc, "", "  ")
}
