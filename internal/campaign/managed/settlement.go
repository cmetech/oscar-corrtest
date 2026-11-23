package managed

import (
	"maps"
	"strings"
)

// The signed status counts leaf physical targets and their aggregate callbacks.
// The proof also includes every predecessor and the expected children within
// each callback. Workload qualification separately matches a specific sink ID.
type Settlement struct {
	Physical, Callbacks map[string]int
	Children            []OriginalChild
}

func (r OriginalReceipt) Settlement(history string, episode int, root string) (Settlement, bool) {
	var none Settlement
	if history == "" || episode < 1 || root == "" || len(r.Children) == 0 {
		return none, false
	}
	operations := map[string]OriginalChild{}
	targets := map[[2]string][]OriginalChild{}
	parents := map[string]bool{}
	for _, c := range r.Children {
		if c.History != history || c.Episode != episode || c.Root != root || c.Operation == "" {
			return none, false
		}
		switch c.OpState {
		case "confirmed", "partial", "rejected", "superseded_no_effect":
		default:
			return none, false
		}
		operations[c.Operation] = c
		parents[c.Parent] = true
		key := [2]string{c.Operation, c.Target}
		targets[key] = append(targets[key], c)
	}
	original, ok := operations[root]
	if !ok || original.Parent != "" {
		return none, false
	}
	for op, c := range operations {
		for hops := 0; op != root; hops++ {
			row, ok := operations[op]
			if !ok || hops >= len(operations) {
				return none, false
			}
			op = row.Parent
		}
		if c.Target == "" && (c.OpState != "superseded_no_effect" || !parents[c.Operation]) {
			return none, false
		}
	}
	out := Settlement{Physical: map[string]int{}, Callbacks: map[string]int{}}
	ids := map[string]bool{}
	for key, rows := range targets {
		c := rows[0]
		if c.Target == "" {
			continue
		}
		if c.Successor != "" {
			if operations[c.Successor].Parent != key[0] || len(targets[[2]string{c.Successor, c.Target}]) == 0 {
				return none, false
			}
		} else {
			out.Physical[c.TargetState]++
		}
		if c.PreEntryNoEffect == 1 && c.TargetState == "no_effect" && c.Callback == "" && len(rows) == 1 {
			continue
		}
		if c.TargetState != "confirmed" || c.Callback == "" || c.ManifestOwned != 1 || c.Held != 0 || c.Expected < 1 || c.Reported != c.Expected || len(rows) != c.Expected {
			return none, false
		}
		names := map[string]bool{}
		sent := 0
		for _, child := range rows {
			if !child.settled() || names[child.Name] || ids[child.Child] {
				return none, false
			}
			names[child.Name], ids[child.Child] = true, true
			if child.State == "sent" {
				sent++
			}
			out.Children = append(out.Children, child)
		}
		want := "partial"
		if sent == 0 {
			want = "no_effect"
		} else if sent == len(rows) {
			want = "confirmed"
		}
		if c.Aggregate != want {
			return none, false
		}
		if c.Successor == "" {
			out.Callbacks[want]++
		}
	}
	return out, len(out.Physical) > 0
}

func (r OriginalReceipt) Resend(status CommandReceipt, history string, episode int, request, root string) (Settlement, bool) {
	if request == "" || status.RequestID != request || status.AlertID != history || status.Operation != root || status.ResendID != root {
		return Settlement{}, false
	}
	proof, ok := r.Settlement(history, episode, root)
	if !ok {
		return Settlement{}, false
	}
	for _, c := range r.Children {
		if c.Kind != "resend" || c.Operation == root && c.OpState != status.NativeState {
			return Settlement{}, false
		}
	}
	return proof, maps.Equal(proof.Physical, status.TargetCounts) && maps.Equal(proof.Callbacks, status.Counts)
}

// The controlled no-ticket workload expects one physical notification and an
// affirmative skip of its History ticket candidate. A terminal failed run can
// still release the original cleanup obligation independently.
func (s Settlement) DisabledTicketNotification(notifier string) string {
	var child string
	var skipped bool
	for _, r := range s.Children {
		if r.Successor != "" {
			continue
		}
		if r.Action == "ticket" {
			if skipped || !strings.EqualFold(r.Name, "ALERTHISTORY") || r.State != "suppressed" {
				return ""
			}
			skipped = true
			continue
		}
		if r.Notifier != notifier {
			continue
		}
		if r.Action != "notification" || r.State != "sent" || child != "" {
			return ""
		}
		child = r.Child
	}
	if !skipped {
		return ""
	}
	return child
}
