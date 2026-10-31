package watgo

import (
	"fmt"
	"slices"
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
			if c.IsList && (isValueBr(c, label) || isValueBrIf(c, label)) {
				return false
			}
		}
	} else {
		for _, c := range n.List[1:] {
			if isValueBrIf(c, label) {
				// a statement br_if with a constant: if not taken, its value
				// is left behind unused, or generation fails for lack of it
				for _, k := range c.List[2:] {
					if !rewritableNode(k, label) {
						return false
					}
				}
			}
		}
	}
	switch n.Head() {
	case "br_if":
		if isValueBrIf(n, label) {
			return true
		}
		for _, a := range n.List[1:] {
			if !a.IsList && a.Atom == label {
				return false
			}
		}
	case "br_table":
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
	if isValueBrIf(n, label) {
		return []*wat.Sexpr{list(atom("if"), storeOne(n.List[3], label, set),
			list(atom("then"), set(n.List[2]), list(atom("br"), atom(label))))}
	}
	return []*wat.Sexpr{storeOne(n, label, set)}
}

func isValueBr(n *wat.Sexpr, label string) bool {
	return n.IsList && n.Head() == "br" && len(n.List) == 3 && n.List[1].Atom == label
}

// isValueBrIf reports `(br_if $label (i32.const v) cond)`.
func isValueBrIf(n *wat.Sexpr, label string) bool {
	return n.IsList && n.Head() == "br_if" && len(n.List) == 4 && n.List[1].Atom == label &&
		(n.List[2].Head() == "i32.const" || n.List[2].Head() == "i64.const")
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
		if sequence[n.Head()] && isValueBrIf(c, label) {
			out.List = append(out.List, list(atom("if"), storeOne(c.List[3], label, set),
				list(atom("then"), set(c.List[2]), list(atom("br"), atom(label)))))
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

// foldConstIfs replaces each statement if whose condition is a constant
// with the arm it takes, so that arm's statements join the sequence around
// it.
func foldConstIfs(nodes []*wat.Sexpr, constOf func(*wat.Sexpr) (uint64, bool)) []*wat.Sexpr {
	var out []*wat.Sexpr
	for _, n := range nodes {
		if !n.IsList {
			out = append(out, n)
			continue
		}
		if n.Head() == "if" {
			if taken, ok := takenArm(n, constOf); ok {
				taken = foldConstIfs(taken, constOf)
				if label := n.List[1]; !label.IsList && strings.HasPrefix(label.Atom, "$") {
					out = append(out, list(append([]*wat.Sexpr{atom("block"), label}, taken...)...))
				} else {
					out = append(out, taken...)
				}
				continue
			}
		}
		head := n.List[:1]
		if n.Head() == "" {
			head = nil
		}
		out = append(out, list(append(slices.Clone(head), foldConstIfs(n.List[len(head):], constOf)...)...))
	}
	return out
}

// takenArm returns the statements of the arm a statement if takes when its
// condition is a constant.
func takenArm(n *wat.Sexpr, constOf func(*wat.Sexpr) (uint64, bool)) ([]*wat.Sexpr, bool) {
	parts := n.List[1:]
	if len(parts) > 0 && !parts[0].IsList {
		parts = parts[1:]
	}
	var cond, thenArm, elseArm *wat.Sexpr
	for _, p := range parts {
		switch p.Head() {
		case "result", "type", "param":
			return nil, false
		case "then":
			thenArm = p
		case "else":
			elseArm = p
		default:
			if cond != nil {
				return nil, false
			}
			cond = p
		}
	}
	if cond == nil {
		return nil, false
	}
	c, ok := constOf(cond)
	if !ok {
		return nil, false
	}
	arm := thenArm
	if c == 0 {
		arm = elseArm
	}
	if arm == nil {
		return nil, true
	}
	return arm.List[1:], true
}

// restoreEqz turns `(i32.shr_u (i32.clz X) (i32.const 5))`, the branchless
// form the preprocessor gives a negated and/or, back into `(i32.eqz X)`,
// which reads as a comparison with zero in Go.
func restoreEqz(nodes []*wat.Sexpr) []*wat.Sexpr {
	out := make([]*wat.Sexpr, len(nodes))
	for i, n := range nodes {
		if !n.IsList {
			out[i] = n
			continue
		}
		if n.Head() == "i32.shr_u" && len(n.List) == 3 {
			x, k := n.List[1], n.List[2]
			if x.Head() == "i32.clz" && len(x.List) == 2 && k.Head() == "i32.const" && len(k.List) == 2 && k.List[1].Atom == "5" {
				out[i] = list(atom("i32.eqz"), restoreEqz(x.List[1:])[0])
				continue
			}
		}
		out[i] = list(restoreEqz(n.List)...)
	}
	return out
}
