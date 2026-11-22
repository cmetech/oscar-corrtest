package resend

import "fmt"

// EntryFact is supplied by an independently observed transport/provider trace.
// A crash before enrollment can remove the sole record of an earlier entry.
type EntryFact struct {
	Scope, Kind, Token string
	Tracked            bool
}

func CheckEntryHistory(facts []EntryFact) error {
	owners := map[string]EntryFact{}
	for _, f := range facts {
		if f.Scope == "" || f.Token == "" {
			return fmt.Errorf("unidentified entry")
		}
		prior, held := owners[f.Scope]
		switch f.Kind {
		case "entry":
			if held {
				return fmt.Errorf("new entry conflicts with original %s", prior.Token)
			}
			owners[f.Scope] = f
		case "enroll":
		// Enrollment cannot erase an original owner's fence.
		case "unknown", "crash", "confirmed", "no_effect":
			if !held || f.Token != prior.Token {
				return fmt.Errorf("foreign result for %s", f.Scope)
			}
			if f.Kind == "confirmed" || f.Kind == "no_effect" || f.Kind == "crash" && !prior.Tracked {
				delete(owners, f.Scope)
			}
		default:
			return fmt.Errorf("unknown entry fact")
		}
	}
	return nil
}
