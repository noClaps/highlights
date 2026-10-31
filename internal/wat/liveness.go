package wat

import (
	"fmt"
	"regexp"
)

type strSet map[string]bool

func (s strSet) clone() strSet {
	out := make(strSet, len(s))
	for k := range s {
		out[k] = true
	}
	return out
}

func union(a, b strSet) strSet {
	out := a.clone()
	for k := range b {
		out[k] = true
	}
	return out
}

// localLiveness finds locals that are read before their next write. seq
// walks expressions backward from the set needed after them. local.get adds
// a name; local.set/tee removes it. Branches include locals needed at their
// target. Loops repeat until the set stops changing.
type localLiveness struct {
	labels map[string]strSet
}

func newLocalLiveness() *localLiveness {
	return &localLiveness{labels: map[string]strSet{}}
}

func (l *localLiveness) seq(nodes []*Sexpr, after strSet) strSet {
	live := after
	for i := len(nodes) - 1; i >= 0; i-- {
		live = l.node(nodes[i], live)
	}
	return live
}

var labelOrIndexRe = regexp.MustCompile(`^[$\d]`)

func (l *localLiveness) node(node *Sexpr, after strSet) strSet {
	if !node.IsList {
		return after
	}
	if len(node.List) == 0 || node.List[0].IsList {
		panic(fmt.Errorf("malformed expression"))
	}
	head := node.List[0].Atom
	rest := node.List[1:]
	switch head {
	case "result", "param", "type", "local":
		return after
	case "local.get":
		out := after.clone()
		out[rest[0].Atom] = true
		return out
	case "local.set", "local.tee":
		before := after.clone()
		delete(before, rest[0].Atom)
		return l.seq(rest[1:], before)
	case "block", "loop", "if":
		return l.structured(head, rest, after)
	case "br":
		return l.seq(rest[1:], l.target(rest[0]))
	case "br_if":
		return l.seq(rest[1:], union(after, l.target(rest[0])))
	case "br_table":
		joined := after
		i := 0
		for ; i < len(rest) && !rest[i].IsList && labelOrIndexRe.MatchString(rest[i].Atom); i++ {
			joined = union(joined, l.target(rest[i]))
		}
		return l.seq(rest[i:], joined)
	case "return", "unreachable":
		ret := l.labels["return"]
		if ret == nil {
			ret = strSet{}
		}
		return l.seq(rest, ret)
	default:
		return l.seq(rest, after)
	}
}

func (l *localLiveness) target(label *Sexpr) strSet {
	if label.IsList || (len(label.Atom) > 0 && label.Atom[0] >= '0' && label.Atom[0] <= '9') {
		s := label.Atom
		if label.IsList {
			s = "[object]"
		}
		panic(fmt.Errorf("label %s must be a name: stream lexers cannot branch by index", s))
	}
	live, ok := l.labels[label.Atom]
	if !ok {
		panic(fmt.Errorf("unknown label %s", label.Atom))
	}
	return live
}

func (l *localLiveness) structured(head string, rest []*Sexpr, after strSet) strSet {
	label := ""
	if len(rest) > 0 && !rest[0].IsList && len(rest[0].Atom) > 0 && rest[0].Atom[0] == '$' {
		label = rest[0].Atom
		rest = rest[1:]
	}
	var body []*Sexpr
	for _, n := range rest {
		if n.IsList {
			h := n.Head()
			if h == "result" || h == "type" || h == "param" {
				continue
			}
		}
		body = append(body, n)
	}
	var before strSet
	outer, hadOuter := strSet(nil), false
	if label != "" {
		outer, hadOuter = l.labels[label]
	}
	if head == "loop" {
		entry := strSet{}
		for {
			if label != "" {
				l.labels[label] = entry
			}
			before = l.seq(body, after)
			if len(before) == len(entry) {
				break
			}
			entry = union(entry, before)
		}
	} else {
		if label != "" {
			l.labels[label] = after
		}
		if head == "block" {
			before = l.seq(body, after)
		} else {
			var thenArm, elseArm *Sexpr
			for _, n := range body {
				if n.IsList && n.Head() == "then" && thenArm == nil {
					thenArm = n
				}
			}
			for _, n := range body {
				if n.IsList && n.Head() == "else" && elseArm == nil {
					elseArm = n
				}
			}
			var condition []*Sexpr
			for _, n := range body {
				if n != thenArm && n != elseArm {
					condition = append(condition, n)
				}
			}
			t := after
			if thenArm != nil {
				t = l.seq(thenArm.List[1:], after)
			}
			e := after
			if elseArm != nil {
				e = l.seq(elseArm.List[1:], after)
			}
			before = l.seq(condition, union(t, e))
		}
	}
	if label != "" {
		if !hadOuter {
			delete(l.labels, label)
		} else {
			l.labels[label] = outer
		}
	}
	return before
}

// liveLocalsAtCheckpoint finds locals read before their next write after
// $lexEmitLeadingContinuation in inner.
func liveLocalsAtCheckpoint(inner string) strSet {
	var body []*Sexpr
	for _, n := range ParseSexpr(inner) {
		if n.IsList {
			body = append(body, n)
		}
	}
	at := -1
	for i, n := range body {
		if len(n.List) >= 2 && !n.List[0].IsList && n.List[0].Atom == "call" && !n.List[1].IsList && n.List[1].Atom == "$lexEmitLeadingContinuation" {
			at = i
			break
		}
	}
	if at < 0 {
		panic(fmt.Errorf("the checkpoint call must be a top-level statement"))
	}
	return newLocalLiveness().seq(body[at+1:], strSet{})
}
