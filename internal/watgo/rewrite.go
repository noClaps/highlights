package watgo

import (
	"fmt"
	"strings"

	"highlights/internal/wat"
)

// storeBlockResults rewrites `(local.set $x (block $L (result T) body))`
// into a block without a result that sets $x where it exits: each
// `(br $L v)` becomes `(local.set $x v) (br $L)`, and the value body ends
// with is stored the same way. Branches with values then become plain
// branches, which the generator can structure. Blocks that a br_if or
// br_table leaves with a value keep their form.
func storeBlockResults(nodes []*wat.Sexpr) []*wat.Sexpr {
	var out []*wat.Sexpr
	for i := 0; i < len(nodes); i++ {
		n := nodes[i]
		// the stack form: a value block, then a local.set without operand
		if i+1 < len(nodes) {
			next := nodes[i+1]
			if next.IsList && next.Head() == "local.set" && len(next.List) == 2 {
				if b := storedBlock(n, next.List[1]); b != nil {
					out = append(out, b)
					i++
					continue
				}
			}
		}
		out = append(out, rewriteNode(n))
	}
	return out
}

func rewriteNode(n *wat.Sexpr) *wat.Sexpr {
	if !n.IsList {
		return n
	}
	if n.Head() == "local.set" && len(n.List) == 3 && !n.List[1].IsList {
		if b := storedBlock(n.List[2], n.List[1]); b != nil {
			return b
		}
	}
	out := &wat.Sexpr{IsList: true, List: []*wat.Sexpr{n.List[0]}}
	if sequence[n.Head()] || n.Head() == "if" {
		out.List = append(out.List, storeBlockResults(n.List[1:])...)
		return out
	}
	for _, c := range n.List[1:] {
		out.List = append(out.List, rewriteNode(c))
	}
	return out
}

// storedBlock rewrites a value block whose result goes to local, or
// returns nil when the block has another shape.
func storedBlock(b, local *wat.Sexpr) *wat.Sexpr {
	return rewriteValueBlock(b, func(v *wat.Sexpr) *wat.Sexpr { return list(atom("local.set"), local, v) })
}

// returnedBlock rewrites a value block that ends a function, so each exit
// returns its value.
func returnedBlock(b *wat.Sexpr) *wat.Sexpr {
	return rewriteValueBlock(b, func(v *wat.Sexpr) *wat.Sexpr { return list(atom("return"), v) })
}

// rewriteValueBlock turns the exits of a value block into set(value).
func rewriteValueBlock(b *wat.Sexpr, set func(*wat.Sexpr) *wat.Sexpr) *wat.Sexpr {
	if !b.IsList || b.Head() != "block" || len(b.List) < 4 || b.List[1].IsList ||
		b.List[1].Atom == "" || b.List[1].Atom[0] != '$' || b.List[2].Head() != "result" {
		return nil
	}
	label := b.List[1].Atom
	body := b.List[3:]
	if !rewritable(body, label) {
		return nil
	}
	// a body that ends in a branch or trap leaves only through branches
	wrapLast := producesValue(body[len(body)-1])
	var newBody []*wat.Sexpr
	for i, s := range body {
		if i == len(body)-1 && wrapLast {
			newBody = append(newBody, set(storeOne(s, label, set)))
			continue
		}
		newBody = append(newBody, storeBranches(s, label, set)...)
	}
	return rewriteNode(list(append([]*wat.Sexpr{atom("block"), b.List[1]}, newBody...)...))
}

func atom(s string) *wat.Sexpr { return &wat.Sexpr{Atom: s} }

func list(items ...*wat.Sexpr) *wat.Sexpr { return &wat.Sexpr{IsList: true, List: items} }

// producesValue reports whether a statement leaves a value: anything but
// a branch, return, or trap at the end of a value block.
func producesValue(n *wat.Sexpr) bool {
	switch n.Head() {
	case "br", "br_table", "return", "unreachable":
		return false
	}
	return true
}

// rewritable reports that only plain br forms leave the block with a
// value, and that no inner block reuses its label.
func rewritable(body []*wat.Sexpr, label string) bool {
	for _, n := range body {
		if !rewritableNode(n, label) {
			return false
		}
	}
	return true
}

func rewritableNode(n *wat.Sexpr, label string) bool {
	if !n.IsList {
		return true
	}
	// a branch with a value may only be a statement of a sequence
	if !sequence[n.Head()] {
		for _, c := range n.List[1:] {
			if c.IsList && c.Head() == "br" && len(c.List) == 3 && c.List[1].Atom == label {
				return false
			}
		}
	}
	switch n.Head() {
	case "br_if", "br_table":
		for _, a := range n.List[1:] {
			if !a.IsList && a.Atom == label {
				return false
			}
		}
	case "block", "loop", "if":
		if len(n.List) > 1 && !n.List[1].IsList && n.List[1].Atom == label {
			return false
		}
	}
	if n.Head() == "br" && len(n.List) > 1 && !n.List[1].IsList && n.List[1].Atom[0] != '$' {
		// a branch by depth could name this block
		return false
	}
	for _, c := range n.List[1:] {
		if !rewritableNode(c, label) {
			return false
		}
	}
	return true
}

// storeBranches turns each `(br $label v)` inside a statement into a
// store and a plain branch. A statement that is itself such a branch
// becomes two statements.
func storeBranches(n *wat.Sexpr, label string, set func(*wat.Sexpr) *wat.Sexpr) []*wat.Sexpr {
	if n.IsList && n.Head() == "br" && len(n.List) == 3 && n.List[1].Atom == label {
		return []*wat.Sexpr{set(storeOne(n.List[2], label, set)), list(atom("br"), atom(label))}
	}
	return []*wat.Sexpr{storeOne(n, label, set)}
}

// sequence lists the forms whose operands are statements.
var sequence = map[string]bool{"block": true, "loop": true, "then": true, "else": true}

func storeOne(n *wat.Sexpr, label string, set func(*wat.Sexpr) *wat.Sexpr) *wat.Sexpr {
	if !n.IsList {
		return n
	}
	out := &wat.Sexpr{IsList: true}
	for _, c := range n.List {
		// a branch as a statement of a nested sequence
		if sequence[n.Head()] && c.IsList && c.Head() == "br" && len(c.List) == 3 && c.List[1].Atom == label {
			out.List = append(out.List, set(storeOne(c.List[2], label, set)), list(atom("br"), atom(label)))
			continue
		}
		out.List = append(out.List, storeOne(c, label, set))
	}
	return out
}

// nameLabels gives every block, loop, and if a label, so the generator can
// name the end of any sequence.
func nameLabels(nodes []*wat.Sexpr) []*wat.Sexpr {
	n := 0
	var walk func(x *wat.Sexpr) *wat.Sexpr
	walk = func(x *wat.Sexpr) *wat.Sexpr {
		if !x.IsList {
			return x
		}
		out := &wat.Sexpr{IsList: true}
		for _, c := range x.List {
			out.List = append(out.List, walk(c))
		}
		switch x.Head() {
		case "block", "loop", "if":
			if len(out.List) < 2 || out.List[1].IsList || !strings.HasPrefix(out.List[1].Atom, "$") {
				n++
				name := atom(fmt.Sprintf("$__%d", n))
				out.List = append([]*wat.Sexpr{out.List[0], name}, out.List[1:]...)
			}
		}
		return out
	}
	out := make([]*wat.Sexpr, len(nodes))
	for i, x := range nodes {
		out[i] = walk(x)
	}
	return out
}
