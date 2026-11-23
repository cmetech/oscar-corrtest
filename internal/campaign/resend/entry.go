package resend

import "fmt"

// EntryFact is independent trace evidence from one exact target scope.
type EntryFact struct{ Scope, Kind, Token string }
type entryOwner struct {
	token                        string
	enrolled, external, accepted bool
}

func CheckEntryHistory(facts []EntryFact) error {
	enrolled := map[string]bool{}
	owners := map[string]entryOwner{}
	for _, f := range facts {
		if f.Scope == "" || f.Token == "" {
			return fmt.Errorf("unidentified entry")
		}
		prior, held := owners[f.Scope]
		switch f.Kind {
		case "enroll":
			enrolled[f.Scope] = true
		case "entry":
			if held {
				return fmt.Errorf("new entry conflicts with original %s", prior.token)
			}
			owners[f.Scope] = entryOwner{token: f.Token, enrolled: enrolled[f.Scope]}
		case "crash", "unknown", "accepted", "confirmed", "no_effect":
			if !held || f.Token != prior.token {
				return fmt.Errorf("foreign result for %s", f.Scope)
			}
			switch f.Kind {
			case "unknown", "accepted":
				prior.external = true
				prior.accepted = prior.accepted || f.Kind == "accepted"
				owners[f.Scope] = prior
			case "crash":
				if !prior.enrolled && !prior.external {
					delete(owners, f.Scope)
				}
			case "no_effect":
				if prior.accepted {
					return fmt.Errorf("accepted original cannot become no effect")
				}
				delete(owners, f.Scope)
			case "confirmed":
				delete(owners, f.Scope)
			}
		default:
			return fmt.Errorf("unknown entry fact")
		}
	}
	return nil
}
